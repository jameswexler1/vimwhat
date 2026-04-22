package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"vimwhat/internal/config"
	"vimwhat/internal/media"
	"vimwhat/internal/store"
	"vimwhat/internal/ui"
)

type Environment struct {
	Paths                config.Paths
	Config               config.Config
	PreviewReport        media.Report
	Store                *store.Store
	OpenWhatsAppSession  WhatsAppSessionOpener
	CheckWhatsAppSession WhatsAppSessionStatusChecker
	RenderQR             QRRenderer
}

func Main(args []string) int {
	env, err := Bootstrap()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vimwhat: %v\n", err)
		return 1
	}
	defer func() {
		if err := env.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "vimwhat: close: %v\n", err)
		}
	}()

	return run(env, args, os.Stdout, os.Stderr)
}

func Bootstrap() (Environment, error) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return Environment{}, err
	}
	if err := paths.Ensure(); err != nil {
		return Environment{}, err
	}

	cfg, err := config.Load(paths)
	if err != nil {
		return Environment{}, err
	}

	db, err := store.Open(paths.DatabaseFile)
	if err != nil {
		return Environment{}, err
	}

	return Environment{
		Paths:                paths,
		Config:               cfg,
		PreviewReport:        media.Detect(cfg.PreviewBackend),
		Store:                db,
		OpenWhatsAppSession:  defaultOpenWhatsAppSession,
		CheckWhatsAppSession: defaultCheckWhatsAppSession,
		RenderQR:             renderTerminalQR,
	}, nil
}

func (e Environment) Close() error {
	if e.Store == nil {
		return nil
	}
	return e.Store.Close()
}

func run(env Environment, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runTUI(env, stderr)
	}

	switch args[0] {
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	case "doctor":
		printDoctor(env, stdout)
		return 0
	case "demo":
		return runDemo(env, args[1:], stdout, stderr)
	case "login":
		return runLogin(env, stdout, stderr)
	case "logout":
		return runLogout(env, stdout, stderr)
	case "media":
		return runMedia(args[1:], stderr)
	case "export":
		return runExport(args[1:], stderr)
	default:
		fmt.Fprintf(stderr, "vimwhat: unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 1
	}
}

func runTUI(env Environment, stderr io.Writer) int {
	snapshot, err := env.Store.LoadSnapshot(context.Background(), 200)
	if err != nil {
		fmt.Fprintf(stderr, "vimwhat: load snapshot: %v\n", err)
		return 1
	}

	opts := ui.Options{
		Paths:         env.Paths,
		Config:        env.Config,
		PreviewReport: env.PreviewReport,
		Snapshot:      snapshot,
		PersistMessage: func(chatID, body string, attachments []ui.Attachment) (store.Message, error) {
			message := pendingOutgoingMessage(chatID, body, attachments)
			if err := env.Store.AddMessageWithMedia(context.Background(), message, message.Media); err != nil {
				return store.Message{}, err
			}

			return message, nil
		},
		LoadMessages: func(chatID string, limit int) ([]store.Message, error) {
			return env.Store.ListMessages(context.Background(), chatID, limit)
		},
		SaveDraft: func(chatID, body string) error {
			return env.Store.SaveDraft(context.Background(), chatID, body)
		},
		SearchChats: func(query string) ([]store.Chat, error) {
			return env.Store.SearchChats(context.Background(), query, 100)
		},
		SearchMessages: func(chatID, query string, limit int) ([]store.Message, error) {
			return env.Store.SearchMessages(context.Background(), chatID, query, limit)
		},
		CopyToClipboard: func(text string) error {
			return copyToClipboard(context.Background(), env.Config.ClipboardCommand, text)
		},
		PickAttachment: func() tea.Cmd {
			return pickAttachment(env.Config.FilePickerCommand)
		},
		OpenMedia: func(media store.MediaMetadata) tea.Cmd {
			return openMedia(env.Config, media)
		},
		StartAudio: func(media store.MediaMetadata) (ui.AudioProcess, error) {
			return startAudio(env.Config, media)
		},
		DeleteMessage: func(messageID string) error {
			return env.Store.DeleteMessage(context.Background(), messageID)
		},
		SaveMedia: func(media store.MediaMetadata) error {
			return env.Store.UpsertMediaMetadata(context.Background(), media)
		},
	}

	if err := ui.Run(opts); err != nil {
		fmt.Fprintf(stderr, "vimwhat: %v\n", err)
		return 1
	}

	return 0
}

