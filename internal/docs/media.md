# Telegram Media

Send and download media: photos, documents, voice messages, videos, albums,
stickers and GIFs. Also index what a chat holds without transferring any of it.

## Actions

### send_photo
Send a photo.
- **chat_id** (required): Chat ID or username
- **file_path_or_url** (required): Local file path or URL
- **caption**: Photo caption

### send_file
Send a document/file.
- **chat_id** (required): Chat ID or username
- **file_path_or_url** (required): Local file path or URL
- **caption**: File caption

### send_voice
Send a voice message.
- **chat_id** (required): Chat ID or username
- **file_path_or_url** (required): Local file path or URL
- **caption**: Voice message caption

### send_video
Send a video.
- **chat_id** (required): Chat ID or username
- **file_path_or_url** (required): Local file path or URL
- **caption**: Video caption

### download
Download media from a message.
- **chat_id** (required): Chat ID or username
- **message_id** (required): Message containing media
- **output_dir**: Directory to save file (default: current directory)

### send_album
Post 2 to 10 files as one grouped message. Sending them one at a time shows up
as separate messages instead.
- **chat_id** (required)
- **files** (required): 2 to 10 local paths or URLs
- **caption**: Shown once for the whole group

### send_sticker
Send a sticker, either from a file or out of an installed set.
- **chat_id** (required)
- **file_path_or_url**: A `.webp` sticker file, or
- **sticker_set**: A set's short name, with one of:
  - **emoji**: The sticker standing for that emoji
  - **index**: Position in the set (default: 0)

Picking out of a set is user mode only; a `.webp` file works in both.

### send_gif
Send one of the account's saved GIFs (user mode).
- **chat_id** (required)
- **document_id** (required): From the `gifs` action
- **caption**: GIF caption

There is no GIF *search*: Telegram removed that method from its schema.

### info
Describe the attachment on a message — kind, size, duration, file name —
without downloading it (user mode).
- **chat_id** (required)
- **message_id** (required)

### photos
Index the photos posted in a chat, without transferring any of them. The ids
are what `download` then takes (user mode).
- **chat_id** (required)
- **limit**: Max photos (default: 20)

For a person's *avatars* rather than photos posted in a chat, use
`profile(action="photos")`.

### sticker_sets
The sticker packs installed on the account (user mode).
No parameters.

### stickers
The stickers in one pack, with the emoji each stands for.
- **sticker_set** (required): The pack's short name

### gifs
The account's saved GIFs (user mode).
- **limit**: Max GIFs (default: 20)
