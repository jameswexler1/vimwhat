# Vimwhat TUI Reliability And Security Audit

Date: 2026-05-05

## Executive Summary

The codebase has meaningful test coverage and the normal verification baseline is healthy:

- `make test` passed.
- `make lint` passed.
- `make test-windows` passed.

No critical memory-corruption issue or obvious default shell-injection flaw was found in this pass. The highest risk is runtime responsiveness under real-world load: long chats, large sync bursts, media preview activity, and live WhatsApp operations can still push work onto the Bubble Tea update/render path or block protocol workers longer than the UI expects.

The most important fix themes are:

- Keep Bubble Tea `Update` and `View` bounded and non-blocking.
- Move network, store, and external process work behind `tea.Cmd` result messages or bounded workers.
- Make cancellation and timeout policy explicit for media and overlay work.
- Use the existing SQLite FTS table for message search instead of scanning full chats in Go.

## Verification Performed

Commands run:

- `make test`: passed.
- `make lint`: passed.
- `make test-windows`: passed.
- `GOCACHE=/tmp/vimwhat-go-build go test -race ./internal/ui`: timed out after 10 minutes.

The race-enabled UI run timed out while running `TestLargeGroupChatScrollUpKeepsViewBounded`. The stack was inside message block rendering:

- `internal/ui/model.go:3406`
- `internal/ui/view.go:1444`
- `internal/ui/view.go:1652`

This supports the unbounded message rendering finding below. The timeout should not be treated as a confirmed data race by itself; it is evidence that the large-chat path is too expensive under race instrumentation and deserves a bounded-render fix.

Live WhatsApp validation was not performed in this audit. Findings that depend on real protocol behavior should be validated against a paired account.

## Findings

### High: Message rendering is not bounded by the viewport

Evidence:

- `internal/ui/view.go:1383` calls `m.messageBlocks(messages, width, ...)` for the entire loaded message slice.
- `internal/ui/view.go:1421` to `internal/ui/view.go:1423` passes `0, len(messages)` into `messageBlocksForRange`.
- `internal/ui/model.go:3406` does the same in `messageScrollTopWithCursorVisible`.
- `internal/ui/model.go:2991` to `internal/ui/model.go:2998` grows per-chat message limits as older history is loaded.
- `internal/ui/view.go:705` iterates candidate scroll positions across all blocks when placing the new-message divider.

Failure mode:

The application has a `visibleMessageRange` helper and a `maxMessageRenderWindow` constant, but the main render path still builds Lip Gloss bubbles for every loaded message before clipping the viewport. As a chat accumulates more loaded history, frame rendering and cursor movement become proportional to loaded history, not terminal height. Large messages make this worse because wrapping and Lip Gloss border rendering are repeated for offscreen rows.

Impact:

- Scrolling can become sluggish or appear frozen in large chats.
- Incoming refreshes can stutter the UI after history fetches increase the loaded window.
- Race-enabled test execution timed out in this area.
- Terminal overlays and media previews can amplify the cost because visible overlay IDs and bubble geometry are recomputed around the same render path.

Fix direction:

- Make the normal message render path use `visibleMessageRange` before rendering bubbles.
- Preserve original message indexes in `messageBlock.messageIndex` so visual mode, cursor movement, quote jump, new-message divider, and footer notices still refer to absolute positions.
- Replace the all-candidate divider placement search with a bounded calculation over the visible range plus overscan.
- Add regression tests that render and scroll a chat with thousands of loaded messages and assert bounded render work or a strict runtime threshold.

Suggested tests:

- A test that loads 5,000 messages, scrolls up repeatedly, and asserts the render path never calls bubble rendering for more than `maxMessageRenderWindow + overscan` messages.
- A test with the new-message divider outside the visible range.
- A test with the divider inside the visible range and the cursor near it.

### High: Bubble Tea `Update` still performs blocking send, reaction, mark-read, and store work

Evidence:

- `internal/ui/model.go:1855` calls `m.persistMessage(request)` directly from insert-mode send handling.
- The live implementation waits for queue results in `internal/app/app.go:198` to `internal/app/app.go:222`.
- Attachment/sticker retry paths similarly wait through `queueMediaSendRequest` at `internal/app/app.go:236` to `internal/app/app.go:258`.
- `internal/ui/model.go:1671` calls `m.markRead(chat, messages)` directly.
- `internal/app/app.go:271` to `internal/app/app.go:277` waits up to `readReceiptQueueTimeout`.
- `internal/ui/model.go:2928` calls `m.sendReaction(message, emoji)` directly.
- `internal/app/app.go:291` to `internal/app/app.go:297` waits up to `reactionQueueTimeout`.
- Synchronous store calls are also reachable in focus/search/filter/history flows, for example `internal/ui/model.go:2746`, `internal/ui/model.go:2789`, and `internal/ui/model.go:2678`.

