# maybewhats

`maybewhats` is a Linux-first, vim-centric WhatsApp TUI in Go. The current codebase is a local SQLite-backed shell: it can render demo chats, persist drafts, search local messages with FTS5, and exercise the modal Bubble Tea interface while the real WhatsApp protocol adapter is still being built.

The GitHub repository is named `vimwhat`, but the binary and package names currently remain `maybewhats`.

## Current status

- Bubble Tea TUI with normal, insert, visual, command, and search modes.
- SQLite state under XDG data paths with migrations, FTS message indexing, drafts, contacts, media metadata, sync cursors, and UI snapshot storage.
- Demo seeding commands for local development before live WhatsApp sync exists.
- Preview backend detection plus in-chat image/video thumbnail rendering through Sixel/`chafa` with graceful fallback rows.
- WhatsApp adapter boundary exists, but live login/sync is not implemented yet.

## Commands

```sh
go run ./cmd/maybewhats
go run ./cmd/maybewhats demo seed
go run ./cmd/maybewhats demo clear
go run ./cmd/maybewhats doctor
```

The CLI reserves these commands for the protocol/media/export work that comes next:

```sh
go run ./cmd/maybewhats login
go run ./cmd/maybewhats logout
go run ./cmd/maybewhats media open <message-id>
go run ./cmd/maybewhats export chat <jid>
```

## Development

```sh
make run
make build
make test
make lint
```

The `Makefile` defaults `GOCACHE` to `/tmp/maybewhats-go-build`, which keeps builds working in constrained environments where the normal Go cache path is read-only.

## Runtime state

Runtime state is kept out of the repository:

- Config: `$XDG_CONFIG_HOME/maybewhats/config.toml`
- App database: `$XDG_DATA_HOME/maybewhats/state.sqlite3`
- WhatsApp session placeholder: `$XDG_DATA_HOME/maybewhats/whatsapp-session.sqlite3`
- Logs/cache/media previews: `$XDG_CACHE_HOME/maybewhats/`

Do not commit session files, SQLite databases, logs, media caches, or generated preview assets.
