# tg-relay

A polling relay for the one bot token this MCP server shares with something
else. Telegram gives a bot exactly one `getUpdates` consumer and stores the
`allowed_updates` filter per bot, so two pollers cannot coexist: they terminate
each other's long polls and the last filter wins. Where a relay already owns
that stream, it has to hand the presses over.

`relay.sh` is that relay. It:

- long-polls `getUpdates` with `allowed_updates=["message","callback_query"]`;
- forwards text messages to a Claude Code container (the "brain") as before;
- for an inline-button press: answers it (`answerCallbackQuery`, so the button
  stops spinning at once), appends the raw update to the MCP server's queue
  file with `"answered": true`, and delivers it to a session;
- ignores presses from anyone but `CHAT_ID`, with a log line.

The MCP server reads the queue instead of polling — see
`TELEGRAM_CALLBACK_QUEUE_FILE` in the server's `messages` documentation.

## Delivering a press to a session

Two transports, tried in order. Both are off until configured; with neither,
the press waits in the queue for the next `message(action="callbacks")`.

1. **resume** (`RESUME_CLI_CONTAINER`) — continues *the session that asked*.
   The relay looks the session up in the resume map that
   `message(action="send", ..., resume_session="session_...")` writes, then
   runs `claude -p --cloud <session_id>` in that container to queue the press
   into it. `--cloud` rejects API-key authentication, so either that container
   is already signed in to a claude.ai account, or you set `RESUME_OAUTH_TOKEN`
   (from `claude setup-token` on a machine where you are signed in): the relay
   injects it for that one exec and blanks the container's own key alongside
   it, leaving the container's normal configuration untouched.
2. **fire** (`ROUTINE_FIRE_URL` + `ROUTINE_FIRE_TOKEN`) — POSTs a Routine's API
   trigger, which always starts a **new** session. The payload carries
   `session_id` when one is registered, so that session can hand the press on.

Either way the press is already in the queue first, so a failed delivery costs
latency, not the decision.

## Configuration

Container environment:

| Variable | Default | Meaning |
|:---------|:--------|:--------|
| `TELEGRAM_BOT_TOKEN` | — | required |
| `CHAT_ID` | `375465077` | the only chat and the only presser accepted |
| `BRAIN` | `app_1016f397_claudecode` | container that answers text messages |
| `MODEL` | `sonnet` | model for those answers |
| `RUN_TIMEOUT` | `420` | seconds before a brain run is killed |
| `MCP_CONTAINER` | `metamcp` | container running the MCP server |
| `MCP_QUEUE_FILE` | `/opt/telegram/data/callbacks.jsonl` | queue path inside it |
| `MCP_RESUME_MAP` | `/opt/telegram/data/resume.jsonl` | resume map path inside it |

Credentials for delivery go in `/app/fire.env` (`chmod 600`), sourced at start,
so turning a transport on is an edit plus a restart rather than a container
recreate:

```sh
RESUME_CLI_CONTAINER=app_1016f397_claudecode
RESUME_OAUTH_TOKEN=sk-ant-oat01-...
ROUTINE_FIRE_URL=https://api.anthropic.com/v1/claude_code/routines/trig_XXXX/fire
ROUTINE_FIRE_TOKEN=sk-ant-oat01-...
```

`RESUME_CLI_CONTAINER` only needs a container with the `claude` CLI installed;
with `RESUME_OAUTH_TOKEN` set it does not need its own claude.ai login.

The startup log line reports what is live:

```
relay up: brain=... chat=... model=... timeout=420s cb->metamcp:/opt/telegram/data/callbacks.jsonl resume=off fire=off
```

## Install

```sh
docker exec tg-relay sh -c '
  cp -f /app/relay.sh /app/relay.sh.bak &&
  curl -fsSL https://raw.githubusercontent.com/david-dvinskykh/better-telegram-mcp/main/contrib/tg-relay/relay.sh -o /app/relay.sh.new &&
  sh -n /app/relay.sh.new && chmod +x /app/relay.sh.new && mv /app/relay.sh.new /app/relay.sh'
docker restart tg-relay
```

`/app` is a host bind mount, so the script, its `offset` cursor, its log and its
own copy of the press queue survive restarts.
