# Repository Guidelines

## Project Structure & Module Organization

This repository is currently plan-first. The canonical product spec lives in `PLAN.md`. As implementation starts, keep the layout simple and Go-standard:

- `cmd/maybewhats/`: CLI entrypoint and startup wiring
- `internal/`: application code that should not be imported externally
- `internal/ui/`: TUI models, panes, modal state, keymaps
- `internal/whatsapp/`: `whatsmeow` integration, sync, session handling
- `internal/store/`: SQLite schema, migrations, repositories, FTS
- `internal/media/`: downloads, previews, backend detection
- `testdata/`: fixtures for DB, events, and UI snapshots

Avoid adding top-level directories unless they clearly map to a subsystem in `PLAN.md`.

## Build, Test, and Development Commands

Prefer a small `Makefile` once the codebase exists. Expected commands:

- `go run ./cmd/maybewhats`: run the app locally
- `go build ./cmd/maybewhats`: build the binary
- `go test ./...`: run all tests
- `go test ./... -cover`: run tests with coverage
- `go fmt ./...`: format Go code
- `go vet ./...`: catch common Go issues

If a `Makefile` is added, mirror these as `make run`, `make build`, `make test`, and `make lint`.

## Coding Style & Naming Conventions

Use idiomatic Go and standard formatting. Let `gofmt` define indentation and spacing; do not hand-format. Keep packages lowercase and short. Exported names use `CamelCase`; internal helpers use `camelCase`. File names should describe the subsystem, for example `chat_list.go`, `history_sync.go`, `preview_backend.go`.

Favor small packages with explicit responsibilities. Keep protocol, storage, UI, and media concerns separated.

## Testing Guidelines

Write table-driven Go tests with the standard `testing` package. Name files `*_test.go` and tests `TestXxx`. Add integration tests around SQLite, history sync, and event ingestion early; the lazy-loading and modal behavior are high-risk areas and should not rely only on manual testing.

## Commit & Pull Request Guidelines

Current git history uses ad hoc messages (`first commit`, `what the hell`), so there is no reliable convention to preserve. From now on, use short imperative commits such as `add chat list model` or `implement sqlite migrations`.

Pull requests should include:

- a clear summary of behavior changes
- notes on storage, protocol, or keymap impact
- test coverage for the changed area
- screenshots or terminal captures for TUI-visible changes

## Security & Configuration Tips

Do not commit WhatsApp session data, SQLite databases, logs, or media caches. Keep runtime state under XDG paths, not in the repo. Treat preview backend commands and external opener configuration as untrusted input surfaces.
