package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateURLBlocksInternalTargets(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"loopback literal", "http://127.0.0.1/x"},
		{"loopback name", "http://localhost/x"},
		{"private range", "http://10.0.0.5/x"},
		{"link local", "http://169.254.169.254/latest/meta-data"},
		{"cloud metadata name", "http://metadata.google.internal/x"},
		{"ipv6 loopback", "http://[::1]/x"},
		{"unique local ipv6", "http://[fc00::1]/x"},
		{"non-http scheme", "file:///etc/passwd"},
		{"no hostname", "http:///x"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ValidateURL(test.url); err == nil {
				t.Fatalf("%s should be refused", test.url)
			}
		})
	}
}

// An IPv4-mapped IPv6 literal is the same loopback wearing a different hat.
func TestValidateURLBlocksIPv4MappedLoopback(t *testing.T) {
	if _, err := ValidateURL("http://[::ffff:127.0.0.1]/x"); err == nil {
		t.Fatal("an IPv4-mapped loopback should be refused")
	}
}

func TestValidateURLDeniesUnresolvableHost(t *testing.T) {
	// Failing open here would make a transient DNS failure into an SSRF bypass.
	if _, err := ValidateURL("http://this-host-does-not-exist.invalid/x"); err == nil {
		t.Fatal("an unresolvable host should be refused, not passed through")
	}
}

func TestValidateFilePathBlocksSensitiveLocations(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"etc", "/etc/passwd"},
		{"proc", "/proc/self/environ"},
		{"root home", "/root/.ssh/id_rsa"},
		{"traversal into etc", "/tmp/../etc/shadow"},
		{"dotfile", filepath.Join(os.TempDir(), ".ssh", "id_rsa")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ValidateFilePath(test.path); err == nil {
				t.Fatalf("%s should be refused", test.path)
			}
		})
	}
}

// A sibling directory whose name merely starts with a blocked one is fine:
// containment is checked by path segment, not by string prefix.
func TestValidateFilePathAllowsBlockedPrefixLookalike(t *testing.T) {
	dir := t.TempDir()
	lookalike := filepath.Join(dir, "etc-decoy")
	if err := os.MkdirAll(lookalike, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(lookalike, "notes.txt")
	if err := os.WriteFile(target, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateFilePath(target); err != nil {
		t.Fatalf("a path under etc-decoy is not under /etc: %v", err)
	}
}

func TestValidateFilePathAcceptsOrdinaryFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := ValidateFilePath(target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(resolved, "photo.jpg") {
		t.Errorf("expected the resolved path to keep the file name, got %s", resolved)
	}
}

func TestValidateOutputDirBlocksSystemDirectories(t *testing.T) {
	for _, dir := range []string{"/usr/bin", "/boot", "/etc", "/var/log"} {
		if _, err := ValidateOutputDir(dir); err == nil {
			t.Errorf("%s should not be writable by a tool", dir)
		}
	}
}

func TestValidateOutputDirAcceptsTempDir(t *testing.T) {
	if _, err := ValidateOutputDir(t.TempDir()); err != nil {
		t.Fatalf("a temp dir must be writable: %v", err)
	}
}

func TestRedactBotToken(t *testing.T) {
	text := "Post \"https://api.telegram.org/bot8123456789:AAG1abcdefghijklmnopqrstuvwxyz0123456/sendMessage\": EOF"
	redacted := RedactBotToken(text)

	if strings.Contains(redacted, "AAG1abcdefghijklmnopqrstuvwxyz0123456") {
		t.Fatalf("the secret survived redaction: %s", redacted)
	}
	if !strings.Contains(redacted, "8123456789:<redacted>") {
		t.Errorf("the bot id should be kept so the message still names the bot: %s", redacted)
	}
}

func TestRedactBotTokenLeavesOrdinaryTextAlone(t *testing.T) {
	text := "chat 12345 has 42 members"
	if got := RedactBotToken(text); got != text {
		t.Errorf("nothing here is a token: %s", got)
	}
}

func TestIsSecurityError(t *testing.T) {
	_, err := ValidateURL("http://127.0.0.1/x")
	if !IsSecurityError(err) {
		t.Errorf("a validation failure should be recognisable as one: %v", err)
	}
}
