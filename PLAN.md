# Vim-Centric WhatsApp TUI for Linux

## Current Stage

Implementation is past the local-shell phase and currently sits at a DB-first, live WhatsApp client with remote history/media support, outbound text and single-attachment media send, protocol-backed read receipts, reactions, replies, quote-jump, typing presence, and the planned media/export CLI helpers. The next major gaps are live validation/polish of the media-send path, especially the known audio-send issue, plus follow-on UX such as retries and attachment draft persistence.

### Implemented now

- Go CLI entrypoint with `vimwhat`, `doctor`, `demo seed`, and `demo clear`.
- XDG config/data/cache path resolution and config loading, including emoji rendering mode and per-mode status indicator color overrides.
- SQLite-backed local state with migrations, chat/message/media/draft storage, stats, and FTS-backed search.
- Bubble Tea TUI with modal interaction (`normal`, `insert`, `visual`, `command`, `search`), chat list, message viewport, optional info pane, composer, filters, and help.
- Local draft persistence, local outgoing message persistence, clipboard integration, attachment staging, message delete flow, and search routing by pane.
- Media backend detection and in-chat preview behavior with `sixel`, `ueberzug++`, `chafa`, compact audio playback rows via `mpv`, plus external open/save fallback paths.
- Real `whatsmeow` session store, QR login, logout, rejected-session cleanup, and `doctor` session status reporting.
- Live WhatsApp connection bootstrap from a paired session, protocol event subscription, inbound chat/message/receipt/media metadata ingestion into SQLite, DB-first UI refreshes, and visible connection state.
- On-demand remote history fetch for the focused chat, using SQLite paging first and then anchored `whatsmeow` history sync requests before the oldest known local message.
- Protocol-backed remote media download for received images, videos, audio, and documents, using persisted WhatsApp download descriptors and cached local files.
- Real outbound send from the inline composer for plain text plus one local attachment per message, with precomputed WhatsApp message IDs, local `sending`/`sent`/`failed` status updates, draft preservation on failure, captions for image/video/document sends, and audio-caption rejection before queueing.
- CLI helpers for persisted data: `vimwhat media open <message-id>` reuses the normal opener flow and auto-downloads remote media when possible, while `vimwhat export chat <jid>` writes a local-only Markdown transcript into the configured downloads directory.
- Protocol-backed message interactions: auto/manual mark-read, reaction send/clear plus reaction rendering, text replies with quoted metadata, quote-jump into loaded history, direct-chat typing presence subscription/display, and best-effort composing/paused presence send while typing.
- Chat title quality tracking with source precedence, group/contact metadata refresh, and safe placeholders so group JIDs/phone-like IDs are not treated as real names.
- Large-history TUI guardrails: historical imports avoid refresh storms, live refreshes are debounced, stale snapshot reloads do not steal chat focus, message rendering is bounded to the visible window, message cursor scrolling behaves like the chat list viewport, duplicate in-flight history requests are suppressed, and `ueberzug++` overlays are cleared while scrolling.
- Terminal/UI polish for real chat data: full/compat/auto emoji rendering, stable emoji fallback for terminals such as `st`, pywal-backed mode indicators with per-mode hex overrides, non-redundant mode prompts, search match counts in the status bar, and `Esc` clearing active search state from both search and normal mode.
- Demo/dev workflows that exercise the full local UI without a live WhatsApp session.

### In progress

- Manual validation and polish of remote media download, outbound text/media send, and the new message interaction flows against real WhatsApp traffic.
- Follow-on UX around failed outgoing media retries, attachment draft persistence, CLI export/open polish, and investigation of the known audio-send failure.

### Not implemented yet

- Retry/resend UX for failed outgoing media messages.
- Attachment draft persistence across restart or failed async send.
- Audio-send reliability fixes beyond the current first-pass attachment send flow.

## Summary

Build a personal, Linux-first WhatsApp TUI in `Go` using `whatsmeow` for protocol access, `Bubble Tea` for the event loop/UI runtime, `Lip Gloss` only for styling, and `SQLite + FTS5` for local state, indexing, and lazy history. The product is a fully modal client, not a terminal chat app with vi-flavored shortcuts.

The app should feel closer to `vim` plus `yazi` than to WhatsApp Web: fast keyboard navigation, explicit modes, repeatable actions, registers, visual selection, `/` search with context-specific behavior and visible match counts, command-line actions via `:`, optional inline composition, optional `nvim` composition for long messages, and adaptive media/emoji rendering that works in `st` first but degrades cleanly elsewhere.

