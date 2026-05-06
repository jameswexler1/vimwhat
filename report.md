# Public Readiness Audit

Date: 2026-05-06

## Verdict

This repository is not ready for broad public end-user release yet.

It is reasonable to share as a clearly labeled pre-alpha or developer preview if the README is conservative about risk and incomplete validation. The codebase is substantially implemented, has meaningful tests, and builds cleanly on the checked platforms, but it still has release blockers around privacy-sensitive file permissions, module/release identity, documentation accuracy, and live WhatsApp validation.

## What Was Reviewed

- Repository structure, tracked files, ignore rules, and public metadata.
- README, PLAN, AGENTS, Makefile, config example, and CI workflow.
- Core packages under `cmd/` and `internal/`, including app wiring, config, store, media, notifications, UI, command parsing, and WhatsApp integration.
- Tests and validation commands.
- Obvious publish risks such as tracked secrets, runtime data, stale TODO-style markers, release packaging, and platform behavior.

No code was intentionally changed during this audit. This report is the only file added.

## Validation Results

These checks passed locally:

```sh
go test ./...
go vet ./...
make test-windows
go build ./cmd/vimwhat
go test -race ./...
go test ./... -cover
```

Coverage from `go test ./... -cover`:

| Package | Coverage |
| --- | ---: |
| `cmd/vimwhat` | 0.0% |
| `internal/app` | 52.2% |
| `internal/commandline` | 75.0% |
| `internal/config` | 78.2% |
| `internal/media` | 54.5% |
| `internal/notify` | 54.9% |
| `internal/store` | 63.3% |
| `internal/textmatch` | 83.0% |
| `internal/ui` | 69.6% |
| `internal/whatsapp` | 40.4% |

Additional observations:

- `go list -m all` completed after network approval.
- `govulncheck`, `staticcheck`, `goreleaser`, and `syft` were not installed, so vulnerability, static analysis, release, and SBOM checks were not run.
- The worktree was clean before and after validation.
- No obvious SQLite databases, WhatsApp session files, logs, media caches, or credentials are tracked.
- An ignored `./vimwhat` build artifact exists locally, but it is not tracked.

## Strengths

- The repository layout is simple and Go-standard, matching the documented package boundaries.
- The implementation is no longer just a plan. The TUI, SQLite store, config generation, media handling, notification paths, and live WhatsApp wiring all exist.
- The test suite is meaningful and covers store behavior, config behavior, media fallback behavior, UI state transitions, command parsing, and WhatsApp event plumbing.
- Linux and Windows platform boundaries are represented with build-tagged files in several places.
- External command execution is generally argv-based and avoids shell interpolation for user-configured command templates.
- Runtime state is kept out of the repository and routed through native per-user paths.
- `.gitignore` excludes common sensitive/runtime artifacts such as SQLite files, logs, media directories, preview directories, and the local binary.

## Release Blockers

### 1. Privacy-Sensitive Runtime Files Need Stricter Permissions

The application stores highly sensitive data locally:

- Message bodies, senders, chats, contacts, media paths, and raw message payloads in `internal/store/store.go`.
- Media descriptors including URLs, direct paths, media keys, hashes, and sticker metadata in `internal/store/store.go`.
- WhatsApp session state under the configured session DB path in `internal/whatsapp/client.go`.

Several runtime directories and files are currently created with permissive defaults:

- `internal/config/paths.go` creates config, data, and cache directories with `0755`.
- `internal/config/default_file.go` writes the generated config with `0644`.
- `internal/whatsapp/client.go` creates the WhatsApp session parent directory with `0755`.
- SQLite and media files may inherit process umask defaults unless the call site explicitly tightens them.

For a WhatsApp client, local disclosure risk is a release blocker. Message and session data should not be readable by other local users.

Direction:

- Create data, cache, state, session, media, avatar, preview, and transient directories with `0700`.
- Create config with `0600` unless there is a deliberate reason for world-readable config.
- Ensure SQLite state and session DB files are created or repaired to `0600`.
- Consider a startup permission repair step for existing installs.
- Add `doctor` checks that warn when state, session, or media paths are too permissive.
- Document exactly what is stored locally and how users can delete or relocate it.

### 2. Public Module and Release Identity Are Not Ready

`go.mod` currently declares:

```go
module vimwhat
```

That works locally, but it is not a public module path. A public Go project usually needs a module path matching its final repository, for example `github.com/<owner>/vimwhat`, so users can install it with:

