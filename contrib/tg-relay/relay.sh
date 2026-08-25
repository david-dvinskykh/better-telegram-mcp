#!/bin/sh
set -u
API="https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}"
CHAT="${CHAT_ID:-375465077}"
BRAIN="${BRAIN:-app_1016f397_claudecode}"
MODEL="${MODEL:-sonnet}"
TMO="${RUN_TIMEOUT:-420}"
# Where the MCP server reads inline-button presses from, and where it records
# which Claude session asked. Both live in the metamcp container's own volume;
# TELEGRAM_CALLBACK_QUEUE_FILE and TELEGRAM_RESUME_MAP_FILE there point at
# these same paths.
MCP_CTR="${MCP_CONTAINER:-metamcp}"
MCP_QUEUE="${MCP_QUEUE_FILE:-/opt/telegram/data/callbacks.jsonl}"
MCP_RESUME="${MCP_RESUME_MAP:-/opt/telegram/data/resume.jsonl}"
D=/app; S="$D/offset"; P="$D/prompt.txt"; L="$D/relay.log"; Q="$D/callbacks.jsonl"
# Delivery of a press, in order of preference. Both are off until configured;
# with neither, the press just waits in the queue for the next digest.
#   1. resume: continue the session that asked, via a container whose `claude`
#      CLI is signed in to claude.ai (RESUME_CLI_CONTAINER).
#   2. fire: POST a Routine's API trigger, which always starts a NEW session.
# Credentials live in $D/fire.env (chmod 600) so turning either on is an edit
# plus a restart, not a container recreate.
[ -f "$D/fire.env" ] && . "$D/fire.env"
RESUME_CTR="${RESUME_CLI_CONTAINER:-}"
RESUME_TOKEN="${RESUME_OAUTH_TOKEN:-}"
FIRE_URL="${ROUTINE_FIRE_URL:-}"
FIRE_TOKEN="${ROUTINE_FIRE_TOKEN:-}"
FIRE_BETA="${ROUTINE_FIRE_BETA:-experimental-cc-routine-2026-04-01}"
log(){ x="$(date -u '+%Y-%m-%dT%H:%M:%SZ') $*"; echo "$x"; echo "$x" >> "$L" 2>/dev/null
  [ "$(wc -c < "$L" 2>/dev/null || echo 0)" -gt 1000000 ] && tail -c 200000 "$L" > "$L.t" && mv "$L.t" "$L"; }
