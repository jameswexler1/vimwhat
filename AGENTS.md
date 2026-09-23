# Repository Guidelines

## Project Structure & Module Organization

This is a Linux-only, DB-first live WhatsApp client. The local shell, SQLite store, Bubble Tea TUI, and live WhatsApp integration are implemented; real-account validation remains ongoing. `PLAN.md` remains the canonical product and stage document. Keep the layout simple and Go-standard:

- `cmd/vimwhat/`: CLI entrypoint and startup wiring
- `internal/app/`: bootstrap, CLI subcommands, doctor output, environment wiring
- `internal/config/`: Linux XDG path resolution and config loading/defaults
- `internal/`: application code that should not be imported externally
- `internal/ui/`: TUI models, panes, modal state, keymaps
- `internal/whatsapp/`: `whatsmeow` integration, sync, session handling
- `internal/store/`: SQLite schema, migrations, repositories, FTS
- `internal/media/`: downloads, previews, backend detection
- `testdata/`: fixtures for DB, events, and UI snapshots

Avoid adding top-level directories unless they clearly map to a subsystem in `PLAN.md`.

## Build, Test, and Development Commands

Use the existing `Makefile` for the common workflow:

- `make run`: run the app locally
- `make build`: build the binary
- `make test`: run all tests
- `make lint`: run `go vet`

Equivalent direct Go commands:

- `go run ./cmd/vimwhat`: run the app locally
- `go build ./cmd/vimwhat`: build the binary
- `go test ./...`: run all tests
- `go test ./... -cover`: run tests with coverage
- `go fmt ./...`: format Go code
- `go vet ./...`: catch common Go issues

The `Makefile` defaults `GOCACHE` to `/tmp/vimwhat-go-build`; prefer that in this repo because some environments have a read-only default Go cache.

Use `make test-race` for concurrency-sensitive changes. Windows is not a supported target.

## Coding Style & Naming Conventions

Use idiomatic Go and standard formatting. Let `gofmt` define indentation and spacing; do not hand-format. Keep packages lowercase and short. Exported names use `CamelCase`; internal helpers use `camelCase`. File names should describe the subsystem, for example `chat_list.go`, `history_sync.go`, `preview_backend.go`.

Favor small packages with explicit responsibilities. Keep protocol, storage, UI, and media concerns separated.

## Linux implementation

Linux is the only supported platform. Do not add Windows compatibility code, defaults, tests, or build obligations. Keep display/session/tool details behind small helpers where this aids testing; use Linux build constraints for OS-dependent code.

Respect XDG config/data/cache paths and private durable-state permissions. External commands must remain argv-based and shell-free. Default image/video/file openers use automatic capability probing; explicitly configured commands should fail visibly when unavailable.

When changing defaults, update both `internal/config/default_file.go` and `config.example.toml`. First-run configuration must remain automatic.

Tests must be hermetic: no real display, D-Bus, clipboard, or installed preview/opener requirement. Use injected lookups, fake processes, and in-memory overlay writers.

## Keybinding Features

Any feature that introduces a keyboard shortcut must use the existing configurable keymap model. Add a named `key_<mode>_<action>` entry in `internal/config/keymap.go`, wire the UI action through `internal/ui/keymap.go`/normal-mode action dispatch as appropriate, and avoid hard-coded shortcuts outside the keymap layer. Also update the generated first-run config source and the checked-in `config.example.toml` so users can discover and edit the new binding.

## Testing Guidelines

Write table-driven Go tests with the standard `testing` package. Name files `*_test.go` and tests `TestXxx`. Add integration tests around SQLite, history sync, and event ingestion early; the lazy-loading and modal behavior are high-risk areas and should not rely only on manual testing.

The current codebase already has meaningful coverage in `internal/ui/`, `internal/store/`, `internal/config/`, `internal/media/`, and `internal/whatsapp/`. Keep extending those tests as behavior changes, especially for viewport behavior, modal transitions, migrations, and preview backend fallbacks.

Before handoff, run `make test`, `make lint`, and `make test-race`, and build Linux amd64 and arm64 binaries when packaging changes.

## Commit & Pull Request Guidelines

Current git history uses ad hoc messages (`first commit`, `what the hell`), so there is no reliable convention to preserve. From now on, use short imperative commits such as `add chat list model` or `implement sqlite migrations`.

Pull requests should include:

- a clear summary of behavior changes
- notes on storage, protocol, or keymap impact
- test coverage for the changed area
- screenshots or terminal captures for TUI-visible changes

## Security & Configuration Tips

Do not commit WhatsApp session data, SQLite databases, logs, or media caches. Keep runtime state under native per-user paths, not in the repo. Use Linux XDG paths. Treat preview backend commands and external opener configuration as untrusted input surfaces.

The live WhatsApp session DB, drafts, and retained outgoing attachments contain private data and must remain out of git.

## Updating plan

After completing changes, check if that checks out with some landmark in PLAN.md and update the PLAN.md according to the current state of the project.

## Peer review

Any code written here will be later reviewed by your competitor Claude Code, so do a perfect job.
