# Telegram Profile

Read and edit the account behind this session, and look up other accounts.
All actions require user mode: a bot has no profile of its own to edit.

## Actions

### me
The signed-in account: id, name, username, phone, premium status.
No parameters.

### update
Edit the profile. Fields you leave out keep their current value; pass an
empty string to clear one.
- **first_name**: New first name
- **last_name**: New last name
- **about**: New bio

### photo_set
Upload a picture and make it the avatar.
- **path** (required): Local file path or an http(s) URL

### photo_delete
Remove the current avatar. Whatever was set before it becomes the account's
photo again.
No parameters.

### photos
List an account's avatars, most recent first.
- **user**: Whose avatars; omit for the signed-in account
- **limit**: Max photos (default: 20, max: 100)

### user
The full profile of a user or a bot: bio, whether you are blocked, how many
chats you share, and for a bot its description and command list.
- **user**: Who to look up; omit for the signed-in account

### privacy_get
Read one privacy setting.
- **key** (required): See the key list below

### privacy_set
Replace one privacy setting with a whole-audience rule.
- **key** (required): See the key list below
- **rule** (required): `everybody` | `contacts` | `nobody`

## Privacy keys

`status_timestamp` (last seen), `phone_number`, `phone_call`, `profile_photo`,
`forwards`, `chat_invite`, `added_by_phone`, `voice_messages`, `about`,
`birthday`.

Per-user exceptions exist in Telegram but are not exposed here: naming
individual accounts in a privacy rule is a change worth making in a client,
where you can see who you are allowing.
