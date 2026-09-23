package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
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
	"vimwhat/internal/config"
	"vimwhat/internal/media"
	"vimwhat/internal/notify"
	"vimwhat/internal/securefs"
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
	if isVersionCommand(args) {
		printVersion(os.Stdout)
		return 0
	}
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
	case "version", "-v", "--version":
		printVersion(stdout)
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
	recoveryCtx, cancelRecovery := storeStartupContext()
	_, recoveryErr := env.Store.RecoverInterruptedSends(recoveryCtx)
	cancelRecovery()
	if recoveryErr != nil {
		fmt.Fprintf(stderr, "vimwhat: recover outgoing messages: %v\n", recoveryErr)
		return 1
	}
	repairCtx, cancelRepair := storeStartupContext()
	if err := runPreStartCanonicalRepair(repairCtx, env); err != nil {
		fmt.Fprintf(stderr, "vimwhat: direct chat repair: %v\n", err)
	}
	cancelRepair()
	snapshotCtx, cancelSnapshot := storeStartupContext()
	snapshot, err := env.Store.LoadSnapshot(snapshotCtx, 200)
	cancelSnapshot()
	if err != nil {
		fmt.Fprintf(stderr, "vimwhat: load snapshot: %v\n", err)
		return 1
	}

	sessionCtx, cancelSession := storeStartupContext()
	initialConnection, liveEnabled := initialLiveConnectionState(env, sessionCtx)
	cancelSession()
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
		BlockLiveStartup:     liveEnabled,
		PersistMessage: func(outgoing ui.OutgoingMessage) (store.Message, error) {
			for i, attachment := range outgoing.Attachments {
				if env.Paths.IsManagedCachePath(attachment.LocalPath) {
					path, err := retainDraftAttachment(env.Paths.DataDir, attachment.LocalPath)
					if err != nil {
						return store.Message{}, err
					}
					outgoing.Attachments[i].LocalPath = path
				}
			}
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
			storeCtx, cancelStore := uiStoreWriteContext()
			defer cancelStore()
			if err := env.Store.AddMessageWithMedia(storeCtx, message, message.Media); err != nil {
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
			request := mediaSendRequest{Context: waitCtx, RetryID: message.ID, Result: result}
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
		ToggleNotificationsMuted: func() (bool, error) {
			storeCtx, cancelStore := uiStoreWriteContext()
			defer cancelStore()
			return env.Store.ToggleGlobalNotificationsMuted(storeCtx)
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
			storeCtx, cancelStore := uiStoreReadContext()
			defer cancelStore()
			return env.Store.ListMessages(storeCtx, chatID, limit)
		},
		LoadMessagesAround: func(chatID, targetID string, limit int) ([]store.Message, error) {
			ctx, cancel := uiStoreReadContext()
			defer cancel()
			return env.Store.ListMessagesAround(ctx, chatID, targetID, limit)
		},
		LoadNewerMessages: func(chatID string, after store.Message, limit int) ([]store.Message, error) {
			ctx, cancel := uiStoreReadContext()
			defer cancel()
			return env.Store.ListMessagesAfter(ctx, chatID, after, limit)
		},
		LoadOlderMessages: func(chatID string, before store.Message, limit int) ([]store.Message, error) {
			storeCtx, cancelStore := uiStoreReadContext()
			defer cancelStore()
			return env.Store.ListMessagesBefore(storeCtx, chatID, before, limit)
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
			storeCtx, cancelStore := uiStoreReadContext()
			defer cancelStore()
			return loadSnapshotForChat(storeCtx, env.Store, activeChatID, limit)
		},
		SaveDraft: func(chatID, body string) error {
			storeCtx, cancelStore := uiStoreWriteContext()
			defer cancelStore()
			return env.Store.SaveDraft(storeCtx, chatID, body)
		},
		SaveComposerDraft: newComposerDraftSaver(env),
		SearchChats: func(query string) ([]store.Chat, error) {
			storeCtx, cancelStore := uiStoreReadContext()
			defer cancelStore()
			return env.Store.SearchChats(storeCtx, query, 100)
		},
		SearchMessages: func(chatID, query string, limit int) ([]store.Message, error) {
			storeCtx, cancelStore := uiStoreReadContext()
			defer cancelStore()
			return env.Store.SearchMessages(storeCtx, chatID, query, limit)
		},
		SearchMentionCandidates: func(chatID, query string, limit int) ([]store.MentionCandidate, error) {
			storeCtx, cancelStore := uiStoreReadContext()
			defer cancelStore()
			return env.Store.SearchMentionCandidates(storeCtx, chatID, query, limit)
		},
		CopyToClipboard: func(text string) error {
			return copyToClipboard(context.Background(), env.Config.ClipboardCommand, text)
		},
		PasteTextFromClipboard: func(chatID string) tea.Cmd {
			return pasteTextFromClipboard(env.Config.ClipboardPasteCommand, chatID)
		},
		PasteAttachmentFromClipboard: func() tea.Cmd {
			return pasteAttachmentFromClipboard(env.Paths, env.Config.ClipboardImagePasteCommand)
		},
		PasteImageFromClipboard: func() tea.Cmd {
			return pasteAttachmentFromClipboard(env.Paths, env.Config.ClipboardImagePasteCommand)
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
		OpenMediaDetached: func(media store.MediaMetadata) tea.Cmd {
			return openMediaDetached(env.Config, media)
		},
		StartAudio: func(media store.MediaMetadata) (ui.AudioProcess, error) {
			return startAudio(env.Config, media)
		},
		DeleteMessage: func(messageID string) error {
			storeCtx, cancelStore := uiStoreWriteContext()
			defer cancelStore()
			return env.Store.DeleteMessage(storeCtx, messageID)
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
		ComposeInEditor: func(chatID, initial string) tea.Cmd {
			return composeInEditor(env.Paths, env.Config, chatID, initial)
		},
		SaveMedia: func(media store.MediaMetadata) error {
			storeCtx, cancelStore := uiStoreWriteContext()
			defer cancelStore()
			return env.Store.UpsertMediaMetadata(storeCtx, media)
		},
		DownloadMedia: func(message store.Message, media store.MediaMetadata) (store.MediaMetadata, error) {
			if !liveEnabled {
				return store.MediaMetadata{}, fmt.Errorf("whatsapp is not paired")
			}
			if strings.TrimSpace(message.ID) == "" {
				return store.MediaMetadata{}, fmt.Errorf("message id is required")
			}
			ctx, cancel := context.WithTimeout(context.Background(), mediaDownloadTimeout)
			defer cancel()
			result := make(chan mediaDownloadResult, 1)
			request := mediaDownloadRequest{
				Context: ctx,
				Message: message,
				Media:   media,
				Result:  result,
			}
			select {
			case mediaDownloadRequests <- request:
			case <-ctx.Done():
				return media, fmt.Errorf("media download timed out")
			default:
				return media, fmt.Errorf("media download request queue is full")
			}

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
	historyPageSize             = 50
	historyRequestTimeout       = 30 * time.Second
	metadataRefreshTimeout      = 30 * time.Second
	startupStoreTimeout         = 15 * time.Second
	uiStoreReadTimeout          = 5 * time.Second
	uiStoreWriteTimeout         = 5 * time.Second
	backgroundStoreWriteTimeout = 5 * time.Second
	textSendQueueSize           = 16
	textSendQueueTimeout        = 5 * time.Second
	textSendTimeout             = 90 * time.Second
	mediaSendQueueSize          = 16
	mediaSendQueueTimeout       = 5 * time.Second
	mediaSendTimeout            = 10 * time.Minute
	readReceiptQueueSize        = 16
	readReceiptQueueTimeout     = 5 * time.Second
	readReceiptTimeout          = 30 * time.Second
	reactionQueueSize           = 16
	reactionQueueTimeout        = 5 * time.Second
	reactionSendTimeout         = 90 * time.Second
	deleteEveryoneQueueSize     = 16
	deleteEveryoneQueueTimeout  = 5 * time.Second
	deleteEveryoneTimeout       = 90 * time.Second
	editMessageQueueSize        = 16
	editMessageQueueTimeout     = 5 * time.Second
	editMessageTimeout          = 90 * time.Second
	forwardMessageQueueSize     = 16
	forwardMessageQueueTimeout  = 5 * time.Second
	forwardMessageTimeout       = 90 * time.Second
	presenceQueueSize           = 32
	presenceSendTimeout         = 5 * time.Second
	mediaDownloadWorkers        = 2
	mediaDownloadQueueSize      = 16
	mediaDownloadTimeout        = 5 * time.Minute
	stickerDownloadTimeout      = 15 * time.Second
	recentStickerCacheWorkers   = 2
	recentStickerCacheQueueSize = 16
	stickerSyncQueueSize        = 4
	stickerSyncTimeout          = 90 * time.Second
	avatarRefreshQueueSize      = 32
	avatarRefreshTimeout        = 30 * time.Second
)

func storeStartupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), startupStoreTimeout)
}

func uiStoreReadContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), uiStoreReadTimeout)
}

func uiStoreWriteContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), uiStoreWriteTimeout)
}

func backgroundStoreWriteContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), backgroundStoreWriteTimeout)
}

var (
	offlineSyncProgressEvery   = 150 * time.Millisecond
	offlineSyncInactivity      = 15 * time.Second
	offlineSyncSettle          = 2 * time.Second
	offlineSyncRecoveryTimeout = 30 * time.Second
	lateReplaySettle           = 5 * time.Second
	lateReplayMaxDuration      = 30 * time.Second
	databaseImportInactivity   = 750 * time.Millisecond
	databaseImportMaxDuration  = 60 * time.Second
	liveStartupSyncSettle      = 1 * time.Second
	startupSyncTimeout         = 2 * time.Minute
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
	RetryID     string
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
	Context context.Context
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

type stickerSyncCompletion struct {
	Result        stickerSyncResult
	Update        ui.LiveUpdate
	StickerEvents []whatsapp.Event
}

type startupAppStateUpdate struct {
	Done   bool
	Err    error
	Update ui.LiveUpdate
}

type pendingNotificationCandidate struct {
	View   notificationContext
	Result whatsapp.ApplyResult
}

type notificationGate struct {
	Pending    bool
	Candidates []pendingNotificationCandidate
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

type recentStickerCacheRequest struct {
	Sticker whatsapp.RecentStickerEvent
}

type recentStickerCacheResult struct {
	Sticker whatsapp.RecentStickerEvent
	Err     error
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

func markLivePresenceAvailable(ctx context.Context, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate) {
	if live == nil {
		return
	}
	presenceCtx, cancel := context.WithTimeout(ctx, presenceSendTimeout)
	defer cancel()
	if err := live.SetPresenceAvailable(presenceCtx, true); err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Status: fmt.Sprintf("presence availability failed: %s", shortStatusError(err)),
		})
	}
}

