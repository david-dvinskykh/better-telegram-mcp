# better-telegram-mcp — Go rewrite

A full rewrite of the server in Go. Same seven MCP tools, same actions, same
result shapes, same error strings; one static binary, no Python runtime.

**Scope: stdio only.** The Python server also ships an HTTP transport, a local
OAuth authorization server, a browser credential form and a multi-user mode.
None of that is here. Authentication is a local CLI step instead, which is what
a home deployment behind MetaMCP actually uses.

## Build and run

```bash
go build -o better-telegram-mcp ./cmd/better-telegram-mcp   # build
go test ./...                                                # test (incl. end-to-end)
go test -short ./...                                         # skip the binary spawn
go vet ./... && gofmt -l cmd internal                        # lint
```

`internal/server/e2e_test.go` is the one that mirrors deployment: it builds the
binary, spawns it as a subprocess speaking MCP over stdin/stdout the way MetaMCP
does, and drives it against a stub Bot API reached through `TELEGRAM_API_BASE`.
No credentials, no network.

Cross-compiling for a Raspberry Pi:

```bash
GOOS=linux GOARCH=arm64 go build -o better-telegram-mcp ./cmd/better-telegram-mcp
```

Stamp the version into the binary with
`-ldflags "-X github.com/david-dvinskykh/better-telegram-mcp/internal/server.Version=4.18.0"`.

## Authentication

```bash
better-telegram-mcp auth --bot-token <token>    # bot mode, token from @BotFather
better-telegram-mcp auth --phone +48123456789   # user mode: prompts for the OTP, then 2FA
better-telegram-mcp logout                      # revoke and remove everything local
```

`auth` writes an AES-256-GCM blob to `~/.better-telegram-mcp/config.enc` (0600).
The key comes from `CREDENTIAL_SECRET` (or `MCP_DCR_SERVER_SECRET`,
`DCR_SERVER_SECRET`, `MASTER_SECRET`) when one is set, otherwise from a machine
key generated once at `~/.better-telegram-mcp/.secret`. The 2FA password is
never stored: it only unlocks the sign-in, and the MTProto session is what
persists.

The environment always wins over the saved blob, so exporting
`TELEGRAM_BOT_TOKEN` in an MCP client config needs no `auth` run at all.

## Layout

```
cmd/better-telegram-mcp/     serve | auth | logout | version
internal/
  config/                    TELEGRAM_* env, mode detection, paths
  credstore/                 encrypted single-user credential blob
  security/                  SSRF (pinned-IP fetch), path traversal, token redaction
  telegram/
    backend.go               the Backend interface both modes implement
    bot.go                   Bot API over HTTP
    user.go                  MTProto over gotd/td
    keyboard.go              buttons -> InlineKeyboardMarkup, Bot API limits
    serialize.go             MTProto objects -> the Bot-API-shaped results
    sessionlock_*.go         one process per session (see below)
  tools/                     the per-tool action dispatch
  server/                    MCP registration, XPIA marking, stdio transport
  docs/                      embedded help documents
```

## Session locking — the fix for the "Not connected" outage

Symptom seen in production: several server processes shared one Telegram
session, every call answered "Not connected", and recovery meant deleting a
stale lock file by hand and restarting the container.

The cause is not file corruption. Telegram invalidates an auth key it sees on
two connections at once, so the second process does not just fail — it takes
the first one down with it. The Python server has no guard against this at all,
and reports the failure as a bare "Not connected. Call connect() first.", which
names neither the cause nor the fix.

This build refuses the second process instead:

- `Connect` takes a **kernel advisory lock** (`flock`) on
  `<session>.session.lock` and holds it for the process lifetime.
- Because the kernel owns the lock, it is released when the holder exits **for
  any reason** — `kill -9`, a container stop, an OOM kill. A stale lock cannot
  outlive its process, so there is never a file to delete by hand. (That is the
  difference from a PID lockfile, which is what leaves stale locks behind.)
- The refusal message names the session, the PID holding it, and the two ways
  out: stop the other process, or give this one its own
  `TELEGRAM_SESSION_NAME`.
- A connection that dies later reports gotd's own reason rather than
  "Not connected", so the next tool call says what actually happened.

Bot mode has the same shape of conflict — Telegram allows one `getUpdates`
consumer per token — and a 409 from a competing poller now comes back naming
`TELEGRAM_CALLBACK_QUEUE_FILE`, which is how two readers can coexist.

## Environment

Unchanged from the Python server, minus the HTTP-only ones:

| Variable | Meaning |
| --- | --- |
| `TELEGRAM_BOT_TOKEN` | bot mode |
| `TELEGRAM_API_ID`, `TELEGRAM_API_HASH` | user mode; both have built-in defaults |
| `TELEGRAM_PHONE` | user mode phone number |
| `TELEGRAM_SESSION_NAME` | session name, default `default` — also the way to run two servers side by side |
| `TELEGRAM_DATA_DIR` | default `~/.better-telegram-mcp` |
| `TELEGRAM_ALLOWED_CALLBACK_SENDERS` | comma-separated user ids allowed to press a button |
| `TELEGRAM_CALLBACK_DATA_PATTERN` | regex the callback data must fully match |
| `TELEGRAM_CALLBACK_QUEUE_FILE` | read presses from a JSONL queue instead of polling |
| `TELEGRAM_RESUME_MAP_FILE` | `message_id -> Claude session` map for routing a press back |
| `TELEGRAM_API_BASE` | Bot API host, default `https://api.telegram.org` — point it at a [self-hosted Bot API server](https://core.telegram.org/bots/api#using-a-local-bot-api-server), or at a stub to exercise the server without touching Telegram |
| `CREDENTIAL_SECRET` | master secret for the credential blob |

Not supported here: `MCP_TRANSPORT`, `TRANSPORT_MODE`, `--http`, `PUBLIC_URL`,
`MCP_DCR_SERVER_SECRET` as a transport switch (it is still read as a master
secret), `MCP_STORAGE_BACKEND`.

## Differences from the Python server

| | Python | Go |
| --- | --- | --- |
| Transports | stdio + HTTP | stdio |
| Setup | CLI or browser relay form | CLI (`auth`) |
| Multi-user | per-JWT-sub backends | single user |
| MTProto | Telethon | gotd/td |
| Session store | SQLite `.session` | gotd JSON session + flock |
| `config__open_relay` | opens the relay URL | reports the local command |
| Concurrency guard | none | advisory lock per session |

Everything a tool call can do is the same. `config(action="setup_start")`
answers `cli_setup_required` with the command to run, where the Python server
answers `stdio_unsupported`.
