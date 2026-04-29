package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"vimwhat/internal/config"
	"vimwhat/internal/media"
	"vimwhat/internal/notify"
	"vimwhat/internal/store"
	"vimwhat/internal/ui"
	"vimwhat/internal/whatsapp"
)

type Environment struct {
	Paths                config.Paths
	Config               config.Config
	PreviewReport        media.Report
	NotificationReport   notify.Report
	Store                *store.Store
	OpenWhatsAppSession  WhatsAppSessionOpener
	CheckWhatsAppSession WhatsAppSessionStatusChecker
	OpenNotifier         NotificationOpener
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
	if err := config.EnsureDefaultFile(paths); err != nil {
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
		NotificationReport:   notify.Detect(cfg),
		Store:                db,
		OpenWhatsAppSession:  defaultOpenWhatsAppSession,
		CheckWhatsAppSession: defaultCheckWhatsAppSession,
		OpenNotifier:         defaultOpenNotifier,
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
		return runMedia(env, args[1:], stdout, stderr)
	case "export":
		return runExport(env, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "vimwhat: unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 1
	}
}

func runTUI(env Environment, stderr io.Writer) int {
	if err := runPreStartCanonicalRepair(context.Background(), env); err != nil {
		fmt.Fprintf(stderr, "vimwhat: direct chat repair: %v\n", err)
	}
	snapshot, err := env.Store.LoadSnapshot(context.Background(), 200)
	if err != nil {
		fmt.Fprintf(stderr, "vimwhat: load snapshot: %v\n", err)
		return 1
	}

	initialConnection, liveEnabled := initialLiveConnectionState(env, context.Background())
	liveUpdates := make(chan ui.LiveUpdate, 64)
	historyRequests := make(chan string, 16)
	textSendRequests := make(chan textSendRequest, textSendQueueSize)
	mediaSendRequests := make(chan mediaSendRequest, mediaSendQueueSize)
	readReceiptRequests := make(chan readReceiptRequest, readReceiptQueueSize)
	reactionRequests := make(chan reactionRequest, reactionQueueSize)
	deleteEveryoneRequests := make(chan deleteEveryoneRequest, deleteEveryoneQueueSize)
	editMessageRequests := make(chan editMessageRequest, editMessageQueueSize)
	forwardRequests := make(chan forwardMessagesRequest, forwardMessageQueueSize)
	presenceRequests := make(chan presenceRequest, presenceQueueSize)
	presenceSubscribeRequests := make(chan presenceSubscribeRequest, presenceQueueSize)
	mediaDownloadRequests := make(chan mediaDownloadRequest, mediaDownloadQueueSize)
	stickerSyncRequests := make(chan stickerSyncRequest, stickerSyncQueueSize)
	activeChatNotifications := make(chan string, 16)
	appFocusNotifications := make(chan bool, 16)
	visibleChatNotifications := make(chan []string, 16)
	var liveUpdateSource <-chan ui.LiveUpdate
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	if liveEnabled {
		liveUpdateSource = liveUpdates
		wg.Add(1)
		go func() {
			defer wg.Done()
			runLiveWhatsApp(ctx, env, liveUpdates, historyRequests, textSendRequests, mediaSendRequests, readReceiptRequests, reactionRequests, deleteEveryoneRequests, editMessageRequests, forwardRequests, presenceRequests, presenceSubscribeRequests, mediaDownloadRequests, stickerSyncRequests, activeChatNotifications, appFocusNotifications, visibleChatNotifications)
		}()
	}
	defer func() {
		cancel()
		wg.Wait()
		close(liveUpdates)
	}()

	opts := ui.Options{
		Paths:                env.Paths,
		Config:               env.Config,
		PreviewReport:        env.PreviewReport,
		Snapshot:             snapshot,
		ConnectionState:      initialConnection,
		LiveUpdates:          liveUpdateSource,
		RequireOnlineForSend: liveEnabled,
		PersistMessage: func(outgoing ui.OutgoingMessage) (store.Message, error) {
			if liveEnabled {
				if len(outgoing.Attachments) > 0 {
					waitCtx, cancel := context.WithTimeout(context.Background(), mediaSendQueueTimeout)
					defer cancel()
					result := make(chan mediaSendQueuedResult, 1)
					request := mediaSendRequest{
						Context:     waitCtx,
						ChatID:      outgoing.ChatID,
						Body:        outgoing.Body,
						Attachments: slices.Clone(outgoing.Attachments),
						Quote:       cloneMessagePtr(outgoing.Quote),
						Mentions:    slices.Clone(outgoing.Mentions),
						Result:      result,
					}
					return queueMediaSendRequest(waitCtx, mediaSendRequests, request)
				}
				waitCtx, cancel := context.WithTimeout(context.Background(), textSendQueueTimeout)
				defer cancel()
				result := make(chan textSendQueuedResult, 1)
				request := textSendRequest{
					Context:  waitCtx,
					ChatID:   outgoing.ChatID,
					Body:     outgoing.Body,
					Quote:    cloneMessagePtr(outgoing.Quote),
					Mentions: slices.Clone(outgoing.Mentions),
					Result:   result,
				}
				select {
				case textSendRequests <- request:
				case <-waitCtx.Done():
					return store.Message{}, fmt.Errorf("text send queue timed out")
				default:
					return store.Message{}, fmt.Errorf("text send request queue is full")
				}

				select {
				case queued := <-result:
					return queued.Message, queued.Err
				case <-waitCtx.Done():
					return store.Message{}, fmt.Errorf("text send queue timed out")
				}
			}

			message := pendingOutgoingMessage(outgoing)
			if err := env.Store.AddMessageWithMedia(context.Background(), message, message.Media); err != nil {
				return store.Message{}, err
			}

			return message, nil
		},
		RetryMessage: func(message store.Message) (store.Message, error) {
			if !liveEnabled {
				return store.Message{}, fmt.Errorf("whatsapp is not paired")
			}
			waitCtx, cancel := context.WithTimeout(context.Background(), mediaSendQueueTimeout)
			defer cancel()
			result := make(chan mediaSendQueuedResult, 1)
			request, err := retryMediaSendRequest(waitCtx, env.Store, message, result)
			if err != nil {
				return store.Message{}, err
			}
			return queueMediaSendRequest(waitCtx, mediaSendRequests, request)
		},
		SendSticker: func(chatID string, sticker store.RecentSticker) (store.Message, error) {
			if !liveEnabled {
				return store.Message{}, fmt.Errorf("whatsapp is not paired")
			}
			waitCtx, cancel := context.WithTimeout(context.Background(), mediaSendQueueTimeout)
			defer cancel()
			result := make(chan mediaSendQueuedResult, 1)
			request := mediaSendRequest{
				Context: waitCtx,
				ChatID:  chatID,
				Sticker: cloneRecentStickerPtr(&sticker),
				Result:  result,
			}
			return queueMediaSendRequest(waitCtx, mediaSendRequests, request)
		},
		MarkRead: func(chat store.Chat, messages []store.Message) error {
			if !liveEnabled {
				return fmt.Errorf("whatsapp is not paired")
			}
			result := make(chan error, 1)
			request := readReceiptRequest{Chat: chat, Messages: messages, Result: result}
			select {
			case readReceiptRequests <- request:
			default:
				return fmt.Errorf("read receipt queue is full")
			}
			waitCtx, cancel := context.WithTimeout(context.Background(), readReceiptQueueTimeout)
			defer cancel()
			select {
			case err := <-result:
				return err
			case <-waitCtx.Done():
				return fmt.Errorf("read receipt queue timed out")
			}
		},
		SendReaction: func(message store.Message, emoji string) error {
			if !liveEnabled {
				return fmt.Errorf("whatsapp is not paired")
			}
			result := make(chan error, 1)
			request := reactionRequest{Message: message, Emoji: emoji, Result: result}
			select {
			case reactionRequests <- request:
			default:
				return fmt.Errorf("reaction queue is full")
			}
			waitCtx, cancel := context.WithTimeout(context.Background(), reactionQueueTimeout)
			defer cancel()
			select {
			case err := <-result:
				return err
			case <-waitCtx.Done():
				return fmt.Errorf("reaction queue timed out")
			}
		},
		SendPresence: func(chatID string, composing bool) error {
			if !liveEnabled {
				return nil
			}
			select {
			case presenceRequests <- presenceRequest{ChatID: chatID, Composing: composing}:
				return nil
			default:
				return fmt.Errorf("presence queue is full")
			}
		},
		SubscribePresence: func(chatID string) error {
			if !liveEnabled {
				return nil
			}
			select {
			case presenceSubscribeRequests <- presenceSubscribeRequest{ChatID: chatID}:
				return nil
			default:
				return fmt.Errorf("presence subscription queue is full")
			}
		},
		LoadMessages: func(chatID string, limit int) ([]store.Message, error) {
			return env.Store.ListMessages(context.Background(), chatID, limit)
		},
		LoadOlderMessages: func(chatID string, before store.Message, limit int) ([]store.Message, error) {
			return env.Store.ListMessagesBefore(context.Background(), chatID, before, limit)
		},
		RequestHistory: func(chatID string) error {
			if !liveEnabled {
				return fmt.Errorf("whatsapp is not paired")
			}
			chatID = strings.TrimSpace(chatID)
			if chatID == "" {
				return fmt.Errorf("no active chat")
			}
			select {
			case historyRequests <- chatID:
				return nil
			default:
				return fmt.Errorf("history request queue is full")
			}
		},
		ReloadSnapshot: func(activeChatID string, limit int) (store.Snapshot, error) {
			return loadSnapshotForChat(context.Background(), env.Store, activeChatID, limit)
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
		SearchMentionCandidates: func(chatID, query string, limit int) ([]store.MentionCandidate, error) {
			return env.Store.SearchMentionCandidates(context.Background(), chatID, query, limit)
		},
		CopyToClipboard: func(text string) error {
			return copyToClipboard(context.Background(), env.Config.ClipboardCommand, text)
		},
		PasteImageFromClipboard: func() tea.Cmd {
			return pasteImageFromClipboard(env.Paths, env.Config.ClipboardImagePasteCommand)
		},
		CopyImageToClipboard: func(media store.MediaMetadata) tea.Cmd {
			return copyImageToClipboard(env.Config.ClipboardImageCopyCommand, media)
		},
		PickAttachment: func() tea.Cmd {
			return pickAttachment(env.Config.FilePickerCommand)
		},
		PickSticker: func() tea.Cmd {
			return pickSticker(env.Paths, env.Config, env.Store)
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
		DeleteMessageForEveryone: func(message store.Message) tea.Cmd {
			return deleteMessageForEveryoneCmd(liveEnabled, deleteEveryoneRequests, message)
		},
		EditMessage: func(message store.Message, body string) tea.Cmd {
			return editMessageCmd(liveEnabled, editMessageRequests, message, body)
		},
		ForwardMessages: func(request ui.ForwardMessagesRequest) tea.Cmd {
			return forwardMessagesCmd(liveEnabled, forwardRequests, request)
		},
		SaveMedia: func(media store.MediaMetadata) error {
			return env.Store.UpsertMediaMetadata(context.Background(), media)
		},
		DownloadMedia: func(message store.Message, media store.MediaMetadata) (store.MediaMetadata, error) {
			if !liveEnabled {
				return store.MediaMetadata{}, fmt.Errorf("whatsapp is not paired")
			}
			if strings.TrimSpace(message.ID) == "" {
				return store.MediaMetadata{}, fmt.Errorf("message id is required")
			}
			result := make(chan mediaDownloadResult, 1)
			request := mediaDownloadRequest{
				Message: message,
				Media:   media,
				Result:  result,
			}
			select {
			case mediaDownloadRequests <- request:
			default:
				return media, fmt.Errorf("media download request queue is full")
			}

			ctx, cancel := context.WithTimeout(context.Background(), mediaDownloadTimeout)
			defer cancel()
			select {
			case downloaded := <-result:
				return downloaded.Media, downloaded.Err
			case <-ctx.Done():
				return media, fmt.Errorf("media download timed out")
			}
		},
	}
	if liveEnabled {
		opts.ActiveChatChanged = func(chatID string) {
			select {
			case activeChatNotifications <- chatID:
			default:
			}
		}
		opts.AppFocusChanged = func(focused bool) {
			select {
			case appFocusNotifications <- focused:
			default:
			}
		}
		opts.VisibleChatsChanged = func(chatIDs []string) {
			select {
			case visibleChatNotifications <- slices.Clone(chatIDs):
			default:
			}
		}
	}

	if err := ui.Run(opts); err != nil {
		fmt.Fprintf(stderr, "vimwhat: %v\n", err)
		return 1
	}

	return 0
}

func initialLiveConnectionState(env Environment, ctx context.Context) (ui.ConnectionState, bool) {
	status, err := checkWhatsAppSession(ctx, env)
	if err != nil {
		return ui.ConnectionOffline, false
	}
	if status.Paired {
		return ui.ConnectionPaired, true
	}
	return ui.ConnectionLoggedOut, false
}

const (
	historyPageSize            = 50
	historyRequestTimeout      = 30 * time.Second
	metadataRefreshTimeout     = 30 * time.Second
	textSendQueueSize          = 16
	textSendQueueTimeout       = 5 * time.Second
	textSendTimeout            = 90 * time.Second
	mediaSendQueueSize         = 16
	mediaSendQueueTimeout      = 5 * time.Second
	mediaSendTimeout           = 10 * time.Minute
	readReceiptQueueSize       = 16
	readReceiptQueueTimeout    = 5 * time.Second
	readReceiptTimeout         = 30 * time.Second
	reactionQueueSize          = 16
	reactionQueueTimeout       = 5 * time.Second
	reactionSendTimeout        = 90 * time.Second
	deleteEveryoneQueueSize    = 16
	deleteEveryoneQueueTimeout = 5 * time.Second
	deleteEveryoneTimeout      = 90 * time.Second
	editMessageQueueSize       = 16
	editMessageQueueTimeout    = 5 * time.Second
	editMessageTimeout         = 90 * time.Second
	forwardMessageQueueSize    = 16
	forwardMessageQueueTimeout = 5 * time.Second
	forwardMessageTimeout      = 90 * time.Second
	presenceQueueSize          = 32
	presenceSendTimeout        = 5 * time.Second
	mediaDownloadWorkers       = 2
	mediaDownloadQueueSize     = 16
	mediaDownloadTimeout       = 5 * time.Minute
	stickerSyncQueueSize       = 4
	stickerSyncTimeout         = 90 * time.Second
	avatarRefreshQueueSize     = 32
	avatarRefreshTimeout       = 30 * time.Second
)

var (
	offlineSyncProgressEvery = 150 * time.Millisecond
	offlineSyncInactivity    = 15 * time.Second
	offlineSyncMaxDuration   = 60 * time.Second
)

type textSendRequest struct {
	Context  context.Context
	ChatID   string
	Body     string
	Quote    *store.Message
	Mentions []store.MessageMention
	Result   chan<- textSendQueuedResult
}

type textSendQueuedResult struct {
	Message store.Message
	Err     error
}

type mediaSendRequest struct {
	Context     context.Context
	ChatID      string
	Body        string
	Attachments []ui.Attachment
	Sticker     *store.RecentSticker
	Quote       *store.Message
	Mentions    []store.MessageMention
	Result      chan mediaSendQueuedResult
}

type mediaSendQueuedResult struct {
	Message store.Message
	Err     error
}

func queueMediaSendRequest(ctx context.Context, requests chan<- mediaSendRequest, request mediaSendRequest) (store.Message, error) {
	select {
	case requests <- request:
	case <-ctx.Done():
		return store.Message{}, fmt.Errorf("media send queue timed out")
	default:
		return store.Message{}, fmt.Errorf("media send request queue is full")
	}

	select {
	case queued := <-request.Result:
		return queued.Message, queued.Err
	case <-ctx.Done():
		return store.Message{}, fmt.Errorf("media send queue timed out")
	}
}

func queueStickerSyncRequest(ctx context.Context, requests chan<- stickerSyncRequest) error {
	if requests == nil {
		return fmt.Errorf("sticker sync queue is unavailable")
	}
	result := make(chan stickerSyncResult, 1)
	request := stickerSyncRequest{Context: ctx, Result: result}
	select {
	case requests <- request:
	case <-ctx.Done():
		return fmt.Errorf("sticker sync queue timed out")
	default:
		return fmt.Errorf("sticker sync queue is full")
	}

	select {
	case synced := <-result:
		return synced.Err
	case <-ctx.Done():
		return fmt.Errorf("sticker sync timed out")
	}
}

type readReceiptRequest struct {
	Chat     store.Chat
	Messages []store.Message
	Result   chan<- error
}

type reactionRequest struct {
	Message store.Message
	Emoji   string
	Result  chan<- error
}

type deleteEveryoneRequest struct {
	Message store.Message
	Result  chan<- deleteEveryoneResult
}

type deleteEveryoneResult struct {
	MessageID string
	Err       error
}

type editMessageRequest struct {
	Message store.Message
	Body    string
	Result  chan<- editMessageResult
}

type editMessageResult struct {
	MessageID string
	Body      string
	EditedAt  time.Time
	Err       error
}

type forwardMessagesRequest struct {
	Context    context.Context
	Messages   []store.Message
	Recipients []store.Chat
	Result     chan<- forwardMessagesResult
}

type forwardMessagesResult struct {
	Sent    int
	Skipped int
	Failed  int
	Err     error
}

func deleteMessageForEveryoneCmd(liveEnabled bool, requests chan<- deleteEveryoneRequest, message store.Message) tea.Cmd {
	return func() tea.Msg {
		if !liveEnabled {
			return ui.MessageDeletedForEveryoneMsg{MessageID: message.ID, Err: fmt.Errorf("whatsapp is not paired")}
		}
		if requests == nil {
			return ui.MessageDeletedForEveryoneMsg{MessageID: message.ID, Err: fmt.Errorf("delete queue is unavailable")}
		}
		result := make(chan deleteEveryoneResult, 1)
		request := deleteEveryoneRequest{Message: message, Result: result}

		queueCtx, cancelQueue := context.WithTimeout(context.Background(), deleteEveryoneQueueTimeout)
		defer cancelQueue()
		select {
		case requests <- request:
		case <-queueCtx.Done():
			return ui.MessageDeletedForEveryoneMsg{MessageID: message.ID, Err: fmt.Errorf("delete queue timed out")}
		default:
			return ui.MessageDeletedForEveryoneMsg{MessageID: message.ID, Err: fmt.Errorf("delete request queue is full")}
		}

		waitCtx, cancelWait := context.WithTimeout(context.Background(), deleteEveryoneTimeout)
		defer cancelWait()
		select {
		case deleted := <-result:
			return ui.MessageDeletedForEveryoneMsg{MessageID: deleted.MessageID, Err: deleted.Err}
		case <-waitCtx.Done():
			return ui.MessageDeletedForEveryoneMsg{MessageID: message.ID, Err: fmt.Errorf("delete for everybody timed out")}
		}
	}
}

func editMessageCmd(liveEnabled bool, requests chan<- editMessageRequest, message store.Message, body string) tea.Cmd {
	return func() tea.Msg {
		if !liveEnabled {
			return ui.MessageEditedMsg{MessageID: message.ID, Body: body, Err: fmt.Errorf("whatsapp is not paired")}
		}
		if requests == nil {
			return ui.MessageEditedMsg{MessageID: message.ID, Body: body, Err: fmt.Errorf("edit queue is unavailable")}
		}
		result := make(chan editMessageResult, 1)
		request := editMessageRequest{Message: message, Body: body, Result: result}

		queueCtx, cancelQueue := context.WithTimeout(context.Background(), editMessageQueueTimeout)
		defer cancelQueue()
		select {
		case requests <- request:
		case <-queueCtx.Done():
			return ui.MessageEditedMsg{MessageID: message.ID, Body: body, Err: fmt.Errorf("edit queue timed out")}
		default:
			return ui.MessageEditedMsg{MessageID: message.ID, Body: body, Err: fmt.Errorf("edit request queue is full")}
		}

		waitCtx, cancelWait := context.WithTimeout(context.Background(), editMessageTimeout)
		defer cancelWait()
		select {
		case edited := <-result:
			return ui.MessageEditedMsg{MessageID: edited.MessageID, Body: edited.Body, EditedAt: edited.EditedAt, Err: edited.Err}
		case <-waitCtx.Done():
			return ui.MessageEditedMsg{MessageID: message.ID, Body: body, Err: fmt.Errorf("edit timed out")}
		}
	}
}

func forwardMessagesCmd(liveEnabled bool, requests chan<- forwardMessagesRequest, request ui.ForwardMessagesRequest) tea.Cmd {
	return func() tea.Msg {
		if !liveEnabled {
			return ui.ForwardMessagesFinishedMsg{Err: fmt.Errorf("whatsapp is not paired")}
		}
		if requests == nil {
			return ui.ForwardMessagesFinishedMsg{Err: fmt.Errorf("forward queue is unavailable")}
		}
		result := make(chan forwardMessagesResult, 1)
		messages := slices.Clone(request.Messages)
		recipients := uniqueForwardRecipients(request.Recipients)
		workCtx, cancelWork := context.WithTimeout(context.Background(), forwardMessageTimeout)
		defer cancelWork()
		queued := forwardMessagesRequest{
			Context:    workCtx,
			Messages:   messages,
			Recipients: recipients,
			Result:     result,
		}

		queueCtx, cancelQueue := context.WithTimeout(context.Background(), forwardMessageQueueTimeout)
		defer cancelQueue()
		select {
		case requests <- queued:
		case <-queueCtx.Done():
			return ui.ForwardMessagesFinishedMsg{Err: fmt.Errorf("forward queue timed out")}
		default:
			return ui.ForwardMessagesFinishedMsg{Err: fmt.Errorf("forward request queue is full")}
		}

		select {
		case forwarded := <-result:
			return ui.ForwardMessagesFinishedMsg{
				Sent:    forwarded.Sent,
				Skipped: forwarded.Skipped,
				Failed:  forwarded.Failed,
				Err:     forwarded.Err,
			}
		case <-workCtx.Done():
			return ui.ForwardMessagesFinishedMsg{Err: fmt.Errorf("forward timed out")}
		}
	}
}

func uniqueForwardRecipients(recipients []store.Chat) []store.Chat {
	out := make([]store.Chat, 0, len(recipients))
	seen := map[string]bool{}
	for _, recipient := range recipients {
		key := strings.TrimSpace(recipient.ID)
		if key == "" {
			key = strings.TrimSpace(recipient.JID)
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, recipient)
	}
	return out
}

type presenceRequest struct {
	ChatID    string
	Composing bool
}

type presenceSubscribeRequest struct {
	ChatID string
}

type mediaDownloadRequest struct {
	Message store.Message
	Media   store.MediaMetadata
	Result  chan<- mediaDownloadResult
}

type mediaDownloadResult struct {
	Media store.MediaMetadata
	Err   error
}

type stickerSyncRequest struct {
	Context context.Context
	Result  chan<- stickerSyncResult
}

type stickerSyncResult struct {
	Stickers int
	Err      error
}

type avatarRefreshRequest struct {
	ChatID string
}

type avatarRefreshResult struct {
	ChatID  string
	Refresh bool
	Status  string
	Err     error
}

func runLiveWhatsApp(
	ctx context.Context,
	env Environment,
	updates chan<- ui.LiveUpdate,
	historyRequests <-chan string,
	textSendRequests <-chan textSendRequest,
	mediaSendRequests <-chan mediaSendRequest,
	readReceiptRequests <-chan readReceiptRequest,
	reactionRequests <-chan reactionRequest,
	deleteEveryoneRequests <-chan deleteEveryoneRequest,
	editMessageRequests <-chan editMessageRequest,
	forwardRequests <-chan forwardMessagesRequest,
	presenceRequests <-chan presenceRequest,
	presenceSubscribeRequests <-chan presenceSubscribeRequest,
	mediaDownloadRequests <-chan mediaDownloadRequest,
	stickerSyncRequests <-chan stickerSyncRequest,
	activeChatUpdates <-chan string,
	appFocusUpdates <-chan bool,
	visibleChatUpdates <-chan []string,
) {
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{ConnectionState: ui.ConnectionConnecting})

	session, err := openWhatsAppSession(ctx, env)
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			ConnectionState: ui.ConnectionOffline,
			Status:          fmt.Sprintf("whatsapp open failed: %s", shortStatusError(err)),
		})
		return
	}
	defer session.Close()
	liveCtx, cancelLive := context.WithCancel(ctx)
	defer cancelLive()

	live, ok := session.(WhatsAppLiveSession)
	if !ok {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			ConnectionState: ui.ConnectionOffline,
			Status:          "whatsapp live session unavailable",
		})
		return
	}

	events, err := live.SubscribeEvents(liveCtx)
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			ConnectionState: ui.ConnectionOffline,
			Status:          fmt.Sprintf("whatsapp subscribe failed: %s", shortStatusError(err)),
		})
		return
	}

	if err := live.Connect(liveCtx); err != nil {
		if ctx.Err() != nil {
			return
		}
		state := ui.ConnectionOffline
		if isLoggedOutConnectionError(err) {
			state = ui.ConnectionLoggedOut
		}
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			ConnectionState: state,
			Status:          fmt.Sprintf("whatsapp connect failed: %s", shortStatusError(err)),
		})
		return
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{ConnectionState: ui.ConnectionOnline})

	var protocolWG sync.WaitGroup
	defer protocolWG.Wait()

	notificationJobs, stopNotifications := startNotificationWorker(ctx, env, updates)
	defer stopNotifications()

	downloadJobs := make(chan mediaDownloadRequest, mediaDownloadQueueSize)
	var downloadWG sync.WaitGroup
	for i := 0; i < mediaDownloadWorkers; i++ {
		downloadWG.Add(1)
		go func() {
			defer downloadWG.Done()
			mediaDownloadWorker(ctx, env.Store, live, env.Paths, downloadJobs)
		}()
	}
	defer func() {
		close(downloadJobs)
		downloadWG.Wait()
	}()

	avatarJobs := make(chan avatarRefreshRequest, avatarRefreshQueueSize)
	avatarResults := make(chan avatarRefreshResult, avatarRefreshQueueSize)
	var avatarWG sync.WaitGroup
	avatarWG.Add(1)
	go func() {
		defer avatarWG.Done()
		avatarRefreshWorker(ctx, env.Store, live, env.Paths, avatarJobs, avatarResults)
	}()
	defer func() {
		close(avatarJobs)
		avatarWG.Wait()
	}()

	ingestor := whatsapp.Ingestor{Store: env.Store}
	historyInflight := map[string]time.Time{}
	avatarInflight := map[string]bool{}
	metadataResults := refreshChatMetadata(ctx, live)
	viewState := notificationContext{}
	online := true
	pendingPreferredChatID := ""
	offlineSync := offlineSyncState{}
	startStickerSync(ctx, env.Store, live, env.Paths, updates, &protocolWG, online)
	var offlineSyncIdleTimer *time.Timer
	var offlineSyncIdleTimerC <-chan time.Time
	var offlineSyncMaxTimer *time.Timer
	var offlineSyncMaxTimerC <-chan time.Time
	resetOfflineSyncIdleTimer := func() {
		if offlineSyncIdleTimer == nil {
			offlineSyncIdleTimer = time.NewTimer(offlineSyncInactivity)
		} else {
			if !offlineSyncIdleTimer.Stop() {
				select {
				case <-offlineSyncIdleTimer.C:
				default:
				}
			}
			offlineSyncIdleTimer.Reset(offlineSyncInactivity)
		}
		offlineSyncIdleTimerC = offlineSyncIdleTimer.C
	}
	startOfflineSyncMaxTimer := func() {
		if offlineSyncMaxTimer == nil {
			offlineSyncMaxTimer = time.NewTimer(offlineSyncMaxDuration)
		} else {
			if !offlineSyncMaxTimer.Stop() {
				select {
				case <-offlineSyncMaxTimer.C:
				default:
				}
			}
			offlineSyncMaxTimer.Reset(offlineSyncMaxDuration)
		}
		offlineSyncMaxTimerC = offlineSyncMaxTimer.C
	}
	clearOfflineSyncTimers := func() {
		if offlineSyncIdleTimer != nil {
			if !offlineSyncIdleTimer.Stop() {
				select {
				case <-offlineSyncIdleTimer.C:
				default:
				}
			}
		}
		if offlineSyncMaxTimer != nil {
			if !offlineSyncMaxTimer.Stop() {
				select {
				case <-offlineSyncMaxTimer.C:
				default:
				}
			}
		}
		offlineSyncIdleTimerC = nil
		offlineSyncMaxTimerC = nil
	}
	finishOfflineSync := func(status string, event whatsapp.OfflineSyncEvent) {
		if !offlineSync.active {
			clearOfflineSyncTimers()
			return
		}
		syncUpdate, dirty := offlineSync.finish(event)
		clearOfflineSyncTimers()
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh:         dirty,
			Status:          status,
			PreferredChatID: pendingPreferredChatID,
			Sync:            &syncUpdate,
		})
		pendingPreferredChatID = ""
	}
	defer clearOfflineSyncTimers()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{ConnectionState: ui.ConnectionOffline})
				return
			}
			viewState = drainPendingLiveViewState(ctx, avatarJobs, avatarInflight, activeChatUpdates, appFocusUpdates, visibleChatUpdates, viewState)
			if event.Kind == whatsapp.EventConnectionState {
				online = event.Connection.State == whatsapp.ConnectionOnline
				sendLiveUpdate(ctx, updates, liveUpdateForConnectionEvent(event.Connection))
				continue
			}
			if event.Kind == whatsapp.EventOfflineSync {
				now := time.Now()
				if event.Offline.Active {
					syncUpdate := offlineSync.start(event.Offline, now)
					resetOfflineSyncIdleTimer()
					startOfflineSyncMaxTimer()
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{
						Status: "syncing WhatsApp updates",
						Sync:   &syncUpdate,
					})
					continue
				}
				if event.Offline.Completed {
					finishOfflineSync("sync complete", event.Offline)
					continue
				}
			}
			if event.Kind == whatsapp.EventChatUpsert {
				mergedAliases, err := mergeEventChatAliases(ctx, env.Store, event.Chat)
				if err != nil {
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{
						Status: fmt.Sprintf("chat merge failed: %s", shortStatusError(err)),
					})
					continue
				}
				if viewState.activeChatID != "" && slices.Contains(mergedAliases, viewState.activeChatID) {
					viewState.activeChatID = event.Chat.ID
					pendingPreferredChatID = event.Chat.ID
				}
			}
			if event.Kind == whatsapp.EventPresenceUpdate {
				sendLiveUpdate(ctx, updates, liveUpdateForPresenceEvent(event.Presence))
				continue
			}
			if event.Kind == whatsapp.EventChatAvatarUpdate {
				handleAvatarEvent(ctx, env.Store, env.Paths, avatarJobs, avatarInflight, updates, event.Avatar)
				continue
			}
			if event.Kind == whatsapp.EventMediaMetadata {
				prepared, err := prepareIncomingMediaEvent(env.Paths, event.Media)
				if err != nil {
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{
						Status: fmt.Sprintf("media cache failed: %s", shortStatusError(err)),
					})
				} else {
					event.Media = prepared
				}
			}
			if event.Kind == whatsapp.EventRecentSticker {
				prepared, err := prepareRecentStickerEvent(ctx, env.Store, live, env.Paths, event.Sticker)
				if err != nil {
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{
						Status: fmt.Sprintf("sticker cache failed: %s", shortStatusError(err)),
					})
				} else {
					event.Sticker = prepared
				}
			}
			result, err := ingestor.Apply(ctx, event)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{
					Status: fmt.Sprintf("whatsapp ingest failed: %s", shortStatusError(err)),
				})
				continue
			}
			if event.Kind == whatsapp.EventHistoryStatus {
				delete(historyInflight, event.History.ChatID)
				if offlineSync.active {
					if syncUpdate, ok := offlineSync.markProcessed(time.Now()); ok {
						sendLiveUpdate(ctx, updates, ui.LiveUpdate{Sync: &syncUpdate})
					}
					resetOfflineSyncIdleTimer()
					continue
				}
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{
					Refresh:         true,
					Status:          historyStatusLine(event.History),
					HistoryChatID:   event.History.ChatID,
					HistoryMessages: event.History.Messages,
					PreferredChatID: pendingPreferredChatID,
				})
				pendingPreferredChatID = ""
				continue
			}
			if offlineSync.active {
				syncUpdate, shouldSend := offlineSync.markProcessed(time.Now())
				resetOfflineSyncIdleTimer()
				if shouldSend {
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{Sync: &syncUpdate})
				}
				continue
			}
			if isHistoricalImportEvent(event) {
				continue
			}
			if note, ok := buildNotification(context.Background(), env.Store, viewState, result); ok {
				if strings.TrimSpace(note.IconPath) == "" {
					enqueueAvatarRefresh(ctx, avatarJobs, avatarInflight, result.Message.ChatID)
				}
				queueNotification(notificationJobs, note)
			}
			sendLiveUpdate(ctx, updates, ui.LiveUpdate{
				Refresh:         true,
				PreferredChatID: pendingPreferredChatID,
			})
			pendingPreferredChatID = ""
		case chatID, ok := <-historyRequests:
			if !ok {
				return
			}
			handleHistoryRequest(ctx, env.Store, live, updates, historyInflight, online, chatID)
		case request, ok := <-textSendRequests:
			if !ok {
				return
			}
			handleTextSendRequest(ctx, env.Store, live, updates, &protocolWG, online, request)
		case request, ok := <-mediaSendRequests:
			if !ok {
				return
			}
			handleMediaSendRequest(ctx, env.Store, live, updates, &protocolWG, online, request)
		case request, ok := <-readReceiptRequests:
			if !ok {
				return
			}
			handleReadReceiptRequest(ctx, env.Store, live, updates, &protocolWG, online, request)
		case request, ok := <-reactionRequests:
			if !ok {
				return
			}
			handleReactionRequest(ctx, env.Store, live, updates, &protocolWG, online, request)
		case request, ok := <-deleteEveryoneRequests:
			if !ok {
				return
			}
			handleDeleteEveryoneRequest(ctx, env.Store, live, updates, &protocolWG, online, request)
		case request, ok := <-editMessageRequests:
			if !ok {
				return
			}
			handleEditMessageRequest(ctx, env.Store, live, updates, &protocolWG, online, request)
		case request, ok := <-forwardRequests:
			if !ok {
				return
			}
			handleForwardMessagesRequest(ctx, env.Store, live, updates, &protocolWG, online, request)
		case request, ok := <-presenceRequests:
			if !ok {
				return
			}
			handlePresenceRequest(ctx, live, online, request)
		case request, ok := <-presenceSubscribeRequests:
			if !ok {
				return
			}
			handlePresenceSubscribeRequest(ctx, live, online, request)
		case result, ok := <-metadataResults:
			if ok {
				ingested := 0
				for _, event := range result.Events {
					if _, err := ingestor.Apply(ctx, event); err != nil {
						sendLiveUpdate(ctx, updates, ui.LiveUpdate{
							Status: fmt.Sprintf("metadata ingest failed: %s", shortStatusError(err)),
						})
						continue
					}
					ingested++
				}
				update := ui.LiveUpdate{Refresh: ingested > 0}
				if offlineSync.active && update.Refresh {
					offlineSync.dirty = true
					update.Refresh = false
				}
				if result.Err != nil {
					update.Status = fmt.Sprintf("metadata refresh failed: %s", shortStatusError(result.Err))
				}
				if update.Refresh || update.Status != "" {
					sendLiveUpdate(ctx, updates, update)
				}
			}
			metadataResults = nil
		case request, ok := <-mediaDownloadRequests:
			if !ok {
				return
			}
			enqueueMediaDownload(ctx, downloadJobs, online, request)
		case request, ok := <-stickerSyncRequests:
			if !ok {
				return
			}
			handleStickerSyncRequest(ctx, env.Store, live, env.Paths, updates, &protocolWG, online, request)
		case chatID, ok := <-activeChatUpdates:
			if !ok {
				activeChatUpdates = nil
				continue
			}
			viewState.activeChatID = chatID
			enqueueAvatarRefresh(ctx, avatarJobs, avatarInflight, chatID)
		case focused, ok := <-appFocusUpdates:
			if !ok {
				appFocusUpdates = nil
				continue
			}
			viewState.appFocusKnown = true
			viewState.appFocused = focused
		case chatIDs, ok := <-visibleChatUpdates:
			if !ok {
				visibleChatUpdates = nil
				continue
			}
			if viewState.activeChatID != "" {
				enqueueAvatarRefresh(ctx, avatarJobs, avatarInflight, viewState.activeChatID)
			}
			for _, chatID := range chatIDs {
				enqueueAvatarRefresh(ctx, avatarJobs, avatarInflight, chatID)
			}
		case result, ok := <-avatarResults:
			if !ok {
				avatarResults = nil
				continue
			}
			delete(avatarInflight, result.ChatID)
			if result.Err != nil {
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{
					Status: fmt.Sprintf("avatar refresh failed: %s", shortStatusError(result.Err)),
				})
				continue
			}
			if result.Refresh || strings.TrimSpace(result.Status) != "" {
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{
					Refresh: result.Refresh,
					Status:  result.Status,
				})
			}
		case <-offlineSyncIdleTimerC:
			finishOfflineSync("sync stalled; refreshed latest data", whatsapp.OfflineSyncEvent{
				Completed: true,
				Total:     offlineSync.total,
				Processed: offlineSync.processed,
			})
		case <-offlineSyncMaxTimerC:
			finishOfflineSync("sync timed out; refreshed latest data", whatsapp.OfflineSyncEvent{
				Completed: true,
				Total:     offlineSync.total,
				Processed: offlineSync.processed,
			})
		case <-ctx.Done():
			return
		}
	}
}

