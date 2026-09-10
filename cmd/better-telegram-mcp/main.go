// Command better-telegram-mcp serves the Telegram MCP tools over stdio, and
// carries the local subcommands that put credentials on this machine.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/term"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/config"
	"github.com/david-dvinskykh/better-telegram-mcp/internal/credstore"
	"github.com/david-dvinskykh/better-telegram-mcp/internal/server"
	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

const usage = `better-telegram-mcp -- Telegram MCP server (stdio)

Usage:
  better-telegram-mcp                        Serve the MCP protocol on stdio
  better-telegram-mcp auth --bot-token TOKEN Authenticate as a bot (@BotFather)
  better-telegram-mcp auth --phone +NUMBER   Authenticate as a user (OTP, then 2FA if set)
  better-telegram-mcp auth --session-string S  Adopt an account already signed in
                                             elsewhere (a Telethon session string)
  better-telegram-mcp logout                 Revoke and remove the local credentials
  better-telegram-mcp version                Print the version

Credentials come from the TELEGRAM_* environment or, failing that, from the
encrypted store this command writes under ~/.better-telegram-mcp.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	// Logs go to stderr: stdout is the MCP transport and must carry nothing else.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	if len(argv) == 0 || strings.HasPrefix(argv[0], "-") {
		if len(argv) > 0 && (argv[0] == "-h" || argv[0] == "--help") {
			fmt.Print(usage)
			return 0
		}
		return serve()
	}

	switch argv[0] {
	case "auth", "login":
		if argv[0] == "login" {
			fmt.Fprintln(os.Stderr, "better-telegram-mcp: `login` is deprecated; use `auth` instead.")
		}
		return authCommand(argv[1:])
	case "logout":
		return logoutCommand()
	case "version":
		fmt.Println(server.Version)
		return 0
	case "help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "better-telegram-mcp: unknown command %q\n\n%s", argv[0], usage)
		return 2
	}
}

// serve runs the MCP server until stdin closes or the process is signalled.
func serve() int {
	srv, err := server.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[better-telegram-mcp] %v\n", err)
		return 1
	}

	if !srv.Settings().IsConfigured() {
		fmt.Fprint(os.Stderr, notConfiguredMessage)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The Telegram connection is started, not awaited: stdio has to be answering
	// the MCP handshake within seconds whatever Telegram is doing.
	srv.StartConnect(ctx)
	defer srv.Close(context.WithoutCancel(ctx))

	if err := srv.Run(ctx); err != nil && !isCleanShutdown(err) {
		fmt.Fprintf(os.Stderr, "[better-telegram-mcp] %v\n", err)
		return 1
	}
	return 0
}

// JSON-RPC codes the SDK uses when a session is torn down on purpose. They are
// not exported as sentinels from a public package, but the wire error they
// travel in is, and it carries the code.
const (
	codeClientClosing = -32003
	codeServerClosing = -32004
)

// isCleanShutdown reports whether the server stopped because the session ended,
// rather than because something went wrong.
//
// A client shuts an stdio server down by closing the pipe: MetaMCP does it on
// restart, an editor does it on quit. Reporting that as a failure puts an error
// in the supervisor's log and a non-zero exit code on an ordinary stop, which
// is how a healthy server gets mistaken for a crashing one.
func isCleanShutdown(err error) bool {
	if err == nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, mcp.ErrConnectionClosed) {
		return true
	}
	var wire *jsonrpc.Error
	if errors.As(err, &wire) {
		return wire.Code == codeServerClosing || wire.Code == codeClientClosing
	}
	return false
}

const notConfiguredMessage = `[better-telegram-mcp] No Telegram credentials configured.

Run one of:
  better-telegram-mcp auth --bot-token <token>   (bot mode, get the token from @BotFather)
  better-telegram-mcp auth --phone <+number>     (user mode, interactive OTP/2FA)

