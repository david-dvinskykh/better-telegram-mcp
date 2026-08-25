from __future__ import annotations

import pytest

from better_telegram_mcp.backends.base import ModeError
from better_telegram_mcp.tools.messages import MessagesArgs, handle_messages


@pytest.mark.asyncio
async def test_send(mock_backend):
    result = await handle_messages(
        mock_backend, MessagesArgs(action="send", chat_id=123, text="hello")
    )
    assert result["message_id"] == 1
    mock_backend.send_message.assert_awaited_once_with(
        123, "hello", reply_to=None, parse_mode=None
    )


@pytest.mark.asyncio
async def test_send_with_reply_and_parse_mode(mock_backend):
    result = await handle_messages(
        mock_backend,
        MessagesArgs(
            action="send",
            chat_id=123,
            text="hi",
            reply_to=5,
            parse_mode="HTML",
        ),
    )
    assert result["message_id"] == 1
    mock_backend.send_message.assert_awaited_once_with(
        123, "hi", reply_to=5, parse_mode="HTML"
    )


@pytest.mark.asyncio
async def test_send_missing_params(mock_backend):
    result = await handle_messages(mock_backend, MessagesArgs(action="send"))
    assert "error" in result

    result = await handle_messages(
        mock_backend, MessagesArgs(action="send", chat_id=123)
    )
    assert "error" in result


@pytest.mark.asyncio
async def test_edit(mock_backend):
    result = await handle_messages(
        mock_backend,
        MessagesArgs(action="edit", chat_id=123, message_id=1, text="edited"),
    )
    assert result["message_id"] == 1
    mock_backend.edit_message.assert_awaited_once_with(
        123, 1, "edited", parse_mode=None
    )


@pytest.mark.asyncio
async def test_edit_missing_params(mock_backend):
    result = await handle_messages(
        mock_backend, MessagesArgs(action="edit", chat_id=123, text="x")
    )
    assert "error" in result

    result = await handle_messages(
        mock_backend, MessagesArgs(action="edit", chat_id=123, message_id=1)
    )
    assert "error" in result


@pytest.mark.asyncio
async def test_delete(mock_backend):
    result = await handle_messages(
        mock_backend, MessagesArgs(action="delete", chat_id=123, message_id=1)
    )
    assert result["deleted"] is True


@pytest.mark.asyncio
async def test_delete_missing_params(mock_backend):
    result = await handle_messages(
        mock_backend, MessagesArgs(action="delete", chat_id=123)
    )
    assert "error" in result


@pytest.mark.asyncio
async def test_forward(mock_backend):
    result = await handle_messages(
        mock_backend,
        MessagesArgs(
            action="forward",
            from_chat=1,
            to_chat=2,
            message_id=10,
        ),
    )
    assert result["message_id"] == 2


@pytest.mark.asyncio
async def test_forward_missing_params(mock_backend):
    result = await handle_messages(
        mock_backend, MessagesArgs(action="forward", from_chat=1, to_chat=2)
    )
    assert "error" in result


@pytest.mark.asyncio
async def test_pin(mock_backend):
    result = await handle_messages(
        mock_backend, MessagesArgs(action="pin", chat_id=123, message_id=1)
    )
    assert result["pinned"] is True


@pytest.mark.asyncio
async def test_pin_missing_params(mock_backend):
    result = await handle_messages(
        mock_backend, MessagesArgs(action="pin", chat_id=123)
    )
    assert "error" in result


@pytest.mark.asyncio
async def test_react(mock_backend):
    result = await handle_messages(
        mock_backend,
        MessagesArgs(action="react", chat_id=123, message_id=1, emoji="👍"),
    )
    assert result["reacted"] is True


@pytest.mark.asyncio
async def test_react_missing_params(mock_backend):
    result = await handle_messages(
        mock_backend, MessagesArgs(action="react", chat_id=123, message_id=1)
    )
    assert "error" in result


@pytest.mark.asyncio
async def test_search(mock_backend):
    result = await handle_messages(
        mock_backend, MessagesArgs(action="search", query="test", limit=10)
    )
    assert result["messages"] == []
    assert result["count"] == 0


@pytest.mark.asyncio
async def test_search_missing_params(mock_backend):
    result = await handle_messages(mock_backend, MessagesArgs(action="search"))
    assert "error" in result


@pytest.mark.asyncio
async def test_history(mock_backend):
    result = await handle_messages(
        mock_backend,
        MessagesArgs(action="history", chat_id=123, limit=5, offset_id=100),
    )
    assert result["messages"] == []
    assert result["count"] == 0


@pytest.mark.asyncio
async def test_history_missing_params(mock_backend):
    result = await handle_messages(mock_backend, MessagesArgs(action="history"))
    assert "error" in result


