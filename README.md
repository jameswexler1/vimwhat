# vimwhat

`vimwhat` is a Linux-first, vim-centric WhatsApp TUI in Go. The current codebase is a SQLite-backed TUI with WhatsApp QR login, live ingestion, on-demand remote history fetch for the focused chat, protocol-backed remote media download, and plain text outbound send.

## Current status

- Bubble Tea TUI with normal, insert, visual, command, and search modes.
- SQLite state under XDG data paths with migrations, FTS message indexing, drafts, contacts, media metadata, sync cursors, and UI snapshot storage.
- Demo seeding commands for local development without a live WhatsApp session.
- Preview backend detection plus in-chat image/video thumbnail rendering through Sixel/`chafa`, and focused audio playback through `mpv`.
- WhatsApp QR login/logout, live inbound ingestion, focused-chat remote history fetch, on-demand remote media download, and real plain text send exist; media sends are still pending.

## Commands

```sh
go run ./cmd/vimwhat
go run ./cmd/vimwhat demo seed
go run ./cmd/vimwhat demo clear
go run ./cmd/vimwhat doctor
go run ./cmd/vimwhat login
go run ./cmd/vimwhat logout
```

The CLI reserves these commands for the protocol/media/export work that comes next:

```sh
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
- Logs/cache/media previews: `$XDG_CACHE_HOME/vimwhat/`

Do not commit session files, SQLite databases, logs, media caches, or generated preview assets.

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
