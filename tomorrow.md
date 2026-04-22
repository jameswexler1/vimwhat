# Tomorrow: Remote Media Download

## Current State

- Pairing, live inbound ingestion, and focused-chat remote history fetch are working.
- The TUI already has download hooks: open/preview/save call `Options.DownloadMedia` when media is remote.
- The store currently persists display metadata only: MIME type, file name, size, local path, thumbnail path, and download state.
- The missing piece is the protocol-backed download descriptor and a downloader that writes media bytes to disk, updates SQLite, and lets the existing UI continue into preview/open/save.

## Goal

Make received remote media openable from the TUI without implementing sending yet.

The first successful slice should let a received image or document go from `DownloadState=remote` to `DownloadState=downloaded`, with `LocalPath` set under the app media cache/data directory, and then reuse the existing preview/open/save behavior.

## Implementation Plan

1. Persist WhatsApp download descriptors.
   - Add store fields or a companion table for the data needed by `whatsmeow` download APIs: media kind, direct path or URL when available, media key, file SHA256, encrypted file SHA256, and file length.
   - Keep existing `store.MediaMetadata` focused on UI-facing metadata, but make sure repository methods can load the download descriptor for a message.
   - Add a migration and SQLite tests for insert, update, and preservation of an already downloaded `LocalPath`.

2. Fill descriptors during event normalization.
   - Extend `internal/whatsapp/events.go` media normalization for image, video, audio, and document messages.
   - Populate descriptor bytes from the original protobuf fields while continuing to emit the existing media metadata event.
   - Cover this with mocked protocol event tests; do not rely only on live WhatsApp traffic.

3. Add a protocol download adapter.
   - Extend the WhatsApp adapter with a media download method that takes an internal download descriptor.
   - Use `whatsmeow.Client.DownloadMediaWithPath` or `DownloadToFile`/`Download` depending on which descriptor shape is easiest to keep typed.
   - Write to a temporary file first, then atomically rename into the media cache/data path.
   - Choose deterministic file names from message ID plus a safe extension derived from MIME type or original file name.

4. Wire app download into the existing UI option.
   - Implement `Options.DownloadMedia` in `internal/app/app.go`.
   - Load the descriptor from SQLite, call the live WhatsApp downloader, update `LocalPath`, `DownloadState`, size, and timestamp through the store.
   - Keep the TUI DB-first: the UI requests a download and receives a result, but it never talks to `whatsmeow`.

5. Tighten user-visible states.
   - Keep `remote`, `downloading`, `downloaded`, and `failed` states explicit enough for status messages.
   - Prevent duplicate download requests for the same focused message while one is already running.
   - Preserve the current behavior where image preview, external open, and save continue automatically after a successful download.

## Tests To Add

- Store migration/repository tests for download descriptor persistence.
- WhatsApp normalization tests proving descriptors are extracted for image, video, audio, and document messages.
- App-level test with a fake live session/downloader that writes bytes and updates SQLite.
- UI test that remote media invokes `DownloadMedia`, updates loaded message media, and queues preview rendering.
- Error-path tests for missing descriptor, offline/not paired, failed protocol download, and duplicate in-flight download.

## Manual Acceptance

- Start paired app with `make run`.
- Receive or use an existing remote image message.
- Focus the message and trigger preview/open.
- Confirm status moves through downloading to downloaded/preview-ready/opened.
- Confirm the media file exists under the app media directory.
- Restart the app and confirm the same media opens from local disk without downloading again.
- Repeat with a document/file attachment.

## After This

Next milestones stay in this order:

1. Remote media download.
2. Remote media thumbnail/download polish if needed.
3. Real text send.
4. Attachment send.