func enqueueMediaDownload(ctx context.Context, jobs chan<- mediaDownloadRequest, online bool, request mediaDownloadRequest) {
	if request.Result == nil {
		return
	}
	if err := mediaDownloadRequestContextErr(request); err != nil {
		sendMediaDownloadResult(ctx, request, request.Media, err)
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
	case <-mediaDownloadRequestDone(request):
		sendMediaDownloadResult(context.Background(), request, request.Media, mediaDownloadRequestContextErr(request))
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
			workCtx, cancel := mediaDownloadContext(ctx, request.Context)
			media, err := downloadRemoteMedia(workCtx, db, live, paths, request)
			cancel()
			sendMediaDownloadResult(ctx, request, media, err)
		case <-ctx.Done():
			return
		}
	}
}

func mediaDownloadContext(parent, request context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if request == nil {
		return parent, func() {}
	}
	ctx, cancel := context.WithCancel(request)
	done := make(chan struct{})
	var once sync.Once
	go func() {
		select {
		case <-parent.Done():
			cancel()
		case <-ctx.Done():
		case <-done:
		}
	}()
	return ctx, func() {
		once.Do(func() {
			close(done)
			cancel()
		})
	}
}

func mediaDownloadRequestDone(request mediaDownloadRequest) <-chan struct{} {
	if request.Context == nil {
		return nil
	}
	return request.Context.Done()
}

