# Telegram Messages

Manage messages: send, edit, delete, forward, pin, react, search, and browse history.

## Actions

### send
Send a new message to a chat.
- **chat_id** (required): Chat ID or username
- **text** (required): Message text
- **reply_to**: Message ID to reply to
- **parse_mode**: "HTML" or "Markdown"
- **buttons**: Inline callback buttons (bot mode only), see below
- **resume_session**: Claude session id the buttons belong to, see below

### edit
Edit an existing message.
- **chat_id** (required): Chat ID or username
- **message_id** (required): Message to edit
- **text**: New text (required unless **buttons** is given)
- **parse_mode**: "HTML" or "Markdown"
- **buttons**: Replace the inline keyboard; `[]` removes it

### delete
Delete a message.
- **chat_id** (required): Chat ID or username
- **message_id** (required): Message to delete

### forward
Forward a message between chats.
- **from_chat** (required): Source chat ID
- **to_chat** (required): Destination chat ID
- **message_id** (required): Message to forward

### pin
Pin a message in a chat.
- **chat_id** (required): Chat ID or username
- **message_id** (required): Message to pin

### react
Add an emoji reaction to a message.
- **chat_id** (required): Chat ID or username
- **message_id** (required): Message to react to
- **emoji** (required): Emoji character (e.g. "👍")

### search
Search messages (user mode only).
- **query** (required): Search query
- **chat_id**: Limit search to specific chat
- **limit**: Max results (default: 20)

### history
Get chat message history (user mode only).
- **chat_id** (required): Chat ID or username
- **limit**: Max messages (default: 20)
- **offset_id**: Start from this message ID

### callbacks
Read inline-button presses (bot mode only). Reads Telegram's `getUpdates`
directly, or a queue file when another process owns that stream (see below).
- **since_id**: Only presses newer than this `update_id`
- **limit**: Max presses per call (default 20, Bot API max 100)
- **auto_answer**: Acknowledge each press (default true)
- **answer_text**: Toast shown on the presser's screen (max 200 chars)
- **allowed_from_ids**: Only accept presses from these user IDs
- **data_pattern**: Regex the callback data must fully match
- **peek**: Read without consuming - the presses stay pending and nothing is
  answered. This is what a watcher polls.
- **message_id**: With `peek`, watch one question only. Refused without `peek`,
  since a filtered consuming read would drop every other press.

Returns:
```json
{"callbacks": [{"update_id": 123456790,
                "callback_query_id": "4382bfdwdsb323b2d9",
                "data": "DEC-12:yes",
                "from_id": 375465077,
                "from_username": "owner",
                "chat_id": 375465077,
                "message_id": 4711,
                "message_date": "2026-08-25T14:03:11Z",
                "received_at": "2026-08-25T14:05:02Z",
                "answered": true}],
 "count": 1,
 "ignored": [], "ignored_count": 0,
 "cursor": 123456790}
```

The Bot API carries no press timestamp: `message_date` is when the question was
posted, `received_at` when this server polled the press.

### answer
Acknowledge a press explicitly (only needed with `auto_answer=false`).
- **callback_query_id** (required): From a `callbacks` result
- **answer_text**: Toast text (max 200 chars)
- **show_alert**: Show a modal instead of a toast

## Inline buttons

Buttons are rows of callback buttons. Bot mode only — a user account cannot
attach an inline keyboard to its own message.

```json
{"action": "send",
 "chat_id": 375465077,
 "text": "DEC-12 — move the insurance payment to 24.10",
 "buttons": [[{"text": "✅ Yes", "data": "DEC-12:yes"},
              {"text": "✖️ No", "data": "DEC-12:no"},
              {"text": "🕓 Later", "data": "DEC-12:later"}]]}
```

Limits, validated before the request is sent: `data` is 1-64 bytes of UTF-8 and
unique within the keyboard, at most 8 buttons per row, 100 rows, 100 buttons in
total.

`data` comes back verbatim as the `data` field of a press, so encode the
decision in it (e.g. `<CARD>:<yes|no|later>`).

## Reading decisions safely

1. `send` the question with buttons.
2. `callbacks` returns each press once. The server owns the cursor — a
   repeated call cannot replay a decision — and persists it to
   `<data_dir>/<session_name>.callbacks.json`, so a restart does not either.
3. `edit` the message with `buttons=[]` (optionally with a new text such as
   `... — ✅ yes`) so the same question cannot be answered twice.

## Watching for a press from a session

A session that asked a question can wait for the answer instead of being told
about it later. Both ways below read with `peek`, so the press stays pending
for whoever acts on it and is answered by nobody:

- **Poll from a watcher**: a background monitor calls
  `callbacks` with `peek=true` and the question's `message_id` every few
  seconds and reports the first non-empty result. Latency is the poll interval.
- **Self-scheduled wake-up**: the session schedules a check-in and peeks on
  each wake. No watcher and no network reach into the queue's host, but the
  granularity is whatever the scheduler allows.

Either way, once the press shows up, the session does the real read
(`callbacks` without `peek`, which consumes it), acts, and strips the keyboard
with `edit(buttons=[])`.

## Continuing the session that asked

A press is delivered by whoever owns the bot's update stream, in a process that
knows nothing about the session that asked the question. Pass `resume_session`
(a Claude session id, `session_...` or `cse_...`) on the `send` that carries the
buttons and the server records `chat_id + message_id -> session_id` in
`TELEGRAM_RESUME_MAP_FILE`:

```json
{"action": "send", "chat_id": 375465077, "text": "DEC-12 - ...",
 "buttons": [[{"text": "Yes", "data": "DEC-12:yes"}]],
 "resume_session": "session_01AbCdEfGhIjKlMnOpQrStUv"}
```

The result comes back with `"resume_registered": true` (false when no map file
is configured). Every press then carries `session_id` in the `callbacks`
result, and the delivering process can route it into that session instead of
starting a fresh one. Only well-formed session ids are accepted, the newest
registration for a message wins, and the map keeps its last 500 entries.

## When another process polls this bot

Telegram gives a bot exactly one `getUpdates` consumer, and the `allowed_updates`
filter is stored per bot. So if a relay, a webhook receiver, or another bot
framework is already polling this token, this server cannot poll it too: the two
callers terminate each other's long polls, and whichever filter was set last
decides which updates Telegram delivers at all.

For that case, point `TELEGRAM_CALLBACK_QUEUE_FILE` at a JSONL file the polling
process appends to — one Bot API update object per line:

```json
{"update_id": 123456790, "callback_query": {"id": "...", "from": {"id": 375465077}, "message": {"message_id": 4711, "date": 1756130591, "chat": {"id": 375465077}}, "data": "DEC-12:yes"}, "answered": true}
```

`callbacks` then reads that file instead of calling `getUpdates`; everything
else — the cursor, the sender and pattern guardrails, the response shape — stays
the same. The writer must poll with `allowed_updates` including
`"callback_query"` and should answer the press itself (`answerCallbackQuery`)
so the button stops spinning immediately; add `"answered": true` to the line and
this server will not try to answer the spent query id again.

Two guardrails can be set once, server-side, instead of on every call:

- `TELEGRAM_ALLOWED_CALLBACK_SENDERS` — comma-separated user IDs; a press
  from anyone else is logged and dropped, never returned and never answered.
- `TELEGRAM_CALLBACK_DATA_PATTERN` — regex the callback data must fully
  match, e.g. `DEC-\d+:(yes|no|later)`.

A per-call `allowed_from_ids` / `data_pattern` overrides the matching env var.
Callback data is data the bot itself sent, but it round-trips through a client:
keep validating it, and check the decision against your own record before
acting on it.
