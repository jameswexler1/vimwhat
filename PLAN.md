# Vim-Centric WhatsApp TUI for Linux

## Current Stage

### Reliability remediation (September 2026)

The supported product is Linux-only. Each item below is a separate,
revertible commit, with focused regression tests and final Linux tests/vet/race
validation. No live account traffic is required for automated validation.

- [x] Isolate failed-send recovery by chat and composer version; late protocol failures retain content on failed messages without overwriting drafts.
- [x] Release search cursors before loading message details (boundary tests cover fewer, exactly, and more than the result limit).
- [x] Preserve newer edits and monotonic receipts during replay, alias merges, and send ACKs; serialize receipt comparisons with writes.
- [x] Persist complete composer drafts, serialize revisioned saves without blocking UI reservations on disk I/O, reuse private retained attachment copies, clear them on explicit logout, and flush on shutdown.
- [x] Recover interrupted outgoing rows as uncertain (never auto-resend); settle cancellation with an independent bounded context and retry text/media using the original delivery ID with atomic duplicate-retry protection.
- [x] Persist local text/media/sticker forwarding payloads, queue forwarded payloads atomically, and rewrite edited text/captions transactionally without allowing late ACKs to replace newer content.
- [x] Track local unread messages with an additive migration; acknowledgements idempotently clear only their targets and preserve concurrent/delayed arrivals and unacknowledged counts.
- [x] Persist explicit invalidation of missing media cache paths, with compare-and-clear protection for concurrent downloads.
- [x] Default Linux image/video/file openers to auto capability probing; preserve strict explicit command overrides and document the distinction in generated/example config.
- [x] Bound retained history to eight chats and 400 messages per window, page in both directions, reload around historical focus, and complete pending quote jumps using direct bounded target lookup.
- [x] Retry initial connection failures with cancellable exponential backoff capped at 30 seconds; keep chats/history/drafts usable during background sync while gating protocol actions until readiness.
- [x] Extract composer persistence/actions, history windows/loading, sync progress/state, live event orchestration, outgoing sends/recovery, and message actions into focused files with regression tests preserved.
- [x] Remove Windows support code/tests/obligations and configure Linux amd64/arm64 CI artifacts plus version-tagged releases.

Schema changes are additive; reverting code must not delete user messages or
drafts. Delivery with an uncertain ACK must remain visibly uncertain until
reconciled or explicitly retried, never silently resent on startup.

### Verification and rollback index

Verified on Linux on 2026-09-23:

- `make test`, `make lint`, and `git diff --check` passed on the final code.
- `make test-race` passed across all packages; targeted app/UI race tests passed again after the final draft and retry follow-ups.
- Static Linux amd64 and arm64 builds passed (`CGO_ENABLED=0`, `-trimpath`). The amd64 `version` and fresh-XDG `doctor` smoke checks passed; arm64 was cross-built, not executed.
- No real-account sending, receipt delivery, notification delivery, or pairing was performed during this remediation. CI/release configuration is committed locally, not remotely run or published.

The starting revision was `706dd85`. Each change can be inspected with `git show <commit>` and reversed with `git revert <commit>`; dependent later edits may require resolving conflicts. Prefer newest-first when rolling back the whole series. The additive unread migration remains in the database when code is reverted.

| Commit | Change |
| --- | --- |
| `823d94b` | Remediation plan |
| `8a19ad9` | Search connection starvation |
| `9af35d1` | Replay/edit/receipt ordering |
| `1d95898` | Missing media cache invalidation |
| `944c3e8` | Late send/composer isolation |
| `9618cc5` | Full durable drafts and ordered shutdown flush |
| `3371a63` | Interrupted-send recovery and stable-ID retries |
| `0e10b3f` | Forwarding payload consistency |
| `c61426b` | Targeted unread acknowledgements |
| `5b76da7` | Automatic Linux opener defaults |
| `30b5775` | Bounded history and quote jumps |
| `f5e9773` | Startup reconnect and nonblocking sync |
| `dd59ce4` | Subsystem extraction |
| `00f0828` | Linux-only support, documentation, and release CI |
| `a6d5744` | Nonblocking draft writes and attachment lifecycle |
| `437d367` | Consistent media/sticker retry helpers |

### Implemented now