type offlineSyncState struct {
	active         bool
	dirty          bool
	total          int
	processed      int
	appDataChanges int
	messages       int
	notifications  int
	receipts       int
	lastProgress   time.Time
}

func (s *offlineSyncState) start(event whatsapp.OfflineSyncEvent, now time.Time) ui.SyncProgressUpdate {
	*s = offlineSyncState{
		active:         true,
		total:          max(0, event.Total),
		appDataChanges: max(0, event.AppDataChanges),
		messages:       max(0, event.Messages),
		notifications:  max(0, event.Notifications),
		receipts:       max(0, event.Receipts),
		lastProgress:   now,
	}
	return s.liveUpdate(true, false)
}

func (s *offlineSyncState) markProcessed(now time.Time) (ui.SyncProgressUpdate, bool) {
	if !s.active {
		return ui.SyncProgressUpdate{}, false
	}
	s.dirty = true
	s.processed++
	if s.total > 0 && s.processed > s.total {
		s.processed = s.total
	}
	if !s.lastProgress.IsZero() && now.Sub(s.lastProgress) < offlineSyncProgressEvery && (s.total == 0 || s.processed < s.total) {
		return ui.SyncProgressUpdate{}, false
	}
	s.lastProgress = now
	return s.liveUpdate(true, false), true
}