## Key Changes

### Platform and stack

- Language: `Go`.
- Protocol library: `go.mau.fi/whatsmeow`.
- TUI runtime: `Bubble Tea`.
- Styling/layout: lightweight use of `Lip Gloss`; no heavy widget dependency.
- Storage: `SQLite` in XDG data dir, plaintext in v1.
- Search: `SQLite FTS5` for chat and message indexing.
- Media preview backends, in priority order:
  `sixel` if available in the current terminal,
  `ueberzug++` if available,
  `chafa` text/image fallback,
  external opener fallback via `xdg-open`/configured command.
- Packaging: single static-ish Linux binary plus config/data/cache dirs under XDG; no Arch-only assumptions in runtime behavior.

### User-facing interface

- Binary name: `vimwhat`.
- CLI surface:
  `vimwhat`
  `vimwhat login`
  `vimwhat logout`
  `vimwhat doctor`
  `vimwhat media open <message-id>`
  `vimwhat export chat <jid>`
- Config file: `$XDG_CONFIG_HOME/vimwhat/config.toml`.
- Config supports `emoji_mode = "auto" | "full" | "compat"` and per-mode indicator colors via `indicator_normal`, `indicator_insert`, `indicator_visual`, `indicator_command`, and `indicator_search`, each defaulting to `"pywal"` with hex overrides allowed.
- Data dir: `$XDG_DATA_HOME/vimwhat/`.
- Cache dir: `$XDG_CACHE_HOME/vimwhat/`.
- State file groups:
  WhatsApp device/session store,
  app SQLite database,
  media cache,
  preview cache,
  logs.
- Core panes:
  left chat list,
  main message viewport,
  optional right info/details pane,
  bottom status/command/composer line.
- Layout must resize cleanly for narrow terminals; when width is low, collapse to one focused pane with Vim-style window switching.

### Modal interaction model

- Modes:
  `normal`, `insert`, `visual`, `command`, `search`.
- `normal` mode:
  motion across chats/messages,
  window focus movement,
  counts,
  marks,
  jumps,
  open thread/chat,
  reply/react/open media/download/archive commands,
  pane toggles and filters.
- `insert` mode:
  inline composition only;
  user can stay here for all messages if desired.
- `visual` mode:
  message-wise selection only in v1, not character-wise text editing inside a message;
  selection supports yank, copy to register, export, forward-ready buffer later, and batch download of attachments.
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
  full message edit/revoke UI,
  starred messages,
  community management.
- If `whatsmeow` exposes revoke/edit primitives cleanly, keep internal architecture ready for them, but do not make them required for initial ship.

## Near-Term Execution Order

1. Run a quick manual regression pass over the recent TUI hardening: big group scrolling, emoji-heavy messages, search counts/clearance, and custom mode indicators.
2. Validate remote media download on live WhatsApp traffic.
3. Validate real text send for plain text composer submissions against live direct and group chats.
4. Validate protocol-backed read receipts, reactions, presence, and replies/quote-jump against real chats.
5. Validate attachment upload/send on real chats, especially audio, then polish retry/draft UX and the new CLI export/open flows based on real usage.

### Current protocol milestone

The QR pairing milestone is complete as of 2026-04-22: `vimwhat login` can pair successfully, `vimwhat logout` clears the local/remote session, rejected partial sessions are cleaned up, and `doctor` reports local pairing state.

The live read-only sync milestone is implemented and has been manually validated against real inbound text, image, and file metadata traffic:

- Bootstrap a `whatsmeow` client from the paired session when the TUI starts.
- Show connection/auth state in the status line without blocking cached DB rendering.
- Subscribe to `whatsmeow` events in one protocol-owned goroutine.
- Convert incoming chat, message, receipt, and media metadata events into the existing `internal/store` schema through `internal/whatsapp.Ingestor`.
- Keep outbound sending disabled or clearly marked pending until incoming event ingestion is stable.
- Add tests with a mocked protocol event source before relying on manual WhatsApp traffic.

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

The remaining CLI surface milestone is now implemented:

- `vimwhat media open <message-id>` can open a stored attachment directly from the CLI and auto-download it through the paired WhatsApp session when only remote metadata exists locally.
- `vimwhat export chat <jid>` can export the locally persisted conversation as a Markdown transcript into the configured downloads directory, including reply markers and media placeholders/local paths.