```sh
go install github.com/<owner>/vimwhat/cmd/vimwhat@latest
```

The remote currently points at `git@github.com:jameswexler1/vimwhat.git`, while the README also contains hard-coded GitHub release URLs for that account. The README includes a tester-specific Windows path under `C:\Users\Otavio\...`, which should not be in public release instructions.

Direction:

- Decide the final repository owner/name before publishing.
- Change the module path to the public import path and update internal references as needed.
- Add a version command or version output in `doctor`.
- Use semver tags for releases.
- Replace tester-local README paths with general user instructions.
- Avoid making a mutable `windows-latest` release the primary public release channel.

### 3. Release Packaging Is Windows-Only and Too Thin

The GitHub Actions workflow builds Windows artifacts and publishes a mutable `windows-latest` release on pushes to `main`. It does not produce Linux release artifacts, checksums, signatures, SBOMs, or versioned releases.

This does not match the README's positioning as a Linux-first terminal app with Windows support.

Direction:

- Add release builds for Linux and Windows.
- Publish versioned GitHub releases from tags.
- Attach checksums for every artifact.
- Consider artifact signing.
- Add an SBOM or dependency inventory.
- Add a smoke test that runs `vimwhat doctor` against each packaged artifact.
- Keep nightly/latest artifacts separate from stable tagged releases.

### 4. Documentation Overstates or Contradicts Current Behavior

There are several public-doc mismatches:

- `PLAN.md` says live validation and polish are still pending.
- `README.md` presents a broad feature set but then lists important unfinished areas.
- `README.md` describes the preview backend order differently from the implementation and from `PLAN.md`.
- `config.example.toml` does not match generated defaults in several places, including emoji mode, indicator color, and leader key.
- Some UI fallback messages still say media download or attachment send is "not implemented" even though live handlers exist in the normal app path.

Direction:

- Recast README as pre-alpha until live validation is complete.
- Put known limitations near the top, not deep in the document.
- Synchronize `config.example.toml` with generated defaults.
- Make README preview-backend order match implementation.
- Replace stale "not implemented" UI wording with more precise status such as "requires a paired WhatsApp session" where appropriate.
- Add a supported-platform matrix with exact tested terminal, OS, and media backend combinations.

### 5. Live WhatsApp Behavior Still Needs Real-Account Validation

The code has real WhatsApp paths for login, reconnect, text send, media send, stickers, media download, events, and notifications. However, the project plan still calls out live validation as pending, and the tests are mostly unit or mocked integration tests.

Before an end-user release, the live protocol path must be exercised with real accounts on supported platforms.

Direction:

- Run a manual acceptance matrix for fresh install, login, reconnect, logout, and relogin.
- Validate one-to-one chats and group chats.
- Validate inbound and outbound text, media, stickers, reactions, read receipts, and remote media downloads.
- Validate notification behavior on Linux and Windows.
- Validate history sync on a fresh database.
- Validate behavior when the network drops, QR login expires, media download fails, and a send fails.
- Capture the tested OS, terminal, WhatsApp account type, and backend details.
- Keep a public known-issues section for unsupported WhatsApp features and protocol instability.

## High Priority Before Wider Sharing

### 6. Security and Supply-Chain Checks Are Missing

The normal local checks pass, but the repo is missing several common public-project gates:

- `govulncheck ./...`
- `staticcheck ./...`
- Dependency license inventory.
- SBOM generation.
- Release artifact checksum verification.
- Secret scanning in CI.

Direction:

- Add `govulncheck` to CI.
- Add `staticcheck` or an equivalent linter.
- Add dependency license reporting before release.
- Add a secret scanner such as `gitleaks` in CI.
- Consider a scheduled CI job for vulnerability checks.

### 7. CI Is Useful But Not Release-Grade

Current CI runs Go tests and vet, plus a Windows compile check, but it does not run race tests, coverage reporting, vulnerability checks, static analysis, or release packaging validation.

Direction:

- Keep `go test ./...`, `go vet ./...`, and `make test-windows`.
- Add `go test -race ./...` at least on scheduled or pre-release workflows.
- Add coverage collection.
- Add package artifact smoke tests.
- Split CI from release workflows.
- Trigger stable releases from tags, not every push to `main`.

### 8. Legal, Policy, and User-Risk Notices Need Work

The project uses GPLv3, but public distribution should include clearer notices around:

- Third-party dependency licenses.
- WhatsApp/Meta non-affiliation.
- The risk of using an unofficial WhatsApp client.
- Local storage of message and session data.
- Security reporting process.