func (s *offlineSyncState) finish(event whatsapp.OfflineSyncEvent) (ui.SyncProgressUpdate, bool) {
	if event.Total > s.total {
		s.total = event.Total
	}
	if event.Processed > s.processed {
		s.processed = event.Processed
	}
	if s.total > 0 {
		s.processed = s.total
	}
	dirty := s.dirty
	update := s.liveUpdate(false, true)
	*s = offlineSyncState{}
	return update, dirty
}

func (s offlineSyncState) liveUpdate(active, completed bool) ui.SyncProgressUpdate {
	return ui.SyncProgressUpdate{
		Active:         active,
		Completed:      completed,
		Total:          s.total,
		Processed:      s.processed,
		AppDataChanges: s.appDataChanges,
		Messages:       s.messages,
		Notifications:  s.notifications,
		Receipts:       s.receipts,
	}
}

type metadataRefreshResult struct {
	Events []whatsapp.Event
	Err    error
}

func refreshChatMetadata(ctx context.Context, live WhatsAppLiveSession) <-chan metadataRefreshResult {
	metadata, ok := live.(WhatsAppMetadataSession)
	if !ok {
		return nil
	}
	results := make(chan metadataRefreshResult, 1)
	go func() {
		defer close(results)
		refreshCtx, cancel := context.WithTimeout(ctx, metadataRefreshTimeout)
		defer cancel()
		events, err := metadata.RefreshChatMetadata(refreshCtx)
		select {
		case results <- metadataRefreshResult{Events: events, Err: err}:
		case <-ctx.Done():
		}
	}()
	return results
}

func drainPendingLiveViewState(
	ctx context.Context,
	avatarJobs chan<- avatarRefreshRequest,
	avatarInflight map[string]bool,
	activeChatUpdates <-chan string,
	appFocusUpdates <-chan bool,
	visibleChatUpdates <-chan []string,
	state notificationContext,
) notificationContext {
	for {
		select {
		case chatID, ok := <-activeChatUpdates:
			if !ok {
				activeChatUpdates = nil
				continue
			}
			state.activeChatID = chatID
			enqueueAvatarRefresh(ctx, avatarJobs, avatarInflight, chatID)
		case focused, ok := <-appFocusUpdates:
			if !ok {
				appFocusUpdates = nil
				continue
			}
			state.appFocusKnown = true
			state.appFocused = focused
		case chatIDs, ok := <-visibleChatUpdates:
			if !ok {
				visibleChatUpdates = nil
				continue
			}
			if state.activeChatID != "" {
				enqueueAvatarRefresh(ctx, avatarJobs, avatarInflight, state.activeChatID)
			}
			for _, chatID := range chatIDs {
				enqueueAvatarRefresh(ctx, avatarJobs, avatarInflight, chatID)
			}
		default:
			return state
		}
	}
}

func handleTextSendRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request textSendRequest,
) {
	if request.Result == nil {
		return
	}
	if request.Context != nil {
		select {
		case <-request.Context.Done():
			return
		default:
		}
	}
	if db == nil {
		sendTextQueuedResult(ctx, request, store.Message{}, fmt.Errorf("store is required"))
		return
	}
	if live == nil {
		sendTextQueuedResult(ctx, request, store.Message{}, fmt.Errorf("whatsapp live session unavailable"))
		return
	}
	if !online {
		sendTextQueuedResult(ctx, request, store.Message{}, fmt.Errorf("text send needs WhatsApp online"))
		return
	}

	body := strings.TrimSpace(request.Body)
	if body == "" {
		sendTextQueuedResult(ctx, request, store.Message{}, fmt.Errorf("text body is required"))
		return
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, request.ChatID)
	if err != nil {
		sendTextQueuedResult(ctx, request, store.Message{}, err)
		return
	}
	remoteID := strings.TrimSpace(live.GenerateMessageID())
	if remoteID == "" {
		sendTextQueuedResult(ctx, request, store.Message{}, fmt.Errorf("generate message id failed"))
		return
	}

	now := time.Now()
	message := store.Message{
		ID:         whatsapp.LocalMessageID(chatJID, remoteID),
		RemoteID:   remoteID,
		ChatID:     chatJID,
		ChatJID:    chatJID,
		Sender:     "me",
		SenderJID:  "me",
		Body:       body,
		Timestamp:  now,
		IsOutgoing: true,
		Status:     "sending",
		Mentions:   cloneMessageMentionsForMessage(request.Mentions, whatsapp.LocalMessageID(chatJID, remoteID), now),
	}
	if request.Quote != nil {
		message.QuotedMessageID = request.Quote.ID
		message.QuotedRemoteID = request.Quote.RemoteID
	}
	if message.ID == "" {
		sendTextQueuedResult(ctx, request, store.Message{}, fmt.Errorf("message id is required"))
		return
	}
	if err := db.AddMessage(ctx, message); err != nil {
		sendTextQueuedResult(ctx, request, store.Message{}, err)
		return
	}

	sendTextQueuedResult(ctx, request, message, nil)
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeQueuedTextSend(ctx, db, live, updates, message, body, request.Quote, request.Mentions)
		}()
		return
	}
	completeQueuedTextSend(ctx, db, live, updates, message, body, request.Quote, request.Mentions)
}

