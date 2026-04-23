# Repository Guidelines

## Project Structure & Module Organization

This repository is no longer plan-first: the local shell, SQLite store, and Bubble Tea TUI are implemented, while live WhatsApp protocol work is still pending. `PLAN.md` remains the canonical product and stage document. Keep the layout simple and Go-standard:

- `cmd/vimwhat/`: CLI entrypoint and startup wiring
- `internal/app/`: bootstrap, CLI subcommands, doctor output, environment wiring
- `internal/config/`: XDG path resolution and config loading/defaults
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

## Coding Style & Naming Conventions

Use idiomatic Go and standard formatting. Let `gofmt` define indentation and spacing; do not hand-format. Keep packages lowercase and short. Exported names use `CamelCase`; internal helpers use `camelCase`. File names should describe the subsystem, for example `chat_list.go`, `history_sync.go`, `preview_backend.go`.

Favor small packages with explicit responsibilities. Keep protocol, storage, UI, and media concerns separated.

## Testing Guidelines

Write table-driven Go tests with the standard `testing` package. Name files `*_test.go` and tests `TestXxx`. Add integration tests around SQLite, history sync, and event ingestion early; the lazy-loading and modal behavior are high-risk areas and should not rely only on manual testing.

The current codebase already has meaningful coverage in `internal/ui/`, `internal/store/`, `internal/config/`, `internal/media/`, and `internal/whatsapp/`. Keep extending those tests as behavior changes, especially for viewport behavior, modal transitions, migrations, and preview backend fallbacks.

## Commit & Pull Request Guidelines

Current git history uses ad hoc messages (`first commit`, `what the hell`), so there is no reliable convention to preserve. From now on, use short imperative commits such as `add chat list model` or `implement sqlite migrations`.

Pull requests should include:

- a clear summary of behavior changes
- notes on storage, protocol, or keymap impact
- test coverage for the changed area
- screenshots or terminal captures for TUI-visible changes

## Security & Configuration Tips

Do not commit WhatsApp session data, SQLite databases, logs, or media caches. Keep runtime state under XDG paths, not in the repo. Treat preview backend commands and external opener configuration as untrusted input surfaces.

The live WhatsApp session DB path already exists in app wiring as a runtime artifact even though login/sync is not implemented yet; it must remain out of git.

## Updating plan

After completing changes, check if that checks out with some landmark in PLAN.md and update the PLAN.md according to the current state of the project.