func pendingOutgoingMessage(chatID, body string, attachments []ui.Attachment) store.Message {
	now := time.Now()
	message := store.Message{
		ID:         fmt.Sprintf("local-%d", now.UnixNano()),
		ChatID:     chatID,
		ChatJID:    chatID,
		Sender:     "me",
		SenderJID:  "me",
		Body:       body,
		Timestamp:  now,
		IsOutgoing: true,
		Status:     "pending",
	}
	message.Media = mediaForOutgoingMessage(message.ID, attachments, now)
	return message
}

func runMedia(args []string, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "open" {
		fmt.Fprintln(stderr, "usage: vimwhat media open <message-id>")
		return 1
	}

	fmt.Fprintf(stderr, "vimwhat: media open is not implemented yet for message %q\n", args[1])
	return 1
}

func runExport(args []string, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "chat" {
		fmt.Fprintln(stderr, "usage: vimwhat export chat <jid>")
		return 1
	}

	fmt.Fprintf(stderr, "vimwhat: export chat is not implemented yet for chat %q\n", args[1])
	return 1
}

func runDemo(env Environment, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: vimwhat demo <seed|clear>")
		return 1
	}

	switch args[0] {
	case "seed":
		if err := env.Store.SeedDemoData(context.Background()); err != nil {
			fmt.Fprintf(stderr, "vimwhat: seed demo data: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "seeded demo data into the local database")
		return 0
	case "clear":
		if err := env.Store.ClearDemoData(context.Background()); err != nil {
			fmt.Fprintf(stderr, "vimwhat: clear demo data: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "cleared demo data from the local database")
		return 0
	default:
		fmt.Fprintln(stderr, "usage: vimwhat demo <seed|clear>")
		return 1
	}
}

func printDoctor(env Environment, w io.Writer) {
	stats, err := env.Store.Stats(context.Background())
	if err != nil {
		stats = store.Stats{}
	}
	appliedMigrations, pendingMigrations, migrationErr := env.Store.MigrationStatus(context.Background())
	sessionStatus := sessionStatusLine(env, context.Background())

	lines := []string{
		"vimwhat doctor",
		"",
		"app: vimwhat",
		fmt.Sprintf("config file: %s", env.Paths.ConfigFile),
		fmt.Sprintf("data dir: %s", env.Paths.DataDir),
		fmt.Sprintf("cache dir: %s", env.Paths.CacheDir),
		fmt.Sprintf("database path: %s", env.Paths.DatabaseFile),
		fmt.Sprintf("session path: %s", env.Paths.SessionFile),
		fmt.Sprintf("session status: %s", sessionStatus),
		fmt.Sprintf("editor: %s", env.Config.Editor),
		fmt.Sprintf("preview max: %dx%d delay=%dms", env.Config.PreviewMaxWidth, env.Config.PreviewMaxHeight, env.Config.PreviewDelayMS),
		fmt.Sprintf("downloads dir: %s", env.Config.DownloadsDir),
		fmt.Sprintf("leader key: %s", env.Config.LeaderKey),
		fmt.Sprintf("image viewer command: %s", emptyAsAuto(env.Config.ImageViewerCommand)),
		fmt.Sprintf("video player command: %s", emptyAsAuto(env.Config.VideoPlayerCommand)),
		fmt.Sprintf("audio player command: %s", emptyAsAuto(env.Config.AudioPlayerCommand)),
		fmt.Sprintf("file opener command: %s", emptyAsAuto(env.Config.FileOpenerCommand)),
		fmt.Sprintf("chat rows: %d", stats.Chats),
		fmt.Sprintf("message rows: %d", stats.Messages),
		fmt.Sprintf("draft rows: %d", stats.Drafts),
		fmt.Sprintf("contact rows: %d", stats.Contacts),
		fmt.Sprintf("media rows: %d", stats.MediaItems),
		fmt.Sprintf("migration rows: %d", stats.Migrations),
	}
	if migrationErr != nil {
		lines = append(lines, fmt.Sprintf("migration status: %v", migrationErr))
	} else {
		lines = append(lines,
			fmt.Sprintf("applied migrations: %s", strings.Join(appliedMigrations, ", ")),
			fmt.Sprintf("pending migrations: %s", noneIfEmpty(pendingMigrations)),
		)
	}
	lines = append(lines, env.PreviewReport.Lines()...)

	fmt.Fprintln(w, strings.Join(lines, "\n"))
}

func emptyAsAuto(value string) string {
	if strings.TrimSpace(value) == "" {
		return "auto"
	}
	return value
}

func noneIfEmpty(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, strings.TrimSpace(`
usage:
  vimwhat
  vimwhat demo seed
  vimwhat demo clear
  vimwhat login
  vimwhat logout
  vimwhat doctor
  vimwhat media open <message-id>
  vimwhat export chat <jid>
`))
}
