// Package security holds the input validation that guards the file- and
// URL-taking tool actions: SSRF blocking with a pinned IP, path-traversal
// checks, and the redaction that keeps a bot token out of error strings.
package security

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Error marks a validation failure. Its message is safe to hand back to the
// caller verbatim: it never quotes a credential.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func newError(format string, args ...any) *Error {
	return &Error{msg: fmt.Sprintf(format, args...)}
}

// IsSecurityError reports whether err came from this package.
func IsSecurityError(err error) bool {
	var se *Error
	return errors.As(err, &se)
}

// blockedNetworks are the ranges an outbound fetch must never reach: loopback,
// private space, link-local (which carries the cloud metadata endpoints), CGNAT
// and the reserved documentation/test ranges.
var blockedNetworks = mustParseCIDRs(
	"0.0.0.0/8",
	"127.0.0.0/8",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
	"100.64.0.0/10", // CGNAT
	"192.0.0.0/24",  // IETF protocol assignments
	"192.0.2.0/24",  // TEST-NET-1
	"198.18.0.0/15", // benchmark testing
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4", // multicast
	"240.0.0.0/4", // reserved
	"255.255.255.255/32",
	"::/128",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
	"2001:db8::/32", // documentation
	"3ffe::/16",     // 6bone
	"ff00::/8",      // multicast
)

var blockedHosts = map[string]bool{
	"localhost":                true,
	"0.0.0.0":                  true,
	"::":                       true,
	"metadata.google.internal": true,
	"metadata.internal":        true,
}

// ValidateURL checks that url points at a public host and returns the resolved
// IP. The caller connects to that exact IP so a second DNS lookup cannot
// rebind the name to a blocked address between the check and the request.
func ValidateURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", newError("Invalid URL: %v", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", newError("Only http/https URLs are allowed, got: %s", parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return "", newError("URL has no hostname")
	}
	if blockedHosts[strings.ToLower(host)] {
		return "", newError("Access to %s is blocked", host)
	}

	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		// A resolution failure denies the request rather than passing it
		// through: a transient NXDOMAIN must not become an SSRF bypass.
		return "", newError("Failed to resolve hostname %s", host)
	}
	for _, ip := range ips {
		if err := validateIP(ip, host); err != nil {
			return "", err
		}
	}
	return ips[0].String(), nil
}

func validateIP(ip net.IP, host string) error {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, network := range blockedNetworks {
		if network.Contains(ip) {
			return newError("Access to internal/private IP %s (%s) is blocked", ip, host)
		}
	}
	return nil
}

// blockedReadPrefixes are directories a tool must never read a file out of.
var blockedReadPrefixes = []string{
	"/etc", "/proc", "/sys", "/dev", "/var/run", "/var/log", "/root",
}

// blockedWritePrefixes additionally covers the directories that hold the system
// itself, so a download cannot overwrite a binary or a boot file.
var blockedWritePrefixes = []string{
	"/etc", "/proc", "/sys", "/dev", "/var/run", "/var/log", "/var/spool",
	"/root", "/usr", "/bin", "/sbin", "/boot", "/lib",
}

// ValidateFilePath resolves a path a tool was asked to read and rejects it if
// it lands in a sensitive directory or in a dotfile (SSH keys, token stores).
func ValidateFilePath(path string) (string, error) {
	return validatePath(path, blockedReadPrefixes, "Access to %s is blocked for security")
}

// ValidateOutputDir resolves a directory a tool was asked to write into.
func ValidateOutputDir(path string) (string, error) {
	return validatePath(path, blockedWritePrefixes, "Writing to %s is blocked for security")
}