- DB-first Linux client with XDG/private state, first-run configuration, SQLite migrations, chat/message/media/contact storage, FTS search, and demo/export/doctor CLI helpers.
- Modal Bubble Tea UI, configurable keys, chat/message search and filters, visual selection, reactions, forwarding, text edits/revokes, replies, quote jumps, mentions, typing presence, and external-editor composition.
- Full durable per-chat drafts with ordered/debounced saves and shutdown flush; transient outgoing/draft attachments copied to private data storage.
- Real paired WhatsApp sessions, QR login/logout, live ingestion, canonical PN/LID identity repair, on-demand remote history, metadata sync, remote media/sticker/avatar downloads, and native Linux notifications.
- Text and single-attachment/sticker sends with durable status, interrupted-send recovery, and explicit same-ID text/media retries. Uncertain delivery is never silently retried.
- Replay-safe edits/receipts/payloads, transactional forwarded-message queueing, targeted read acknowledgements, and persisted missing-cache invalidation.
- Bounded history windows and chat retention with direct target lookup; initial reconnect backoff and nonblocking local UI during sync.
- Linux media previews (`ueberzug++` → `chafa` → external), automatic image/video/file opener defaults, argv-safe configurable integrations, clipboard and audio playback.
- Linux amd64/arm64 CI artifacts; tests, vet, race checks, and version-tag-only release publication with checksums.

### Remaining validation and limits

- Real-account daily-use validation of notifications, reconnect, remote history/media, sending, stickers, avatars, and uncertain-delivery reconciliation. Automated tests do not prove live protocol compatibility.
- Voice-note/PTT-specific send semantics beyond generic audio/document attachments.
- Retained draft/outgoing attachment files are deliberately not automatically garbage-collected yet; preserve recoverability and plan reference-aware cleanup separately.
- Calls, channels/newsletters, statuses, community administration, and business-only features are outside v1.
- Hard process termination can lose edits made within the 250 ms debounce interval; normal shutdown flushes synchronously. Reverting additive migrations does not delete data, but old binaries cannot understand newer recovery/draft semantics.

## Summary

Build a personal native Linux WhatsApp TUI in `Go` using `whatsmeow` for protocol access, `Bubble Tea` for the event loop/UI runtime, `Lip Gloss` only for styling, and `SQLite + FTS5` for local state, indexing, and lazy history. The product is a fully modal client, not a terminal chat app with vi-flavored shortcuts.

The app should feel closer to `vim` plus `yazi` than to WhatsApp Web: fast keyboard navigation, explicit modes, repeatable actions, registers, visual selection, `/` search with context-specific behavior and visible match counts, command-line actions via `:`, optional inline composition, optional `nvim` composition for long messages, and adaptive media/emoji rendering that works in `st` first but degrades cleanly elsewhere.

## Key Changes

### Platform and stack

- Language: `Go`.
- Protocol library: `go.mau.fi/whatsmeow`.
- TUI runtime: `Bubble Tea`.
- Styling/layout: lightweight use of `Lip Gloss`; no heavy widget dependency.
- Storage: `SQLite` in native per-user data dir, plaintext in v1.
- Search: `SQLite FTS5` for chat and message indexing.
- Media preview backends are platform-aware:
  Linux auto prefers `ueberzug++`, then `chafa`, then the external opener. Explicit backend settings are respected when available.
- Packaging: single static-ish Linux amd64 and arm64 binaries plus native config/data/cache dirs; no Arch-only assumptions in runtime behavior.

### User-facing interface

- Binary name: `vimwhat`.
- CLI surface:
  `vimwhat`
  `vimwhat login`
  `vimwhat logout`
  `vimwhat doctor`
  `vimwhat media open <message-id>`
  `vimwhat export chat <jid>`
- Config file: `$XDG_CONFIG_HOME/vimwhat/config.toml` (default `~/.config/vimwhat/config.toml`).
- Config supports `emoji_mode = "auto" | "full" | "compat"`, per-mode indicator colors via `indicator_normal`, `indicator_insert`, `indicator_visual`, `indicator_command`, `indicator_search`, a notification mute indicator via `indicator_notifications_muted`, flat `key_<mode>_<action>` keybinding overrides, plus `notification_backend = "auto" | "none" | "command" | "linux-dbus"` and `notification_command` for desktop delivery overrides.
- Data dir: `$XDG_DATA_HOME/vimwhat/` (default `~/.local/share/vimwhat/`).
- Cache dir: `$XDG_CACHE_HOME/vimwhat/` (default `~/.cache/vimwhat/`).
- Transient cache dir: per-user app directory under `os.TempDir()`.
- State file groups:
  WhatsApp device/session store,
  app SQLite database,
  logs,
  non-exported media cache,
  preview cache.
