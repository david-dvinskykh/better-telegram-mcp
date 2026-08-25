import logging
import re
from collections.abc import Callable, Coroutine
from datetime import UTC, datetime
from typing import Any

from pydantic import BaseModel

from ..backends.base import ModeError, TelegramBackend
from ..utils.formatting import err, ok, safe_error

logger = logging.getLogger(__name__)

# answerCallbackQuery notification text is capped at 200 characters.
MAX_ANSWER_TEXT = 200


class MessagesArgs(BaseModel):
    action: str
    chat_id: str | int | None = None
    text: str | None = None
    message_id: int | None = None
    reply_to: int | None = None
    parse_mode: str | None = None
    from_chat: str | int | None = None
    to_chat: str | int | None = None
    emoji: str | None = None
    query: str | None = None
    limit: int = 20
    offset_id: int | None = None
    # Inline buttons + callback queries (bot mode)
    buttons: list[Any] | None = None
    since_id: int | None = None
    callback_query_id: str | None = None
    answer_text: str | None = None
    show_alert: bool = False
    auto_answer: bool = True
    allowed_from_ids: list[int] | None = None
    data_pattern: str | None = None
    resume_session: str | None = None
    peek: bool = False


async def _handle_send(backend: TelegramBackend, args: MessagesArgs) -> dict[str, Any]:
    if not args.chat_id or not args.text:
        return err(
            "'send' requires chat_id and text. "
            "chat_id: positive int (user), negative int (group), or @username. "
            "Optional parse_mode: HTML, MarkdownV2, or Markdown. "
            'Optional buttons: [[{"text": "Yes", "data": "DEC-12:yes"}]].'
        )
    # Keep the no-buttons call shape untouched so backends that predate inline
    # keyboards (and every existing caller) behave exactly as before.
    extra: dict[str, Any] = {} if args.buttons is None else {"buttons": args.buttons}
    result = await backend.send_message(
        args.chat_id,
        args.text,
        reply_to=args.reply_to,
        parse_mode=args.parse_mode,
        **extra,
    )

    if args.resume_session:
        # Tie the buttons to the asking session, so the press can be delivered
        # back into it instead of starting a fresh one.
        message_id = result.get("message_id") if isinstance(result, dict) else None
        if message_id is None:
            return ok({**result, "resume_registered": False})
        registered = await backend.record_resume_session(
            args.chat_id, message_id, args.resume_session
        )
        return ok({**result, "resume_registered": registered})
    return ok(result)


async def _handle_edit(backend: TelegramBackend, args: MessagesArgs) -> dict[str, Any]:
    if (
        not args.chat_id
        or args.message_id is None
        or not (args.text or args.buttons is not None)
    ):
        return err(
            "'edit' requires chat_id, message_id, and text and/or buttons. "
            "Optional parse_mode: HTML, MarkdownV2, or Markdown. "
            "buttons=[] strips the inline keyboard from the message."
        )
    if not args.text:
        result = await backend.edit_message_buttons(
            args.chat_id, args.message_id, args.buttons
        )
        return ok(result)
    extra: dict[str, Any] = {} if args.buttons is None else {"buttons": args.buttons}
    result = await backend.edit_message(
        args.chat_id,
        args.message_id,
        args.text,
        parse_mode=args.parse_mode,
        **extra,
    )
    return ok(result)


async def _handle_delete(
    backend: TelegramBackend, args: MessagesArgs
) -> dict[str, Any]:
    if not args.chat_id or args.message_id is None:
        return err("'delete' requires chat_id and message_id")
    result = await backend.delete_message(args.chat_id, args.message_id)
    return ok({"deleted": result})


async def _handle_forward(
    backend: TelegramBackend, args: MessagesArgs
) -> dict[str, Any]:
    if not args.from_chat or not args.to_chat or args.message_id is None:
        return err("'forward' requires from_chat, to_chat, and message_id")
    result = await backend.forward_message(
        args.from_chat, args.to_chat, args.message_id
    )
    return ok(result)


async def _handle_pin(backend: TelegramBackend, args: MessagesArgs) -> dict[str, Any]:
    if not args.chat_id or args.message_id is None:
        return err("'pin' requires chat_id and message_id")
    result = await backend.pin_message(args.chat_id, args.message_id)
    return ok({"pinned": result})


async def _handle_react(backend: TelegramBackend, args: MessagesArgs) -> dict[str, Any]:
    if not args.chat_id or args.message_id is None or not args.emoji:
        return err("'react' requires chat_id, message_id, and emoji")
    result = await backend.react_to_message(args.chat_id, args.message_id, args.emoji)
    return ok({"reacted": result})


async def _handle_search(
    backend: TelegramBackend, args: MessagesArgs
) -> dict[str, Any]:
    if not args.query:
        return err("'search' requires query")
    results = await backend.search_messages(
        args.query, chat_id=args.chat_id, limit=args.limit
    )
    return ok({"messages": results, "count": len(results)})


async def _handle_history(
    backend: TelegramBackend, args: MessagesArgs
) -> dict[str, Any]:
    if not args.chat_id:
        return err("'history' requires chat_id")
    results = await backend.get_history(
        args.chat_id, limit=args.limit, offset_id=args.offset_id
    )
    return ok({"messages": results, "count": len(results)})


def _iso_utc(timestamp: Any) -> str | None:
    """Format a Bot API unix timestamp as an ISO-8601 UTC string."""
    if not isinstance(timestamp, (int, float)):
        return None
    return datetime.fromtimestamp(timestamp, UTC).isoformat().replace("+00:00", "Z")


