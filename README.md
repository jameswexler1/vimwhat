# vimwhat

`vimwhat` is a Linux-first, vim-centric WhatsApp TUI in Go. The current codebase is a SQLite-backed TUI with WhatsApp QR login, live ingestion, on-demand remote history fetch for the focused chat, protocol-backed remote media download, outbound text plus single-attachment media send, desktop notifications, and the remaining media/export CLI helpers.

## Current status

- Bubble Tea TUI with normal, insert, visual, command, and search modes.
- SQLite state under XDG data paths with migrations, FTS message indexing, drafts, contacts, media metadata, sync cursors, and UI snapshot storage.
- Demo seeding commands for local development without a live WhatsApp session.
- Preview backend detection plus in-chat image/video thumbnail rendering through Sixel/`chafa`, and focused audio playback through `mpv`.
- WhatsApp QR login/logout, live inbound ingestion, focused-chat remote history fetch, on-demand remote media download, real outbound text plus single-attachment media send, inactive-chat desktop notifications, `media open`, and `export chat` exist.
- `vimwhat logout` clears local app/session state and returns the client to first-use state while leaving config and explicit Downloads saves intact.

## Commands

```sh
go run ./cmd/vimwhat
go run ./cmd/vimwhat demo seed
go run ./cmd/vimwhat demo clear
go run ./cmd/vimwhat doctor
go run ./cmd/vimwhat login
go run ./cmd/vimwhat logout
go run ./cmd/vimwhat media open <message-id>
go run ./cmd/vimwhat export chat <jid>
```

## Development

```sh
make run
make build
make test
make lint
```

The `Makefile` defaults `GOCACHE` to `/tmp/vimwhat-go-build`, which keeps builds working in constrained environments where the normal Go cache path is read-only.

## Runtime state

Runtime state is kept out of the repository:

- Config: `$XDG_CONFIG_HOME/vimwhat/config.toml`
- App database: `$XDG_DATA_HOME/vimwhat/state.sqlite3`
- WhatsApp session placeholder: `$XDG_DATA_HOME/vimwhat/whatsapp-session.sqlite3`
- Logs: `$XDG_CACHE_HOME/vimwhat/`
- Non-exported chat media cache: `$TMPDIR/vimwhat-*/media`
- Generated preview cache: `$TMPDIR/vimwhat-*/preview`

Do not commit session files, SQLite databases, logs, media caches, or generated preview assets. Files saved explicitly with `leader s` still go to the configured downloads directory and are not part of the transient cache.

## Emoji rendering

Emoji rendering defaults to `emoji_mode = "auto"` in `config.toml`. Auto mode preserves full emoji sequences on most UTF-8 terminals, but uses the stable compatibility path for terminals such as `st` that can display emoji glyphs while still misreporting complex emoji cell widths. Set `emoji_mode = "compat"` to force stable degraded rendering, or `emoji_mode = "full"` to force skin tones, ZWJ professions/families, and flags.

## Mode indicators

Mode indicator colors default to pywal-derived theme colors:

```toml
indicator_normal = "pywal"
indicator_insert = "pywal"
indicator_visual = "pywal"
indicator_command = "pywal"
indicator_search = "pywal"
```

Replace any value with a hex color such as `"#7ED7C1"` or `"#f0a"` to override only that mode.

## Notifications

Desktop notifications default to `notification_backend = "auto"` and notify only for new incoming messages from inactive, unmuted chats. `notification_command` remains available as an explicit override and is treated as an argv template rather than a shell snippet.

```toml
notification_backend = "auto"
notification_command = ""
```

Supported built-in backend values are `auto`, `none`, `command`, `linux-dbus`, `macos-osascript`, and `windows-powershell`. If `notification_command` is set while `notification_backend = "auto"`, the configured command wins. `vimwhat doctor` reports the selected notification path and backend availability.