func sendTextQueuedResult(ctx context.Context, request textSendRequest, message store.Message, err error) {
	if request.Result == nil {
		return
	}
	select {
	case request.Result <- textSendQueuedResult{Message: message, Err: err}:
	case <-ctx.Done():
	}
}

func completeQueuedTextSend(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, message store.Message, body string, quote *store.Message, mentions []store.MessageMention) {
	sendCtx, cancel := context.WithTimeout(ctx, textSendTimeout)
	defer cancel()
	request := whatsapp.TextSendRequest{
		ChatJID:       message.ChatJID,
		Body:          body,
		RemoteID:      message.RemoteID,
		MentionedJIDs: mentionedJIDs(mentions),
	}
	if quote != nil {
		request.QuotedRemoteID = quote.RemoteID
		request.QuotedSenderJID = quote.SenderJID
		request.QuotedMessageBody = quote.Body
	}
	result, err := live.SendText(sendCtx, request)
	if err != nil {
		_ = db.UpdateMessageStatus(context.Background(), message.ID, "failed")
		_ = db.SaveDraft(context.Background(), message.ChatID, body)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("send failed: %s", shortStatusError(err)),
		})
		return
	}

	status := strings.TrimSpace(result.Status)
	if status == "" {
		status = "sent"
	}
	if err := db.UpdateMessageStatus(context.Background(), message.ID, status); err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("send status failed: %s", shortStatusError(err)),
		})
		return
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  "sent message",
	})
}

func handleMediaSendRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request mediaSendRequest,
) {
	if request.Result == nil {
		return
	}
	if request.Context != nil {
		select {
		case <-request.Context.Done():
			return
		default:
		}
	}
	if db == nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("store is required"))
		return
	}
	if live == nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("whatsapp live session unavailable"))
		return
	}
	if !online {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("media send needs WhatsApp online"))
		return
	}
	if request.Sticker != nil {
		handleStickerSendRequest(ctx, db, live, updates, wg, request)
		return
	}
	attachment, err := prepareLiveAttachmentForSend(request.Attachments)
	if err != nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, err)
		return
	}
	body := strings.TrimSpace(request.Body)
	if media.MediaKind(attachment.MIMEType, attachment.FileName) == media.KindAudio && body != "" {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("audio attachments do not support captions"))
		return
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, request.ChatID)
	if err != nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, err)
		return
	}
	remoteID := strings.TrimSpace(live.GenerateMessageID())
	if remoteID == "" {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("generate message id failed"))
		return
	}

	now := time.Now()
	message := store.Message{
		ID:         whatsapp.LocalMessageID(chatJID, remoteID),
		RemoteID:   remoteID,
		ChatID:     chatJID,
		ChatJID:    chatJID,
		Sender:     "me",
		SenderJID:  "me",
		Body:       body,
		Timestamp:  now,
		IsOutgoing: true,
		Status:     "sending",
		Media:      liveMediaForOutgoingMessage(whatsapp.LocalMessageID(chatJID, remoteID), []ui.Attachment{attachment}, now),
		Mentions:   cloneMessageMentionsForMessage(request.Mentions, whatsapp.LocalMessageID(chatJID, remoteID), now),
	}
	if request.Quote != nil {
		message.QuotedMessageID = request.Quote.ID
		message.QuotedRemoteID = request.Quote.RemoteID
	}
	if message.ID == "" {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("message id is required"))
		return
	}
	if err := db.AddMessage(ctx, message); err != nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, err)
		return
	}

	sendMediaQueuedResult(ctx, request, message, nil)
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeQueuedMediaSend(ctx, db, live, updates, message, attachment, request.Quote, request.Mentions)
		}()
		return
	}
	completeQueuedMediaSend(ctx, db, live, updates, message, attachment, request.Quote, request.Mentions)
}

func handleStickerSendRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	request mediaSendRequest,
) {
	sticker, err := prepareLiveStickerForSend(*request.Sticker)
	if err != nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, err)
		return
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, request.ChatID)
	if err != nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, err)
		return
	}
	remoteID := strings.TrimSpace(live.GenerateMessageID())
	if remoteID == "" {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("generate message id failed"))
		return
	}

	messageID := whatsapp.LocalMessageID(chatJID, remoteID)
	now := time.Now()
	message := store.Message{
		ID:         messageID,
		RemoteID:   remoteID,
		ChatID:     chatJID,
		ChatJID:    chatJID,
		Sender:     "me",
		SenderJID:  "me",
		Timestamp:  now,
		IsOutgoing: true,
		Status:     "sending",
		Media:      mediaForOutgoingStickerMessage(messageID, sticker, now),
	}
	if request.Quote != nil {
		message.QuotedMessageID = request.Quote.ID
		message.QuotedRemoteID = request.Quote.RemoteID
	}
	if message.ID == "" {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("message id is required"))
		return
	}
	if err := db.AddMessage(ctx, message); err != nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, err)
		return
	}

	sendMediaQueuedResult(ctx, request, message, nil)
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeQueuedStickerSend(ctx, db, live, updates, message, sticker, request.Quote)
		}()
		return
	}
	completeQueuedStickerSend(ctx, db, live, updates, message, sticker, request.Quote)
}

func sendMediaQueuedResult(ctx context.Context, request mediaSendRequest, message store.Message, err error) {
	if request.Result == nil {
		return
	}
	select {
	case request.Result <- mediaSendQueuedResult{Message: message, Err: err}:
	case <-ctx.Done():
	}
}

func completeQueuedMediaSend(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, message store.Message, attachment ui.Attachment, quote *store.Message, mentions []store.MessageMention) {
	sendCtx, cancel := context.WithTimeout(ctx, mediaSendTimeout)
	defer cancel()
	request := whatsapp.MediaSendRequest{
		ChatJID:       message.ChatJID,
		LocalPath:     attachment.LocalPath,
		FileName:      attachment.FileName,
		MIMEType:      attachment.MIMEType,
		Caption:       message.Body,
		RemoteID:      message.RemoteID,
		MentionedJIDs: mentionedJIDs(mentions),
	}
	if quote != nil {
		request.QuotedRemoteID = quote.RemoteID
		request.QuotedSenderJID = quote.SenderJID
		request.QuotedMessageBody = quote.Body
	}
	result, err := live.SendMedia(sendCtx, request)
	if err != nil {
		_ = db.UpdateMessageStatus(context.Background(), message.ID, "failed")
		_ = db.SaveDraft(context.Background(), message.ChatID, message.Body)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("send failed: %s", shortStatusError(err)),
		})
		return
	}
	status := strings.TrimSpace(result.Status)
	if status == "" {
		status = "sent"
	}
	if err := db.UpdateMessageStatus(context.Background(), message.ID, status); err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("send status failed: %s", shortStatusError(err)),
		})
		return
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  mediaSendStatus(result),
	})
}

func completeQueuedStickerSend(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, message store.Message, sticker store.RecentSticker, quote *store.Message) {
	sendCtx, cancel := context.WithTimeout(ctx, mediaSendTimeout)
	defer cancel()
	request := whatsapp.StickerSendRequest{
		ChatJID:            message.ChatJID,
		LocalPath:          sticker.LocalPath,
		FileName:           sticker.FileName,
		MIMEType:           sticker.MIMEType,
		Width:              uint32(max(0, sticker.Width)),
		Height:             uint32(max(0, sticker.Height)),
		IsAnimated:         sticker.IsAnimated,
		IsLottie:           sticker.IsLottie,
		AccessibilityLabel: "",
		RemoteID:           message.RemoteID,
	}
	if quote != nil {
		request.QuotedRemoteID = quote.RemoteID
		request.QuotedSenderJID = quote.SenderJID
		request.QuotedMessageBody = quote.Body
	}
	result, err := live.SendSticker(sendCtx, request)
	if err != nil {
		_ = db.UpdateMessageStatus(context.Background(), message.ID, "failed")
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("sticker send failed: %s", shortStatusError(err)),
		})
		return
	}
	status := strings.TrimSpace(result.Status)
	if status == "" {
		status = "sent"
	}
	if err := db.UpdateMessageStatus(context.Background(), message.ID, status); err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("send status failed: %s", shortStatusError(err)),
		})
		return
	}
	refreshRecentStickerAfterSend(context.Background(), db, sticker, time.Now())
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  stickerSendStatus(result),
	})
}

func mediaSendStatus(result whatsapp.SendResult) string {
	if notice := strings.TrimSpace(result.Notice); notice != "" {
		return "sent attachment; " + notice
	}
	return "sent attachment"
}

func stickerSendStatus(result whatsapp.SendResult) string {
	if notice := strings.TrimSpace(result.Notice); notice != "" {
		return "sent sticker; " + notice
	}
	return "sent sticker"
}

func retryMediaSendRequest(ctx context.Context, db *store.Store, message store.Message, result chan mediaSendQueuedResult) (mediaSendRequest, error) {
	if retryMessageHasSticker(message) {
		sticker, err := retryStickerForMessage(message)
		if err != nil {
			return mediaSendRequest{}, err
		}
		request := mediaSendRequest{
			Context: ctx,
			ChatID:  retryChatID(message),
			Sticker: &sticker,
			Result:  result,
		}
		quote, err := retryQuoteForMessage(ctx, db, message)
		if err != nil {
			return mediaSendRequest{}, err
		}
		request.Quote = quote
		return request, nil
	}
	attachment, err := retryAttachmentForMessage(message)
	if err != nil {
		return mediaSendRequest{}, err
	}
	request := mediaSendRequest{
		Context:     ctx,
		ChatID:      retryChatID(message),
		Body:        strings.TrimSpace(message.Body),
		Attachments: []ui.Attachment{attachment},
		Mentions:    slices.Clone(message.Mentions),
		Result:      result,
	}
	quote, err := retryQuoteForMessage(ctx, db, message)
	if err != nil {
		return mediaSendRequest{}, err
	}
	request.Quote = quote
	return request, nil
}

func retryMessageHasSticker(message store.Message) bool {
	return len(message.Media) == 1 && strings.EqualFold(strings.TrimSpace(message.Media[0].Kind), "sticker")
}

func retryAttachmentForMessage(message store.Message) (ui.Attachment, error) {
	item, err := retryMediaItemForMessage(message)
	if err != nil {
		return ui.Attachment{}, err
	}
	if strings.EqualFold(strings.TrimSpace(item.Kind), "sticker") {
		return ui.Attachment{}, fmt.Errorf("retry sticker media through sticker send")
	}
	attachment, err := prepareLiveAttachmentForSend([]ui.Attachment{{
		LocalPath:     item.LocalPath,
		FileName:      item.FileName,
		MIMEType:      item.MIMEType,
		SizeBytes:     item.SizeBytes,
		ThumbnailPath: item.ThumbnailPath,
		DownloadState: item.DownloadState,
	}})
	if err != nil {
		return ui.Attachment{}, err
	}
	return attachment, nil
}

func retryStickerForMessage(message store.Message) (store.RecentSticker, error) {
	item, err := retryMediaItemForMessage(message)
	if err != nil {
		return store.RecentSticker{}, err
	}
	sticker := store.RecentSticker{
		ID:         localStickerID(store.RecentSticker{LocalPath: item.LocalPath, FileName: item.FileName}),
		MIMEType:   item.MIMEType,
		FileName:   item.FileName,
		LocalPath:  item.LocalPath,
		FileLength: item.SizeBytes,
		IsAnimated: item.IsAnimated,
		IsLottie:   item.IsLottie,
		UpdatedAt:  time.Now(),
	}
	return prepareLiveStickerForSend(sticker)
}

func retryMediaItemForMessage(message store.Message) (store.MediaMetadata, error) {
	if !message.IsOutgoing {
		return store.MediaMetadata{}, fmt.Errorf("retry needs an outgoing message")
	}
	if strings.TrimSpace(message.Status) != "failed" {
		return store.MediaMetadata{}, fmt.Errorf("retry needs a failed message")
	}
	if len(message.Media) == 0 {
		return store.MediaMetadata{}, fmt.Errorf("retry needs a media attachment")
	}
	if len(message.Media) > 1 {
		return store.MediaMetadata{}, fmt.Errorf("only one attachment per message is supported")
	}
	return message.Media[0], nil
}

func retryQuoteForMessage(ctx context.Context, db *store.Store, message store.Message) (*store.Message, error) {
	if db == nil || strings.TrimSpace(message.QuotedMessageID) == "" {
		return retryRemoteOnlyQuote(message), nil
	}
	quoted, ok, err := db.MessageByID(ctx, message.QuotedMessageID)
	if err != nil {
		return nil, err
	}
	if ok {
		return &quoted, nil
	}
	return retryRemoteOnlyQuote(message), nil
}

func retryRemoteOnlyQuote(message store.Message) *store.Message {
	quotedRemoteID := strings.TrimSpace(message.QuotedRemoteID)
	if quotedRemoteID == "" {
		return nil
	}
	return &store.Message{RemoteID: quotedRemoteID}
}

func retryChatID(message store.Message) string {
	if chatJID := strings.TrimSpace(message.ChatJID); chatJID != "" {
		return chatJID
	}
	return strings.TrimSpace(message.ChatID)
}

func prepareLiveAttachmentForSend(attachments []ui.Attachment) (ui.Attachment, error) {
	if len(attachments) == 0 {
		return ui.Attachment{}, fmt.Errorf("attachment is required")
	}
	if len(attachments) > 1 {
		return ui.Attachment{}, fmt.Errorf("only one attachment per message is supported")
	}
	attachment := attachments[0]
	localPath := strings.TrimSpace(attachment.LocalPath)
	if localPath == "" {
		return ui.Attachment{}, fmt.Errorf("attachment local path is required")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return ui.Attachment{}, fmt.Errorf("stat attachment: %w", err)
	}
	if info.IsDir() {
		return ui.Attachment{}, fmt.Errorf("attachment path is a directory")
	}
	if strings.TrimSpace(attachment.FileName) == "" {
		attachment.FileName = info.Name()
	}
	if attachment.SizeBytes <= 0 {
		attachment.SizeBytes = info.Size()
	}
	if strings.TrimSpace(attachment.MIMEType) == "" {
		if guessed := mime.TypeByExtension(strings.ToLower(filepath.Ext(attachment.FileName))); guessed != "" {
			attachment.MIMEType = guessed
		} else {
			attachment.MIMEType = "application/octet-stream"
		}
	}
	attachment.LocalPath = localPath
	attachment.DownloadState = "downloaded"
	return attachment, nil
}

