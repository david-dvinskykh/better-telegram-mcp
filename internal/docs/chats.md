# Telegram Chats

List, look up, create, join and leave chats; moderate the ones you administer;
and manage how they appear in your own chat list.

A bot administers chats it has been made an admin of, so the moderation half
works in both modes. Anything belonging to a person's own chat list — muting,
archiving, the public directory — is marked user mode below.

## Actions

### list
List your chats (user mode only).
- **limit**: Max chats to return (default: 50)

### info
Get detailed chat information.
- **chat_id** (required): Chat ID or username

### create
Create a new group or channel (user mode only).
- **title** (required): Chat title
- **is_channel**: Create a channel instead of group (default: false)

### join
Join a chat by invite link (user mode only).
- **link_or_hash** (required): Invite link or hash

### leave
Leave a chat.
- **chat_id** (required): Chat ID or username

### members
Get chat members/administrators.
- **chat_id** (required): Chat ID or username
- **limit**: Max members (default: 50)

### admin
Promote or demote a chat admin.
- **chat_id** (required): Chat ID or username
- **user_id** (required): User to promote/demote
- **demote**: Set true to demote (default: false)

### settings
Update chat settings (title, description).
- **chat_id** (required): Chat ID or username
- **title**: New title
- **description**: New description

### topics
Manage forum topics.
- **chat_id** (required): Chat ID or username
- **topic_action** (required): "list", "create", or "close"
- **topic_id**: Topic ID (for close)
- **topic_name**: Topic name (for create)

### full
The detail `info` leaves out: description, member and admin counts, how many
members are online, the slow-mode delay, and the pinned message id. It costs a
second round trip, so `info` stays the one to use when you only need to know
which chat this is.
- **chat_id** (required)

### invite
Add people to a group or channel (user mode). No back history is shared: a
person added to a group does not receive what was said before they arrived.
- **chat_id** (required)
- **user_ids** (required): Accounts to add

### admins
List the administrators of a chat.
- **chat_id** (required)
- **limit**: Max entries (default: 50)

### banned
List the accounts kept out of a chat (user mode).
- **chat_id** (required)
- **limit**: Max entries (default: 50)

### ban
Remove someone from a chat and keep them out, or lift that ban.
- **chat_id** (required)
- **user_id** (required)
- **unban**: Set true to lift the ban (default: false)

### permissions
Replace what ordinary members of a chat may do.

The map is the whole rule, not a patch: every permission you do not name is
granted. That is deliberate — a patch would let an old restriction survive a
change that reads as if it lifted it.
- **chat_id** (required)
- **permissions** (required): `{"send_messages": true, "send_media": false}`

Keys: `send_messages`, `send_media`, `send_stickers`, `send_gifs`,
`send_polls`, `embed_links`, `change_info`, `invite_users`, `pin_messages`,
`manage_topics`.

### slow_mode
Limit how often a member may post (user mode).
- **chat_id** (required)
- **seconds**: Delay between posts; 0 turns it off

### photo
Set the chat's picture, or remove it.
- **chat_id** (required)
- **path**: File path or URL; leave empty to remove the picture

### recent_actions
The administrative log: who was promoted, banned, or edited what (user mode).
- **chat_id** (required)
- **limit**: Max events (default: 50)

### invite_link
The chat's primary invite link.
- **chat_id** (required)
- **revoke**: Replace the existing link, which is how a leaked one is taken
  out of circulation (default: false)

### read_by
Who has read a message. Telegram answers this only for small groups and only
for recent messages (user mode).
- **chat_id** (required)
- **message_id** (required)

### mute
Silence a chat's notifications (user mode).
- **chat_id** (required)
- **mute**: Set false to unmute (default: true)

### archive
Move a chat into the archive (user mode).
- **chat_id** (required)
- **archive**: Set false to move it back (default: true)

### resolve
What a @name points at — a person, a bot, a group or a channel — without
joining or messaging it (user mode).
- **username** (required)

### search_public
Search Telegram's public directory (user mode).
- **query** (required)
- **limit**: Max results (default: 20)

### common
The groups and channels you share with someone (user mode).
- **user_id** (required)
- **limit**: Max chats (default: 50)