@pytest.mark.asyncio
async def test_unknown_action(mock_backend):
    result = await handle_messages(mock_backend, MessagesArgs(action="unknown"))
    assert "error" in result
    assert "Unknown action" in result["error"]


@pytest.mark.asyncio
async def test_mode_error(mock_backend):
    mock_backend.search_messages.side_effect = ModeError("user")
    result = await handle_messages(
        mock_backend, MessagesArgs(action="search", query="test")
    )
    assert "error" in result
    assert "user mode" in result["error"]


@pytest.mark.asyncio
async def test_general_exception(mock_backend):
    mock_backend.send_message.side_effect = RuntimeError("boom")
    result = await handle_messages(
        mock_backend, MessagesArgs(action="send", chat_id=1, text="x")
    )
    assert "error" in result
    assert "RuntimeError" in result["error"]


@pytest.mark.asyncio
async def test_unknown_action_suggestion(mock_backend):
    # 'sendd' should suggest 'send'
    result = await handle_messages(mock_backend, MessagesArgs(action="sendd"))
    assert "error" in result
    assert "Unknown action 'sendd'." in result["error"]
    assert "Did you mean 'send'?" in result["error"]
    assert (
        "Valid: answer|callbacks|delete|edit|forward|history|pin|react|search|send"
        in result["error"]
    )


# --- Inline buttons + callback queries ---

_BUTTONS = [[{"text": "✅ Yes", "data": "DEC-12:yes"}]]


def _callback(
    update_id: int = 100, *, from_id: int = 375465077, data: str = "DEC-12:yes"
):
    return {
        "update_id": update_id,
        "callback_query": {
            "id": f"cbq-{update_id}",
            "from": {"id": from_id, "username": "owner"},
            "message": {"message_id": 4711, "date": 1756130591, "chat": {"id": 42}},
            "data": data,
        },
    }


@pytest.mark.asyncio
async def test_send_with_buttons(mock_backend):
    await handle_messages(
        mock_backend,
        MessagesArgs(action="send", chat_id=42, text="q", buttons=_BUTTONS),
    )
    mock_backend.send_message.assert_awaited_once_with(
        42, "q", reply_to=None, parse_mode=None, buttons=_BUTTONS
    )


@pytest.mark.asyncio
async def test_edit_buttons_only_strips_keyboard(mock_backend):
    result = await handle_messages(
        mock_backend,
        MessagesArgs(action="edit", chat_id=42, message_id=4711, buttons=[]),
    )
    assert result["message_id"] == 1
    mock_backend.edit_message_buttons.assert_awaited_once_with(42, 4711, [])
    mock_backend.edit_message.assert_not_awaited()


@pytest.mark.asyncio
async def test_edit_text_and_buttons(mock_backend):
    await handle_messages(
        mock_backend,
        MessagesArgs(
            action="edit", chat_id=42, message_id=4711, text="q — ✅ yes", buttons=[]
        ),
    )
    mock_backend.edit_message.assert_awaited_once_with(
        42, 4711, "q — ✅ yes", parse_mode=None, buttons=[]
    )


@pytest.mark.asyncio
async def test_edit_without_text_or_buttons_errors(mock_backend):
    result = await handle_messages(
        mock_backend, MessagesArgs(action="edit", chat_id=42, message_id=4711)
    )
    assert "error" in result


@pytest.mark.asyncio
async def test_callbacks_returns_normalized_presses(mock_backend):
    mock_backend.get_callback_queries.return_value = [_callback(100)]
    result = await handle_messages(
        mock_backend, MessagesArgs(action="callbacks", since_id=99)
    )

    assert result["count"] == 1
    press = result["callbacks"][0]
    assert press["update_id"] == 100
    assert press["data"] == "DEC-12:yes"
    assert press["from_id"] == 375465077
    assert press["chat_id"] == 42
    assert press["message_id"] == 4711
    assert press["callback_query_id"] == "cbq-100"
    assert press["message_date"] == "2025-08-25T14:03:11Z"
    assert result["cursor"] == 100
    mock_backend.get_callback_queries.assert_awaited_once_with(since_id=99, limit=20)


@pytest.mark.asyncio
async def test_callbacks_auto_answers_each_press(mock_backend):
    mock_backend.get_callback_queries.return_value = [_callback(100)]
    result = await handle_messages(
        mock_backend, MessagesArgs(action="callbacks", answer_text="Accepted")
    )

    mock_backend.answer_callback_query.assert_awaited_once_with(
        "cbq-100", text="Accepted", show_alert=False
    )
    assert result["callbacks"][0]["answered"] is True


