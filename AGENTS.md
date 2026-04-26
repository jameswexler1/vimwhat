# Repository Guidelines

## Project Structure & Module Organization

This repository is no longer plan-first: the local shell, SQLite store, and Bubble Tea TUI are implemented, while live WhatsApp protocol work is still pending. `PLAN.md` remains the canonical product and stage document. Keep the layout simple and Go-standard:

- `cmd/vimwhat/`: CLI entrypoint and startup wiring
- `internal/app/`: bootstrap, CLI subcommands, doctor output, environment wiring
- `internal/config/`: native Linux/Windows path resolution and config loading/defaults
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

Use `make test-windows` when a change touches platform abstractions, command execution, paths, notifications, media/openers, config defaults, or anything that may affect Windows compilation.

## Coding Style & Naming Conventions

Use idiomatic Go and standard formatting. Let `gofmt` define indentation and spacing; do not hand-format. Keep packages lowercase and short. Exported names use `CamelCase`; internal helpers use `camelCase`. File names should describe the subsystem, for example `chat_list.go`, `history_sync.go`, `preview_backend.go`.

Favor small packages with explicit responsibilities. Keep protocol, storage, UI, and media concerns separated.

## Cross-Platform Implementation

New features must be cross-platform by default. Preserve Linux behavior unless the task explicitly changes it, and add Windows support in the same change when the feature touches runtime paths, command execution, desktop integration, media handling, clipboard behavior, notifications, terminal assumptions, file names, or generated config defaults.

Use thin shared interfaces or helper functions in the main package file, with platform-specific implementations in build-tagged files such as `foo_unix.go`, `foo_windows.go`, `backend_linux.go`, or `backend_darwin.go`. Keep `runtime.GOOS` checks out of feature logic when build constraints can express the boundary more cleanly. Shared code should call small helpers such as `platformDefault...`, `platform...Commands`, or `platform...Path`; the OS-specific files should contain the platform details.

Do not hard-code Unix tools or paths in shared code. Linux defaults may use tools such as `xdg-open`, `notify-send`, `wl-copy`, `xclip`, `mpv`, `nsxiv`, `chafa`, and `ueberzug++`; Windows defaults should use native mechanisms such as `%APPDATA%`, `%LOCALAPPDATA%`, PowerShell, `rundll32.exe url.dll,FileProtocolHandler`, `explorer.exe`, and Windows toast/clipboard APIs. External command templates must stay argv-based and shell-free.

When adding config defaults, update both `internal/config/default_file.go` and `config.example.toml` as needed. First-run config generation must remain automatic on every supported platform; Windows users should not have to guess paths. Current Windows config location is `%APPDATA%\vimwhat\config.toml`; data and cache live under `%LOCALAPPDATA%\vimwhat\`.

Tests for platform behavior should be hermetic. Do not require a real display server, D-Bus session, `ueberzug++`, clipboard, PowerShell, or external opener unless the test is explicitly an integration test and is skipped when unavailable. Use in-memory fakes such as overlay writers and injectable command/path lookups.

## Keybinding Features

Any feature that introduces a keyboard shortcut must use the existing configurable keymap model. Add a named `key_<mode>_<action>` entry in `internal/config/keymap.go`, wire the UI action through `internal/ui/keymap.go`/normal-mode action dispatch as appropriate, and avoid hard-coded shortcuts outside the keymap layer. Also update the generated first-run config source and the checked-in `config.example.toml` so users can discover and edit the new binding.

## Testing Guidelines

Write table-driven Go tests with the standard `testing` package. Name files `*_test.go` and tests `TestXxx`. Add integration tests around SQLite, history sync, and event ingestion early; the lazy-loading and modal behavior are high-risk areas and should not rely only on manual testing.

The current codebase already has meaningful coverage in `internal/ui/`, `internal/store/`, `internal/config/`, `internal/media/`, and `internal/whatsapp/`. Keep extending those tests as behavior changes, especially for viewport behavior, modal transitions, migrations, and preview backend fallbacks.

For cross-platform work, include Linux-preserving tests plus Windows build-tagged tests where behavior differs. At minimum, run `make test`, `make lint`, and `make test-windows` before handing off changes.

## Commit & Pull Request Guidelines

Current git history uses ad hoc messages (`first commit`, `what the hell`), so there is no reliable convention to preserve. From now on, use short imperative commits such as `add chat list model` or `implement sqlite migrations`.

Pull requests should include:

- a clear summary of behavior changes
- notes on storage, protocol, or keymap impact
- test coverage for the changed area
- screenshots or terminal captures for TUI-visible changes

## Security & Configuration Tips

Do not commit WhatsApp session data, SQLite databases, logs, or media caches. Keep runtime state under native per-user paths, not in the repo. On Linux this means XDG paths; on Windows this means AppData/LocalAppData paths. Treat preview backend commands and external opener configuration as untrusted input surfaces.

The live WhatsApp session DB path already exists in app wiring as a runtime artifact even though login/sync is not implemented yet; it must remain out of git.

## Updating plan

After completing changes, check if that checks out with some landmark in PLAN.md and update the PLAN.md according to the current state of the project.