func mediaDownloadRequestContextErr(request mediaDownloadRequest) error {
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

func sendMediaDownloadResult(ctx context.Context, request mediaDownloadRequest, media store.MediaMetadata, err error) {
	if request.Result == nil {
		return
	}
	select {
	case request.Result <- mediaDownloadResult{Media: media, Err: err}:
	case <-ctx.Done():
	}
}

func startStartupAppStateSync(ctx context.Context, db *store.Store, live WhatsAppLiveSession, paths config.Paths, wg *sync.WaitGroup, online bool) <-chan startupAppStateUpdate {
	out := make(chan startupAppStateUpdate, 4)
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		if wg != nil {
			defer wg.Done()
		}
		defer close(out)
		sendStartupAppStateUpdate(ctx, out, startupAppStateUpdate{
			Update: ui.LiveUpdate{Status: "syncing WhatsApp app state"},
		})
		if db == nil {
			sendStartupAppStateUpdate(ctx, out, startupAppStateUpdate{
				Done:   true,
				Update: ui.LiveUpdate{Status: "app-state sync failed: store is required"},
			})
			return
		}
		if live == nil {
			sendStartupAppStateUpdate(ctx, out, startupAppStateUpdate{
				Done:   true,
				Update: ui.LiveUpdate{Status: "app-state sync failed: whatsapp live session unavailable"},
			})
			return
		}
		if !online {
			sendStartupAppStateUpdate(ctx, out, startupAppStateUpdate{
				Done:   true,
				Update: ui.LiveUpdate{Status: "app-state sync needs WhatsApp online"},
			})
			return
		}
		request := stickerSyncRequest{Context: ctx}
		completion := runStickerSync(ctx, db, live, paths, request, false)
		sendStartupAppStateUpdate(ctx, out, startupAppStateUpdate{
			Done:   true,
			Err:    completion.Result.Err,
			Update: completion.Update,
		})
		if len(completion.StickerEvents) == 0 {
			return
		}
		cacheUpdate := cacheStartupStickerFiles(ctx, db, live, paths, completion.StickerEvents)
		if cacheUpdate.Refresh || strings.TrimSpace(cacheUpdate.Status) != "" {
			sendStartupAppStateUpdate(ctx, out, startupAppStateUpdate{Update: cacheUpdate})
		}
	}()
	return out
}

func sendStartupAppStateUpdate(ctx context.Context, updates chan<- startupAppStateUpdate, update startupAppStateUpdate) {
	select {
	case updates <- update:
	case <-ctx.Done():
	}
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

	sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "syncing WhatsApp app state"})
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeStickerSyncRequest(ctx, db, live, paths, updates, request, true)
		}()
		return
	}
	completeStickerSyncRequest(ctx, db, live, paths, updates, request, true)
}

func completeStickerSyncRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	paths config.Paths,
	updates chan<- ui.LiveUpdate,
	request stickerSyncRequest,
	cacheFiles bool,
) {
	completion := runStickerSync(ctx, db, live, paths, request, cacheFiles)
	sendStickerSyncResult(ctx, request, completion.Result.Stickers, completion.Result.Err)
	if completion.Update.Refresh || strings.TrimSpace(completion.Update.Status) != "" {
		sendLiveUpdate(ctx, updates, completion.Update)
	}
}

func runStickerSync(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	paths config.Paths,
	request stickerSyncRequest,
	cacheFiles bool,
) stickerSyncCompletion {
	syncCtx, cancel := stickerSyncContext(ctx, request.Context)
	defer cancel()

	events, syncErr := live.SyncAppState(syncCtx)
	ingestor := whatsapp.Ingestor{Store: db}
	cached := 0
	metadataSynced := 0
	settingsSynced := 0
	contactsSynced := 0
	cacheFailures := 0
	var cacheErr error
	var applyErr error
	var stickerEvents []whatsapp.Event
	for _, event := range events {
		cacheUsable := false
		switch event.Kind {
		case whatsapp.EventContactUpsert:
			contactsSynced++
		case whatsapp.EventRecentSticker:
			event.Sticker = normalizeRecentStickerMetadata(event.Sticker)
			if cacheFiles {
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
			} else {
				if current, ok, err := db.RecentSticker(syncCtx, event.Sticker.ID); err != nil {
					applyErr = errors.Join(applyErr, fmt.Errorf("load recent sticker %s: %w", event.Sticker.ID, err))
				} else if ok && mediaPathAvailable(current.LocalPath) {
					event.Sticker.LocalPath = current.LocalPath
					cacheUsable = stickerPickerUsable(current)
				}
				stickerEvents = append(stickerEvents, event)
			}
		case whatsapp.EventRecentStickerRemove:
		case whatsapp.EventChatUpsert:
			if !event.Chat.MutedKnown && !event.Chat.PinnedKnown {
				continue
			}
		default:
			continue
		}
		if _, err := ingestor.Apply(syncCtx, event); err != nil {
			applyErr = errors.Join(applyErr, fmt.Errorf("apply app-state event: %w", err))
			continue
		}
		if event.Kind == whatsapp.EventRecentSticker {
			metadataSynced++
			if cacheUsable {
				cached++
			}
		}
		if event.Kind == whatsapp.EventChatUpsert {
			settingsSynced++
		}
	}

	err := stickerSyncResultError(cached, metadataSynced, cacheFailures, cacheErr, syncErr, applyErr)
	completion := stickerSyncCompletion{
		Result: stickerSyncResult{
			Stickers: cached,
			Err:      err,
		},
		StickerEvents: stickerEvents,
	}
	if err != nil {
		completion.Update = ui.LiveUpdate{
			Refresh: metadataSynced > 0 || settingsSynced > 0 || contactsSynced > 0,
			Status:  fmt.Sprintf("sticker sync failed: %s", shortStatusError(err)),
		}
		return completion
	}
	completion.Update = ui.LiveUpdate{
		Refresh: metadataSynced > 0 || settingsSynced > 0 || contactsSynced > 0,
		Status:  stickerSyncStatus(cached, metadataSynced, settingsSynced, cacheFailures, cacheErr, errors.Join(syncErr, applyErr)),
	}
	return completion
}

