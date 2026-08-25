from __future__ import annotations

import json

import httpx
import pytest

from better_telegram_mcp.backends.base import ModeError
from better_telegram_mcp.backends.bot_backend import BotBackend, TelegramAPIError
from better_telegram_mcp.backends.inline_keyboard import InlineKeyboardError

_TOKEN = "123456:AAEAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

_BUTTONS = [
    [
        {"text": "✅ Yes", "data": "DEC-12:yes"},
        {"text": "✖️ No", "data": "DEC-12:no"},
    ]
]


def _callback_update(update_id: int, data: str = "DEC-12:yes") -> dict:
    return {
        "update_id": update_id,
        "callback_query": {
            "id": f"cbq-{update_id}",
            "from": {"id": 375465077, "username": "owner"},
            "message": {"message_id": 4711, "date": 1756130591, "chat": {"id": 42}},
            "data": data,
        },
    }


def _bot(responses: list, *, cursor_path=None) -> tuple[BotBackend, list]:
    """BotBackend whose transport replays `responses` and records requests."""
    calls: list[tuple[str, dict]] = []
    queue = list(responses)

    def handler(request: httpx.Request) -> httpx.Response:
        method = request.url.path.rsplit("/", 1)[-1]
        calls.append((method, json.loads(request.content)))
        result = queue.pop(0) if queue else True
        if isinstance(result, tuple):  # (description, error_code) -> API error
            description, code = result
            return httpx.Response(
                200, json={"ok": False, "description": description, "error_code": code}
            )
        return httpx.Response(200, json={"ok": True, "result": result})

    bot = BotBackend(_TOKEN, cursor_path=cursor_path)
    bot._client = httpx.AsyncClient(
        transport=httpx.MockTransport(handler), base_url=bot._base_url
    )
    return bot, calls


# --- send / edit with buttons ---


async def test_send_message_attaches_inline_keyboard():
    bot, calls = _bot([{"message_id": 1}])
    await bot.send_message(42, "Question?", buttons=_BUTTONS)

    method, payload = calls[0]
    assert method == "sendMessage"
    assert payload["reply_markup"] == {
        "inline_keyboard": [
            [
                {"text": "✅ Yes", "callback_data": "DEC-12:yes"},
                {"text": "✖️ No", "callback_data": "DEC-12:no"},
            ]
        ]
    }


async def test_send_message_without_buttons_sends_no_markup():
    bot, calls = _bot([{"message_id": 1}])
    await bot.send_message(42, "plain")
    assert "reply_markup" not in calls[0][1]


async def test_send_message_rejects_oversized_callback_data():
    bot, calls = _bot([{"message_id": 1}])
    with pytest.raises(InlineKeyboardError):
        await bot.send_message(42, "q", buttons=[[{"text": "x", "data": "a" * 65}]])
    assert calls == []  # rejected before the request leaves the process


async def test_edit_message_buttons_strips_keyboard():
    bot, calls = _bot([{"message_id": 4711}])
    await bot.edit_message_buttons(42, 4711, [])

    method, payload = calls[0]
    assert method == "editMessageReplyMarkup"
    assert payload["reply_markup"] == {"inline_keyboard": []}


async def test_edit_message_can_replace_text_and_keyboard():
    bot, calls = _bot([{"message_id": 4711}])
    await bot.edit_message(42, 4711, "done — ✅ yes", buttons=[])
    method, payload = calls[0]
    assert method == "editMessageText"
    assert payload["reply_markup"] == {"inline_keyboard": []}


# --- callback queries ---


async def test_get_callback_queries_returns_and_confirms():
    bot, calls = _bot([[_callback_update(100), _callback_update(101)], []])
    updates = await bot.get_callback_queries()

    assert [u["update_id"] for u in updates] == [100, 101]
    poll, confirm = calls
    assert poll[0] == "getUpdates"
    assert "offset" not in poll[1]  # first poll starts wherever Telegram is
    assert poll[1]["allowed_updates"] == ["callback_query"]
    # The confirming call is what makes Telegram drop the delivered updates.
    assert confirm[0] == "getUpdates"
    assert confirm[1]["offset"] == 102


async def test_cursor_advances_between_calls():
    bot, calls = _bot([[_callback_update(100)], [], [], []])
    await bot.get_callback_queries()
    await bot.get_callback_queries()

    assert calls[2][1]["offset"] == 101  # second poll resumes past the first press


async def test_stale_since_id_cannot_replay():
    bot, calls = _bot([[_callback_update(100)], [], [], []])
    await bot.get_callback_queries()
    # A caller replaying an old cursor must not get the press a second time.
    await bot.get_callback_queries(since_id=5)
    assert calls[2][1]["offset"] == 101


async def test_since_id_ahead_of_cursor_is_honoured():
    bot, calls = _bot([[]])
    await bot.get_callback_queries(since_id=500)
    assert calls[0][1]["offset"] == 501


async def test_cursor_persists_across_restart(tmp_path):
    cursor = tmp_path / "default.callbacks.json"
    bot, _ = _bot([[_callback_update(100)], []], cursor_path=cursor)
    await bot.get_callback_queries()
    assert json.loads(cursor.read_text())["offset"] == 101

    restarted, calls = _bot([[]], cursor_path=cursor)
    await restarted.get_callback_queries()
    assert calls[0][1]["offset"] == 101


async def test_non_callback_updates_still_advance_the_cursor():
    bot, calls = _bot([[{"update_id": 200, "message": {"message_id": 1}}], [], []])
    assert await bot.get_callback_queries() == []
    assert calls[1][1]["offset"] == 201


async def test_limit_capped_at_bot_api_maximum():
    bot, calls = _bot([[]])
    await bot.get_callback_queries(limit=500)
    assert calls[0][1]["limit"] == 100


async def test_webhook_conflict_explains_the_fix():
    bot, _ = _bot(
        [("Conflict: can't use getUpdates method while webhook is active", 409)]
    )
    with pytest.raises(TelegramAPIError, match="deleteWebhook"):
        await bot.get_callback_queries()


async def test_failed_confirmation_keeps_local_cursor(tmp_path):
    cursor = tmp_path / "default.callbacks.json"
    bot, calls = _bot(
        [[_callback_update(100)], ("Too Many Requests: retry after 5", 429), []],
        cursor_path=cursor,
    )
    await bot.get_callback_queries()
    # Telegram may re-deliver update 100, but the local cursor skips past it.
    await bot.get_callback_queries()
    assert calls[2][1]["offset"] == 101


async def test_answer_callback_query():
    bot, calls = _bot([True])
    assert await bot.answer_callback_query("cbq-100", text="Accepted") is True
    assert calls[0] == (
        "answerCallbackQuery",
        {"callback_query_id": "cbq-100", "text": "Accepted", "show_alert": False},
    )


# --- user mode has no callback queries ---


async def test_user_backend_rejects_buttons(mock_user_backend):
    from better_telegram_mcp.backends.user_backend import UserBackend

    with pytest.raises(ModeError, match="requires bot mode"):
        await UserBackend.send_message(mock_user_backend, 42, "q", buttons=_BUTTONS)


async def test_callback_queries_require_bot_mode(mock_user_backend):
    from better_telegram_mcp.backends.base import TelegramBackend

    with pytest.raises(ModeError, match="requires bot mode"):
        await TelegramBackend.get_callback_queries(mock_user_backend)
