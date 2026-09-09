package server

// The tool descriptions are what the model reads before choosing an action, so
// they carry the full action list with each action's required and optional
// arguments rather than a one-line summary.

const messageDescription = `Send, edit, delete, forward, pin, react, search, browse history, run
polls, keep drafts and schedule messages, and ask a question with inline
buttons + read the presses.

Actions (chat_id: "@username" | int | a saved alias):
- send (chat_id, text -> reply_to, parse_mode, buttons, resume_session)
- edit (chat_id, message_id, text and/or buttons -> parse_mode)
- delete (chat_id, message_id)
- delete_bulk (chat_id, message_ids)
- purge (chat_id -> revoke): Clear the whole conversation. revoke=true also
  removes it for the other side and cannot be undone.
- forward (from_chat, to_chat, message_id)
- forward_bulk (from_chat, to_chat, message_ids)
- pin (chat_id, message_id)
- unpin (chat_id, message_id)
- unpin_all (chat_id)
- pinned (chat_id -> limit=20)
- react (chat_id, message_id, emoji)
- reactions (chat_id, message_id -> limit=50): Who reacted, and with what  [user mode]
- read (chat_id -> message_id): Mark read up to a message, or the whole chat  [user mode]
- search (query -> chat_id, limit=20): Omit chat_id to search every chat
- history (chat_id -> limit=20, offset_id)
- context (chat_id, message_id -> around=5): The messages either side of one  [user mode]
- link (chat_id, message_id): The t.me link to a message
- poll (chat_id, question, options -> multiple_choice, anonymous, close_at)
- draft_save (chat_id -> text): Store unsent text; empty text clears it  [user mode]
- draft_list: Every chat with unsent text in it  [user mode]
- schedule (chat_id, text, send_at): Queue a message for later  [user mode]
- schedule_list (chat_id)  [user mode]
- schedule_cancel (chat_id, message_ids)  [user mode]
- callbacks (-> since_id, limit=20, auto_answer, answer_text,
  allowed_from_ids, data_pattern, peek, message_id)  [bot mode]
- answer (callback_query_id -> answer_text, show_alert)  [bot mode]

Times (send_at, close_at) are RFC3339, e.g. 2026-01-31T09:00:00Z.

Inline buttons (bot mode only) are rows of callback buttons:
buttons=[[{"text": "Yes", "data": "DEC-12:yes"},
          {"text": "No", "data": "DEC-12:no"}]]
Each ` + "`data`" + ` is <=64 bytes, <=8 buttons per row, and comes back verbatim in
` + "`callbacks`" + `. ` + "`edit`" + ` with buttons=[] strips the keyboard so a question
cannot be answered twice.

` + "`resume_session`" + ` (a Claude session id) on a ` + "`send`" + ` with buttons records
which session the question belongs to. Whoever delivers the press reads that
map to continue *that* session, and ` + "`callbacks`" + ` echoes it back as
` + "`session_id`" + ` on each press.

` + "`callbacks`" + ` returns presses since the last read -- the server keeps the
cursor, so a repeated call never replays a decision -- and acknowledges each
one (answerCallbackQuery) unless auto_answer=false.

` + "`callbacks`" + ` with peek=true reads without consuming: the presses stay
pending for whoever acts on them, and nothing is answered. That is what a
watcher polls, optionally narrowed to one question with message_id.`

const chatDescription = `List, create, join, leave, manage members, settings, and topics.

Actions:
- list (-> limit=50)
- info (chat_id)
- create (title -> is_channel)
- join (link_or_hash)
- leave (chat_id)
- members (chat_id -> limit=50)
- admin (chat_id, user_id -> demote)
- settings (chat_id, title|description)
- topics (chat_id, topic_action -> topic_id, topic_name)`

const mediaDescription = `Send photos, files, voice, video, and download media from messages.

Actions (file_path_or_url: local path or URL):
- send_photo (chat_id, file_path_or_url -> caption)
- send_file (chat_id, file_path_or_url -> caption)
- send_voice (chat_id, file_path_or_url -> caption)
- send_video (chat_id, file_path_or_url -> caption)
- download (chat_id, message_id -> output_dir; bot mode also requires file_id)`

const contactDescription = `Manage contacts: list, search, add, and block/unblock users (user mode only).

Actions:
- list: Show all contacts
- search (query): Find contacts by name
- add (phone, first_name -> last_name)
- block (user_id -> unblock=true)`

const configDescription = `Server configuration and runtime settings.

Actions (required params):
- status: Show connection state, mode, and current config
- set (key+value, or message_limit|timeout): Update a runtime limit.
  Generic form: set(key='message_limit', value='50'). Typed sugar:
  set(message_limit=50).
- cache_clear: Clear internal caches
- setup_status: Show credential state and how to configure
- setup_start (-> key='force'): Print the local auth command to run
- setup_reset: Clear saved credentials
- setup_complete: Re-read credentials and reconnect after running auth`

const profileDescription = `Read and edit the account behind this session, and look up other accounts.

Actions (user mode only):
- me: The signed-in account
- update (first_name|last_name|about): Edit the profile; omitted fields stay
- photo_set (path): Upload and set the avatar
- photo_delete: Remove the current avatar
- photos (-> user, limit=20): List an account's avatars
- user (-> user): Full profile of a user or bot -- bio, common chats, and for a
  bot its description and command list. Omit user for the signed-in account.
- privacy_get (key)
- privacy_set (key, rule)

Privacy keys: status_timestamp | phone_number | phone_call | profile_photo |
forwards | chat_invite | added_by_phone | voice_messages | about | birthday
Privacy rules: everybody | contacts | nobody`

const folderDescription = `Manage chat folders -- the tabs above the chat list (user mode only).

Actions:
- list: Every folder, in tab order
- get (folder_id): One folder with the chats in it
- create (title -> chats): New folder holding those chats
- delete (folder_id): Remove the folder; the chats themselves are untouched
- add_chat (folder_id, chat_id)
- remove_chat (folder_id, chat_id)
- reorder (order): Folder ids in the order the tabs should appear; ids left out
  keep their relative order after the ones named`
