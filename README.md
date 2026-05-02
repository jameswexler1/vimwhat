# vimwhat

`vimwhat` is a native Linux and Windows vim-centric WhatsApp TUI written in Go. The app is now a DB-first live client: the Bubble Tea interface renders from local SQLite state while a paired `whatsmeow` session connects in the background for live WhatsApp traffic.

The project is still pre-release, but it is no longer just a local shell. Current builds include QR login, live ingestion, lazy history fetch, remote media download, outbound text, single-attachment media send, recent-sticker send, message interactions, desktop notifications, avatar/sticker rendering, and data export helpers.

## Current status

Implemented:

- Modal Bubble Tea TUI with normal, insert, visual, command, search, and confirm flows.
- Chat list, message viewport, optional info pane, inline composer, help overlay, compact layout, pane switching, visual selection, search, filters, pinned/unread sorting, and local draft persistence.
- SQLite state under native per-user data paths with migrations, chats, contacts, messages, FTS search, drafts, media metadata, media download descriptors, reactions, avatars, sync cursors, and UI snapshots.
- First-run config generation plus flat configurable keybindings, emoji mode, pywal/hex mode indicators, preview settings, opener/player commands, and notification settings.
- WhatsApp QR login/logout with a real session store, rejected-session cleanup, paired-state reporting in `doctor`, and first-use local reset on logout.
- Live WhatsApp bootstrap from a paired session, connection status in the TUI, protocol event ingestion, receipt/status updates, metadata refresh, and DB-first UI refreshes.
- On-demand remote history fetch for the focused chat, triggered by `:history fetch` or by scrolling above the loaded message window.
- Remote media download for received images, videos, audio, documents, and stickers, using persisted WhatsApp download descriptors and temp-backed managed caches.
- Outbound plain text plus one local attachment per message, including quote context, image/video/document captions, generic audio send, audio-caption rejection, `sending`/`sent`/`failed` status updates, and failed-media retry via `R` or `:retry-message`.
- Recent WhatsApp stickers are cached in temporary storage when seen in history/app-state sync, selectable with the configured sticker picker, and sent through WhatsApp as sticker messages rather than image attachments.
- Message interactions: auto/manual mark-read, reactions send/clear and rendering, own-message edit, own-message delete-for-everyone, inbound edit/revoke ingestion, replies, quote-jump, right-edge reply gesture, and typing presence.
- Media UI with backend detection for Sixel, Unix `ueberzug++`, `chafa`, native external openers, in-chat image/video/sticker previews, chat avatars, stable overlay pause/resume while scrolling, and focused audio playback through platform defaults or configured commands.
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
  - Linux: `yazi` for the default attachment picker.
  - Linux/Windows terminals with support: `chafa` or `img2sixel` for inline media and avatar previews.
  - Linux graphical terminals: `ueberzug++` for overlay previews.
  - Linux/Windows: `ffmpeg` for generated video thumbnails.
  - Linux: `mpv`, `nsxiv`, and `xdg-open` for audio/video/image/open fallback commands and the default sticker thumbnail picker.
  - Linux: `wl-copy`/`wl-paste`, `xclip`, or configured equivalents for clipboard image copy/paste. Windows uses native PowerShell/clipboard commands by default.
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
make test-windows
make lint
```

Equivalent direct Go commands:

```sh
go run ./cmd/vimwhat
go build ./cmd/vimwhat
go test ./...
GOOS=windows GOARCH=amd64 go test ./... -run '^$' -exec=true
go vet ./...
go fmt ./...
```

The `Makefile` defaults `GOCACHE` to `/tmp/vimwhat-go-build`, which keeps builds working in constrained environments where the normal Go cache path is read-only.

GitHub Actions is configured in `.github/workflows/ci.yml` to run tests, vet, the Windows build-graph check, and to upload `vimwhat-windows-amd64.exe` and `vimwhat-windows-arm64.exe` artifacts from every run. Branch pushes and manual workflow dispatches also update the stable `windows-latest` GitHub release assets.

For a Windows tester who already uses `C:\Users\Otavio\Zap\vimwhat\vimwhat.exe`, this PowerShell snippet updates the binary in place:

```powershell
$ErrorActionPreference = "Stop"
$dir = "$env:USERPROFILE\Zap\vimwhat"
$tmp = Join-Path $env:TEMP "vimwhat.exe"
New-Item -ItemType Directory -Force $dir | Out-Null
Invoke-WebRequest "https://github.com/jameswexler1/vimwhat/releases/download/windows-latest/vimwhat-windows-amd64.exe" -OutFile $tmp
Move-Item -Force $tmp (Join-Path $dir "vimwhat.exe")
Unblock-File (Join-Path $dir "vimwhat.exe") -ErrorAction SilentlyContinue
& (Join-Path $dir "vimwhat.exe") doctor
```

## Runtime state

Runtime state is kept out of the repository:

Linux:

- Config: `$XDG_CONFIG_HOME/vimwhat/config.toml`
- App database: `$XDG_DATA_HOME/vimwhat/state.sqlite3`
- WhatsApp session database: `$XDG_DATA_HOME/vimwhat/whatsapp-session.sqlite3`
- Logs/cache root: `$XDG_CACHE_HOME/vimwhat/`
- Non-exported chat media cache: `$TMPDIR/vimwhat-*/media`
- Generated preview cache: `$TMPDIR/vimwhat-*/preview`

Windows:

- Config: `%APPDATA%\vimwhat\config.toml`
- App database: `%LOCALAPPDATA%\vimwhat\data\state.sqlite3`
- WhatsApp session database: `%LOCALAPPDATA%\vimwhat\data\whatsapp-session.sqlite3`
- Logs/cache root: `%LOCALAPPDATA%\vimwhat\cache\`
- Non-exported chat media cache: `%TEMP%\vimwhat-*\media`
- Generated preview cache: `%TEMP%\vimwhat-*\preview`

Do not commit session files, SQLite databases, logs, media caches, or generated preview assets. Files saved explicitly with the configured save-media key go to the configured downloads directory and are not part of the transient cache.

On first run, `vimwhat` creates the native config file with the full default configuration so it can be edited in place. Linux defaults keep the existing Unix tools; Windows defaults use native PowerShell, clipboard, and shell opener commands. A Linux-oriented baseline is checked in as `config.example.toml`.

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
key_normal_edit_message = "leader e"
key_normal_pick_sticker = "leader t"
key_normal_copy_image = "leader y"
key_normal_save_media = "leader s"
key_normal_unload_previews = "leader h f"
key_normal_delete_for_everybody = "leader d e"

key_insert_send = "enter"
key_insert_newline = "ctrl+j"
key_insert_newline_alt = "shift+enter"
key_insert_attach = "ctrl+f"
key_insert_paste_image = "ctrl+v"
key_insert_remove_attachment = "ctrl+x"

key_visual_yank = "y"
key_visual_forward = "f"

key_forward_send = "enter"
key_forward_toggle = "space"
key_forward_cancel = "esc"
key_forward_search = "/"
key_forward_move_down = "j"
key_forward_move_up = "k"
key_forward_backspace = "backspace"
key_forward_backspace_alt = "ctrl+h"
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
:edit-message
:sticker
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

Sticker sending uses `key_normal_pick_sticker` or `:sticker`. Linux defaults to `sticker_picker_command = "nsxiv -t -o -p {files}"`; commands may use `{files}` for the temporary sticker file list, `{dir}` for the picker directory, and `{chooser}` for a chooser-output file. WebP stickers are selectable in v1; Lottie/TGS stickers are cached as metadata but skipped by the current picker/send path.

All TUI action keys are configurable in `config.toml` using flat `key_<mode>_<action>` variables. Bindings accept printable single keys plus named tokens such as `space`, `enter`, `shift+enter`, `esc`, `tab`, `shift+tab`, `backspace`, `ctrl+x`, `alt+x`, `alt+enter`, and leader sequences. Duplicate bindings in the same mode and prefix conflicts such as binding both `space` and `leader s` are rejected at startup with a config error. Composer `shift+enter` only works when the terminal reports it distinctly; `ctrl+j` is the reliable newline fallback.

## Media and previews

`preview_backend = "auto"` chooses the best available renderer. On Linux the order is Sixel, `ueberzug++`, `chafa`, external opener, none. On Windows the order is Sixel, native external opener, `chafa`, none, so Windows does not default to low-resolution symbol previews when the native opener is available. If Sixel is unavailable but Chafa is installed on Windows, the TUI asks before enabling Chafa as a lower-resolution inline fallback for previews and chat avatars. Images, videos, stickers, and avatars can render inline when a capable backend is selected. Videos use generated thumbnails when `ffmpeg` is available. Audio messages render as compact playback rows and use `audio_player_command`, which defaults to `mpv --no-video --no-terminal --really-quiet {path}` on Linux and the native Windows opener on Windows.

Remote media starts as metadata in SQLite. Opening, previewing, saving, or running `vimwhat media open <message-id>` can download it through the paired WhatsApp session when a download descriptor exists.

## Emoji and theming

Emoji rendering defaults to `emoji_mode = "auto"` in `config.toml`. Auto mode preserves full emoji sequences on most UTF-8 terminals, but uses the stable compatibility path for terminals such as `st` and Windows Terminal/classic Windows console hosts that can display emoji glyphs while still misreporting complex emoji cell widths. Set `emoji_mode = "compat"` to force stable degraded rendering, or `emoji_mode = "full"` to force skin tones, ZWJ professions/families, and flags.

On Windows, run `vimwhat doctor` if the TUI looks wrong. It reports the terminal environment variables used for media, emoji, and focus-reporting decisions. Older console hosts may not support all optional terminal features; Windows focus reporting is enabled only for known modern hosts or with `VIMWHAT_FORCE_REPORT_FOCUS=1`.

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