- Durable local state is private by default: startup repairs config/data dirs to `0700` and config/state/session files to `0600`.
- Transient media, sticker, and preview caches stay compatibility-managed because external preview, opener, picker, and download helpers need to consume those paths.
- `vimwhat doctor` reports runtime permission diagnostics for config/data dirs plus SQLite state/session files and sidecars.
- Core panes:
  left chat list,
  main message viewport,
  optional right info/details pane,
  bottom status/command/composer line.
- Layout must resize cleanly for narrow terminals; when width is low, collapse to one focused pane with Vim-style window switching.

### Modal interaction model

- Modes:
  `normal`, `insert`, `visual`, `forward`, `command`, `search`.
- `normal` mode:
  motion across chats/messages,
  window focus movement,
  counts,
  marks,
  jumps,
  open thread/chat,
  reply/react/forward/open media/download/archive commands,
  pane toggles and filters.
- `insert` mode:
  inline composition only;
  user can stay here for all messages if desired.
- `visual` mode:
  message-wise selection only in v1, not character-wise text editing inside a message;
  selection supports yank, copy to register, forwarding, delete-for-everyone, export, and batch download of attachments.
- `forward` mode:
  recipient picker for focused normal-mode messages or selected visual-mode messages, with configurable Vim-style movement, slash search, toggle, send, and cancel bindings.
- `command` mode:
  `:` command line for app actions such as open chat, filter unread, sync, doctor, switch backend, compose in editor, export, clear preview cache, quit.
- `search` mode:
  `/` in chat list searches chats,
  `/` in message pane searches current chat contents,
  `n`/`N` repeat movement,
  results are incremental and backed by FTS,
  the status bar shows the current match count,
  `Esc` clears the active search after a search has been submitted.
- Registers:
  unnamed register plus named registers `a-z` for yanked message text or selected message blocks;
  optional system clipboard integration through configured external command.
- Marks:
  local chat marks and global marks for quick jump targets.
- Repeat:
  last structural action repeat with `.` for actions that are safe and deterministic.
- No mouse dependence in the core workflow.

### Composition model

- Default compose path: inline composer in the TUI.
- Optional external compose path: open current draft in `nvim` through temp file + blocking editor session.
- External editor is user-configurable and defaults to `$EDITOR`, with `nvim` examples in docs.
- User can choose either flow per message without changing global mode.
- Drafts persist per chat in SQLite and survive restart.
- Composer capabilities in v1:
  multiline text,
  emoji input via plain text entry,
  quoted reply context,
  attachment insertion command,
  send/cancel,
  draft restore.
- No requirement that long messages must use `nvim`; `nvim` is an optional accelerator only.

### WhatsApp capabilities in v1

- Supported chat types:
  direct messages and groups.
- Supported actions:
  receive/send text,
  receive/send common attachments,
  quoted replies,
  own outgoing text edits and own-message revoke,
  selected outgoing message revoke from visual mode,
  forwarding selected messages from visual mode,
  reactions,
  read receipts handling,
  typing presence display if exposed cleanly,
  unread state,
  pinned/muted indicators if available through app state sync.
- Deferred from v1 unless trivial during implementation:
  calls,
  channels/newsletters,
  status posting/viewing,
  business-only features,
  batch or media-caption message edit/revoke UI,
  starred messages,
  community management.
- If `whatsmeow` exposes revoke/edit primitives cleanly, keep internal architecture ready for them, but do not make them required for initial ship.

## Near-Term Execution Order

1. Run a manual notification pass first: inactive-chat delivery, focused-window active-chat suppression, blurred-window active-chat delivery, muted-chat suppression, Linux native backend selection, `notification_command` override behavior, and a fresh-config verification that `notification_backend` truly defaults to `auto` without requiring an explicit config entry.
2. Validate remote media download plus sticker auto-render/download and chat-avatar refresh on live WhatsApp traffic.
3. Validate real text send for plain text composer submissions against live direct and group chats.
4. Validate protocol-backed read receipts, reactions, presence, replies/quote-jump, and the right-edge `l` reply gesture against real chats.
5. Validate attachment upload/send, durable full drafts, and same-ID text/media retries on real chats, including generic audio/document fallback and uncertain acknowledgements.