func normalizeRecentStickerMetadata(event whatsapp.RecentStickerEvent) whatsapp.RecentStickerEvent {
	if strings.TrimSpace(event.FileName) == "" {
		event.FileName = "sticker" + recentStickerExtension(event.MIMEType, event.FileName, event.IsLottie)
	}
	return event
}

func cacheStartupStickerFiles(ctx context.Context, db *store.Store, live WhatsAppLiveSession, paths config.Paths, events []whatsapp.Event) ui.LiveUpdate {
	cacheCtx, cancel := context.WithTimeout(ctx, stickerSyncTimeout)
	defer cancel()

	ingestor := whatsapp.Ingestor{Store: db}
	cached := 0
	cacheFailures := 0
	var cacheErr error
	var applyErr error
	for _, event := range events {
		if event.Kind != whatsapp.EventRecentSticker {
			continue
		}
		prepared, err := prepareRecentStickerEvent(cacheCtx, db, live, paths, event.Sticker)
		if err != nil {
			cacheFailures++
			if cacheErr == nil {
				cacheErr = err
			}
			continue
		}
		event.Sticker = prepared
		if !stickerPickerUsable(store.RecentSticker{
			ID:        prepared.ID,
			MIMEType:  prepared.MIMEType,
			FileName:  prepared.FileName,
			LocalPath: prepared.LocalPath,
			IsLottie:  prepared.IsLottie,
		}) {
			continue
		}
		if _, err := ingestor.Apply(cacheCtx, event); err != nil {
			applyErr = errors.Join(applyErr, fmt.Errorf("apply cached sticker event: %w", err))
			continue
		}
		cached++
	}
	if cached > 0 {
		status := fmt.Sprintf("cached %d WhatsApp sticker(s)", cached)
		if cacheFailures > 0 {
			status = fmt.Sprintf("%s; %d unavailable", status, cacheFailures)
		}
		return ui.LiveUpdate{Refresh: true, Status: status}
	}
	if cacheErr != nil || applyErr != nil {
		return ui.LiveUpdate{Status: fmt.Sprintf("sticker cache incomplete: %s", shortStatusError(errors.Join(cacheErr, applyErr)))}
	}
	return ui.LiveUpdate{}
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

func stickerSyncStatus(cached, metadataSynced, settingsSynced, cacheFailures int, cacheErr, warning error) string {
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
	if settingsSynced > 0 {
		return fmt.Sprintf("synced %d WhatsApp chat setting(s)", settingsSynced)
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
) bool {
	chatID := strings.TrimSpace(event.ChatID)
	if chatID == "" {
		chatID = strings.TrimSpace(event.ChatJID)
	}
	if chatID == "" {
		return false
	}
	if event.Remove {
		changed, err := clearStoredChatAvatar(ctx, db, paths, chatID, event.UpdatedAt)
		if err != nil {
			sendLiveUpdate(ctx, updates, ui.LiveUpdate{
				Status: fmt.Sprintf("avatar cleanup failed: %s", shortStatusError(err)),
			})
			return false
		}
		return changed
	}
	enqueueAvatarRefresh(ctx, jobs, inflight, chatID)
	return false
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
	tmp, err := os.CreateTemp(cacheDir, "avatar-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create avatar temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	hasher := sha256.New()
	written, err := io.Copy(tmp, io.TeeReader(response.Body, hasher))
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
	contentHash := hex.EncodeToString(hasher.Sum(nil))
	finalPath := avatarCachePath(cacheDir, chatID, avatar.AvatarID, contentHash, ext)
	if info, err := os.Stat(finalPath); err == nil && !info.IsDir() {
		return finalPath, nil
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", fmt.Errorf("store avatar file: %w", err)
	}
	return finalPath, nil
}

func avatarCachePath(cacheDir, chatID, avatarID, contentHash, ext string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{chatID, avatarID, contentHash}, "\x00")))
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
	event, needsDownload, err := prepareRecentStickerEventMetadata(ctx, db, paths, event)
	if err != nil || !needsDownload || live == nil {
		return event, err
	}
	return downloadRecentStickerFile(ctx, live, paths, event)
}