def _normalize_callback(update: dict[str, Any], polled_at: str) -> dict[str, Any]:
    query = update.get("callback_query") or {}
    sender = query.get("from") or {}
    message = query.get("message") or {}
    chat = message.get("chat") or {}
    return {
        "update_id": update.get("update_id"),
        "callback_query_id": query.get("id"),
        "data": query.get("data"),
        "from_id": sender.get("id"),
        "from_username": sender.get("username"),
        "chat_id": chat.get("id"),
        "message_id": message.get("message_id"),
        # The Bot API carries no press timestamp; `message_date` is when the
        # question was posted, `received_at` when this server polled it.
        "message_date": _iso_utc(message.get("date")),
        "received_at": polled_at,
        # True when whoever queued this press already acknowledged it (queue
        # mode); such a callback_query_id is spent and must not be answered again.
        "answered": bool(update.get("answered")),
    }


async def _handle_callbacks(
    backend: TelegramBackend, args: MessagesArgs
) -> dict[str, Any]:
    """Read inline-button presses (bot mode) and acknowledge them."""
    if args.answer_text and len(args.answer_text) > MAX_ANSWER_TEXT:
        return err(f"answer_text exceeds {MAX_ANSWER_TEXT} characters")

    if args.data_pattern:
        try:
            pattern = re.compile(args.data_pattern)
        except re.error as e:
            return err(f"Invalid data_pattern regex: {e}")
    else:
        pattern = None

    if args.message_id is not None and not args.peek:
        return err(
            "'callbacks' with message_id only makes sense together with "
            "peek=true: a filtered consuming read would drop every other "
            "press instead of leaving it for whoever acts on it."
        )

    allowed = set(args.allowed_from_ids or ())
    updates = await backend.get_callback_queries(
        since_id=args.since_id, limit=args.limit, consume=not args.peek
    )
    polled_at = datetime.now(UTC).isoformat().replace("+00:00", "Z")

    accepted: list[dict[str, Any]] = []
    ignored: list[dict[str, Any]] = []
    cursor = args.since_id
    for update in updates:
        entry = _normalize_callback(update, polled_at)
        if isinstance(entry["update_id"], int):
            cursor = max(cursor or 0, entry["update_id"])

        # A watcher waiting on one question ignores presses on the others.
        if args.message_id is not None and entry["message_id"] != args.message_id:
            continue

        reason = None
        if not entry["callback_query_id"] or not isinstance(entry["data"], str):
            reason = "malformed"
        elif allowed and entry["from_id"] not in allowed:
            reason = "unauthorized_sender"
        elif pattern is not None and not pattern.fullmatch(entry["data"]):
            reason = "data_pattern_mismatch"

        if reason:
            logger.warning(
                "Ignoring callback_query %s from user %s: %s",
                entry["update_id"],
                entry["from_id"],
                reason,
            )
            ignored.append({**entry, "reason": reason})
            continue

        if args.auto_answer and not args.peek and not entry["answered"]:
            # An unanswered query leaves a spinner on the button in every
            # client, so acknowledge before handing the press to the caller.
            try:
                await backend.answer_callback_query(
                    entry["callback_query_id"],
                    text=(args.answer_text or None),
                    show_alert=args.show_alert,
                )
                entry["answered"] = True
            except Exception as e:
                # A stale query id must not swallow the decision itself.
                logger.warning(
                    "answerCallbackQuery failed for %s: %s",
                    entry["update_id"],
                    type(e).__name__,
                )
        # Which session asked, when the sender registered one at send time.
        if entry["chat_id"] is not None and entry["message_id"] is not None:
            entry["session_id"] = await backend.lookup_resume_session(
                entry["chat_id"], entry["message_id"]
            )
        accepted.append(entry)

    return ok(
        {
            "callbacks": accepted,
            "count": len(accepted),
            "ignored": ignored,
            "ignored_count": len(ignored),
            # A peek leaves every press pending, so the reader knows nothing
            # was taken from whoever acts on it.
            "peek": args.peek,
            # Pass this back as since_id on the next call; the server keeps the
            # same cursor itself, so a repeated call never replays a press.
            "cursor": cursor,
        }
    )


async def _handle_answer(
    backend: TelegramBackend, args: MessagesArgs
) -> dict[str, Any]:
    if not args.callback_query_id:
        return err(
            "'answer' requires callback_query_id (from a 'callbacks' result). "
            "Optional answer_text (<=200 chars) and show_alert."
        )
    text = args.answer_text
    if text and len(text) > MAX_ANSWER_TEXT:
        return err(f"answer_text exceeds {MAX_ANSWER_TEXT} characters")
    result = await backend.answer_callback_query(
        args.callback_query_id, text=text, show_alert=args.show_alert
    )
    return ok({"answered": bool(result)})


_ACTION_HANDLERS: dict[
    str, Callable[[TelegramBackend, MessagesArgs], Coroutine[Any, Any, dict[str, Any]]]
] = {
    "send": _handle_send,
    "edit": _handle_edit,
    "delete": _handle_delete,
    "forward": _handle_forward,
    "pin": _handle_pin,
    "react": _handle_react,
    "search": _handle_search,
    "history": _handle_history,
    "callbacks": _handle_callbacks,
    "answer": _handle_answer,
}


async def handle_messages(
    backend: TelegramBackend,
    args: MessagesArgs,
) -> dict[str, Any]:
    try:
        handler = _ACTION_HANDLERS.get(args.action)
        if handler is None:
            import difflib

            valid = sorted(_ACTION_HANDLERS)
            closest = difflib.get_close_matches(args.action, valid, n=1)
            suggestion = f" Did you mean '{closest[0]}'?" if closest else ""
            return err(
                f"Unknown action '{args.action}'.{suggestion} Valid: {'|'.join(valid)}"
            )
        return await handler(backend, args)
    except ModeError as e:
        return err(str(e))
    except Exception as e:
        return safe_error(e)