### Current protocol milestone

The QR pairing milestone is complete as of 2026-04-22: `vimwhat login` can pair successfully, `vimwhat logout` clears the local/remote session plus app state back to first-use, rejected partial sessions are cleaned up, and `doctor` reports local pairing state.

The live read-only sync milestone is implemented and has been manually validated against real inbound text, image, sticker, file metadata, and profile-picture change traffic:

- Bootstrap a `whatsmeow` client from the paired session when the TUI starts.
- Show connection/auth state in the status line and block the TUI behind sync progress while startup or reconnect readiness is pending.
- Subscribe to `whatsmeow` events in one protocol-owned goroutine.
- Convert incoming chat, message, receipt, and media metadata events into the existing `internal/store` schema through `internal/whatsapp.Ingestor`.
- Keep outbound sending disabled or clearly marked pending until incoming event ingestion is stable.
- Add tests with a mocked protocol event source before relying on manual WhatsApp traffic.
- Protocol maintenance is active: after local sync stopped around 2026-06-06/2026-06-07 on the January 2026 `whatsmeow` snapshot, the dependency was refreshed to the 2026-06-11 upstream snapshot and the direct-path media download adapter was updated for the current hash-validation API.

The remote history fetch milestone now has an implemented first pass:

- Keep startup DB-first; cached chat rendering remains instant while WhatsApp connects in the background.
- Load older rows from SQLite before making any remote request.
- Trigger remote history from `:history fetch` or by scrolling above the loaded message window.
- Request up to 50 messages before the oldest known local message using `BuildHistorySyncRequest` and `SendPeerMessage`.
- Normalize `ON_DEMAND` `HistorySync` responses into internal chat/message/media events through `internal/whatsapp.Ingestor`.
- Persist historical messages idempotently without incrementing unread counts.
- Track per-chat end-of-history state in `sync_cursors`.
- Keep the TUI usable while importing large chats by batching refreshes, preserving the user's selected chat across stale reloads, rendering only a bounded message window, and avoiding repeated overlay sync commands for unchanged media placements.

The remote media download milestone now has an implemented first pass:

- Persist WhatsApp download descriptors for received image, video, audio, and document messages.
- Download media on demand through the live WhatsApp session into the app media cache, using temp-file writes and atomic rename.
- Update SQLite media state through `remote`, `downloading`, `downloaded`, and `failed`.
- Reuse existing TUI preview/open/save/audio flows once the file has a local path.
- Suppress duplicate focused-message download requests while a download is already in flight.

The attachment upload/send milestone now has an implemented first pass:

- Queue live single-attachment sends separately from text sends while preserving the existing composer/staging flow.
- Upload local image, video, audio, and document files through `whatsmeow` and send them with precomputed WhatsApp message IDs.
- Persist outgoing media messages locally before upload with `sending` / `sent` / `failed` status transitions and file-backed media rows so preview/open/save keep working on failures.
- Use the composer body as the caption for image, video, and document sends; reject audio captions before queueing.
- Reuse quoted reply metadata for outgoing media messages so replied-to media sends carry the same context shape as text replies.
- Paste clipboard images into the composer as the same single staged attachment used by normal media sends; retain transient images durably for drafts/outgoing messages. Copy focused downloaded images through configured or detected Linux clipboard tools.

The remaining CLI surface milestone is now implemented:

- `vimwhat media open <message-id>` can open a stored attachment directly from the CLI and auto-download it through the paired WhatsApp session when only remote metadata exists locally.
- `vimwhat export chat <jid>` can export the locally persisted conversation as a Markdown transcript into the configured downloads directory, including reply markers and media placeholders/local paths.

The large-chat and title-correctness hardening milestone is implemented:

- Add `title_source` to chat rows and only allow stronger title sources to replace weaker JID/placeholders.
- Treat group subjects from history sync, joined-group metadata, and group-info events as authoritative.
- Ingest contact and push-name updates without creating empty chats or erasing better saved names.
- Canonicalize direct chats onto the mapped WhatsApp LID identity when PN/LID aliases are known, and merge split alias threads so one person cannot appear as multiple chats after history sync or mixed-device traffic.
- Refresh joined group/contact metadata after the live WhatsApp connection comes online, without blocking TUI startup.
- Display neutral group placeholders when old rows contain phone-like/JID-derived group titles.
- Debounce live DB snapshot refreshes, consolidate reconnect catch-up as nonblocking background progress and a final snapshot barrier, require WhatsApp's ordered offline-complete marker before readiness, treat pre-marker inactivity only as a stall warning, wait for known post-marker message recoveries with a bounded fallback, summarize catch-up notifications once, and bound message render windows for chats with hundreds of loaded messages.

The TUI stability and modal polish milestone is implemented:

- Message scrolling now keeps the cursor moving inside the viewport until it reaches a viewport edge, matching chat-list navigation instead of pinning the selected message to the bottom.
- Emoji rendering is configurable through `emoji_mode`; `auto` preserves full emoji on capable UTF-8 terminals and falls back to compatibility rendering for terminals such as `st`.
- The status bar has a single authoritative mode indicator, keeps pywal colors by default, and supports per-mode hex overrides.
- `/` search shows match counts in the status bar and `Esc` clears active search state without requiring a blank search.
- The current chat/message cursor items use stronger terminal-safe border treatments, while visual-mode message ranges use a dark theme-blended tint, a full-strength configurable accent rail, readable foreground contrast, and a distinct thick endpoint cursor so every selected bubble is immediately recognizable without looking like an opaque form field.
- Normal mode can yank the focused message body directly, while visual mode continues to yank selected message ranges through the same register/clipboard path.
- Active-chat refreshes auto-follow newly appended messages only when the cursor was already on the previous latest message; otherwise the viewport stays anchored, a persistent `Mensagens novas: X` divider is inserted into the chat flow until the user reaches the tail, and the footer/composer shows a compact down-arrow count for messages still below the viewport.
- Unread chat counters render as compact highlighted badges capped at `99+`, while thick borders remain reserved for cursor/focus state.
- The help overlay is structured into quick actions, navigation, media, mode, command, and state sections while preserving configured key labels.
- The insert composer now keeps the visible tail of the last line on screen at display-width boundaries, including multi-line drafts and wide grapheme clusters, instead of leaving freshly typed text off-screen.
- Tests cover large-chat/message viewport behavior, emoji compatibility, indicator config parsing, status color resolution, search counts, search clearing, current-item cursor styling, visual-mode selection styling, unread badge rendering, and help overlay rendering.

The desktop notification milestone is implemented:

- Add `notification_backend` selection plus `notification_command` override support in config, with `doctor` reporting the selected delivery path and backend availability.
- Deliver notifications only for genuinely new incoming messages, suppressing duplicates, outgoing sends, historical imports, reaction-only updates, WhatsApp-muted chats, locally globally muted notification state, and the currently selected chat only while the app window is known to be focused.
- Sync mute/pin chat settings from WhatsApp app-state patches, persist timed mute expiry, and preserve known settings when generic message chat upserts arrive without setting metadata.
- Format notification payloads from normalized message previews so bodyless media messages still show attachment-aware summaries.
- Auto-select Linux notification helpers (`notify-send`, `gdbus`, `dbus-send`) with argv-safe overrides, cached avatar icons, and helper fallback.

The recent-sticker send milestone now has an implemented first pass:

- Normalize WhatsApp history-sync `recentStickers` and app-state sticker actions into internal recent-sticker events.
- Persist recent sticker metadata in SQLite and cache renderable WebP sticker files in the transient app directory when remote download descriptors are available.
- Add a configurable normal-mode sticker picker binding, defaulting to `leader t`, plus `:sticker` / `:pick-sticker` commands.
- Fetch WhatsApp app-state once after the live WhatsApp session connects, applying favorite/recent sticker metadata and chat mute/pin settings in a background startup task so the live event loop can keep processing DB catch-up and user-visible updates.
- Treat favorite-sticker file caching as best-effort background work: try WhatsApp media URLs before direct paths, persist metadata even when an individual sticker download is stale/unavailable, continue with other stickers, and report the first cache failure reason alongside unavailable counts without blocking startup navigation.
- Use `nsxiv -t -o -p {files}` as the default sticker picker; keep commands configurable.
- Send selected stickers through a dedicated WhatsApp sticker message rather than as generic image attachments, preserving the composer/draft state.
- Keep Lottie/TGS stickers out of picker/send until a compatible render/send path exists.