func prepareRecentStickerEventMetadata(ctx context.Context, db *store.Store, paths config.Paths, event whatsapp.RecentStickerEvent) (whatsapp.RecentStickerEvent, bool, error) {
	if strings.TrimSpace(event.ID) == "" {
		return event, false, fmt.Errorf("recent sticker id is required")
	}
	if strings.TrimSpace(event.FileName) == "" {
		event.FileName = "sticker" + recentStickerExtension(event.MIMEType, event.FileName, event.IsLottie)
	}
	if event.IsLottie || strings.EqualFold(filepath.Ext(event.FileName), ".tgs") {
		return event, false, nil
	}
	dir := recentStickerCacheDir(paths)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return event, false, fmt.Errorf("create sticker cache dir: %w", err)
	}

	finalPath := recentStickerCachePath(dir, event)
	if mediaPathAvailable(finalPath) {
		event.LocalPath = finalPath
		return event, false, nil
	}
	if db != nil {
		if current, ok, err := db.RecentSticker(ctx, event.ID); err != nil {
			return event, false, err
		} else if ok && mediaPathAvailable(current.LocalPath) {
			event.LocalPath = current.LocalPath
			return event, false, nil
		}
	}
	if (strings.TrimSpace(event.DirectPath) == "" && strings.TrimSpace(event.URL) == "") || len(event.MediaKey) == 0 || len(event.FileEncSHA256) == 0 {
		return event, false, nil
	}
	return event, true, nil
}