func prepareLiveStickerForSend(sticker store.RecentSticker) (store.RecentSticker, error) {
	localPath := strings.TrimSpace(sticker.LocalPath)
	if localPath == "" {
		return store.RecentSticker{}, fmt.Errorf("sticker local path is required")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return store.RecentSticker{}, fmt.Errorf("stat sticker file: %w", err)
	}
	if info.IsDir() {
		return store.RecentSticker{}, fmt.Errorf("sticker path is a directory")
	}
	fileName := strings.TrimSpace(sticker.FileName)
	if fileName == "" {
		fileName = info.Name()
	}
	mimeType := strings.TrimSpace(sticker.MIMEType)
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName)))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if sticker.IsLottie || strings.EqualFold(filepath.Ext(fileName), ".tgs") {
		return store.RecentSticker{}, fmt.Errorf("lottie sticker send is not supported yet")
	}
	if !isSupportedOutgoingSticker(mimeType, fileName) {
		return store.RecentSticker{}, fmt.Errorf("unsupported sticker MIME type %q", mimeType)
	}
	sticker.ID = localStickerID(sticker)
	sticker.LocalPath = localPath
	sticker.FileName = fileName
	sticker.MIMEType = mimeType
	sticker.FileLength = info.Size()
	sticker.UpdatedAt = time.Now()
	return sticker, nil
}

func isSupportedOutgoingSticker(mimeType, fileName string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	fileName = strings.ToLower(strings.TrimSpace(fileName))
	return mimeType == "image/webp" || strings.HasSuffix(fileName, ".webp")
}

func handleReadReceiptRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request readReceiptRequest,
) {
	if request.Result == nil {
		return
	}
	if db == nil {
		sendReadReceiptResult(ctx, request, fmt.Errorf("store is required"))
		return
	}
	if live == nil {
		sendReadReceiptResult(ctx, request, fmt.Errorf("whatsapp live session unavailable"))
		return
	}
	if !online {
		sendReadReceiptResult(ctx, request, fmt.Errorf("read receipts need WhatsApp online"))
		return
	}
	targets := readReceiptTargets(request.Chat, request.Messages)
	if len(targets) == 0 {
		sendReadReceiptResult(ctx, request, fmt.Errorf("no loaded unread messages to mark read"))
		return
	}
	sendReadReceiptResult(ctx, request, nil)
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeReadReceipt(ctx, db, live, updates, request.Chat.ID, targets)
		}()
		return
	}
	completeReadReceipt(ctx, db, live, updates, request.Chat.ID, targets)
}

func sendReadReceiptResult(ctx context.Context, request readReceiptRequest, err error) {
	if request.Result == nil {
		return
	}
	select {
	case request.Result <- err:
	case <-ctx.Done():
	}
}

func completeReadReceipt(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, chatID string, targets []whatsapp.ReadReceiptTarget) {
	readCtx, cancel := context.WithTimeout(ctx, readReceiptTimeout)
	defer cancel()
	if err := live.MarkRead(readCtx, targets); err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			ReadChatID: chatID,
			Status:     fmt.Sprintf("mark read failed: %s", shortStatusError(err)),
		})
		return
	}
	if err := db.ClearChatUnread(context.Background(), chatID); err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			ReadChatID: chatID,
			Refresh:    true,
			Status:     fmt.Sprintf("clear unread failed: %s", shortStatusError(err)),
		})
		return
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		ReadChatID: chatID,
		Refresh:    true,
		Status:     "marked chat read",
	})
}

func readReceiptTargets(chat store.Chat, messages []store.Message) []whatsapp.ReadReceiptTarget {
	chatJID := strings.TrimSpace(chat.JID)
	if chatJID == "" {
		chatJID = strings.TrimSpace(chat.ID)
	}
	targets := make([]whatsapp.ReadReceiptTarget, 0, len(messages))
	for _, message := range messages {
		if message.IsOutgoing || strings.TrimSpace(message.RemoteID) == "" {
			continue
		}
		messageChatJID := strings.TrimSpace(message.ChatJID)
		if messageChatJID == "" {
			messageChatJID = chatJID
		}
		targets = append(targets, whatsapp.ReadReceiptTarget{
			ChatJID:   messageChatJID,
			RemoteID:  message.RemoteID,
			SenderJID: message.SenderJID,
			Timestamp: message.Timestamp,
		})
	}
	return targets
}

func handleReactionRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request reactionRequest,
) {
	if request.Result == nil {
		return
	}
	if db == nil {
		sendReactionResult(ctx, request, fmt.Errorf("store is required"))
		return
	}
	if live == nil {
		sendReactionResult(ctx, request, fmt.Errorf("whatsapp live session unavailable"))
		return
	}
	if !online {
		sendReactionResult(ctx, request, fmt.Errorf("reactions need WhatsApp online"))
		return
	}
	if strings.TrimSpace(request.Message.RemoteID) == "" {
		sendReactionResult(ctx, request, fmt.Errorf("reaction target has no WhatsApp id"))
		return
	}
	chatJID := strings.TrimSpace(request.Message.ChatJID)
	if chatJID == "" {
		chatJID = strings.TrimSpace(request.Message.ChatID)
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, chatJID)
	if err != nil {
		sendReactionResult(ctx, request, err)
		return
	}
	remoteID := strings.TrimSpace(live.GenerateMessageID())
	if remoteID == "" {
		sendReactionResult(ctx, request, fmt.Errorf("generate message id failed"))
		return
	}
	sendReactionResult(ctx, request, nil)
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeReaction(ctx, db, live, updates, request.Message, chatJID, request.Emoji, remoteID)
		}()
		return
	}
	completeReaction(ctx, db, live, updates, request.Message, chatJID, request.Emoji, remoteID)
}

func sendReactionResult(ctx context.Context, request reactionRequest, err error) {
	if request.Result == nil {
		return
	}
	select {
	case request.Result <- err:
	case <-ctx.Done():
	}
}

func completeReaction(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, message store.Message, chatJID, emoji, remoteID string) {
	sendCtx, cancel := context.WithTimeout(ctx, reactionSendTimeout)
	defer cancel()
	_, err := live.SendReaction(sendCtx, whatsapp.ReactionSendRequest{
		ChatJID:         chatJID,
		TargetRemoteID:  message.RemoteID,
		TargetSenderJID: message.SenderJID,
		Emoji:           emoji,
		RemoteID:        remoteID,
	})
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("reaction failed: %s", shortStatusError(err)),
		})
		return
	}
	if err := db.UpsertReaction(context.Background(), store.Reaction{
		MessageID:  message.ID,
		SenderJID:  "me",
		Emoji:      emoji,
		Timestamp:  time.Now(),
		IsOutgoing: true,
		UpdatedAt:  time.Now(),
	}); err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("reaction store failed: %s", shortStatusError(err)),
		})
		return
	}
	status := "sent reaction"
	if strings.TrimSpace(emoji) == "" {
		status = "cleared reaction"
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  status,
	})
}

func handleDeleteEveryoneRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request deleteEveryoneRequest,
) {
	if request.Result == nil {
		return
	}
	if db == nil {
		sendDeleteEveryoneResult(ctx, request, "", fmt.Errorf("store is required"))
		return
	}
	if live == nil {
		sendDeleteEveryoneResult(ctx, request, request.Message.ID, fmt.Errorf("whatsapp live session unavailable"))
		return
	}
	if !online {
		sendDeleteEveryoneResult(ctx, request, request.Message.ID, fmt.Errorf("delete for everybody needs WhatsApp online"))
		return
	}
	if !request.Message.IsOutgoing {
		sendDeleteEveryoneResult(ctx, request, request.Message.ID, fmt.Errorf("only your outgoing messages can be deleted for everybody"))
		return
	}
	if strings.TrimSpace(request.Message.RemoteID) == "" {
		sendDeleteEveryoneResult(ctx, request, request.Message.ID, fmt.Errorf("delete target has no WhatsApp id"))
		return
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, retryChatID(request.Message))
	if err != nil {
		sendDeleteEveryoneResult(ctx, request, request.Message.ID, err)
		return
	}
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeDeleteForEveryone(ctx, db, live, updates, request, chatJID)
		}()
		return
	}
	completeDeleteForEveryone(ctx, db, live, updates, request, chatJID)
}

func sendDeleteEveryoneResult(ctx context.Context, request deleteEveryoneRequest, messageID string, err error) {
	if request.Result == nil {
		return
	}
	if messageID == "" {
		messageID = request.Message.ID
	}
	select {
	case request.Result <- deleteEveryoneResult{MessageID: messageID, Err: err}:
	case <-ctx.Done():
	}
}

func completeDeleteForEveryone(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, request deleteEveryoneRequest, chatJID string) {
	sendCtx, cancel := context.WithTimeout(ctx, deleteEveryoneTimeout)
	defer cancel()
	result, err := live.DeleteMessageForEveryone(sendCtx, whatsapp.DeleteForEveryoneRequest{
		ChatJID:        chatJID,
		TargetRemoteID: request.Message.RemoteID,
	})
	if err != nil {
		sendDeleteEveryoneResult(ctx, request, request.Message.ID, err)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Status: fmt.Sprintf("delete for everybody failed: %s", shortStatusError(err)),
		})
		return
	}
	messageID := strings.TrimSpace(request.Message.ID)
	if messageID == "" {
		messageID = strings.TrimSpace(result.MessageID)
	}
	if _, err := db.DeleteMessageForEveryone(context.Background(), messageID); err != nil {
		sendDeleteEveryoneResult(ctx, request, messageID, err)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("delete local state failed: %s", shortStatusError(err)),
		})
		return
	}
	sendDeleteEveryoneResult(ctx, request, messageID, nil)
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  "deleted message for everybody",
	})
}

func handleEditMessageRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request editMessageRequest,
) {
	if request.Result == nil {
		return
	}
	if db == nil {
		sendEditMessageResult(ctx, request, "", time.Time{}, fmt.Errorf("store is required"))
		return
	}
	if live == nil {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, fmt.Errorf("whatsapp live session unavailable"))
		return
	}
	if !online {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, fmt.Errorf("edit needs WhatsApp online"))
		return
	}
	if !request.Message.IsOutgoing {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, fmt.Errorf("only your outgoing text messages can be edited"))
		return
	}
	if strings.TrimSpace(request.Message.RemoteID) == "" {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, fmt.Errorf("edit target has no WhatsApp id"))
		return
	}
	if strings.TrimSpace(request.Body) == "" {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, fmt.Errorf("edit body is required"))
		return
	}
	if len(request.Message.Media) > 0 {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, fmt.Errorf("media captions are not editable yet"))
		return
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, retryChatID(request.Message))
	if err != nil {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, err)
		return
	}
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeEditMessage(ctx, db, live, updates, request, chatJID)
		}()
		return
	}
	completeEditMessage(ctx, db, live, updates, request, chatJID)
}

func sendEditMessageResult(ctx context.Context, request editMessageRequest, messageID string, editedAt time.Time, err error) {
	if request.Result == nil {
		return
	}
	if messageID == "" {
		messageID = request.Message.ID
	}
	select {
	case request.Result <- editMessageResult{MessageID: messageID, Body: strings.TrimSpace(request.Body), EditedAt: editedAt, Err: err}:
	case <-ctx.Done():
	}
}

func completeEditMessage(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, request editMessageRequest, chatJID string) {
	sendCtx, cancel := context.WithTimeout(ctx, editMessageTimeout)
	defer cancel()
	body := strings.TrimSpace(request.Body)
	result, err := live.EditMessage(sendCtx, whatsapp.EditMessageRequest{
		ChatJID:        chatJID,
		TargetRemoteID: request.Message.RemoteID,
		Body:           body,
	})
	if err != nil {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, err)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Status: fmt.Sprintf("edit failed: %s", shortStatusError(err)),
		})
		return
	}
	messageID := strings.TrimSpace(request.Message.ID)
	if messageID == "" {
		messageID = strings.TrimSpace(result.MessageID)
	}
	editedAt := result.Timestamp
	if editedAt.IsZero() {
		editedAt = time.Now()
	}
	updated, err := db.UpdateMessageBody(context.Background(), messageID, body, editedAt)
	if err != nil {
		sendEditMessageResult(ctx, request, messageID, editedAt, err)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("edit local state failed: %s", shortStatusError(err)),
		})
		return
	}
	if !updated {
		err := fmt.Errorf("message %s does not exist", messageID)
		sendEditMessageResult(ctx, request, messageID, editedAt, err)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("edit local state failed: %s", shortStatusError(err)),
		})
		return
	}
	sendEditMessageResult(ctx, request, messageID, editedAt, nil)
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  "edited message",
	})
}

func handleForwardMessagesRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request forwardMessagesRequest,
) {
	if request.Result == nil {
		return
	}
	if request.Context != nil {
		select {
		case <-request.Context.Done():
			return
		default:
		}
	}
	if db == nil {
		sendForwardMessagesResult(ctx, request, forwardMessagesResult{Err: fmt.Errorf("store is required")})
		return
	}
	if live == nil {
		sendForwardMessagesResult(ctx, request, forwardMessagesResult{Err: fmt.Errorf("whatsapp live session unavailable")})
		return
	}
	if !online {
		sendForwardMessagesResult(ctx, request, forwardMessagesResult{Err: fmt.Errorf("forwarding needs WhatsApp online")})
		return
	}
	if len(request.Messages) == 0 {
		sendForwardMessagesResult(ctx, request, forwardMessagesResult{Err: fmt.Errorf("no messages selected")})
		return
	}
	recipients := uniqueForwardRecipients(request.Recipients)
	if len(recipients) == 0 {
		sendForwardMessagesResult(ctx, request, forwardMessagesResult{Err: fmt.Errorf("no forward recipients selected")})
		return
	}

	var result forwardMessagesResult
	for _, source := range request.Messages {
		if err := forwardRequestContextErr(request); err != nil {
			result.Err = err
			sendForwardMessagesResult(ctx, request, result)
			return
		}
		payload, ok, err := db.MessagePayload(ctx, source.ID)
		if err != nil {
			result.Failed++
			continue
		}
		if !ok || len(payload.Payload) == 0 {
			result.Skipped++
			continue
		}
		if _, err := whatsapp.ForwardedMessageFromPayload(payload.Payload); err != nil {
			result.Skipped++
			continue
		}
		markForwarded := !source.IsOutgoing
		for _, recipient := range recipients {
			if err := forwardRequestContextErr(request); err != nil {
				result.Err = err
				sendForwardMessagesResult(ctx, request, result)
				return
			}
			message, err := queueForwardedMessage(ctx, db, live, source, payload.Payload, recipient)
			if err != nil {
				result.Failed++
				continue
			}
			result.Sent++
			if wg != nil {
				wg.Add(1)
				go func(message store.Message, payload []byte, markForwarded bool) {
					defer wg.Done()
					completeQueuedForwardSend(ctx, db, live, updates, message, payload, markForwarded)
				}(message, cloneBytes(payload.Payload), markForwarded)
				continue
			}
			completeQueuedForwardSend(ctx, db, live, updates, message, payload.Payload, markForwarded)
		}
	}
	if result.Sent == 0 && result.Failed > 0 {
		result.Err = fmt.Errorf("forward failed for all available targets")
	}
	sendForwardMessagesResult(ctx, request, result)
}