Failure mode:

Bubble Tea expects `Update` to return quickly. Several user actions currently call functions that can wait on channels, SQLite, or live protocol coordination before returning. Even with timeouts, this can freeze input handling and screen updates for seconds. On a slow disk, during sync, or while the single SQLite connection is busy, these calls become visible UI stalls.

Impact:

- Sending a message can freeze the TUI if the live worker is busy or the queue is slow.
- Mark-read and reaction commands can block normal navigation.
- Search, quote jump, and history-loading commands can block while SQLite scans or attaches message details.
- A user may press keys during the stall and see delayed or surprising state changes when the update returns.

Fix direction:

- Convert send, mark-read, reaction, search, history load, and other store-backed actions into `tea.Cmd` operations that return typed result messages.
- Apply optimistic UI state where appropriate, then reconcile on result.
- Keep synchronous validation in `Update` only for cheap local checks such as empty body, selected chat, and current connection state.
- Add tests that simulate slow callbacks and assert `Update` returns without waiting.

Suggested tests:

- A fake `PersistMessage` that blocks; verify insert send returns a command immediately.
- A fake `MarkRead` that blocks; verify navigation remains possible before the result message arrives.
- A fake `SearchMessages` that blocks; verify the search mode can still cancel or show pending state.

### High: Message search scans full chats in Go instead of using FTS

Evidence:

- `internal/ui/model.go:2678` calls `m.searchMessages(chatID, query, 200)` from `runStoreSearch`.
- `internal/store/repository.go:444` to `internal/store/repository.go:453` selects all renderable messages for the chat in timestamp order.
- `internal/store/repository.go:459` to `internal/store/repository.go:470` filters with `textmatch.Contains` in Go and stops only after enough matches are found.
- `message_fts` exists in migrations and is maintained on message insert/edit/delete, but it is not used by `SearchMessages`.

Failure mode:

Searching a large chat requires scanning every renderable row until enough matches are found. If the query is rare or absent, this is effectively a full chat scan plus message detail attachment. Because the UI calls it synchronously, search can freeze the TUI.

Impact:

- `/` search becomes progressively slower as chat history grows.
- Accent-aware matching is currently implemented in Go, but that should not require reading every message into the application for common cases.
- SQLite's single connection means a long search also delays live writes and snapshot reloads.

Fix direction:

- Use `message_fts` for candidate selection.
- Preserve the current accent-folding behavior by either adding a normalized FTS column or using FTS for candidate narrowing followed by `textmatch.Contains` on a bounded result set.
- Execute search asynchronously through a `tea.Cmd`.
- Consider paging search results instead of replacing the current chat message slice with only the first 200 matches.

Suggested tests:

- Verify `SearchMessages` uses FTS results and respects limit without scanning nonmatching rows.
- Include accent-sensitive and accent-insensitive query cases.
- Include a rare-query case over a large fixture.

### High: Recent sticker caching can block the live WhatsApp event loop

Evidence:

- `internal/app/app.go:1129` to `internal/app/app.go:1136` handles `EventRecentSticker` inline in the main live select loop.
- `internal/app/app.go:3450` to `internal/app/app.go:3504` prepares the sticker and may call `live.DownloadMedia`.
- The result is applied only after preparation completes at `internal/app/app.go:1139`.

Failure mode:

Recent sticker events can trigger media download before ingestion continues. If WhatsApp media download stalls, the main live loop stops processing other events: messages, receipts, connection updates, app-state changes, and UI refresh signals.

Impact:

- Incoming messages can appear late while sticker caching is slow.
- Offline sync progress can appear stuck.
- Connection state and notifications can lag behind real protocol state.
- A run of multiple sticker events can cause repeated stalls during startup app-state sync.

Fix direction:

- Ingest recent sticker metadata immediately.
- Queue renderable sticker file caching to a bounded worker pool.
- Apply per-sticker timeout shorter than general media send/download timeouts.
- Send a refresh only when a worker successfully adds `LocalPath`, and suppress repeated failure statuses for the same sticker ID.

Suggested tests:

- A fake live session whose sticker download blocks; assert the live event loop still processes a following message event.
- A worker queue saturation test.
- A retry/cached-path test where metadata exists first and `LocalPath` arrives later.

### High: Media download timeout is not propagated to worker execution

Evidence:

- UI-facing download waits with `mediaDownloadTimeout` at `internal/app/app.go:416` to `internal/app/app.go:422`.
- `mediaDownloadRequest` has no context or deadline.
- The worker calls `downloadRemoteMedia(ctx, ...)` with the app-level context at `internal/app/app.go:2851`.
- The actual protocol call uses that same context at `internal/app/app.go:3629`.

