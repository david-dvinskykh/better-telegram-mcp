# better-telegram-mcp — Go rewrite

A full rewrite of the server in Go, and the place where the two Telegram
projects meet: it speaks the Bot API as a bot and MTProto as a user account
signed in by phone, from one static binary with no Python runtime.

It began as the seven consolidated tools of the Python `better-telegram-mcp`
-- same actions, same result shapes, same error strings -- and now also
carries the user-account surface of `telegram-mcp`, folded into those tools as
actions plus two new tools (`profile`, `folder`) rather than as 127 separate
ones. See **Tool surface** below.

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
    capabilities.go          the optional per-domain interfaces (see below)
    keyboard.go              buttons -> InlineKeyboardMarkup, Bot API limits
    serialize.go             MTProto objects -> the Bot-API-shaped results
    sessionlock_*.go         one process per session (see below)
    aliasref.go              alias-aware peer resolution shared by both backends
    user_*.go                the MTProto half of each extended domain
    bot_*.go                 the Bot API half, and the mode errors for the rest
  aliases/                   the local "what you call someone" -> id map
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
  outlive its process, so there is never a file to delete by hand. The lock
  *file* stays on disk either way -- it is the lock, not the file, that the
  kernel releases -- so deleting it is never the fix, and never necessary.
- The refusal message names the session, the PID holding it, and the two ways
  out: stop the other process, or give this one its own
  `TELEGRAM_SESSION_NAME`.
- A connection that dies later reports gotd's own reason rather than
  "Not connected", so the next tool call says what actually happened.

Bot mode has the same shape of conflict — Telegram allows one `getUpdates`
consumer per token — and a 409 from a competing poller now comes back naming
`TELEGRAM_CALLBACK_QUEUE_FILE`, which is how two readers can coexist.

## Tool surface

Nine tools. Each takes an `action`; the tool's description and the `help`
document for its topic list every action with its arguments.

| Tool | Covers |
| --- | --- |
| `message` | send, edit, delete, forward, pin/unpin, react, search, history, context, links, polls, drafts, scheduled sends, bulk delete/forward, chat purge, inline buttons and callback presses |
| `chat` | list, look up, create, join, leave, members, topics, and the moderation set: admins, bans, default permissions, slow mode, invite links, chat photo, admin log; plus mute, archive and the public directory |
| `media` | photos, files, voice, video, albums, stickers, saved GIFs, downloads, media description, chat photo index |
| `contact` | the address book, blocked list, contact cards, and the alias map |
| `profile` | the signed-in account and any other user or bot; privacy settings |
| `folder` | the chat-folder tabs |
| `config`, `config__open_relay`, `help` | server state and documentation |

An action the connected mode cannot serve answers with the mode error naming
the mode that would, rather than a bare failure. That split is expressed as
optional interfaces in `capabilities.go`: a backend implements the domains it
can serve, and the dispatch layer turns a missing one into that message. It
keeps the Bot API implementation from growing several dozen stubs whose only
job is to return the same error.

Two things telegram-mcp exposes are **not** here: the incoming-message feed
(`wait_for_new_message`, `enable_incoming_feed` and friends) and the two
Python-specific helpers, voice transcription and the contact-sheet image
builder. Everything else it does against a user account has an equivalent
above.

## Contact aliases

Chat ids are unusable in conversation, so the server keeps a local map from
the words a person actually says -- "андрей бекендер", "мама" -- to the id
behind them, consulted inside peer resolution in **both** modes.

A reference nobody has explained comes back as an instruction to ask who that
is; the answer is saved with `contact(action="alias_set")` and the same
wording resolves silently from then on. An alias that looks like a username or
id is refused, because it would shadow the real account of that name, and
repointing an existing alias needs `replace=true`.

The file is `aliases.json` under the data directory (override with
`TELEGRAM_ALIASES_FILE`), written 0600, and never sent to Telegram.

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
| `TELEGRAM_ALIASES_FILE` | the local name map, default `<data dir>/aliases.json` |
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
| Tools | 7 | 9 (the 7, plus `profile` and `folder`) |
| Name aliases | none | local map, applied in both modes |

Every action the Python `better-telegram-mcp` has behaves the same here.
`config(action="setup_start")` answers `cli_setup_required` with the command
to run, where the Python server answers `stdio_unsupported`.

Against `telegram-mcp`, the differences worth knowing:

- **Quiz polls** are refused. gotd v0.161 types the correct-answer field as a
  vector of ints where the schema says byte strings, so a quiz built through
  it is rejected by Telegram. (The Python server accepts `quiz_mode` but never
  sends a correct answer either, so nothing working is lost.)
- **GIF search** is gone, replaced by the account's saved GIFs. Telegram
  removed `messages.searchGifs` from its schema; the Python implementation
  still calls it.
- **Sticker sending** additionally accepts a set short name with an emoji or
  an index, not only a `.webp` file.