@pytest.mark.asyncio
async def test_callbacks_auto_answer_can_be_disabled(mock_backend):
    mock_backend.get_callback_queries.return_value = [_callback(100)]
    result = await handle_messages(
        mock_backend, MessagesArgs(action="callbacks", auto_answer=False)
    )
    mock_backend.answer_callback_query.assert_not_awaited()
    assert result["callbacks"][0]["answered"] is False


@pytest.mark.asyncio
async def test_callbacks_still_returns_press_when_answer_fails(mock_backend):
    mock_backend.get_callback_queries.return_value = [_callback(100)]
    mock_backend.answer_callback_query.side_effect = RuntimeError("query too old")
    result = await handle_messages(mock_backend, MessagesArgs(action="callbacks"))

    assert result["count"] == 1
    assert result["callbacks"][0]["answered"] is False


@pytest.mark.asyncio
async def test_callbacks_ignores_unauthorized_sender(mock_backend):
    mock_backend.get_callback_queries.return_value = [
        _callback(100, from_id=999),
        _callback(101),
    ]
    result = await handle_messages(
        mock_backend,
        MessagesArgs(action="callbacks", allowed_from_ids=[375465077]),
    )

    assert [p["update_id"] for p in result["callbacks"]] == [101]
    assert result["ignored"][0]["reason"] == "unauthorized_sender"
    assert result["ignored_count"] == 1
    # An ignored press is neither answered nor left behind the cursor.
    mock_backend.answer_callback_query.assert_awaited_once_with(
        "cbq-101", text=None, show_alert=False
    )
    assert result["cursor"] == 101


@pytest.mark.asyncio
async def test_callbacks_enforces_data_pattern(mock_backend):
    mock_backend.get_callback_queries.return_value = [
        _callback(100, data="rm -rf /"),
        _callback(101, data="DEC-12:later"),
    ]
    result = await handle_messages(
        mock_backend,
        MessagesArgs(action="callbacks", data_pattern=r"DEC-\d+:(yes|no|later)"),
    )

    assert [p["data"] for p in result["callbacks"]] == ["DEC-12:later"]
    assert result["ignored"][0]["reason"] == "data_pattern_mismatch"


@pytest.mark.asyncio
async def test_callbacks_rejects_invalid_data_pattern(mock_backend):
    result = await handle_messages(
        mock_backend, MessagesArgs(action="callbacks", data_pattern="DEC-[")
    )
    assert "error" in result
    mock_backend.get_callback_queries.assert_not_awaited()


@pytest.mark.asyncio
async def test_callbacks_drops_malformed_update(mock_backend):
    mock_backend.get_callback_queries.return_value = [
        {"update_id": 100, "callback_query": {"from": {"id": 375465077}}}
    ]
    result = await handle_messages(mock_backend, MessagesArgs(action="callbacks"))
    assert result["count"] == 0
    assert result["ignored"][0]["reason"] == "malformed"


@pytest.mark.asyncio
async def test_callbacks_requires_bot_mode(mock_user_backend):
    from better_telegram_mcp.backends.base import TelegramBackend

    mock_user_backend.get_callback_queries = (
        TelegramBackend.get_callback_queries.__get__(mock_user_backend)
    )
    result = await handle_messages(mock_user_backend, MessagesArgs(action="callbacks"))
    assert "requires bot mode" in result["error"]


@pytest.mark.asyncio
async def test_answer_action(mock_backend):
    result = await handle_messages(
        mock_backend,
        MessagesArgs(
            action="answer",
            callback_query_id="cbq-100",
            answer_text="Accepted",
            show_alert=True,
        ),
    )
    assert result["answered"] is True
    mock_backend.answer_callback_query.assert_awaited_once_with(
        "cbq-100", text="Accepted", show_alert=True
    )


@pytest.mark.asyncio
async def test_answer_requires_callback_query_id(mock_backend):
    result = await handle_messages(mock_backend, MessagesArgs(action="answer"))
    assert "error" in result


@pytest.mark.asyncio
async def test_answer_text_length_capped(mock_backend):
    result = await handle_messages(
        mock_backend,
        MessagesArgs(action="answer", callback_query_id="c", answer_text="x" * 201),
    )
    assert "error" in result
    mock_backend.answer_callback_query.assert_not_awaited()


@pytest.mark.asyncio
async def test_callbacks_does_not_reanswer_a_queued_press(mock_backend):
    """In queue mode the poller already answered; the id is spent."""
    queued = _callback(100)
    queued["answered"] = True
    mock_backend.get_callback_queries.return_value = [queued]

    result = await handle_messages(mock_backend, MessagesArgs(action="callbacks"))

    assert result["callbacks"][0]["answered"] is True
    mock_backend.answer_callback_query.assert_not_awaited()