func forwardRequestContextErr(request forwardMessagesRequest) error {
	if request.Context == nil {
		return nil
	}
	select {
	case <-request.Context.Done():
		return request.Context.Err()
	default:
		return nil
	}
}

func queueForwardedMessage(ctx context.Context, db *store.Store, live WhatsAppLiveSession, source store.Message, payload []byte, recipient store.Chat) (store.Message, error) {
	if len(payload) == 0 {
		return store.Message{}, fmt.Errorf("forward payload is required")
	}
	chatID := strings.TrimSpace(recipient.JID)
	if chatID == "" {
		chatID = strings.TrimSpace(recipient.ID)
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, chatID)
	if err != nil {
		return store.Message{}, err
	}
	remoteID := strings.TrimSpace(live.GenerateMessageID())
	if remoteID == "" {
		return store.Message{}, fmt.Errorf("generate message id failed")
	}
	message := forwardedLocalMessage(source, chatJID, remoteID, time.Now())
	if message.ID == "" {
		return store.Message{}, fmt.Errorf("message id is required")
	}
	if err := db.AddMessageWithMedia(ctx, message, message.Media); err != nil {
		return store.Message{}, err
	}
	if err := db.UpsertMessagePayload(ctx, store.MessagePayload{
		MessageID: message.ID,
		Payload:   payload,
		UpdatedAt: time.Now(),
	}); err != nil {
		return store.Message{}, err
	}
	return message, nil
}

func forwardedLocalMessage(source store.Message, chatJID, remoteID string, now time.Time) store.Message {
	messageID := whatsapp.LocalMessageID(chatJID, remoteID)
	return store.Message{
		ID:         messageID,
		RemoteID:   remoteID,
		ChatID:     chatJID,
		ChatJID:    chatJID,
		Sender:     "me",
		SenderJID:  "me",
		Body:       source.Body,
		Timestamp:  now,
		IsOutgoing: true,
		Status:     "sending",
		Media:      cloneForwardedMedia(messageID, source.Media, now),
	}
}

func cloneForwardedMedia(messageID string, mediaItems []store.MediaMetadata, updatedAt time.Time) []store.MediaMetadata {
	if len(mediaItems) == 0 {
		return nil
	}
	out := make([]store.MediaMetadata, 0, len(mediaItems))
	for _, item := range mediaItems {
		item.MessageID = messageID
		item.UpdatedAt = updatedAt
		out = append(out, item)
	}
	return out
}

func sendForwardMessagesResult(ctx context.Context, request forwardMessagesRequest, result forwardMessagesResult) {
	if request.Result == nil {
		return
	}
	select {
	case request.Result <- result:
	case <-ctx.Done():
	}
}

func completeQueuedForwardSend(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, message store.Message, payload []byte, markForwarded bool) {
	sendCtx, cancel := context.WithTimeout(ctx, forwardMessageTimeout)
	defer cancel()
	result, err := live.ForwardMessage(sendCtx, whatsapp.ForwardMessageRequest{
		ChatJID:       message.ChatJID,
		Payload:       payload,
		RemoteID:      message.RemoteID,
		MarkForwarded: markForwarded,
	})
	if err != nil {
		_ = db.UpdateMessageStatus(context.Background(), message.ID, "failed")
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("forward failed: %s", shortStatusError(err)),
		})
		return
	}
	status := strings.TrimSpace(result.Status)
	if status == "" {
		status = "sent"
	}
	if err := db.UpdateMessageStatus(context.Background(), message.ID, status); err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("forward status failed: %s", shortStatusError(err)),
		})
		return
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  "forwarded message",
	})
}

func handlePresenceRequest(ctx context.Context, live WhatsAppLiveSession, online bool, request presenceRequest) {
	if live == nil || !online || strings.TrimSpace(request.ChatID) == "" {
		return
	}
	chatID, err := canonicalizeLiveChatID(ctx, live, request.ChatID)
	if err != nil {
		return
	}
	presenceCtx, cancel := context.WithTimeout(ctx, presenceSendTimeout)
	defer cancel()
	_ = live.SendChatPresence(presenceCtx, chatID, request.Composing)
}

func handlePresenceSubscribeRequest(ctx context.Context, live WhatsAppLiveSession, online bool, request presenceSubscribeRequest) {
	if live == nil || !online || strings.TrimSpace(request.ChatID) == "" {
		return
	}
	chatID, err := canonicalizeLiveChatID(ctx, live, request.ChatID)
	if err != nil {
		return
	}
	presenceCtx, cancel := context.WithTimeout(ctx, presenceSendTimeout)
	defer cancel()
	_ = live.SubscribePresence(presenceCtx, chatID)
}

func enqueueMediaDownload(ctx context.Context, jobs chan<- mediaDownloadRequest, online bool, request mediaDownloadRequest) {
	if request.Result == nil {
		return
	}
	if !online {
		sendMediaDownloadResult(ctx, request, request.Media, fmt.Errorf("media download needs WhatsApp online"))
		return
	}
	select {
	case jobs <- request:
	case <-ctx.Done():
		sendMediaDownloadResult(context.Background(), request, request.Media, ctx.Err())
	default:
		sendMediaDownloadResult(ctx, request, request.Media, fmt.Errorf("media download queue is full"))
	}
}

func mediaDownloadWorker(ctx context.Context, db *store.Store, live WhatsAppLiveSession, paths config.Paths, jobs <-chan mediaDownloadRequest) {
	for {
		select {
		case request, ok := <-jobs:
			if !ok {
				return
			}
			media, err := downloadRemoteMedia(ctx, db, live, paths, request)
			sendMediaDownloadResult(ctx, request, media, err)
		case <-ctx.Done():
			return
		}
	}
}

func sendMediaDownloadResult(ctx context.Context, request mediaDownloadRequest, media store.MediaMetadata, err error) {
	if request.Result == nil {
		return
	}
	select {
	case request.Result <- mediaDownloadResult{Media: media, Err: err}:
	case <-ctx.Done():
	}
}

func startStickerSync(ctx context.Context, db *store.Store, live WhatsAppLiveSession, paths config.Paths, updates chan<- ui.LiveUpdate, wg *sync.WaitGroup, online bool) {
	result := make(chan stickerSyncResult, 1)
	handleStickerSyncRequest(ctx, db, live, paths, updates, wg, online, stickerSyncRequest{
		Context: ctx,
		Result:  result,
	})
}

func handleStickerSyncRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	paths config.Paths,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request stickerSyncRequest,
) {
	if request.Result == nil {
		return
	}
	if request.Context != nil {
		select {
		case <-request.Context.Done():
			sendStickerSyncResult(ctx, request, 0, request.Context.Err())
			return
		default:
		}
	}
	if db == nil {
		sendStickerSyncResult(ctx, request, 0, fmt.Errorf("store is required"))
		return
	}
	if live == nil {
		sendStickerSyncResult(ctx, request, 0, fmt.Errorf("whatsapp live session unavailable"))
		return
	}
	if !online {
		sendStickerSyncResult(ctx, request, 0, fmt.Errorf("sticker sync needs WhatsApp online"))
		return
	}

	sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "syncing WhatsApp stickers"})
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeStickerSyncRequest(ctx, db, live, paths, updates, request)
		}()
		return
	}
	completeStickerSyncRequest(ctx, db, live, paths, updates, request)
}

func completeStickerSyncRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	paths config.Paths,
	updates chan<- ui.LiveUpdate,
	request stickerSyncRequest,
) {
	syncCtx, cancel := stickerSyncContext(ctx, request.Context)
	defer cancel()

	events, syncErr := live.SyncRecentStickers(syncCtx)
	ingestor := whatsapp.Ingestor{Store: db}
	cached := 0
	metadataSynced := 0
	cacheFailures := 0
	var cacheErr error
	var applyErr error
	for _, event := range events {
		cacheUsable := false
		switch event.Kind {
		case whatsapp.EventRecentSticker:
			prepared, err := prepareRecentStickerEvent(syncCtx, db, live, paths, event.Sticker)
			if err != nil {
				cacheFailures++
				if cacheErr == nil {
					cacheErr = err
				}
			} else {
				event.Sticker = prepared
				cacheUsable = stickerPickerUsable(store.RecentSticker{
					ID:        prepared.ID,
					MIMEType:  prepared.MIMEType,
					FileName:  prepared.FileName,
					LocalPath: prepared.LocalPath,
					IsLottie:  prepared.IsLottie,
				})
			}
		case whatsapp.EventRecentStickerRemove:
		default:
			continue
		}
		if _, err := ingestor.Apply(syncCtx, event); err != nil {
			applyErr = errors.Join(applyErr, fmt.Errorf("apply sticker event: %w", err))
			continue
		}
		if event.Kind == whatsapp.EventRecentSticker {
			metadataSynced++
			if cacheUsable {
				cached++
			}
		}
	}

	err := stickerSyncResultError(cached, metadataSynced, cacheFailures, cacheErr, syncErr, applyErr)
	sendStickerSyncResult(ctx, request, cached, err)
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: metadataSynced > 0,
			Status:  fmt.Sprintf("sticker sync failed: %s", shortStatusError(err)),
		})
		return
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: metadataSynced > 0,
		Status:  stickerSyncStatus(cached, metadataSynced, cacheFailures, cacheErr, errors.Join(syncErr, applyErr)),
	})
}

func stickerSyncResultError(cached, metadataSynced, cacheFailures int, cacheErr, syncErr, applyErr error) error {
	if cached > 0 {
		return nil
	}
	if metadataSynced > 0 && cacheFailures > 0 {
		if cacheErr != nil {
			return fmt.Errorf("no sticker files cached; %d metadata record(s) synced, %d download(s) failed: %w", metadataSynced, cacheFailures, cacheErr)
		}
		return fmt.Errorf("no sticker files cached; %d metadata record(s) synced, %d download(s) failed", metadataSynced, cacheFailures)
	}
	return errors.Join(syncErr, applyErr)
}

func stickerSyncStatus(cached, metadataSynced, cacheFailures int, cacheErr, warning error) string {
	if cached > 0 {
		if cacheFailures > 0 {
			if cacheErr != nil {
				return fmt.Sprintf("synced %d WhatsApp sticker(s); %d unavailable: %s", cached, cacheFailures, shortStatusError(cacheErr))
			}
			return fmt.Sprintf("synced %d WhatsApp sticker(s); %d unavailable", cached, cacheFailures)
		}
		if warning != nil {
			return fmt.Sprintf("synced %d WhatsApp sticker(s); sync warnings", cached)
		}
		return fmt.Sprintf("synced %d WhatsApp sticker(s)", cached)
	}
	if metadataSynced > 0 {
		return fmt.Sprintf("synced %d sticker metadata record(s); no renderable stickers cached", metadataSynced)
	}
	return "no WhatsApp sticker favorites found"
}

func stickerSyncContext(parent, request context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, stickerSyncTimeout)
	if request == nil {
		return ctx, cancel
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-request.Done():
			cancel()
		case <-ctx.Done():
		case <-done:
		}
	}()
	return ctx, func() {
		close(done)
		cancel()
	}
}

func sendStickerSyncResult(ctx context.Context, request stickerSyncRequest, stickers int, err error) {
	if request.Result == nil {
		return
	}
	select {
	case request.Result <- stickerSyncResult{Stickers: stickers, Err: err}:
	case <-ctx.Done():
	}
}

func enqueueAvatarRefresh(ctx context.Context, jobs chan<- avatarRefreshRequest, inflight map[string]bool, chatID string) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" || inflight[chatID] {
		return
	}
	inflight[chatID] = true
	select {
	case jobs <- avatarRefreshRequest{ChatID: chatID}:
	case <-ctx.Done():
		delete(inflight, chatID)
	default:
		delete(inflight, chatID)
	}
}

func handleAvatarEvent(
	ctx context.Context,
	db *store.Store,
	paths config.Paths,
	jobs chan<- avatarRefreshRequest,
	inflight map[string]bool,
	updates chan<- ui.LiveUpdate,
	event whatsapp.AvatarEvent,
) {
	chatID := strings.TrimSpace(event.ChatID)
	if chatID == "" {
		chatID = strings.TrimSpace(event.ChatJID)
	}
	if chatID == "" {
		return
	}
	if event.Remove {
		changed, err := clearStoredChatAvatar(ctx, db, paths, chatID, event.UpdatedAt)
		if err != nil {
			sendLiveUpdate(ctx, updates, ui.LiveUpdate{
				Status: fmt.Sprintf("avatar cleanup failed: %s", shortStatusError(err)),
			})
			return
		}
		if changed {
			sendLiveUpdate(ctx, updates, ui.LiveUpdate{
				Refresh: true,
				Status:  "chat avatar removed",
			})
		}
		return
	}
	enqueueAvatarRefresh(ctx, jobs, inflight, chatID)
}

