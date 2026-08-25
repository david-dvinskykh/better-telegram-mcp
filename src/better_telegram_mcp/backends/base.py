from __future__ import annotations

from abc import ABC, abstractmethod
from typing import Any


class ModeError(Exception):
    def __init__(self, required_mode: str):
        if required_mode == "user":
            msg = (
                "This action requires user mode. "
                "Set TELEGRAM_API_ID + TELEGRAM_API_HASH + TELEGRAM_PHONE."
            )
        elif required_mode == "bot":
            msg = (
                "This action requires bot mode. Set TELEGRAM_BOT_TOKEN "
                "(inline buttons and callback queries exist only in the Bot API)."
            )
        else:
            msg = f"This action requires {required_mode} mode."
        super().__init__(msg)
        self.required_mode = required_mode


class TelegramBackend(ABC):
    def __init__(self, mode: str):
        self.mode = mode

    def ensure_mode(self, required: str) -> None:
        if self.mode != required:
            raise ModeError(required)

    # --- Connection ---
    @abstractmethod
    async def connect(self) -> None: ...
    @abstractmethod
    async def disconnect(self) -> None: ...
    @abstractmethod
    async def is_connected(self) -> bool: ...

    # --- Auth ---
    @abstractmethod
    async def is_authorized(self) -> bool: ...
    @abstractmethod
    async def send_code(self, phone: str) -> None: ...
    @abstractmethod
    async def sign_in(
        self, phone: str, code: str, *, password: str | None = None
    ) -> dict[str, Any]: ...

    # --- Cache ---
    @abstractmethod
    async def clear_cache(self) -> None: ...

    # --- Messages ---
    @abstractmethod
    async def send_message(
        self,
        chat_id: str | int,
        text: str,
        *,
        reply_to: int | None = None,
        parse_mode: str | None = None,
        buttons: Any | None = None,
    ) -> dict[str, Any]: ...
    @abstractmethod
    async def edit_message(
        self,
        chat_id: str | int,
        message_id: int,
        text: str,
        *,
        parse_mode: str | None = None,
        buttons: Any | None = None,
    ) -> dict[str, Any]: ...
    @abstractmethod
    async def delete_message(self, chat_id: str | int, message_id: int) -> bool: ...
    @abstractmethod
    async def forward_message(
        self, from_chat: str | int, to_chat: str | int, message_id: int
    ) -> dict[str, Any]: ...
    @abstractmethod
    async def pin_message(self, chat_id: str | int, message_id: int) -> bool: ...
    @abstractmethod
    async def react_to_message(
        self, chat_id: str | int, message_id: int, emoji: str
    ) -> bool: ...
    @abstractmethod
    async def search_messages(
        self,
        query: str,
        *,
        chat_id: str | int | None = None,
        limit: int = 20,
    ) -> list[dict[str, Any]]: ...
    @abstractmethod
    async def get_history(
        self,
        chat_id: str | int,
        *,
        limit: int = 20,
        offset_id: int | None = None,
    ) -> list[dict[str, Any]]: ...

    # --- Inline buttons / callback queries (bot mode only) ---
    # Concrete, not abstract: only the Bot API exposes them, so every other
    # backend inherits the ModeError instead of restating it.
    async def edit_message_buttons(
        self, chat_id: str | int, message_id: int, buttons: Any | None
    ) -> dict[str, Any]:
        """Replace (or, with an empty ``buttons``, strip) a message's keyboard."""
        raise ModeError("bot")

    async def get_callback_queries(
        self, *, since_id: int | None = None, limit: int = 50
    ) -> list[dict[str, Any]]:
        """Fetch pending ``callback_query`` updates, oldest first.

        Returns raw Bot API update objects. The backend owns the cursor: an
        update handed out here is confirmed with Telegram and never returned
        again, so a repeated call cannot replay a decision.
        """
        raise ModeError("bot")

    async def answer_callback_query(
        self,
        callback_query_id: str,
        *,
        text: str | None = None,
        show_alert: bool = False,
    ) -> bool:
        """Acknowledge a press so the client stops showing a spinner."""
        raise ModeError("bot")

    # --- Chats ---
    @abstractmethod
    async def list_chats(self, *, limit: int = 50) -> list[dict[str, Any]]: ...
    @abstractmethod
    async def get_chat_info(self, chat_id: str | int) -> dict[str, Any]: ...
    @abstractmethod
    async def create_chat(
        self, title: str, *, is_channel: bool = False
    ) -> dict[str, Any]: ...
    @abstractmethod
    async def join_chat(self, link_or_hash: str) -> bool: ...
    @abstractmethod
    async def leave_chat(self, chat_id: str | int) -> bool: ...
    @abstractmethod
    async def get_members(
        self, chat_id: str | int, *, limit: int = 50
    ) -> list[dict[str, Any]]: ...
    @abstractmethod
    async def promote_admin(
        self, chat_id: str | int, user_id: int, *, demote: bool = False
    ) -> bool: ...
    @abstractmethod
    async def update_chat_settings(self, chat_id: str | int, **kwargs: Any) -> bool: ...
    @abstractmethod
    async def manage_topics(
        self, chat_id: str | int, action: str, **kwargs: Any
    ) -> dict[str, Any]: ...

    # --- Media ---
    @abstractmethod
    async def send_media(
        self,
        chat_id: str | int,
        media_type: str,
        file_path_or_url: str,
        *,
        caption: str | None = None,
    ) -> dict[str, Any]: ...
    @abstractmethod
    async def download_media(
        self,
        chat_id: str | int,
        message_id: int,
        *,
        file_id: str | None = None,
        output_dir: str | None = None,
    ) -> str: ...

    # --- Contacts ---
    @abstractmethod
    async def list_contacts(self) -> list[dict[str, Any]]: ...
    @abstractmethod
    async def search_contacts(self, query: str) -> list[dict[str, Any]]: ...
    @abstractmethod
    async def add_contact(
        self, phone: str, first_name: str, *, last_name: str | None = None
    ) -> bool: ...
    @abstractmethod
    async def block_user(self, user_id: int, *, unblock: bool = False) -> bool: ...