The composer, forwarding, and presence polish milestone now has an implemented first pass:

- `Shift+Enter` is the generated default alternate newline binding for insert mode, with `ctrl+j` retained as the portable fallback and all bindings kept editable through config.
- The insert composer soft-wraps long input by display width inside the footer, preserves explicit newlines in the draft/body, and keeps the cursor visible without relying on terminal edge wrapping.
- Normal mode can forward the focused message with `key_normal_forward = "f"`, while visual mode can forward the selected message range through the same recipient picker with `key_visual_forward = "f"`; the picker uses Vim-style `j`/`k` movement and `/` contact search before typing filter text.
- Normal mode can delete the focused outgoing message for everybody with `key_normal_delete_for_everybody = "leader d e"`, while visual mode can apply the same WhatsApp revoke path to the selected outgoing range with `key_visual_delete_for_everybody = "leader d e"` after uppercase-`Y` confirmation; completed deletes stay visible as "This message was deleted" tombstones.
- Forwarding preserves WhatsApp forwarded metadata for received source messages, while self-authored outgoing source messages are resent without the forwarded tag.
- WhatsApp message protobuf payloads are persisted for ingested messages so forwarding can resend the original supported message shape instead of reconstructing from rendered text/media metadata.
- Forwarded messages are persisted locally as outgoing `sending` rows before protocol send, then transition to sent/failed through the same live-update path as other sends.
- Typing presence is shown in the active chat header and chat-list previews, direct-chat online/last-seen presence is shown in the active chat header when WhatsApp exposes it, and presence subscriptions are refreshed when the live connection returns online.

The next protocol milestone is live validation/polish of the completed notification, media-send, recent-sticker, and forwarding paths, especially Linux desktop delivery, audio uploads, recent-sticker send on real synced accounts, visual forward behavior on mixed text/media selections, and follow-on resend/draft UX around failed outgoing attachments.

### Data model and lazy loading

- SQLite is the source of truth for local app state.
- Separate tables/indexes for:
  chats,
  chat metadata,
  contacts,
  messages,
  message bodies,
  message search terms,
  message mentions,
  group participants,
  media metadata,
  drafts,
  marks/register snapshots,
  sync cursors,
  unread counters,
  preview cache metadata.
- Message bodies indexed via FTS5 with normalized search text.
- Lazy loading behavior:
  app boots from local DB immediately,
  chat list renders from DB first,
  opening a chat loads only a visible window plus small buffer around viewport,
  scrolling upward requests older pages from DB first and then remote sync when missing,
  scrolling downward uses DB/local events first,
  background prefetch keeps a small horizon above and below current viewport.
- History sync strategy:
  initial login performs required app state sync and minimal recent history acquisition,
  older history is fetched on demand using whatsmeow history sync/message request primitives,
  giant chats are never fully materialized in memory,
  media bytes are never fetched unless preview/download is requested.
- Event ingestion:
  new WhatsApp events append/update DB first,
  UI subscribes to local state changes rather than using the protocol client as the rendering source.

### Media handling

- Media pipeline stages:
  metadata discovered from incoming message,
  thumbnail fetched when available,
  full media downloaded only on explicit preview/open/save or when needed for image preview generation.
- Message viewport media behavior:
  images render inline in the chat history through the selected backend,
  videos show an in-chat thumbnail/first-frame preview plus metadata,
  audio shows an in-chat compact player/metadata row with playback command integration,
  documents show an in-chat attachment row with icon, name, mime, size, and open/save actions,
  `ueberzug++` image/sticker overlays reserve blank space during scroll/focus transition resync instead of dropping to low-resolution text fallbacks.
- Optional right info/details pane behavior:
  shows verbose metadata, debug/status information, selected message details, and alternate actions only when toggled on;
  it is not the primary media preview surface.
- Backend detection occurs at startup and can be re-run with `:doctor` or `:preview-backend auto`.
- Preview backend order in v1:
  Unix `ueberzug++`,

  `chafa`,
  external opener.
- The app must remain fully usable without graphical preview support.

### Suggested UX features to include in v1

