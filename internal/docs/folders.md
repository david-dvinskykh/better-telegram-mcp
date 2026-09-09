# Telegram Folders

Chat folders are the tabs above the chat list. They are a user-account
feature: the Bot API has no equivalent at all.

A folder is a view, not a container. Deleting one leaves every chat in it
exactly where it was.

## Actions

### list
Every folder, in the order the tabs appear.
No parameters.

### get
One folder, with the chats it holds.
- **folder_id** (required): From `list`

### create
Make a folder holding the given chats.
- **title** (required): The tab's name
- **chats**: Chat ids, usernames or saved aliases to put in it

### delete
Remove a folder.
- **folder_id** (required)

### add_chat
Put a chat in a folder.
- **folder_id** (required)
- **chat_id** (required)

### remove_chat
Take a chat out of a folder.
- **folder_id** (required)
- **chat_id** (required)

### reorder
Set the order the tabs appear in.
- **order** (required): Folder ids, first tab first. Any id you leave out
  keeps its relative position after the ones you named.
