# vimwhat

`vimwhat` is a Linux-first, vim-centric WhatsApp TUI in Go. The current codebase is a local SQLite-backed shell: it can render demo chats, persist drafts, search local messages with FTS5, and exercise the modal Bubble Tea interface while the real WhatsApp protocol adapter is still being built.

## Current status

- Bubble Tea TUI with normal, insert, visual, command, and search modes.
- SQLite state under XDG data paths with migrations, FTS message indexing, drafts, contacts, media metadata, sync cursors, and UI snapshot storage.
- Demo seeding commands for local development before live WhatsApp sync exists.
- Preview backend detection plus in-chat image/video thumbnail rendering through Sixel/`chafa`, and focused audio playback through `mpv`.
- WhatsApp adapter boundary exists, but live login/sync is not implemented yet.

## Commands

```sh
go run ./cmd/vimwhat
go run ./cmd/vimwhat demo seed
go run ./cmd/vimwhat demo clear
go run ./cmd/vimwhat doctor
```

The CLI reserves these commands for the protocol/media/export work that comes next:

```sh
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
- Logs/cache/media previews: `$XDG_CACHE_HOME/vimwhat/`

Do not commit session files, SQLite databases, logs, media caches, or generated preview assets.