- Unread-only filter and pinned-first sorting toggle.
- Jump to last unread in current chat.
- Chat and message search history.
- Quote-jump: from a reply, jump to the referenced message if present locally, else fetch around it.
- Per-chat draft indicator in the chat list.
- Desktop notifications for inactive, unmuted chats with native backend auto-detection and command override support.
- Download/open attachment commands with sane default save paths.
- `:help` with discoverable keymaps, mode-specific bindings, and configured key hints.
- Editable keymap overrides in the generated default `config.toml`, while still shipping a strong default instead of making the user design their own from scratch.

### Internal architecture

- Core modules:
  protocol adapter,
  sync service,
  SQLite repositories,
  search/index service,
  media service,
  preview backend manager,
  modal input/keymap engine,
  Bubble Tea models for panes and overlays,
  command parser,
  composer service,
  config/logging/doctor utilities.
- Concurrency model:
  one protocol event ingestion path,
  one DB writer queue or transaction manager,
  background workers for media download, thumbnail generation, indexing, and history prefetch,
  UI reads from snapshots/state reducers to avoid direct protocol coupling.
- Failure handling:
  protocol reconnect loop with visible status,
  DB corruption detection in `doctor`,
  preview backend failures degrade to next backend without crashing UI,
  failed media downloads become retryable items in-message.

## Test Plan

- Unit tests:
  modal state transitions,
  keymap resolution with counts/registers/mode context,
  command parsing,
  search query routing by focused pane,
  lazy-loading window calculations,
  preview backend selection,
  notification backend selection and command templating,
  draft persistence,
  message selection/yank behavior.
- Integration tests:
  protocol adapter against a mocked whatsmeow-facing layer,
  SQLite migrations,
  event ingestion into DB,
  notification suppression for duplicate/historical/outgoing events,
  FTS indexing and search results,
  history page fetch and viewport refill,
  media metadata to preview pipeline.
- TUI behavior tests:
  snapshot tests for major panes and narrow-width layouts,
  mode line/status line updates,
  search overlays,
  search match counts and `Esc` clearing,
  emoji width/sanitization behavior,
  mode indicator config overrides,
  large chat scrolling,
  visual selection rendering.
- Manual acceptance scenarios:
  first login and QR pairing,
  restart with restored session,
  chat list search with thousands of chats,
  open huge chat and scroll up through unloaded history,
  search inside large chat,
  select several messages in visual mode and yank to register,
  compose inline only for an entire session,
  open one draft in `nvim`, return, and send,
  preview image/video/document in `st`,
  operate with no image backend available,
  receive a new message while another chat is selected and confirm one desktop notification appears,
  receive new messages while browsing old history,
  send image/document/audio file,
  reply to a message and jump back to source.
- Performance targets:
  cold start should render cached chat list before network sync,
  opening a cached chat should feel immediate,
  scrolling should not block on remote fetch,
  memory use should be bounded by viewport windows and cache policy, not total history size.

## Assumptions and defaults

- The supported targets are Linux desktop terminals, especially `st`. Preserve correct terminal layout and emoji widths.
- The app is single-user and local-first; no multi-account support in v1.
- Plaintext SQLite is acceptable because host-level security is assumed.
- Default UX favors a complete Vim model over beginner discoverability.
- Default compose mode is inline; external `nvim` compose is optional per message and never mandatory.
- Default preview mode is auto-detect with graceful fallback.
- Default notification mode is auto-detect with native OS delivery; when `notification_command` is set alongside `notification_backend = "auto"`, the configured command overrides native selection.
- Default notification policy is one desktop notification per genuinely new incoming message in an inactive, unmuted chat, plus the selected chat whenever app-window focus is unknown or blurred.
- Default emoji mode is `auto`; terminals known to misreport complex emoji widths should use the compatibility renderer unless explicitly forced to `full`.
- Default mode indicator colors come from pywal; users can override each mode with a hex color in config.
- v1 supports DMs and groups only.
- v1 includes replies, reactions, media send/receive, lazy history, search, visual selection, registers, drafts, and notifications.
- v1 excludes calls, channels/status, and broad WhatsApp surface-area parity.
- If upstream WhatsApp protocol changes break behavior, the app should fail visibly and recover safely, but protocol resilience beyond ordinary reconnect logic is not a v1 feature.