Or set TELEGRAM_BOT_TOKEN in your MCP client's server config.
`

// --- auth ---

func authCommand(argv []string) int {
	flags := flag.NewFlagSet("auth", flag.ContinueOnError)
	botToken := flags.String("bot-token", "", "Bot API token from @BotFather (bot mode)")
	phone := flags.String("phone", "", "Phone number as +<country><number> (user mode, interactive)")
	sessionString := flags.String("session-string", "",
		"Telethon session string of an account already signed in (user mode)")
	if err := flags.Parse(argv); err != nil {
		return 2
	}

	given := 0
	for _, value := range []string{*botToken, *phone, *sessionString} {
		if value != "" {
			given++
		}
	}
	if given > 1 {
		fmt.Fprintln(os.Stderr,
			"auth takes one of --bot-token, --phone or --session-string, not several.")
		return 2
	}

	switch {
	case *botToken != "":
		return authBot(*botToken)
	case *phone != "":
		return authPhone(*phone)
	case *sessionString != "":
		return authSessionString(*sessionString)
	default:
		fmt.Fprintln(os.Stderr,
			"auth requires --bot-token <token>, --phone <+number> or --session-string <string>.")
		return 2
	}
}

// authSessionString adopts an account that is already signed in somewhere else.
//
// The auth key inside the string is the account. Telegram invalidates a key it
// sees on two connections at once, so this is a move, not a copy: whatever
// server the string came from has to stop first, or both end up disconnected.
func authSessionString(sessionString string) int {
	settings, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		return 1
	}

	ctx := context.Background()
	if err := os.MkdirAll(filepath.Dir(settings.SessionPath()), 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		return 1
	}
	if err := telegram.ImportTelethonSessionFile(ctx, settings.SessionPath(), sessionString); err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		return 1
	}

	backend := telegram.NewUserBackend(telegram.UserOptions{
		APIID:       settings.APIID,
		APIHash:     settings.APIHash,
		SessionPath: settings.SessionPath(),
		AliasesPath: settings.AliasesPath(),
	})
	if err := backend.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		return 1
	}
	defer backend.Disconnect(ctx)

	if !backend.IsAuthorized(ctx) {
		fmt.Fprintln(os.Stderr,
			"That session string did not sign in. It may belong to a different "+
				"api_id, or it may have been revoked -- Telegram invalidates an "+
				"auth key used from two connections at once.")
		return 1
	}

	who := "the account"
	if account, err := backend.GetMe(ctx); err == nil {
		if name, ok := account["first_name"].(string); ok && name != "" {
			who = name
		}
		if username, ok := account["username"].(string); ok && username != "" {
			who = "@" + username
		}
	}
	fmt.Printf("Adopted the session of %s (user mode). Saved to %s.\n",
		who, settings.SessionPath())
	return 0
}

func authBot(token string) int {
	settings, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		return 1
	}

	ctx := context.Background()
	backend := telegram.NewBotBackend(token, telegram.BotOptions{APIBase: settings.APIBase})
	if err := backend.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		return 1
	}
	defer backend.Disconnect(ctx)

	if err := credstore.New(settings.DataDir).Save(map[string]string{
		"TELEGRAM_BOT_TOKEN": token,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		return 1
	}

	name := "the bot"
	if username, ok := backend.BotInfo()["username"].(string); ok && username != "" {
		name = "@" + username
	}
	fmt.Printf("Logged in as %s (bot mode). Credentials saved to the local config.\n", name)
	return 0
}

func authPhone(phone string) int {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr,
			"auth --phone requires an interactive terminal (the OTP code is prompted on stdin).")
		return 1
	}

	settings, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		return 1
	}

	ctx := context.Background()
	backend := telegram.NewUserBackend(telegram.UserOptions{
		APIID:       settings.APIID,
		APIHash:     settings.APIHash,
		SessionPath: settings.SessionPath(),
	})
	if err := backend.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		return 1
	}
	defer backend.Disconnect(ctx)

	if err := backend.SendCode(ctx, phone); err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		return 1
	}

	code, err := prompt("Enter the OTP code sent to your Telegram app: ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		return 1
	}

	result, err := backend.SignIn(ctx, phone, code, "")
	if err != nil {
		if !needs2FA(err) {
			fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
			return 1
		}
		password, err := promptPassword("Enter your 2FA password: ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
			return 1
		}
		result, err = backend.SignIn(ctx, phone, code, password)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
			return 1
		}
	}

	// The phone is stored; the 2FA password never is -- it is only needed to
	// create the session, which is what persists.
	if err := credstore.New(settings.DataDir).Save(map[string]string{
		"TELEGRAM_PHONE": phone,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		return 1
	}

	who := "user"
	if name, ok := result["authenticated_as"].(string); ok && name != "" {
		who = name
	}
	fmt.Printf("Logged in as %s (user mode). Session saved to %s.\n", who, settings.SessionPath())
	return 0
}

// needs2FA reports whether sign-in stopped because the account has a password.
func needs2FA(err error) bool {
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"password", "2fa", "two-factor", "srp"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// --- logout ---

func logoutCommand() int {
	settings, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Logout failed: %v\n", err)
		return 1
	}
	ctx := context.Background()
	var actions []string

	if _, err := os.Stat(settings.SessionPath()); err == nil {
		backend := telegram.NewUserBackend(telegram.UserOptions{
			APIID:       settings.APIID,
			APIHash:     settings.APIHash,
			SessionPath: settings.SessionPath(),
		})
		if err := backend.Connect(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not revoke the session server-side: %v\n", err)
		} else {
			if _, err := backend.LogOut(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not revoke the session server-side: %v\n", err)
			} else {
				actions = append(actions, "revoked the Telegram session server-side")
			}
			_ = backend.Disconnect(ctx)
		}
		if err := os.Remove(settings.SessionPath()); err == nil {
			actions = append(actions, "deleted the local session file")
		}
	}

	store := credstore.New(settings.DataDir)
	if store.Exists() {
		if err := store.Clear(); err != nil {
			fmt.Fprintf(os.Stderr, "Logout failed: %v\n", err)
			return 1
		}
		actions = append(actions, "cleared the saved credentials")
	}

	if len(actions) == 0 {
		fmt.Println("Nothing to log out.")
		return 0
	}
	for _, action := range actions {
		fmt.Printf("- %s\n", action)
	}
	fmt.Println("Logged out.")
	return 0
}

// --- prompts ---

func prompt(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// promptPassword reads without echoing: a 2FA password must not end up in a
// terminal scrollback or a screen recording.
func promptPassword(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}
