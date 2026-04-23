# Tomorrow: Validate Media and Text Send, Then Next Protocol Features

## Current State

- Pairing, live inbound ingestion, focused-chat remote history fetch, and protocol-backed remote media download are implemented.
- Received image, video, audio, and document messages persist WhatsApp download descriptors.
- The TUI can request a download through the app layer, cache the file locally, update SQLite, and continue into preview/open/save/audio playback.
- Recent TUI hardening is implemented: big-chat message scrolling behaves like chat-list scrolling, emoji rendering has `auto`/`full`/`compat` modes, mode indicators default to pywal but support per-mode hex overrides, and `/` search shows match counts and clears with `Esc`.
- Real plain text outbound send is implemented in live mode; attachment send remains blocked until media upload exists.

## First Task: Quick TUI Regression

1. Run `make test`, `make lint`, and `make build`.
2. Run `./vimwhat doctor` with the real config to catch strict config parser regressions.
3. Start a paired app with `./vimwhat` or `make run`.
4. Open the large group chat that previously broke the TUI and scroll up/down through emoji-heavy messages.
5. Search with `/`, confirm the status bar shows `current/total`, move with `n`/`N`, then press `Esc` from normal mode and confirm the search count/highlights disappear.
6. Test one custom mode indicator color, then restore `"pywal"` if desired.

## Second Task: Manual Media Validation

1. From another account, send:
   - one image,
   - one document/file,
   - one audio/voice note if convenient,
   - one short video if convenient.
2. Focus each media message and trigger the natural action:
   - image/video: preview/open,
   - document: open/save,
   - audio: play.
3. Confirm:
   - status moves through downloading and then preview/open/save/play,
   - files appear under `~/.cache/vimwhat/media`,
   - restarting the app reuses the local path without another download,
   - duplicate Enter presses do not start duplicate downloads.

## If Media Validation Fails

- Fix descriptor extraction first if the error says download details are unavailable.
- Fix protocol download/writing if the descriptor exists but download fails.
- Fix TUI state only if the file downloads correctly but preview/open/save/play does not continue.

## Implemented Slice: Real Text Send

Plain text send now follows this shape:

1. The WhatsApp live session has a `SendText` path that sends a plain text message to the current chat JID.
2. Live-mode text-only composer submissions use protocol-backed send instead of global send blocking.
3. Outgoing text messages are persisted locally with precomputed WhatsApp remote IDs and `sending`, then updated to `sent` or `failed`.
4. Attachments remain blocked until media upload/send is implemented.
5. Tests cover successful send, protocol failure, invalid chat JID, composer/draft preservation, and local status update.

## Next Implementation Slice

After live validation of media download and plain text send:

1. Add protocol-backed read receipts.
2. Add reactions.
3. Add typing presence if exposed cleanly.
4. Add reply metadata and quote-jump behavior.
5. Add attachment upload/send after text send remains stable.
