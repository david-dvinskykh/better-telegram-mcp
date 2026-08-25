from __future__ import annotations

import pytest

from better_telegram_mcp.backends.inline_keyboard import (
    InlineKeyboardError,
    build_inline_keyboard,
)


def test_maps_rows_to_bot_api_markup():
    markup = build_inline_keyboard(
        [
            [
                {"text": "✅ Yes", "data": "DEC-12:yes"},
                {"text": "✖️ No", "data": "DEC-12:no"},
            ],
            [{"text": "🕓 Later", "data": "DEC-12:later"}],
        ]
    )
    assert markup == {
        "inline_keyboard": [
            [
                {"text": "✅ Yes", "callback_data": "DEC-12:yes"},
                {"text": "✖️ No", "callback_data": "DEC-12:no"},
            ],
            [{"text": "🕓 Later", "callback_data": "DEC-12:later"}],
        ]
    }


def test_flat_list_becomes_single_row():
    markup = build_inline_keyboard([{"text": "Yes", "data": "1:yes"}])
    assert markup == {"inline_keyboard": [[{"text": "Yes", "callback_data": "1:yes"}]]}


def test_callback_data_alias_accepted():
    markup = build_inline_keyboard([[{"text": "Yes", "callback_data": "1:yes"}]])
    assert markup["inline_keyboard"][0][0]["callback_data"] == "1:yes"


def test_empty_keyboard_removes_buttons():
    assert build_inline_keyboard([]) == {"inline_keyboard": []}
    assert build_inline_keyboard([[]]) == {"inline_keyboard": [[]]}


def test_callback_data_over_64_bytes_rejected():
    # 33 two-byte characters = 66 bytes, under the 64-char limit but over 64 bytes.
    with pytest.raises(InlineKeyboardError, match="66 bytes"):
        build_inline_keyboard([[{"text": "x", "data": "ю" * 33}]])


def test_callback_data_at_limit_accepted():
    build_inline_keyboard([[{"text": "x", "data": "a" * 64}]])


def test_row_over_eight_buttons_rejected():
    row = [{"text": str(i), "data": f"d{i}"} for i in range(9)]
    with pytest.raises(InlineKeyboardError, match="at most 8 fit in one row"):
        build_inline_keyboard([row])


def test_too_many_rows_rejected():
    rows = [[{"text": "x", "data": f"d{i}"}] for i in range(101)]
    with pytest.raises(InlineKeyboardError, match="at most 100"):
        build_inline_keyboard(rows)


def test_missing_data_rejected():
    with pytest.raises(InlineKeyboardError, match=r"buttons\[0\]\[0\].data"):
        build_inline_keyboard([[{"text": "Yes"}]])


def test_missing_text_rejected():
    with pytest.raises(InlineKeyboardError, match=r"buttons\[0\]\[0\].text"):
        build_inline_keyboard([[{"data": "1:yes"}]])


def test_duplicate_data_rejected():
    with pytest.raises(InlineKeyboardError, match="duplicate callback data"):
        build_inline_keyboard(
            [[{"text": "a", "data": "1:yes"}, {"text": "b", "data": "1:yes"}]]
        )


def test_unsupported_field_rejected():
    with pytest.raises(InlineKeyboardError, match="url"):
        build_inline_keyboard([[{"text": "a", "data": "1:yes", "url": "http://x"}]])


def test_non_list_rejected():
    with pytest.raises(InlineKeyboardError, match="must be a list of rows"):
        build_inline_keyboard("yes")