command -v jq >/dev/null 2>&1 || apk add --no-cache curl jq >/dev/null 2>&1
for b in curl jq docker; do command -v "$b" >/dev/null 2>&1 || { echo "FATAL: net $b"; exit 1; }; done
[ -f "$P" ] || { echo "FATAL: net prompt.txt"; exit 1; }
[ -f "$S" ] || echo 0 > "$S"
[ -f "$Q" ] || : > "$Q"
# Append one press to the MCP-side queue and cap that file, in a single exec.
PUSH='cat >> '"$MCP_QUEUE"'; L=$(wc -l < '"$MCP_QUEUE"' 2>/dev/null || echo 0); [ "$L" -gt 500 ] && { tail -n 200 '"$MCP_QUEUE"' > '"$MCP_QUEUE"'.t && mv '"$MCP_QUEUE"'.t '"$MCP_QUEUE"'; chmod 644 '"$MCP_QUEUE"'; }; true'
docker exec -u root "$MCP_CTR" sh -c "[ -f $MCP_QUEUE ] || install -m 644 /dev/null $MCP_QUEUE" >/dev/null 2>&1 || log "cb queue: $MCP_CTR unreachable at startup"
send(){ curl -s -m 40 -X POST "$API/sendMessage" --data-urlencode "chat_id=$CHAT" --data-urlencode "text=$1" -d disable_web_page_preview=true >/dev/null 2>&1; }
# Which session registered these buttons at send time, if any. Newest wins, and
# only a well-formed session id is ever returned.
resume_lookup(){
  docker exec -u root "$MCP_CTR" cat "$MCP_RESUME" 2>/dev/null \
    | jq -r --arg c "$1" --arg m "$2" \
        'select((.chat_id|tostring) == $c and (.message_id|tostring) == $m) | .session_id' 2>/dev/null \
    | grep -E '^(session|cse)_[A-Za-z0-9]{10,48}$' | tail -n 1
}
# Continue that session by queueing the press into it. `--cloud` rejects API-key
# authentication, so when the container's own `claude` is signed in that way,
# RESUME_OAUTH_TOKEN (from `claude setup-token`) is injected for this one exec
# and the container's key is blanked out alongside it, since a key set in the
# environment takes precedence over an account login.
resume(){
  [ -n "$RESUME_CTR" ] || return 1
  if [ -n "$RESUME_TOKEN" ]; then
    printf '%s' "$2" | timeout 60 docker exec -i \
      -e ANTHROPIC_API_KEY= -e ANTHROPIC_AUTH_TOKEN= \
      -e CLAUDE_CODE_OAUTH_TOKEN="$RESUME_TOKEN" \
      "$RESUME_CTR" claude -p --cloud "$1" >/dev/null 2>&1
  else
    printf '%s' "$2" | timeout 60 docker exec -i "$RESUME_CTR" claude -p --cloud "$1" >/dev/null 2>&1
  fi
}
# Fire the Routine API trigger with the press as its payload. Starts a NEW
# session; the payload carries session_id so that session can hand the press on.
fire(){
  [ -n "$FIRE_URL" ] && [ -n "$FIRE_TOKEN" ] || return 1
  n=0
  while [ "$n" -lt 2 ]; do
    n=$((n+1))
    r="$(curl -s -m 30 -X POST "$FIRE_URL" \
      -H "Authorization: Bearer $FIRE_TOKEN" \
      -H "anthropic-beta: $FIRE_BETA" \
      -H "anthropic-version: 2023-06-01" \
      -H "Content-Type: application/json" \
      -d "$(jq -nc --arg t "$1" '{text: $t}')" 2>/dev/null)"
    sid="$(echo "$r" | jq -r '.claude_code_session_id // empty' 2>/dev/null)"
    [ -n "$sid" ] && { log "fire: $sid"; return 0; }
    [ "$n" -lt 2 ] && sleep 3
  done
  log "fire failed: $(echo "$r" | head -c 200)"
  return 1
}
log "relay up: brain=$BRAIN chat=$CHAT model=$MODEL timeout=${TMO}s cb->$MCP_CTR:$MCP_QUEUE resume=${RESUME_CTR:-off} fire=$([ -n "$FIRE_URL" ] && [ -n "$FIRE_TOKEN" ] && echo on || echo off)"
while true; do
  O="$(cat "$S" 2>/dev/null || echo 0)"
  R="$(curl -s -m 70 "$API/getUpdates?offset=$O&timeout=50&allowed_updates=%5B%22message%22%2C%22callback_query%22%5D" 2>/dev/null)"
  [ -z "$R" ] && { sleep 3; continue; }
  echo "$R" | jq -e '.ok == true' >/dev/null 2>&1 || { log "tg api: $(echo "$R" | head -c 200)"; sleep 10; continue; }
  N="$(echo "$R" | jq '.result | length')"
  [ "$N" -eq 0 ] && continue
  i=0
  while [ "$i" -lt "$N" ]; do
    U="$(echo "$R" | jq -c ".result[$i]")"; i=$((i+1))
    echo $(( $(echo "$U" | jq '.update_id') + 1 )) > "$S"
    # Inline-button press. This relay owns the only getUpdates stream this bot
    # gets, so it answers the press itself, queues it for the MCP server, and
    # delivers it to a session. Presses never go to the brain.
    CQ="$(echo "$U" | jq -r '.callback_query.id // empty')"
    if [ -n "$CQ" ]; then
      CFROM="$(echo "$U" | jq -r '.callback_query.from.id // empty')"
      CDATA="$(echo "$U" | jq -r '.callback_query.data // empty')"
      CCHAT="$(echo "$U" | jq -r '.callback_query.message.chat.id // empty')"
      CMSG="$(echo "$U" | jq -r '.callback_query.message.message_id // empty')"
      if [ "$CFROM" != "$CHAT" ]; then log "cb skip from $CFROM: $(echo "$CDATA" | head -c 60)"; continue; fi
      curl -s -m 10 -X POST "$API/answerCallbackQuery" --data-urlencode "callback_query_id=$CQ" --data-urlencode "text=Принято" >/dev/null 2>&1
      echo "$U" | jq -c '. + {answered: true}' >> "$Q"
      [ "$(wc -l < "$Q" 2>/dev/null || echo 0)" -gt 500 ] && tail -n 200 "$Q" > "$Q.t" && mv "$Q.t" "$Q"
      tail -n 1 "$Q" | docker exec -i -u root "$MCP_CTR" sh -c "$PUSH" >/dev/null 2>&1 \
        || log "cb (queue push to $MCP_CTR failed): $(echo "$CDATA" | head -c 60)"
      SESS="$(resume_lookup "$CCHAT" "$CMSG")"
      PRESS="$(echo "$U" | jq -c --arg s "$SESS" '{update_id, data: .callback_query.data, from_id: .callback_query.from.id, chat_id: .callback_query.message.chat.id, message_id: .callback_query.message.message_id, callback_query_id: .callback_query.id, session_id: (if $s == "" then null else $s end)}')"
      if [ -n "$SESS" ] && resume "$SESS" "$PRESS"; then
        log "cb: $(echo "$CDATA" | head -c 60) -> resumed $SESS"
      elif fire "$PRESS"; then
        log "cb: $(echo "$CDATA" | head -c 60) -> fired${SESS:+ (target $SESS)}"
      else
        log "cb: $(echo "$CDATA" | head -c 60) -> queued${SESS:+ (session $SESS)}"
      fi
      continue
    fi
    CID="$(echo "$U" | jq -r '.message.chat.id // empty')"
    T="$(echo "$U" | jq -r '.message.text // .message.caption // empty')"
    [ "$CID" = "$CHAT" ] || { log "skip chat $CID"; continue; }
    [ "$(echo "$U" | jq -r '.message.from.is_bot // false')" = "true" ] && { log "skip bot"; continue; }
    [ -z "$T" ] && { send "Пока понимаю только текст — картинки, голосовые и файлы не умею."; continue; }
    log "msg: $(echo "$T" | head -c 150)"
    ( while :; do curl -s -m 10 -X POST "$API/sendChatAction" -d "chat_id=$CHAT" -d action=typing >/dev/null 2>&1; sleep 4; done ) &
    TYPER=$!
    ST="$(date +%s)"
    OUT="$(printf '%s\n%s\n--- KONEC ---' "$(cat "$P")" "$T" | timeout "$TMO" docker exec -i -e IS_SANDBOX=1 -w /tmp "$BRAIN" claude -p --model "$MODEL" --permission-mode bypassPermissions 2>&1)"
    RC=$?
    kill "$TYPER" 2>/dev/null; wait "$TYPER" 2>/dev/null
    EL=$(( $(date +%s) - ST ))
    OUT="$(printf '%s\n' "$OUT" | grep -v 'Permission allow rule')"
    if [ "$RC" -ne 0 ] || [ -z "$(printf '%s' "$OUT" | tr -d '[:space:]')" ]; then
      log "FAIL rc=$RC ${EL}s: $(printf '%s' "$OUT" | tail -c 400)"
      send "$(printf 'Не смог обработать (код %s, %s с). Подробности в логе релея.' "$RC" "$EL")"
    else
      log "ok ${EL}s"
      send "$(printf '%s' "$OUT" | head -c 3500)"
    fi
  done
done
