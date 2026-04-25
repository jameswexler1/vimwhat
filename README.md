# vimwhat

`vimwhat` is a Linux-first, vim-centric WhatsApp TUI written in Go. The app is now a DB-first live client: the Bubble Tea interface renders from local SQLite state while a paired `whatsmeow` session connects in the background for live WhatsApp traffic.

The project is still pre-release, but it is no longer just a local shell. Current builds include QR login, live ingestion, lazy history fetch, remote media download, outbound text and single-attachment media send, message interactions, desktop notifications, avatar/sticker rendering, and data export helpers.

## Current status

Implemented:

- Modal Bubble Tea TUI with normal, insert, visual, command, search, and confirm flows.
- Chat list, message viewport, optional info pane, inline composer, help overlay, compact layout, pane switching, visual selection, search, filters, pinned/unread sorting, and local draft persistence.
- SQLite state under XDG data paths with migrations, chats, contacts, messages, FTS search, drafts, media metadata, media download descriptors, reactions, avatars, sync cursors, and UI snapshots.
- First-run config generation plus flat configurable keybindings, emoji mode, pywal/hex mode indicators, preview settings, opener/player commands, and notification settings.
- WhatsApp QR login/logout with a real session store, rejected-session cleanup, paired-state reporting in `doctor`, and first-use local reset on logout.
- Live WhatsApp bootstrap from a paired session, connection status in the TUI, protocol event ingestion, receipt/status updates, metadata refresh, and DB-first UI refreshes.
- On-demand remote history fetch for the focused chat, triggered by `:history fetch` or by scrolling above the loaded message window.
- Remote media download for received images, videos, audio, documents, and stickers, using persisted WhatsApp download descriptors and temp-backed managed caches.
- Outbound plain text plus one local attachment per message, including quote context, image/video/document captions, generic audio send, audio-caption rejection, `sending`/`sent`/`failed` status updates, and failed-media retry via `R` or `:retry-message`.
- Message interactions: auto/manual mark-read, reactions send/clear and rendering, own-message delete-for-everyone, inbound revoke ingestion, replies, quote-jump, right-edge reply gesture, and typing presence.
- Media UI with backend detection for Sixel, `ueberzug++`, `chafa`, external openers, in-chat image/video/sticker previews, chat avatars, stable overlay pause/resume while scrolling, and focused audio playback through `mpv`.
- Desktop notifications for new incoming messages with native Linux/macOS/Windows backends or an argv-safe command override, with suppression for muted chats, duplicates, outgoing messages, historical imports, reaction-only updates, and the active chat while the app is known to be focused.
- Demo data commands for local TUI development without a live WhatsApp session.
- CLI helpers for `media open` and `export chat`.

Known gaps:

- Live validation and polish are still ongoing for notification delivery, media send/download, stickers, avatar refresh, audio fallback behavior, and failed-media retry against real daily WhatsApp usage.
- Attachment draft persistence across restart or failed async send is not implemented yet.
- Retry/resend UX for failed text-only messages is not implemented yet.
- Voice-note/PTT-specific audio send semantics are not implemented beyond the current generic audio/document attachment flow.
- Calls, channels/newsletters, statuses, community management, and business-only features are outside the current v1 surface.

For more detailed stage notes and upcoming validation work, see `PLAN.md`.

## Requirements

- Go 1.26, matching `go.mod`.
- A WhatsApp account for live mode, paired with `vimwhat login`.
- Optional external tools improve the experience:
  - `yazi` for the default attachment picker.
  - `chafa`, `img2sixel`, or `ueberzug++` for inline media and avatar previews.
  - `ffmpeg` for generated video thumbnails.
  - `mpv`, `nsxiv`, and `xdg-open` for audio/video/image/open fallback commands.
  - `wl-copy`/`wl-paste`, `xclip`, or configured equivalents for clipboard image copy/paste.
  - `notify-send`, `gdbus`, `dbus-send`, `osascript`, or `powershell.exe` for native notifications, depending on OS.

## Quick start

Build and check the local environment:

```sh
make build
./vimwhat doctor
```

Pair WhatsApp, then start the TUI:

```sh
./vimwhat login
./vimwhat
```

Run with demo data instead of a live WhatsApp session:

```sh
go run ./cmd/vimwhat demo seed
go run ./cmd/vimwhat
go run ./cmd/vimwhat demo clear
```

## Commands

```sh
go run ./cmd/vimwhat
go run ./cmd/vimwhat demo seed
go run ./cmd/vimwhat demo clear
go run ./cmd/vimwhat doctor
go run ./cmd/vimwhat login
go run ./cmd/vimwhat logout
go run ./cmd/vimwhat media open <message-id>
go run ./cmd/vimwhat export chat <jid>
```

`vimwhat logout` logs out remotely when possible, clears local app/session state, and returns the client to first-use state while leaving config and explicitly saved Downloads files in place.

`vimwhat media open <message-id>` opens a stored attachment and can auto-download remote media first if the paired WhatsApp session has the needed descriptor. `vimwhat export chat <jid>` writes a Markdown transcript of locally persisted history to the configured downloads directory.

## Development

```sh
make run
make build
make test
make lint
```

Equivalent direct Go commands:

```sh
go run ./cmd/vimwhat
go build ./cmd/vimwhat
go test ./...
go vet ./...
go fmt ./...
```

The `Makefile` defaults `GOCACHE` to `/tmp/vimwhat-go-build`, which keeps builds working in constrained environments where the normal Go cache path is read-only.

## Runtime state

Runtime state is kept out of the repository:

- Config: `$XDG_CONFIG_HOME/vimwhat/config.toml`
- App database: `$XDG_DATA_HOME/vimwhat/state.sqlite3`
- WhatsApp session database: `$XDG_DATA_HOME/vimwhat/whatsapp-session.sqlite3`
- Logs/cache root: `$XDG_CACHE_HOME/vimwhat/`
- Non-exported chat media cache: `$TMPDIR/vimwhat-*/media`
- Generated preview cache: `$TMPDIR/vimwhat-*/preview`

Do not commit session files, SQLite databases, logs, media caches, or generated preview assets. Files saved explicitly with the configured save-media key go to the configured downloads directory and are not part of the transient cache.

On first run, `vimwhat` creates `$XDG_CONFIG_HOME/vimwhat/config.toml` with the full default configuration so it can be edited in place. The same baseline is checked in as `config.example.toml`.

## TUI workflow

Important default keys:

```toml
leader_key = "space"

key_normal_insert = "i"
key_normal_reply = "r"
key_normal_retry_failed_media = "R"
key_normal_command = ":"
key_normal_search = "/"
key_normal_focus_next = "tab"
key_normal_focus_previous = "shift+tab"
key_normal_focus_left = "h"
key_normal_focus_right_or_reply = "l"
key_normal_move_down = "j"
key_normal_move_up = "k"
key_normal_open = "enter"
key_normal_open_media = "o"
key_normal_yank_message = "y"
key_normal_copy_image = "leader y"
key_normal_save_media = "leader s"
key_normal_unload_previews = "leader h f"
key_normal_delete_for_everybody = "leader d e"

key_insert_send = "enter"
key_insert_newline = "ctrl+j"
key_insert_newline_alt = "alt+enter"
key_insert_attach = "ctrl+f"
key_insert_paste_image = "ctrl+v"
key_insert_remove_attachment = "ctrl+x"

key_visual_yank = "y"
key_confirm_run = "enter"
key_confirm_cancel = "esc"
```

Useful command-mode actions:

```text
:history fetch
:mark-read
:quote-jump
:react <emoji>
:react clear
:retry-message
:media preview
:media open
:media save
:copy-image
:paste-image
:attach
:attach <path>
:delete-message
:delete-message-everybody
:filter unread
:filter all
:filter messages <query>
:filter messages clear
:sort pinned
:sort recent
:preview-backend <auto|sixel|ueberzug++|chafa|external|none>
:clear-preview-cache
:quit
```

Image clipboard paste/copy is image-only. `key_insert_paste_image` stages the current clipboard image as the composer attachment, preserving composer text as the caption. `key_normal_copy_image` copies the focused image message to the clipboard and auto-downloads remote image media first when possible. The default image clipboard commands auto-detect Wayland/X11 tools; set `clipboard_image_paste_command` or `clipboard_image_copy_command` to override them. Paste commands may write to `{path}` or stdout, and copy commands may use `{path}` and `{mime}` or receive image bytes on stdin.

All TUI action keys are configurable in `config.toml` using flat `key_<mode>_<action>` variables. Bindings accept printable single keys plus named tokens such as `space`, `enter`, `esc`, `tab`, `shift+tab`, `backspace`, `ctrl+x`, `alt+x`, `alt+enter`, and leader sequences. Duplicate bindings in the same mode and prefix conflicts such as binding both `space` and `leader s` are rejected at startup with a config error.

## Media and previews

`preview_backend = "auto"` chooses the best available inline renderer in this order: Sixel, `ueberzug++`, `chafa`, external opener, none. Images, videos, stickers, and avatars can render inline when a capable backend is available. Videos use generated thumbnails when `ffmpeg` is available. Audio messages render as compact playback rows and use `audio_player_command`, which defaults to `mpv --no-video --no-terminal --really-quiet {path}`.

Remote media starts as metadata in SQLite. Opening, previewing, saving, or running `vimwhat media open <message-id>` can download it through the paired WhatsApp session when a download descriptor exists.

## Emoji and theming

Emoji rendering defaults to `emoji_mode = "auto"` in `config.toml`. Auto mode preserves full emoji sequences on most UTF-8 terminals, but uses the stable compatibility path for terminals such as `st` that can display emoji glyphs while still misreporting complex emoji cell widths. Set `emoji_mode = "compat"` to force stable degraded rendering, or `emoji_mode = "full"` to force skin tones, ZWJ professions/families, and flags.

Mode indicator colors default to pywal-derived theme colors:

```toml
indicator_normal = "pywal"
indicator_insert = "pywal"
indicator_visual = "pywal"
indicator_command = "pywal"
indicator_search = "pywal"
```

Replace any value with a hex color such as `"#7ED7C1"` or `"#f0a"` to override only that mode.

## Notifications

Desktop notifications default to `notification_backend = "auto"` and notify only for new incoming messages from unmuted chats. The selected chat is suppressed only while the app window is known to be focused. If the app window is blurred, or the terminal never reports focus, the selected chat still notifies rather than risking missed messages. When a cached chat avatar or group picture is available, Linux desktop notifications include it as the notification icon.

```toml
notification_backend = "auto"
notification_command = ""
```

Supported built-in backend values are `auto`, `none`, `command`, `linux-dbus`, `macos-osascript`, and `windows-powershell`. If `notification_command` is set while `notification_backend = "auto"`, the configured command wins. `notification_command` is treated as an argv template, not as a shell snippet, and supports `{title}`, `{body}`, `{chat}`, `{sender}`, and `{icon}` placeholders. `vimwhat doctor` reports the selected notification path and backend availability.
