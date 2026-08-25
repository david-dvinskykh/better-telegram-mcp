"""Inline keyboard construction + validation for Bot API callback buttons.

The MCP ``message`` tool accepts a compact ``buttons`` shape -- a list of rows,
each button ``{"text": ..., "data": ...}`` -- and this module maps it to the
Bot API ``InlineKeyboardMarkup`` object while enforcing the platform limits
*before* the request leaves the process, so callers get an actionable error
instead of an opaque "Bad Request: BUTTON_DATA_INVALID".
"""

from __future__ import annotations

from typing import Any

# Bot API limits (https://core.telegram.org/bots/api#inlinekeyboardbutton).
MAX_CALLBACK_DATA_BYTES = 64
MAX_BUTTONS_PER_ROW = 8
MAX_ROWS = 100
MAX_BUTTONS = 100
MAX_BUTTON_TEXT_CHARS = 64


class InlineKeyboardError(ValueError):
    """Raised when a ``buttons`` payload violates a Bot API limit."""


def _normalize_rows(buttons: Any) -> list[list[Any]]:
    """Accept both ``[[btn, btn]]`` and the flat ``[btn, btn]`` single-row form."""
    if not isinstance(buttons, list):
        raise InlineKeyboardError(
            "buttons must be a list of rows, each row a list of "
            '{"text": ..., "data": ...} objects'
        )
    if buttons and all(isinstance(b, dict) for b in buttons):
        return [list(buttons)]
    rows: list[list[Any]] = []
    for i, row in enumerate(buttons):
        if not isinstance(row, list):
            raise InlineKeyboardError(
                f"buttons[{i}] must be a list of buttons (a keyboard row), "
                f"got {type(row).__name__}"
            )
        rows.append(row)
    return rows


def _button(row_index: int, index: int, button: Any) -> dict[str, str]:
    where = f"buttons[{row_index}][{index}]"
    if not isinstance(button, dict):
        raise InlineKeyboardError(
            f'{where} must be an object like {{"text": "Yes", "data": "DEC-12:yes"}}'
        )

    text = button.get("text")
    if not isinstance(text, str) or not text.strip():
        raise InlineKeyboardError(f"{where}.text is required and must be non-empty")
    if len(text) > MAX_BUTTON_TEXT_CHARS:
        raise InlineKeyboardError(
            f"{where}.text exceeds {MAX_BUTTON_TEXT_CHARS} characters"
        )

    # "callback_data" is the Bot API's own field name; accept it as an alias so
    # callers can paste raw Bot API buttons without translating them.
    data = button.get("data", button.get("callback_data"))
    if not isinstance(data, str) or not data:
        raise InlineKeyboardError(
            f"{where}.data is required and must be a non-empty string "
            "(it is echoed back verbatim when the button is pressed)"
        )
    size = len(data.encode("utf-8"))
    if size > MAX_CALLBACK_DATA_BYTES:
        raise InlineKeyboardError(
            f"{where}.data is {size} bytes; the Bot API allows at most "
            f"{MAX_CALLBACK_DATA_BYTES} bytes"
        )

    unsupported = set(button) - {"text", "data", "callback_data"}
    if unsupported:
        raise InlineKeyboardError(
            f"{where} has unsupported field(s): {', '.join(sorted(unsupported))}. "
            "Only callback buttons (text + data) are supported."
        )
    return {"text": text, "callback_data": data}


def build_inline_keyboard(buttons: Any) -> dict[str, Any]:
    """Build a Bot API ``InlineKeyboardMarkup`` from the compact button shape.

    ``[]`` (or ``[[]]``) yields an empty keyboard, which is how the Bot API
    removes the buttons from an existing message.

    Raises:
        InlineKeyboardError: on any malformed button or exceeded Bot API limit.
    """
    rows = _normalize_rows(buttons)
    if len(rows) > MAX_ROWS:
        raise InlineKeyboardError(
            f"buttons has {len(rows)} rows; at most {MAX_ROWS} are allowed"
        )

    keyboard: list[list[dict[str, str]]] = []
    total = 0
    for row_index, row in enumerate(rows):
        if len(row) > MAX_BUTTONS_PER_ROW:
            raise InlineKeyboardError(
                f"buttons[{row_index}] has {len(row)} buttons; at most "
                f"{MAX_BUTTONS_PER_ROW} fit in one row"
            )
        total += len(row)
        if total > MAX_BUTTONS:
            raise InlineKeyboardError(
                f"buttons has more than {MAX_BUTTONS} buttons in total"
            )
        keyboard.append([_button(row_index, i, button) for i, button in enumerate(row)])

    duplicates = _duplicate_data(keyboard)
    if duplicates:
        raise InlineKeyboardError(
            f"duplicate callback data: {', '.join(duplicates)}. "
            "Each button needs distinct data so presses can be told apart."
        )
    return {"inline_keyboard": keyboard}


def _duplicate_data(keyboard: list[list[dict[str, str]]]) -> list[str]:
    seen: set[str] = set()
    dupes: set[str] = set()
    for row in keyboard:
        for button in row:
            data = button["callback_data"]
            if data in seen:
                dupes.add(data)
            seen.add(data)
    return sorted(dupes)
