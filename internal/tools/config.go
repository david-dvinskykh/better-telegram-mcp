package tools

import (
	"context"
	"sort"
	"strconv"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// ServerState is what the `config` tool needs to know about the process it runs
// in. The server owns this state; the tool only reads and nudges it.
type ServerState interface {
	// Backend is the connected backend, or nil when nothing is configured.
	Backend() telegram.Backend
	// Configured reports whether usable credentials were found.
	Configured() bool
	// PendingAuth reports a user-mode session that exists but is not signed in.
	PendingAuth() bool
	// CredentialState is the human-readable state name: awaiting_setup or
	// configured.
	CredentialState() string
	// RuntimeConfig is the mutable limit set the `set` action edits.
	RuntimeConfig() map[string]int
	SetRuntimeValue(key string, value int)
	// ResetCredentials clears the saved credentials.
	ResetCredentials() error
	// RefreshCredentials re-reads them, which is how a fresh `auth` run is
	// picked up without a restart.
	RefreshCredentials() error
}

// ConfigArgs are the arguments of the `config` tool.
type ConfigArgs struct {
	Action       string `json:"action" jsonschema:"status|set|cache_clear|setup_status|setup_start|setup_reset|setup_complete"`
	MessageLimit *int   `json:"message_limit,omitempty"`
	Timeout      *int   `json:"timeout,omitempty"`
	Key          string `json:"key,omitempty"`
	Value        string `json:"value,omitempty"`
}

// settableKeys are the runtime limits `config(action="set")` may change.
var settableKeys = []string{"message_limit", "timeout"}

var configActions = []string{
	"status", "set", "cache_clear",
	"setup_status", "setup_start", "setup_reset", "setup_complete",
}

// setupInstructions is the one thing an unconfigured stdio server can usefully
// say: how to get credentials onto this machine.
func setupInstructions() Result {
	return Result{
		"bot_mode": Result{
			"command": "better-telegram-mcp auth --bot-token <token>",
			"env_var": "TELEGRAM_BOT_TOKEN",
			"how":     "Get a token from @BotFather on Telegram",
		},
		"user_mode": Result{
			"command": "better-telegram-mcp auth --phone +<country><number>",
			"env_var": "TELEGRAM_PHONE",
			"how": "Runs the interactive OTP (and 2FA) flow in a terminal and " +
				"stores the MTProto session locally",
		},
	}
}

// HandleConfig dispatches one call of the `config` tool.
func HandleConfig(ctx context.Context, state ServerState, args ConfigArgs) Result {
	// The setup actions describe and change credential state, so they answer
	// whether or not the server is configured.
	switch args.Action {
	case "setup_status":
		return Ok(Result{
			"state":        state.CredentialState(),
			"configured":   state.Configured(),
			"pending_auth": state.PendingAuth(),
			"setup":        setupInstructions(),
		})

	case "setup_start":
		if state.Configured() && args.Key != "force" {
			return Ok(Result{
				"status":  "already_configured",
				"message": "Already configured. Use key='force' to reconfigure.",
			})
		}
		return Ok(Result{
			"status": "cli_setup_required",
			"message": "This server speaks stdio only, so setup is a local CLI step, " +
				"not a browser flow. Run `better-telegram-mcp auth --bot-token <token>` " +
				"for bot mode, or `better-telegram-mcp auth --phone +<number>` for user " +
				"mode (it prompts for the OTP and, if set, the 2FA password), then call " +
				"config(action='setup_complete').",
			"setup": setupInstructions(),
		})

	case "setup_reset":
		if err := state.ResetCredentials(); err != nil {
			return SafeError(err)
		}
		return Ok(Result{
			"status":  "ok",
			"message": "Credentials cleared. Run `better-telegram-mcp auth` to reconfigure.",
		})

	case "setup_complete":
		if err := state.RefreshCredentials(); err != nil {
			return SafeError(err)
		}
		return Ok(Result{
			"status":  "ok",
			"state":   state.CredentialState(),
			"message": "Credential state refreshed.",
		})
	}

	backend := state.Backend()
	if backend == nil {
		if args.Action == "status" {
			return Ok(Result{
				"mode":       nil,
				"connected":  false,
				"authorized": false,
				"configured": false,
				"config":     state.RuntimeConfig(),
				"setup":      setupInstructions(),
				"hint":       "Use action='setup_start' for the exact command to run.",
			})
		}
		return NotReady(state)
	}

	switch args.Action {
	case "status":
		return Ok(Result{
			"mode":         string(backend.Mode()),
			"connected":    backend.IsConnected(),
			"authorized":   backend.IsAuthorized(ctx),
			"pending_auth": state.PendingAuth(),
			"config":       state.RuntimeConfig(),
		})

	case "set":
		return handleSet(state, args)

	case "cache_clear":
		if err := backend.ClearCache(ctx); err != nil {
			return SafeError(err)
		}
		return Ok(Result{"message": "Cache cleared."})

	default:
		return unknownAction(args.Action, configActions)
	}
}

func handleSet(state ServerState, args ConfigArgs) Result {
	updated := map[string]int{}

	// Generic key/value form, for parity with the other servers' `set`.
	if args.Key != "" || args.Value != "" {
		if args.Key == "" || args.Value == "" {
			return Err("set (generic form) requires both key and value")
		}
		if !slicesContains(settableKeys, args.Key) {
			sorted := append([]string(nil), settableKeys...)
			sort.Strings(sorted)
			suggestion := ""
			if closest := closestMatch(args.Key, sorted); closest != "" {
				suggestion = " Did you mean '" + closest + "'?"
			}
			return Err("Invalid key: %s.%s Valid: %s", args.Key, suggestion, joinPipe(sorted))
		}
		coerced, err := strconv.Atoi(args.Value)
		if err != nil {
			return Err("'%s' must be an integer, got: %q", args.Key, args.Value)
		}
		state.SetRuntimeValue(args.Key, coerced)
		updated[args.Key] = coerced
	}

	// Typed sugar params.
	if args.MessageLimit != nil {
		state.SetRuntimeValue("message_limit", *args.MessageLimit)
		updated["message_limit"] = *args.MessageLimit
	}
	if args.Timeout != nil {
		state.SetRuntimeValue("timeout", *args.Timeout)
		updated["timeout"] = *args.Timeout
	}

	if len(updated) == 0 {
		return Err("set requires at least one of: key+value, message_limit, timeout")
	}
	return Ok(Result{"updated": updated, "current": state.RuntimeConfig()})
}

// NotReady is what every domain tool answers with while the server has no
// usable credentials, so the model is told how to fix it instead of seeing a
// bare failure.
func NotReady(state ServerState) Result {
	if !state.Configured() {
		return Ok(Result{
			"error": "Not configured",
			"setup": setupInstructions(),
			"hint":  "Use config(action='setup_start') for the exact command to run.",
		})
	}
	return Err("Telegram session not authenticated. " +
		"Run `better-telegram-mcp auth --phone +<number>` to sign in, then " +
		"config(action='setup_complete').")
}

// OpenRelay answers the standard `config__open_relay` tool. This build has no
// HTTP transport, so there is no relay URL to open: the honest answer is the
// local command that does the same job.
func OpenRelay() Result {
	return Ok(Result{
		"status": "stdio_only",
		"message": "This build serves stdio only and has no browser relay. " +
			"Authenticate locally with `better-telegram-mcp auth --bot-token <token>` " +
			"or `better-telegram-mcp auth --phone +<number>`.",
		"setup": setupInstructions(),
	})
}

func joinPipe(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += "|"
		}
		out += item
	}
	return out
}