func avatarRefreshWorker(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	paths config.Paths,
	jobs <-chan avatarRefreshRequest,
	results chan<- avatarRefreshResult,
) {
	for {
		select {
		case request, ok := <-jobs:
			if !ok {
				return
			}
			result := refreshChatAvatar(ctx, db, live, paths, request.ChatID)
			select {
			case results <- result:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func refreshChatAvatar(ctx context.Context, db *store.Store, live WhatsAppLiveSession, paths config.Paths, chatID string) avatarRefreshResult {
	if db == nil {
		return avatarRefreshResult{ChatID: chatID, Err: fmt.Errorf("store is required")}
	}
	if live == nil {
		return avatarRefreshResult{ChatID: chatID, Err: fmt.Errorf("whatsapp live session unavailable")}
	}
	chat, ok, err := db.ChatByID(ctx, chatID)
	if err != nil {
		return avatarRefreshResult{ChatID: chatID, Err: err}
	}
	if !ok || strings.TrimSpace(chat.JID) == "" {
		return avatarRefreshResult{ChatID: chatID}
	}

	refreshCtx, cancel := context.WithTimeout(ctx, avatarRefreshTimeout)
	defer cancel()
	avatar, err := live.GetChatAvatar(refreshCtx, chat.JID, chat.AvatarID)
	if err != nil {
		return avatarRefreshResult{ChatID: chatID, Err: err}
	}
	if avatar.Cleared {
		changed, err := clearStoredChatAvatar(ctx, db, paths, chatID, avatar.UpdatedAt)
		if err != nil {
			return avatarRefreshResult{ChatID: chatID, Err: err}
		}
		if !changed {
			return avatarRefreshResult{ChatID: chatID}
		}
		return avatarRefreshResult{
			ChatID:  chatID,
			Refresh: true,
			Status:  "chat avatar removed",
		}
	}
	if !avatar.Changed || strings.TrimSpace(avatar.URL) == "" {
		return avatarRefreshResult{ChatID: chatID}
	}

	localPath, err := downloadChatAvatar(refreshCtx, paths.AvatarCacheDir, chatID, avatar)
	if err != nil {
		return avatarRefreshResult{ChatID: chatID, Err: err}
	}
	if err := db.SetChatAvatar(ctx, chatID, avatar.AvatarID, localPath, localPath, avatar.UpdatedAt); err != nil {
		return avatarRefreshResult{ChatID: chatID, Err: err}
	}
	if oldPath := strings.TrimSpace(chat.AvatarPath); oldPath != "" && oldPath != localPath && paths.IsManagedCachePath(oldPath) {
		_ = os.Remove(oldPath)
	}
	if oldThumb := strings.TrimSpace(chat.AvatarThumbPath); oldThumb != "" && oldThumb != localPath && oldThumb != chat.AvatarPath && paths.IsManagedCachePath(oldThumb) {
		_ = os.Remove(oldThumb)
	}
	return avatarRefreshResult{
		ChatID:  chatID,
		Refresh: true,
		Status:  "chat avatar updated",
	}
}

func clearStoredChatAvatar(ctx context.Context, db *store.Store, paths config.Paths, chatID string, updatedAt time.Time) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("store is required")
	}
	chat, ok, err := db.ChatByID(ctx, chatID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if err := db.SetChatAvatar(ctx, chatID, "", "", "", updatedAt); err != nil {
		return false, err
	}
	for _, candidate := range []string{chat.AvatarPath, chat.AvatarThumbPath} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || !paths.IsManagedCachePath(candidate) {
			continue
		}
		_ = os.Remove(candidate)
	}
	return chat.AvatarID != "" || chat.AvatarPath != "" || chat.AvatarThumbPath != "", nil
}

func downloadChatAvatar(ctx context.Context, cacheDir, chatID string, avatar whatsapp.ChatAvatarResult) (string, error) {
	if strings.TrimSpace(cacheDir) == "" {
		return "", fmt.Errorf("avatar cache dir is required")
	}
	if strings.TrimSpace(avatar.URL) == "" {
		return "", fmt.Errorf("avatar url is required")
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", fmt.Errorf("create avatar cache dir: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, avatar.URL, nil)
	if err != nil {
		return "", fmt.Errorf("create avatar request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("download avatar: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("download avatar: unexpected status %s", response.Status)
	}

	ext := avatarFileExtension(response.Header.Get("Content-Type"), avatar.URL)
	finalPath := avatarCachePath(cacheDir, chatID, avatar.AvatarID, ext)
	tmp, err := os.CreateTemp(cacheDir, "avatar-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create avatar temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	written, err := io.Copy(tmp, response.Body)
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("write avatar file: %w", err)
	}
	if written <= 0 {
		return "", fmt.Errorf("avatar download was empty")
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", fmt.Errorf("store avatar file: %w", err)
	}
	return finalPath, nil
}

func avatarCachePath(cacheDir, chatID, avatarID, ext string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{chatID, avatarID}, "\x00")))
	return filepath.Join(cacheDir, "avatar-"+hex.EncodeToString(sum[:])[:20]+ext)
}

func avatarFileExtension(contentType, rawURL string) string {
	contentType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	if exts, _ := mime.ExtensionsByType(contentType); len(exts) > 0 && validMediaExtension(exts[0]) {
		return exts[0]
	}
	if parsed, err := neturl.Parse(strings.TrimSpace(rawURL)); err == nil {
		if ext := strings.ToLower(filepath.Ext(parsed.Path)); validMediaExtension(ext) {
			return ext
		}
	}
	return ".jpg"
}

func prepareIncomingMediaEvent(paths config.Paths, event whatsapp.MediaEvent) (whatsapp.MediaEvent, error) {
	if !strings.EqualFold(strings.TrimSpace(event.Kind), "sticker") || len(event.ThumbnailData) == 0 || strings.TrimSpace(event.ThumbnailPath) != "" {
		event.ThumbnailData = nil
		return event, nil
	}
	thumbnailPath, err := storeStickerThumbnail(paths.MediaDir, event)
	if err != nil {
		return event, err
	}
	event.ThumbnailPath = thumbnailPath
	event.ThumbnailData = nil
	return event, nil
}

func prepareRecentStickerEvent(ctx context.Context, db *store.Store, live WhatsAppLiveSession, paths config.Paths, event whatsapp.RecentStickerEvent) (whatsapp.RecentStickerEvent, error) {
	if strings.TrimSpace(event.ID) == "" {
		return event, fmt.Errorf("recent sticker id is required")
	}
	if strings.TrimSpace(event.FileName) == "" {
		event.FileName = "sticker" + recentStickerExtension(event.MIMEType, event.FileName, event.IsLottie)
	}
	if event.IsLottie || strings.EqualFold(filepath.Ext(event.FileName), ".tgs") {
		return event, nil
	}
	dir := filepath.Join(paths.TransientDir, "stickers", "files")
	if strings.TrimSpace(paths.TransientDir) == "" {
		dir = filepath.Join(os.TempDir(), "vimwhat-stickers")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return event, fmt.Errorf("create sticker cache dir: %w", err)
	}

	finalPath := recentStickerCachePath(dir, event)
	if mediaPathAvailable(finalPath) {
		event.LocalPath = finalPath
		return event, nil
	}
	if db != nil {
		if current, ok, err := db.RecentSticker(ctx, event.ID); err != nil {
			return event, err
		} else if ok && mediaPathAvailable(current.LocalPath) {
			event.LocalPath = current.LocalPath
			return event, nil
		}
	}
	if live == nil || (strings.TrimSpace(event.DirectPath) == "" && strings.TrimSpace(event.URL) == "") || len(event.MediaKey) == 0 || len(event.FileEncSHA256) == 0 {
		return event, nil
	}

	tmp, err := os.CreateTemp(dir, "sticker-download-*.tmp")
	if err != nil {
		return event, fmt.Errorf("create sticker temp file: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	descriptor := whatsapp.MediaDownloadDescriptor{
		Kind:          "sticker",
		URL:           event.URL,
		DirectPath:    event.DirectPath,
		MediaKey:      cloneBytes(event.MediaKey),
		FileSHA256:    cloneBytes(event.FileSHA256),
		FileEncSHA256: cloneBytes(event.FileEncSHA256),
		FileLength:    event.FileLength,
	}
	if err := live.DownloadMedia(ctx, descriptor, tmpPath); err != nil {
		return event, err
	}
	info, err := os.Stat(tmpPath)
	if err != nil {
		return event, fmt.Errorf("stat downloaded sticker: %w", err)
	}
	if info.Size() <= 0 {
		return event, fmt.Errorf("downloaded sticker is empty")
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return event, fmt.Errorf("store downloaded sticker: %w", err)
	}
	event.LocalPath = finalPath
	event.FileLength = info.Size()
	return event, nil
}

func storeStickerThumbnail(mediaDir string, event whatsapp.MediaEvent) (string, error) {
	if strings.TrimSpace(mediaDir) == "" {
		return "", fmt.Errorf("media dir is required")
	}
	if len(event.ThumbnailData) == 0 {
		return "", fmt.Errorf("sticker thumbnail is empty")
	}
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		return "", fmt.Errorf("create media dir: %w", err)
	}
	sum := sha256.Sum256(append([]byte(event.MessageID), event.ThumbnailData...))
	finalPath := filepath.Join(mediaDir, "sticker-thumb-"+hex.EncodeToString(sum[:])[:20]+".png")
	if mediaPathAvailable(finalPath) {
		return finalPath, nil
	}
	tmp, err := os.CreateTemp(mediaDir, "sticker-thumb-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create sticker thumbnail temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(event.ThumbnailData); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write sticker thumbnail: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close sticker thumbnail: %w", err)
	}
	defer os.Remove(tmpPath)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", fmt.Errorf("store sticker thumbnail: %w", err)
	}
	return finalPath, nil
}

func downloadRemoteMedia(ctx context.Context, db *store.Store, live WhatsAppLiveSession, paths config.Paths, request mediaDownloadRequest) (store.MediaMetadata, error) {
	if db == nil {
		return request.Media, fmt.Errorf("store is required")
	}
	if live == nil {
		return request.Media, fmt.Errorf("whatsapp live session unavailable")
	}

	messageID := strings.TrimSpace(request.Media.MessageID)
	if messageID == "" {
		messageID = strings.TrimSpace(request.Message.ID)
	}
	if messageID == "" {
		return request.Media, fmt.Errorf("message id is required")
	}
	mediaItem := request.Media
	mediaItem.MessageID = messageID

	current, err := db.MediaMetadata(ctx, messageID)
	if err != nil {
		return mediaItem, err
	}
	mediaItem = mergeAppMediaMetadata(current, mediaItem)
	mediaItem, _, err = repairManagedMediaMetadata(ctx, db, paths, mediaItem)
	if err != nil {
		return mediaItem, err
	}
	if mediaPathAvailable(mediaItem.LocalPath) {
		mediaItem.DownloadState = "downloaded"
		mediaItem.UpdatedAt = time.Now()
		if err := db.UpsertMediaMetadata(ctx, mediaItem); err != nil {
			return mediaItem, err
		}
		return mediaItem, nil
	}

	descriptor, ok, err := db.MediaDownloadDescriptor(ctx, messageID)
	if err != nil {
		return mediaItem, err
	}
	if !ok {
		return mediaItem, fmt.Errorf("media download details unavailable; receive or refetch this message with the current build")
	}

	mediaItem.DownloadState = "downloading"
	mediaItem.UpdatedAt = time.Now()
	if err := db.UpsertMediaMetadata(ctx, mediaItem); err != nil {
		return mediaItem, err
	}

	if err := os.MkdirAll(paths.MediaDir, 0o700); err != nil {
		return failMediaDownload(ctx, db, mediaItem, fmt.Errorf("create media cache dir: %w", err))
	}
	finalPath := mediaCachePath(paths.MediaDir, messageID, mediaItem)
	if mediaPathAvailable(finalPath) {
		mediaItem.LocalPath = finalPath
		mediaItem.DownloadState = "downloaded"
		mediaItem.UpdatedAt = time.Now()
		if info, err := os.Stat(finalPath); err == nil {
			mediaItem.SizeBytes = info.Size()
		}
		if err := db.UpsertMediaMetadata(ctx, mediaItem); err != nil {
			return mediaItem, err
		}
		return mediaItem, nil
	}

	tmp, err := os.CreateTemp(paths.MediaDir, "download-*.tmp")
	if err != nil {
		return failMediaDownload(ctx, db, mediaItem, fmt.Errorf("create media temp file: %w", err))
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	if err := live.DownloadMedia(ctx, whatsappDescriptorFromStore(descriptor), tmpPath); err != nil {
		return failMediaDownload(ctx, db, mediaItem, err)
	}
	info, err := os.Stat(tmpPath)
	if err != nil {
		return failMediaDownload(ctx, db, mediaItem, fmt.Errorf("stat downloaded media: %w", err))
	}
	if info.Size() <= 0 {
		return failMediaDownload(ctx, db, mediaItem, fmt.Errorf("downloaded media is empty"))
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return failMediaDownload(ctx, db, mediaItem, fmt.Errorf("store downloaded media: %w", err))
	}

	mediaItem.LocalPath = finalPath
	mediaItem.SizeBytes = info.Size()
	mediaItem.DownloadState = "downloaded"
	mediaItem.UpdatedAt = time.Now()
	if err := db.UpsertMediaMetadata(ctx, mediaItem); err != nil {
		return mediaItem, err
	}
	return mediaItem, nil
}

func failMediaDownload(ctx context.Context, db *store.Store, mediaItem store.MediaMetadata, err error) (store.MediaMetadata, error) {
	mediaItem.DownloadState = "failed"
	mediaItem.UpdatedAt = time.Now()
	if db != nil {
		_ = db.UpsertMediaMetadata(ctx, mediaItem)
	}
	return mediaItem, err
}

func mergeAppMediaMetadata(existing, next store.MediaMetadata) store.MediaMetadata {
	if strings.TrimSpace(existing.MessageID) == "" {
		return next
	}
	incomingLocalPath := strings.TrimSpace(next.LocalPath) != ""
	if strings.TrimSpace(next.MessageID) == "" {
		next.MessageID = existing.MessageID
	}
	if strings.TrimSpace(next.Kind) == "" {
		next.Kind = existing.Kind
	}
	if strings.TrimSpace(next.MIMEType) == "" {
		next.MIMEType = existing.MIMEType
	}
	if strings.TrimSpace(next.FileName) == "" {
		next.FileName = existing.FileName
	}
	if next.SizeBytes <= 0 {
		next.SizeBytes = existing.SizeBytes
	}
	if strings.TrimSpace(next.LocalPath) == "" {
		next.LocalPath = existing.LocalPath
	}
	if strings.TrimSpace(next.ThumbnailPath) == "" {
		next.ThumbnailPath = existing.ThumbnailPath
	}
	if strings.TrimSpace(existing.LocalPath) != "" && !incomingLocalPath && strings.TrimSpace(next.DownloadState) == "remote" {
		next.DownloadState = existing.DownloadState
	}
	if !incomingLocalPath && strings.TrimSpace(next.DownloadState) == "" {
		next.DownloadState = existing.DownloadState
	}
	if !next.IsAnimated {
		next.IsAnimated = existing.IsAnimated
	}
	if !next.IsLottie {
		next.IsLottie = existing.IsLottie
	}
	if strings.TrimSpace(next.AccessibilityLabel) == "" {
		next.AccessibilityLabel = existing.AccessibilityLabel
	}
	if next.UpdatedAt.IsZero() {
		next.UpdatedAt = existing.UpdatedAt
	}
	return next
}

func mediaPathAvailable(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func mediaCachePath(mediaDir, messageID string, mediaItem store.MediaMetadata) string {
	sum := sha256.Sum256([]byte(messageID))
	name := "wa-" + hex.EncodeToString(sum[:])[:16] + mediaFileExtension(mediaItem)
	return filepath.Join(mediaDir, name)
}

func recentStickerCachePath(dir string, event whatsapp.RecentStickerEvent) string {
	name := safeFileStem(event.ID)
	if name == "" {
		sum := sha256.Sum256([]byte(strings.Join([]string{event.URL, event.DirectPath, event.ImageHash}, "\x00")))
		name = "sticker-" + hex.EncodeToString(sum[:])[:24]
	}
	return filepath.Join(dir, name+recentStickerExtension(event.MIMEType, event.FileName, event.IsLottie))
}

func mediaFileExtension(mediaItem store.MediaMetadata) string {
	if ext := strings.ToLower(filepath.Ext(strings.TrimSpace(mediaItem.FileName))); validMediaExtension(ext) {
		return ext
	}
	if exts, _ := mime.ExtensionsByType(strings.TrimSpace(mediaItem.MIMEType)); len(exts) > 0 && validMediaExtension(exts[0]) {
		return exts[0]
	}
	return ".bin"
}

func recentStickerExtension(mimeType, fileName string, lottie bool) string {
	if lottie {
		return ".tgs"
	}
	if ext := strings.ToLower(filepath.Ext(strings.TrimSpace(fileName))); validStickerExtension(ext) {
		return ext
	}
	switch strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0])) {
	case "image/webp":
		return ".webp"
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "application/x-tgsticker", "application/x-tgs", "application/gzip":
		return ".tgs"
	default:
		return ".webp"
	}
}