Direction:

- Add a concise disclaimer that the project is unofficial and not affiliated with WhatsApp or Meta.
- Add a privacy/security section explaining local plaintext storage and file paths.
- Add `SECURITY.md`.
- Add dependency license inventory or a `NOTICE` file if needed.
- Make README license wording explicit.

### 9. Public Contributor Hygiene Is Incomplete

For a public repository, the following are still missing or rough:

- `CONTRIBUTING.md`
- `SECURITY.md`
- `CHANGELOG.md` or release notes process
- Issue templates
- Clean release history or a documented decision to leave history as-is
- Removal of the empty tracked `.codex` file unless it has a real purpose

Direction:

- Add minimal contributor and security docs before announcing the project.
- Add issue templates for bug reports and live WhatsApp validation reports.
- Squash or rewrite local history before publishing if the current ad hoc commit messages are not desirable.
- Remove empty or tool-local tracked files that do not help users.

## Maintainability Risks

Several core files are large enough to slow future changes:

- `internal/ui/model.go`: over 6,000 lines.
- `internal/ui/view.go`: over 3,000 lines.
- `internal/ui/model_test.go`: over 9,000 lines.
- `internal/app/app.go`: over 4,000 lines.
- `internal/app/whatsapp_cli_test.go`: over 4,000 lines.
- `internal/store/repository.go`: over 2,800 lines.

This is not a blocker for source sharing, but it is a risk before more features land.

Direction:

- Split app orchestration into smaller files around login/session, sync, sending, media download, notification, and CLI command surfaces.
- Split UI update handling by mode or domain, while keeping Bubble Tea model behavior coherent.
- Split store repositories by domain: chats, messages, media, contacts, stickers, jobs.
- Keep tests near the behavior they exercise so failures are easier to diagnose.
- Avoid broad refactors during release hardening unless they directly reduce release risk.

## Lower Priority Findings

### Stale Not-Implemented Markers

There are still visible or code-level "not implemented" markers:

- `internal/whatsapp/client.go` defines `ErrNotImplemented`, which appears stale now that integration exists.
- UI fallback paths can still display "not implemented" for attachment send or media download when callbacks are missing.
- Lottie sticker support and editable media captions are intentionally unsupported.

Direction:

- Remove stale symbols.
- Reword fallback UI messages to reflect the current mode or missing capability.
- Keep real limitations documented in README and PLAN.

### Protocol Event Backpressure Should Be Explicit

The WhatsApp event subscription path uses buffered channels. If the consumer falls behind, event handling can block. That may be acceptable, but for a live protocol client it should be an intentional policy.

Direction:

- Decide whether protocol events should block, drop lower-priority events, or apply backpressure.
- Add tests around burst behavior.
- Log or surface degraded state when event queues overflow.

### Go Version May Limit Contributors

The module requires Go 1.26, and local validation used Go 1.26.2. If Go 1.26 is the intended public baseline, document that clearly. If not, consider lowering the minimum supported Go version.

Direction:

- Decide the supported Go version.
- Add it to README, CI, and release notes.
- Avoid relying on newer Go features unless they are worth the install friction.

## Suggested Implementation Order

1. Tighten runtime permissions for config, state, session, cache, media, and SQLite files.
2. Sync README, `PLAN.md`, generated config defaults, and `config.example.toml`.
3. Decide public repository path and update `go.mod`.
4. Add explicit unofficial-client, privacy, and local-storage warnings.
5. Add `govulncheck`, static analysis, secret scanning, and release artifact smoke tests to CI.
6. Replace the current mutable Windows-only release flow with tagged, versioned Linux and Windows releases.
7. Run and document the live WhatsApp validation matrix.
8. Add contributor/security/release docs.
9. Clean up stale not-implemented symbols and public-facing fallback messages.
10. Start gradual maintainability splits only after release-blocking behavior is stable.

## Publish Recommendation

Safe to share now:

- With a small trusted group.
- As source code only.
- With a clear "pre-alpha, use at your own risk" label.
- With explicit warnings that local WhatsApp messages and session state are stored on disk.

Not ready yet:

- Package-manager distribution.
- Broad social announcement.
- End-user release binaries presented as stable.
- Claims of complete WhatsApp functionality.

The most important release hardening task is local data protection. After that, the next highest-impact work is making public documentation match the actual state of the application and validating the live WhatsApp path on real accounts.