func downloadRecentStickerFile(ctx context.Context, live WhatsAppLiveSession, paths config.Paths, event whatsapp.RecentStickerEvent) (whatsapp.RecentStickerEvent, error) {
	if live == nil {
		return event, nil
	}
	dir := recentStickerCacheDir(paths)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return event, fmt.Errorf("create sticker cache dir: %w", err)
	}
	finalPath := recentStickerCachePath(dir, event)
	if mediaPathAvailable(finalPath) {
		event.LocalPath = finalPath
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
	downloadCtx, cancel := context.WithTimeout(ctx, stickerDownloadTimeout)
	defer cancel()
	if err := live.DownloadMedia(downloadCtx, descriptor, tmpPath); err != nil {
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

func recentStickerCacheDir(paths config.Paths) string {
	if strings.TrimSpace(paths.TransientDir) == "" {
		return filepath.Join(os.TempDir(), "vimwhat-stickers")
	}
	return filepath.Join(paths.TransientDir, "stickers", "files")
}

func recentStickerCacheWorker(ctx context.Context, db *store.Store, live WhatsAppLiveSession, paths config.Paths, jobs <-chan recentStickerCacheRequest, results chan<- recentStickerCacheResult) {
	for request := range jobs {
		sticker, err := prepareRecentStickerEvent(ctx, db, live, paths, request.Sticker)
		result := recentStickerCacheResult{Sticker: sticker, Err: err}
		select {
		case results <- result:
		case <-ctx.Done():
			return
		}
	}
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
	presence := ui.PresenceUpdate{
		ChatID:              event.ChatID,
		SenderJID:           event.SenderJID,
		Sender:              event.Sender,
		TypingChanged:       event.TypingChanged,
		Typing:              event.Typing,
		AvailabilityChanged: event.AvailabilityChanged,
		Online:              event.Online,
		LastSeen:            event.LastSeen,
	}
	if event.TypingChanged && event.Typing {
		expiresAt := event.UpdatedAt
		if expiresAt.IsZero() {
			expiresAt = time.Now()
		}
		presence.ExpiresAt = expiresAt.Add(6 * time.Second)
	}
	return ui.LiveUpdate{
		Presence: presence,
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
	notificationsMuted, err := db.GlobalNotificationsMuted(ctx)
	if err != nil {
		return store.Snapshot{}, err
	}
	snapshot.NotificationsMuted = notificationsMuted
	snapshot.ComposerDrafts, err = db.ListComposerDrafts(ctx)
	if err != nil {
		return store.Snapshot{}, err
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

func mentionWireBody(body string, mentions []store.MessageMention) string {
	if body == "" || len(mentions) == 0 {
		return body
	}
	items := slices.Clone(mentions)
	slices.SortStableFunc(items, func(left, right store.MessageMention) int {
		if left.StartByte != right.StartByte {
			return left.StartByte - right.StartByte
		}
		return left.EndByte - right.EndByte
	})
	var out strings.Builder
	out.Grow(len(body))
	last := 0
	replaced := false
	for _, mention := range items {
		if mention.StartByte < last || mention.EndByte <= mention.StartByte || mention.EndByte > len(body) {
			continue
		}
		if !strings.HasPrefix(body[mention.StartByte:mention.EndByte], "@") {
			continue
		}
		token := mentionWireToken(mention.JID)
		if token == "" {
			continue
		}
		out.WriteString(body[last:mention.StartByte])
		out.WriteString(token)
		last = mention.EndByte
		replaced = true
	}
	if !replaced {
		return body
	}
	out.WriteString(body[last:])
	return out.String()
}

func mentionWireToken(jid string) string {
	jid = strings.TrimSpace(jid)
	if jid == "" {
		return ""
	}
	user := jid
	if before, _, ok := strings.Cut(jid, "@"); ok {
		user = before
	}
	if user = strings.TrimSpace(user); user == "" {
		return ""
	}
	return "@" + user
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
		fmt.Sprintf("build: %s", formatBuildInfo(currentBuildInfo())),
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
	}
	lines = append(lines, runtimePermissionLines(env.Paths)...)
	lines = append(lines, ui.ProbeTerminalOutput().Lines()...)
	lines = append(lines,
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
	)
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

func runtimePermissionLines(paths config.Paths) []string {
	statuses := []struct {
		label  string
		status securefs.Status
	}{
		{label: "config dir", status: securefs.PrivateDirStatus(paths.ConfigDir)},
		{label: "data dir", status: securefs.PrivateDirStatus(paths.DataDir)},
		{label: "config file", status: securefs.PrivateFileStatus(paths.ConfigFile)},
		{label: "database file", status: securefs.PrivateFileStatus(paths.DatabaseFile)},
		{label: "database wal", status: sqliteSidecarStatus(paths.DatabaseFile, "-wal")},
		{label: "database shm", status: sqliteSidecarStatus(paths.DatabaseFile, "-shm")},
		{label: "session file", status: securefs.PrivateFileStatus(paths.SessionFile)},
		{label: "session wal", status: sqliteSidecarStatus(paths.SessionFile, "-wal")},
		{label: "session shm", status: sqliteSidecarStatus(paths.SessionFile, "-shm")},
	}

	lines := []string{"runtime permissions:"}
	for _, item := range statuses {
		lines = append(lines, fmt.Sprintf("%s permissions: %s", item.label, permissionStatusSummary(item.status)))
	}
	lines = append(lines, "transient media permissions: compatibility-managed for external preview, opener, picker, and download helpers")
	return lines
}

func sqliteSidecarStatus(path, suffix string) securefs.Status {
	path = strings.TrimSpace(path)
	if path == "" {
		return securefs.PrivateFileStatus("")
	}
	return securefs.PrivateFileStatus(path + suffix)
}

func permissionStatusSummary(status securefs.Status) string {
	if strings.TrimSpace(status.Path) == "" {
		return "missing path"
	}
	if !status.Exists {
		return "missing"
	}
	if status.Warning != "" {
		return "warning: " + status.Warning
	}
	if status.NotApplicable {
		return "ok (per-user ACL; chmod not applicable)"
	}
	if status.OK {
		return fmt.Sprintf("ok (%04o)", status.Mode)
	}
	return "warning: status unavailable"
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
  vimwhat version
  vimwhat doctor
  vimwhat media open <message-id>
  vimwhat export chat <jid>
`))
}