func validStickerExtension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".webp", ".png", ".jpg", ".jpeg", ".gif", ".tgs":
		return true
	default:
		return false
	}
}

func validMediaExtension(ext string) bool {
	if len(ext) < 2 || len(ext) > 12 || ext[0] != '.' || strings.ContainsAny(ext, `/\`) {
		return false
	}
	for _, r := range ext[1:] {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func safeFileStem(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func whatsappDescriptorFromStore(descriptor store.MediaDownloadDescriptor) whatsapp.MediaDownloadDescriptor {
	return whatsapp.MediaDownloadDescriptor{
		MessageID:     descriptor.MessageID,
		Kind:          descriptor.Kind,
		URL:           descriptor.URL,
		DirectPath:    descriptor.DirectPath,
		MediaKey:      cloneBytes(descriptor.MediaKey),
		FileSHA256:    cloneBytes(descriptor.FileSHA256),
		FileEncSHA256: cloneBytes(descriptor.FileEncSHA256),
		FileLength:    descriptor.FileLength,
		UpdatedAt:     descriptor.UpdatedAt,
	}
}

func cloneBytes(input []byte) []byte {
	if len(input) == 0 {
		return nil
	}
	out := make([]byte, len(input))
	copy(out, input)
	return out
}

func cloneRecentStickerPtr(sticker *store.RecentSticker) *store.RecentSticker {
	if sticker == nil {
		return nil
	}
	out := *sticker
	out.MediaKey = cloneBytes(sticker.MediaKey)
	out.FileSHA256 = cloneBytes(sticker.FileSHA256)
	out.FileEncSHA256 = cloneBytes(sticker.FileEncSHA256)
	return &out
}

func localStickerID(sticker store.RecentSticker) string {
	if id := strings.TrimSpace(sticker.ID); id != "" {
		return id
	}
	seed := strings.Join([]string{
		strings.TrimSpace(sticker.LocalPath),
		strings.TrimSpace(sticker.FileName),
		strings.TrimSpace(sticker.MIMEType),
	}, "\x00")
	if strings.Trim(seed, "\x00") == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(seed))
	return "local-sticker-" + hex.EncodeToString(sum[:])[:24]
}

func refreshRecentStickerAfterSend(ctx context.Context, db *store.Store, sticker store.RecentSticker, usedAt time.Time) {
	if db == nil {
		return
	}
	if usedAt.IsZero() {
		usedAt = time.Now()
	}
	sticker.ID = localStickerID(sticker)
	if sticker.ID == "" {
		return
	}
	sticker.LastUsedAt = usedAt
	sticker.UpdatedAt = usedAt
	_ = db.UpsertRecentSticker(ctx, sticker)
}

func isHistoricalImportEvent(event whatsapp.Event) bool {
	switch event.Kind {
	case whatsapp.EventChatUpsert:
		return event.Chat.Historical
	case whatsapp.EventMessageUpsert:
		return event.Message.Historical
	case whatsapp.EventMessageEdit:
		return event.Edit.Historical
	case whatsapp.EventMessageDelete:
		return event.Delete.Historical
	case whatsapp.EventMediaMetadata:
		return event.Media.Historical
	case whatsapp.EventRecentSticker:
		return event.Sticker.Historical
	default:
		return false
	}
}

func handleHistoryRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	inflight map[string]time.Time,
	online bool,
	chatID string,
) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "history fetch needs an active chat"})
		return
	}
	canonicalChatID, err := canonicalizeHistoryChatID(ctx, live, chatID)
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: fmt.Sprintf("history chat failed: %s", shortStatusError(err))})
		return
	}
	chatID = canonicalChatID
	if !online {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "history fetch needs WhatsApp online"})
		return
	}
	if requestedAt, ok := inflight[chatID]; ok && time.Since(requestedAt) < historyRequestTimeout {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "history already loading"})
		return
	}

	exhausted, err := db.SyncCursor(ctx, whatsapp.HistoryExhaustedCursor(chatID))
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: fmt.Sprintf("history cursor failed: %s", shortStatusError(err))})
		return
	}
	if status := blockedHistoryCursorStatus(exhausted); status != "" {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: status})
		return
	}

	anchorMessage, ok, err := db.OldestMessage(ctx, chatID)
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: fmt.Sprintf("history anchor failed: %s", shortStatusError(err))})
		return
	}
	if !ok {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "history fetch needs a local message anchor"})
		return
	}
	if strings.TrimSpace(anchorMessage.RemoteID) == "" {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "history anchor has no WhatsApp message id"})
		return
	}

	anchor := whatsapp.HistoryAnchor{
		ChatJID:   anchorMessage.ChatJID,
		MessageID: anchorMessage.RemoteID,
		IsFromMe:  anchorMessage.IsOutgoing,
		Timestamp: anchorMessage.Timestamp,
	}
	if strings.TrimSpace(anchor.ChatJID) == "" {
		anchor.ChatJID = anchorMessage.ChatID
	}

	inflight[chatID] = time.Now()
	if err := live.RequestHistoryBefore(ctx, anchor, historyPageSize); err != nil {
		delete(inflight, chatID)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: fmt.Sprintf("history request failed: %s", shortStatusError(err))})
		return
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "requested older history"})
}

func historyStatusLine(event whatsapp.HistoryEvent) string {
	switch event.TerminalReason {
	case "no_more":
		return fmt.Sprintf("history exhausted; imported %d older message(s)", event.Messages)
	case "no_access":
		return fmt.Sprintf("older history unavailable; imported %d older message(s)", event.Messages)
	}
	if event.Messages == 0 {
		return "history response contained no older messages"
	}
	return fmt.Sprintf("imported %d older message(s)", event.Messages)
}

func blockedHistoryCursorStatus(value string) string {
	switch value {
	case "no_more":
		return "history exhausted"
	case "no_access":
		return "older history unavailable from primary"
	default:
		return ""
	}
}

func liveUpdateForConnectionEvent(event whatsapp.ConnectionEvent) ui.LiveUpdate {
	update := ui.LiveUpdate{
		ConnectionState: uiConnectionState(event.State),
	}
	if strings.TrimSpace(event.Detail) != "" {
		update.Status = fmt.Sprintf("whatsapp: %s", event.Detail)
	}
	return update
}

func liveUpdateForPresenceEvent(event whatsapp.PresenceEvent) ui.LiveUpdate {
	expiresAt := event.UpdatedAt
	if expiresAt.IsZero() {
		expiresAt = time.Now()
	}
	expiresAt = expiresAt.Add(6 * time.Second)
	return ui.LiveUpdate{
		Presence: ui.PresenceUpdate{
			ChatID:    event.ChatID,
			SenderJID: event.SenderJID,
			Sender:    event.Sender,
			Typing:    event.Typing,
			ExpiresAt: expiresAt,
		},
	}
}

func uiConnectionState(state whatsapp.ConnectionState) ui.ConnectionState {
	switch state {
	case whatsapp.ConnectionPaired:
		return ui.ConnectionPaired
	case whatsapp.ConnectionConnecting:
		return ui.ConnectionConnecting
	case whatsapp.ConnectionOnline:
		return ui.ConnectionOnline
	case whatsapp.ConnectionReconnecting:
		return ui.ConnectionReconnecting
	case whatsapp.ConnectionLoggedOut:
		return ui.ConnectionLoggedOut
	default:
		return ui.ConnectionOffline
	}
}

func sendLiveUpdate(ctx context.Context, updates chan<- ui.LiveUpdate, update ui.LiveUpdate) {
	select {
	case updates <- update:
	case <-ctx.Done():
	}
}

func isLoggedOutConnectionError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not paired") ||
		strings.Contains(msg, "logged out") ||
		strings.Contains(msg, "session was rejected")
}

func shortStatusError(err error) string {
	if err == nil {
		return ""
	}
	return truncateStatus(err.Error(), 96)
}

func truncateStatus(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func loadSnapshotForChat(ctx context.Context, db *store.Store, activeChatID string, limit int) (store.Snapshot, error) {
	if limit <= 0 {
		limit = 200
	}
	chats, err := db.ListChats(ctx)
	if err != nil {
		return store.Snapshot{}, err
	}
	drafts, err := db.ListDrafts(ctx)
	if err != nil {
		return store.Snapshot{}, err
	}

	snapshot := store.Snapshot{
		Chats:          chats,
		MessagesByChat: map[string][]store.Message{},
		DraftsByChat:   drafts,
	}
	if len(chats) == 0 {
		return snapshot, nil
	}

	selected := activeChatID
	if !snapshotHasChat(chats, selected) {
		selected = chats[0].ID
	}
	snapshot.ActiveChatID = selected

	messages, err := db.ListMessages(ctx, selected, limit)
	if err != nil {
		return store.Snapshot{}, err
	}
	snapshot.MessagesByChat[selected] = messages
	return snapshot, nil
}

func snapshotHasChat(chats []store.Chat, chatID string) bool {
	for _, chat := range chats {
		if chat.ID == chatID {
			return true
		}
	}
	return false
}

func cloneMessagePtr(message *store.Message) *store.Message {
	if message == nil {
		return nil
	}
	clone := *message
	clone.Mentions = slices.Clone(message.Mentions)
	return &clone
}

func cloneMessageMentionsForMessage(mentions []store.MessageMention, messageID string, updatedAt time.Time) []store.MessageMention {
	if len(mentions) == 0 {
		return nil
	}
	out := make([]store.MessageMention, 0, len(mentions))
	seen := map[string]bool{}
	for _, mention := range mentions {
		mention.JID = strings.TrimSpace(mention.JID)
		if mention.JID == "" || seen[mention.JID] {
			continue
		}
		seen[mention.JID] = true
		mention.MessageID = messageID
		if mention.UpdatedAt.IsZero() {
			mention.UpdatedAt = updatedAt
		}
		out = append(out, mention)
	}
	return out
}

func mentionedJIDs(mentions []store.MessageMention) []string {
	if len(mentions) == 0 {
		return nil
	}
	out := make([]string, 0, len(mentions))
	seen := map[string]bool{}
	for _, mention := range mentions {
		jid := strings.TrimSpace(mention.JID)
		if jid == "" || seen[jid] {
			continue
		}
		seen[jid] = true
		out = append(out, jid)
	}
	return out
}

func pendingOutgoingMessage(outgoing ui.OutgoingMessage) store.Message {
	now := time.Now()
	message := store.Message{
		ID:         fmt.Sprintf("local-%d", now.UnixNano()),
		ChatID:     outgoing.ChatID,
		ChatJID:    outgoing.ChatID,
		Sender:     "me",
		SenderJID:  "me",
		Body:       strings.TrimSpace(outgoing.Body),
		Timestamp:  now,
		IsOutgoing: true,
		Status:     "pending",
		Mentions:   cloneMessageMentionsForMessage(outgoing.Mentions, "", now),
	}
	for i := range message.Mentions {
		message.Mentions[i].MessageID = message.ID
	}
	if outgoing.Quote != nil {
		message.QuotedMessageID = outgoing.Quote.ID
		message.QuotedRemoteID = outgoing.Quote.RemoteID
	}
	message.Media = mediaForOutgoingMessage(message.ID, outgoing.Attachments, now)
	return message
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
	emojiMode := env.Config.EmojiMode
	if strings.TrimSpace(emojiMode) == "" {
		emojiMode = config.EmojiModeAuto
	}
	resolvedEmojiMode := config.ResolveEmojiMode(emojiMode)

	lines := []string{
		"vimwhat doctor",
		"",
		"app: vimwhat",
		fmt.Sprintf("config file: %s", env.Paths.ConfigFile),
		fmt.Sprintf("data dir: %s", env.Paths.DataDir),
		fmt.Sprintf("cache dir: %s", env.Paths.CacheDir),
		fmt.Sprintf("transient dir: %s", env.Paths.TransientDir),
		fmt.Sprintf("database path: %s", env.Paths.DatabaseFile),
		fmt.Sprintf("session path: %s", env.Paths.SessionFile),
		fmt.Sprintf("media cache dir: %s", env.Paths.MediaDir),
		fmt.Sprintf("preview cache dir: %s", env.Paths.PreviewCacheDir),
		fmt.Sprintf("session status: %s", sessionStatus),
		fmt.Sprintf("editor: %s", env.Config.Editor),
		fmt.Sprintf("preview max: %dx%d delay=%dms", env.Config.PreviewMaxWidth, env.Config.PreviewMaxHeight, env.Config.PreviewDelayMS),
		fmt.Sprintf("downloads dir: %s", env.Config.DownloadsDir),
		fmt.Sprintf("leader key: %s", env.Config.LeaderKey),
		fmt.Sprintf("emoji mode: %s -> %s (TERM=%s UTF-8=%s)", emojiMode, resolvedEmojiMode, emptyAsAuto(os.Getenv("TERM")), yesNo(config.LocaleLooksUTF8())),
		fmt.Sprintf("terminal env: %s", terminalEnvSummary()),
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
	notificationReport := env.NotificationReport
	if notificationReport.Requested == "" && notificationReport.Selected == "" {
		notificationReport = notify.Detect(env.Config)
	}
	lines = append(lines, notificationReport.Lines()...)
	lines = append(lines, env.PreviewReport.Lines()...)

	fmt.Fprintln(w, strings.Join(lines, "\n"))
}

func emptyAsAuto(value string) string {
	if strings.TrimSpace(value) == "" {
		return "auto"
	}
	return value
}

func terminalEnvSummary() string {
	keys := []string{"TERM", "TERM_PROGRAM", "COLORTERM", "WT_SESSION", "ConEmuANSI", "ANSICON", "VIMWHAT_FORCE_SIXEL", "VIMWHAT_FORCE_REPORT_FOCUS", "VIMWHAT_DISABLE_REPORT_FOCUS"}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, emptyAsAuto(os.Getenv(key))))
	}
	return strings.Join(parts, " ")
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
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
