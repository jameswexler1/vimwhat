# Tomorrow: Validate Message Interactions, Then Build Media Send

## Current State

- Pairing, live inbound ingestion, focused-chat remote history fetch, protocol-backed remote media download, and real plain text outbound send are implemented.
- Protocol-backed message interactions are now implemented:
  - mark-read for loaded inbound messages,
  - reaction send/clear and local reaction rendering,
  - text replies with quoted WhatsApp metadata,
  - `:quote-jump` into loaded/local older history,
  - direct-chat typing presence subscription/display plus best-effort composing/paused sends.
- Attachment send remains blocked until media upload/send exists.

## First Task: Live Validation Pass

1. Run `make test`, `make lint`, and `make build`.
2. Start a paired app with `./vimwhat` or `make run`.
3. Validate plain text send again in one direct chat and one group chat.
4. Validate replies:
   - focus a message,
   - press `r`,
   - send a reply,
   - confirm the quote metadata appears locally and on the phone.
5. Validate reactions:
   - run `:react 🔥` on an incoming message,
   - run `:react clear`,
   - confirm both local rendering and phone-side state.
6. Validate read receipts:
   - open a chat with unread inbound messages,
   - confirm unread count clears only after the protocol-backed mark-read path succeeds,
   - retry with `:mark-read` if needed.
7. Validate quote-jump:
   - focus a reply,
   - run `:quote-jump`,
   - confirm it jumps locally or requests older history if the target is not loaded.
8. Validate typing presence in a direct chat:
   - type for a few seconds,
   - confirm the other device sees composing,
   - confirm the local status bar shows remote typing when the peer types.

## If Validation Fails

- Fix protocol payload shape first for replies/reactions/read receipts.
- Fix local store/update wiring only if the phone state is correct but the TUI does not reflect it.
- Fix presence last if it is noisy or inconsistent; it is best-effort and lower priority than message correctness.

## Next Implementation Slice: Media Send

1. Upload local attachments through `whatsmeow`.
2. Reuse the existing staged attachment/composer flow for live sends.
3. Persist outgoing media messages locally with `sending`/`sent`/`failed`, matching text send behavior.
4. Support one attachment per message first, with optional caption for image/video/document where the protocol shape is straightforward.
5. Keep `media open <message-id>` and `export chat <jid>` for the slice after media send is stable.