The large-chat and title-correctness hardening milestone is implemented:

- Add `title_source` to chat rows and only allow stronger title sources to replace weaker JID/placeholders.
- Treat group subjects from history sync, joined-group metadata, and group-info events as authoritative.
- Ingest contact and push-name updates without creating empty chats or erasing better saved names.
- Refresh joined group/contact metadata after the live WhatsApp connection comes online, without blocking TUI startup.
- Display neutral group placeholders when old rows contain phone-like/JID-derived group titles.
- Debounce live DB snapshot refreshes and bound message render windows for chats with hundreds of loaded messages.

The TUI stability and modal polish milestone is implemented:

- Message scrolling now keeps the cursor moving inside the viewport until it reaches a viewport edge, matching chat-list navigation instead of pinning the selected message to the bottom.
- Emoji rendering is configurable through `emoji_mode`; `auto` preserves full emoji on capable UTF-8 terminals and falls back to compatibility rendering for terminals such as `st`.
- The status bar has a single authoritative mode indicator, keeps pywal colors by default, and supports per-mode hex overrides.
- `/` search shows match counts in the status bar and `Esc` clears active search state without requiring a blank search.
- Tests cover large-chat/message viewport behavior, emoji compatibility, indicator config parsing, status color resolution, search counts, and search clearing.

The next protocol milestone is live validation/polish of the completed media-send path, especially audio uploads, followed by resend/draft UX around failed outgoing attachments.

### Data model and lazy loading

- SQLite is the source of truth for local app state.
- Separate tables/indexes for:
  chats,
  chat metadata,
  contacts,
  messages,
  message bodies,
  message search terms,
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
  documents show an in-chat attachment row with icon, name, mime, size, and open/save actions.
- Optional right info/details pane behavior:
  shows verbose metadata, debug/status information, selected message details, and alternate actions only when toggled on;
  it is not the primary media preview surface.
- Backend detection occurs at startup and can be re-run with `:doctor` or `:preview-backend auto`.
- Preview backend order in v1:
  terminal-native `sixel`,
  `ueberzug++`,
  `chafa`,
  external opener.
- The app must remain fully usable without graphical preview support.

### Suggested UX features to include in v1

- Unread-only filter and pinned-first sorting toggle.
- Jump to last unread in current chat.
- Chat and message search history.
- Quote-jump: from a reply, jump to the referenced message if present locally, else fetch around it.
- Per-chat draft indicator in the chat list.
- Notification hook command for desktop notifications.
- Download/open attachment commands with sane default save paths.
- `:help` with discoverable keymaps and mode-specific bindings.
- Optional keymap override file, but ship a strong default instead of making the user design their own from scratch.

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
  draft persistence,
  message selection/yank behavior.
- Integration tests:
  protocol adapter against a mocked whatsmeow-facing layer,
  SQLite migrations,
  event ingestion into DB,
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
  receive new messages while browsing old history,
  send image/document/audio file,
  reply to a message and jump back to source.
- Performance targets:
  cold start should render cached chat list before network sync,
  opening a cached chat should feel immediate,
  scrolling should not block on remote fetch,
  memory use should be bounded by viewport windows and cache policy, not total history size.

## Assumptions and defaults

- Primary target is Linux desktop use, especially `st`; portability to other Linux terminals matters, but `st` behavior is the non-negotiable path.
- The app is single-user and local-first; no multi-account support in v1.
- Plaintext SQLite is acceptable because host-level security is assumed.
- Default UX favors a complete Vim model over beginner discoverability.
- Default compose mode is inline; external `nvim` compose is optional per message and never mandatory.
- Default preview mode is auto-detect with graceful fallback.
- Default emoji mode is `auto`; terminals known to misreport complex emoji widths should use the compatibility renderer unless explicitly forced to `full`.
- Default mode indicator colors come from pywal; users can override each mode with a hex color in config.
- v1 supports DMs and groups only.
- v1 includes replies, reactions, media send/receive, lazy history, search, visual selection, registers, drafts, and notifications.
- v1 excludes calls, channels/status, and broad WhatsApp surface-area parity.
- If upstream WhatsApp protocol changes break behavior, the app should fail visibly and recover safely, but protocol resilience beyond ordinary reconnect logic is not a v1 feature.