func validatePath(path string, prefixes []string, message string) (string, error) {
	expanded, err := expandUser(path)
	if err != nil {
		return "", newError("%v", err)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", newError("Invalid path: %v", err)
	}
	// Resolve symlinks where the path already exists, so a link into /etc is
	// caught; a path that does not exist yet (a download target) is checked
	// lexically, which is all there is to check.
	resolved := abs
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		resolved = real
	}

	for _, candidate := range []string{resolved, abs, expanded} {
		if blocked := underBlockedPrefix(candidate, prefixes); blocked != "" {
			return "", newError(message, blocked)
		}
	}
	if hidden := hiddenComponent(resolved); hidden != "" {
		if strings.HasPrefix(message, "Writing") {
			return "", newError("Writing to hidden directories (%s) is blocked", hidden)
		}
		return "", newError("Access to hidden files/directories (%s) is blocked", hidden)
	}
	return resolved, nil
}

// underBlockedPrefix reports which blocked directory contains path, comparing
// whole path segments so a sibling such as /etc-decoy is not mistaken for /etc.
// Each prefix is canonicalized too, so a platform that firmlinks /etc to
// /private/etc still matches.
func underBlockedPrefix(path string, prefixes []string) string {
	for _, prefix := range prefixes {
		candidates := []string{prefix}
		if real, err := filepath.EvalSymlinks(prefix); err == nil && real != prefix {
			candidates = append(candidates, real)
		}
		for _, blocked := range candidates {
			if path == blocked || strings.HasPrefix(path, blocked+string(filepath.Separator)) {
				return prefix
			}
		}
	}
	return ""
}

func hiddenComponent(path string) string {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "" || part == "." || part == ".." {
			continue
		}
		if strings.HasPrefix(part, ".") {
			return part
		}
	}
	return ""
}

func expandUser(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

// MaxFetchSize is the ceiling on anything a tool pulls in from a URL. It
// matches the Bot API's own upload limit, so a larger file could not be sent on
// anyway.
const MaxFetchSize = 50 * 1024 * 1024

// FetchURL downloads a validated URL, connecting to the IP that validation
// resolved so DNS cannot be rebound underneath the check. Redirects are not
// followed: the redirect target would bypass the same check.
func FetchURL(rawURL string, timeout time.Duration) ([]byte, error) {
	ip, err := ValidateURL(rawURL)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, newError("Invalid URL: %v", err)
	}

	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	pinned := *parsed
	pinned.Host = net.JoinHostPort(ip, port)

	req, err := http.NewRequest(http.MethodGet, pinned.String(), nil)
	if err != nil {
		return nil, err
	}
	// The pinned URL carries an IP, so the original name has to travel in the
	// Host header (and, over TLS, in the SNI set below) for the server to
	// answer and for certificate validation to check the right name.
	req.Host = parsed.Hostname()

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = parsed.Hostname()

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", parsed.Hostname(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetching %s returned HTTP %d", parsed.Hostname(), resp.StatusCode)
	}
	if declared := resp.Header.Get("Content-Length"); declared != "" {
		if n, err := strconv.ParseInt(declared, 10, 64); err == nil && n > MaxFetchSize {
			return nil, newError("File size exceeds maximum allowed (%d bytes)", MaxFetchSize)
		}
	}

	// Read one byte past the ceiling so an oversize body is rejected rather
	// than silently truncated.
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxFetchSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxFetchSize {
		return nil, newError("File size exceeds maximum allowed (%d bytes)", MaxFetchSize)
	}
	return body, nil
}

// botTokenRe matches a Telegram bot token ("<bot_id>:<35+ char secret>"), which
// travels inside every Bot API request URL and so can surface in a transport
// error message.
var botTokenRe = regexp.MustCompile(`\d+:[A-Za-z0-9_-]{35,}`)

// RedactBotToken masks the secret half of any bot token in text, keeping the
// bot id so the message still says which bot failed.
func RedactBotToken(text string) string {
	return botTokenRe.ReplaceAllStringFunc(text, func(match string) string {
		id, _, _ := strings.Cut(match, ":")
		return id + ":<redacted>"
	})
}

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("security: bad CIDR " + cidr)
		}
		out = append(out, network)
	}
	return out
}
