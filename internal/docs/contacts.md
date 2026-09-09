# Telegram Contacts

Manage the address book, and teach the server what you call people.

The address-book actions need user mode: a bot has no contacts of its own.
The alias actions at the end are local and work in both modes.

## Actions

### list
List all contacts.
No parameters required.

### search
Search contacts by name or username.
- **query** (required): Search query

### add
Add a new contact.
- **phone** (required): Phone number (international format, e.g. "+1234567890")
- **first_name** (required): First name
- **last_name**: Last name (optional)

### block
Block or unblock a user.
- **user_id** (required): User ID to block/unblock
- **unblock**: Set true to unblock (default: false)

### delete
Remove a contact entry. The conversation with them stays.
- **user_id** (required)

### import
Add several people at once, and find out which numbers had no Telegram
account behind them.
- **contacts** (required): `[{"phone": "+491701234567", "first_name": "Ada"}]`

### export
The whole address book, which is what you back up or diff against another
source.
No parameters.

### blocked
The accounts this one has blocked.
- **limit**: Max entries (default: 50)

### last_seen
When someone was last seen and when the conversation with them last moved —
together, whether the contact is still live.
- **user_id** (required)

### send_card
Share someone's contact card into a chat. Their phone number has to be
visible to you, or there is no card to share.
- **chat_id** (required)
- **user_id** (required)

### direct
The one-to-one chat with a person, by any reference — username, phone, id or
a saved alias. Use it when you hold a name and need the id every other action
takes.
- **query** (required)

## Aliases

Chat ids are unusable in conversation. An alias is the local map from the
words a person actually says — "андрей бекендер", "мама" — to the id behind
them, and it is consulted inside peer resolution for every tool, in both bot
and user mode.

The loop is: a reference nobody has explained comes back as an instruction to
ask who that is. Ask the user. Save the answer with `alias_set`, using the
wording *they* used. Retry. From then on that wording resolves silently.

One person may have any number of aliases, which is how tags work: save both
"андрей бекендер" and "бекендер" and either resolves.

The file lives beside the session (override with `TELEGRAM_ALIASES_FILE`), is
written 0600, and never reaches Telegram.

### alias_set
Remember a reference.
- **alias** (required): The wording to remember
- **chat_id** (required): Who or what it points at
- **replace**: Required to repoint an alias that already names someone else.
  The guard exists because a silently redirected alias sends messages to the
  wrong person.

An alias that looks like a username or id is refused: it would shadow the real
account of that name. So are "me" and "self", which always mean the signed-in
account.

### alias_list
Every saved alias.
No parameters.

### alias_delete
Forget one.
- **alias** (required)
