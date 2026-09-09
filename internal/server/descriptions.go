package server

// The tool descriptions are what the model reads before choosing an action, so
// they carry the full action list with each action's required and optional
// arguments rather than a one-line summary.

const messageDescription = `Send, edit, delete, forward, pin, react, search, browse history, and
ask a question with inline buttons + read the presses.

Actions (chat_id: "@username" | int):
- send (chat_id, text -> reply_to, parse_mode, buttons, resume_session)
- edit (chat_id, message_id, text and/or buttons -> parse_mode)
- delete (chat_id, message_id)
- forward (from_chat, to_chat, message_id)
- pin (chat_id, message_id)
- react (chat_id, message_id, emoji)
- search (query -> chat_id, limit=20)
- history (chat_id -> limit=20, offset_id)
- callbacks (-> since_id, limit=20, auto_answer, answer_text,
  allowed_from_ids, data_pattern, peek, message_id)  [bot mode]
- answer (callback_query_id -> answer_text, show_alert)  [bot mode]

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