Failure mode:

The UI can time out after five minutes, but the worker can continue the download indefinitely until the whole app context is canceled. Because there are only two media download workers, two stuck downloads can block all future media downloads even though the UI reports timeout.

Impact:

- Media retry/open/save commands can stop working after a few stuck downloads.
- Rows may stay in `downloading` or flip state late after the UI has moved on.
- Shutdown may wait on protocol work longer than expected if the live call ignores cancellation promptly.

Fix direction:

- Add `Context context.Context` or `Deadline time.Time` to `mediaDownloadRequest`.
- Create the timeout context before enqueueing and pass it into the worker.
- Mark the media row failed on timeout.
- Ensure timed-out worker results cannot overwrite a newer successful download state.

Suggested tests:

- A fake live downloader that waits on context; assert the request context is canceled at timeout.
- A test with both workers occupied by blocked downloads; assert a later request receives queue/full/timeout deterministically.
- A stale-result test where a timed-out first request returns after a second request succeeds.

### Medium: Terminal overlay and Sixel sync ignore cancellation and can leave pending state stuck

Evidence:

- `internal/ui/model.go:3736`, `internal/ui/model.go:3759`, `internal/ui/model.go:3793`, `internal/ui/model.go:3828`, and `internal/ui/model.go:3862` call overlay/Sixel sync with `context.Background()`.
- `internal/media/overlay.go:95` to `internal/media/overlay.go:145` holds the overlay mutex while starting the process and writing commands.
- `internal/media/sixel.go:49` explicitly ignores the context.
- UI pending flags are cleared only when a result message returns, for example `overlaySyncPending` and `sixelSyncPending` in `internal/ui/model.go`.

Failure mode:

If `ueberzug++` starts slowly, its stdin blocks, or Sixel output blocks, the corresponding command can hang. The UI command itself runs outside `Update`, but the model may keep thinking an overlay sync is pending and skip future sync attempts. On quit, cleanup also depends on manager close behavior.

Impact:

- Pixel overlays can disappear and never resume.
- Media/avatar previews can become visually stale after resize or scroll.
- Quit can feel delayed if renderer cleanup stalls.

Fix direction:

- Use short timeout contexts for every overlay/Sixel sync command.
- Clear pending flags on timeout and invalidate the manager.
- For `OverlayManager`, avoid holding the mutex across subprocess start/write operations where possible.
- For `SixelManager`, honor context before large writes and after generating clear/payload buffers.

Suggested tests:

- A blocking writer test for Sixel sync.
- A fake overlay stdin that blocks on write.
- A model-level test that a failed sync clears pending state and allows a later sync attempt.

### Medium: Clipboard image paste/copy can allocate unbounded memory

Evidence:

- `internal/app/integrations.go:173` to `internal/app/integrations.go:180` captures clipboard command stdout into an unbounded `bytes.Buffer`.
- `internal/app/integrations.go:261` to `internal/app/integrations.go:267` reads the entire image file into memory before copying to clipboard.
- `internal/app/integrations.go:129` and `internal/app/integrations.go:244` add timeouts, but no size limits.

Failure mode:

A clipboard command or configured helper can return a huge payload. The app buffers it entirely in memory before validating MIME type or writing to a temp file. Copying a very large image similarly reads the whole file into memory.

Impact:

- Memory spike or process termination on large clipboard data.
- TUI unresponsiveness while buffering/copying large payloads.
- A malicious or misconfigured clipboard helper can cause resource exhaustion.

Fix direction:

- Add a maximum clipboard payload size.
- Use `io.LimitedReader` when reading stdout.
- Stream image copy where possible instead of reading the whole file.
- Reject files above a configurable or conservative maximum for clipboard copy.

Suggested tests:

- Clipboard paste command returning more than the allowed bytes.
- Clipboard paste command returning non-image data just below the cap.
- Copy-image path with a file above the cap.

### Medium: Single SQLite connection increases UI contention risk

Evidence:

- `internal/store/store.go:265` to `internal/store/store.go:267` sets `MaxOpenConns(1)`.
- Background send completion writes use `context.Background()` at `internal/app/app.go:1652`, `internal/app/app.go:1665`, `internal/app/app.go:1866`, `internal/app/app.go:1878`, `internal/app/app.go:1913`, and `internal/app/app.go:1924`.
- Snapshot reloads use the same store connection at `internal/app/app.go:4126` to `internal/app/app.go:4158`.

Failure mode:

One long query, transaction, or blocked write serializes all other store operations. Because some UI actions synchronously wait on store operations and several background writes ignore cancellation, contention can surface as UI stalls rather than background delay.

Impact:

- Refreshes can lag during sync.
- Search or message loading can delay send status updates.
- Shutdown or cancel can leave background writes running until SQLite returns.

Fix direction:

- Keep single-connection mode if required by `modernc.org/sqlite` behavior, but enforce short contexts on UI-facing reads and background status writes.
- Move all UI store calls into async commands.
- Consider separate read/write handles only if tests prove it is safe with WAL and the chosen driver.

Suggested tests:

- Inject a store callback that blocks and verify UI actions remain responsive.
- Add timeout tests around status updates after canceled live context.

### Low: Notification queue silently drops jobs under burst load

Evidence:

- `internal/app/notifications.go:17` sets queue size to 64.
- `internal/app/notifications.go:79` to `internal/app/notifications.go:87` silently drops when full.
- The startup notification gate also drops candidates over the same size at `internal/app/notifications.go:102`.

Failure mode:

During startup or a large inactive-chat burst, notification jobs can be dropped with no diagnostic. This may be intentional to protect the UI, but it makes delivery behavior hard to reason about during live validation.

Impact:

- Users may miss notifications during catch-up bursts.
- Developers may misdiagnose missing notifications as backend failure.

Fix direction:

- Count dropped notification jobs.
- Emit at most one coalesced status such as `notifications dropped: N`.
- Prefer grouping burst notifications per chat rather than sending one per message.

Suggested tests:

- Queue more than 64 notifications and assert a drop counter/status is emitted.
- Verify muted and active-chat suppression still happen before counting drops.

## Security Notes

The command-template paths generally avoid shells by splitting into argv and using `exec.Command` or `exec.CommandContext`. That is good. The remaining security concern is resource exhaustion rather than shell injection:

- Clipboard stdout and image copy can consume unbounded memory.
- External preview helpers process user-controlled media files.
- Notification, opener, preview, and clipboard commands are user-configured execution surfaces.

The developer should preserve the argv-based, shell-free model. Do not replace it with `sh -c` convenience wrappers in shared code. If shell behavior is needed, it should remain an explicit user configuration choice.

## Recommended Fix Order

1. Bound message rendering and cursor visibility calculations. This is the clearest TUI breakage risk and is supported by the race-enabled timeout.
2. Move synchronous send, mark-read, reaction, search, and history load paths out of `Update`.
3. Replace message search full scans with FTS-backed candidate selection.
4. Move recent sticker file downloads out of the live event loop.
5. Propagate media download request timeouts into workers and protocol calls.
6. Add timeout/error recovery around overlay and Sixel sync.
7. Add clipboard size caps and streaming.
8. Add notification drop accounting.

## Fix Pass Status

Implemented in the follow-up fix pass:

- Message rendering and cursor visibility now build only the visible message window before rendering bubbles.
- `SearchMessages` now uses the maintained `message_fts` table for candidate selection while preserving the existing `textmatch.Contains` accent behavior as a final filter.
- Media download requests now carry the caller timeout into the worker/protocol download path.
- Recent sticker file download now has a per-sticker timeout.
- Overlay and Sixel sync commands now use bounded contexts, and the media writers honor canceled contexts before terminal writes.
- Clipboard image paste/copy now rejects payloads/files above a 64 MiB ceiling instead of buffering unbounded data.
- Notification queuing now applies short backpressure and emits a status when a burst overflows the pending or delivery queues.

Still needing a larger refactor:

- Send, mark-read, reaction, filter/search, and several store-backed UI actions still need a full conversion to asynchronous `tea.Cmd` result messages to make Bubble Tea `Update` consistently non-blocking.
- Recent sticker metadata is still prepared inline when it already has enough information for immediate cache lookup; the longer-term design should ingest metadata first and perform file downloads in a bounded worker.
- SQLite is still intentionally single-connection; more UI calls need bounded async command wrappers before changing the store connection model.

## Manual Validation Checklist

Run these after the fixes above:

- Open a chat with thousands of loaded messages; hold scroll-up/down and verify frame stability.
- Run `/` search on a large chat with common, rare, absent, accent-free, and accent-sensitive queries.
- Trigger offline sync while navigating between chats.
- Receive many recent-sticker events during startup and confirm normal message ingestion continues.
- Start two media downloads with a fake or unreliable network, then request a third download.
- Resize the terminal rapidly while Sixel or `ueberzug++` previews are visible.
- Test notification delivery in inactive chat, active focused chat suppression, blurred active-chat delivery, and muted-chat suppression.
- Paste an oversized clipboard payload and verify the app rejects it without memory growth.

## Residual Risks

- Live WhatsApp behavior was not exercised in this audit, so protocol-specific timing issues may still exist.
- Windows terminal visual behavior was compile-checked but not manually rendered.
- The race-enabled UI run passed after bounding message rendering: `GOCACHE=/tmp/vimwhat-go-build go test -race -count=1 ./internal/ui`.
