package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
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
	presenceRequests := make(chan presenceRequest, presenceQueueSize)
	presenceSubscribeRequests := make(chan presenceSubscribeRequest, presenceQueueSize)
	mediaDownloadRequests := make(chan mediaDownloadRequest, mediaDownloadQueueSize)
	activeChatNotifications := make(chan string, 16)
	var liveUpdateSource <-chan ui.LiveUpdate
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	if liveEnabled {
		liveUpdateSource = liveUpdates
		wg.Add(1)
		go func() {
			defer wg.Done()
			runLiveWhatsApp(ctx, env, liveUpdates, historyRequests, textSendRequests, mediaSendRequests, readReceiptRequests, reactionRequests, presenceRequests, presenceSubscribeRequests, mediaDownloadRequests, activeChatNotifications)
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
						Result:      result,
					}
					return queueMediaSendRequest(waitCtx, mediaSendRequests, request)
				}
				waitCtx, cancel := context.WithTimeout(context.Background(), textSendQueueTimeout)
				defer cancel()
				result := make(chan textSendQueuedResult, 1)
				request := textSendRequest{
					Context: waitCtx,
					ChatID:  outgoing.ChatID,
					Body:    outgoing.Body,
					Quote:   cloneMessagePtr(outgoing.Quote),
					Result:  result,
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
	historyPageSize         = 50
	historyRequestTimeout   = 30 * time.Second
	metadataRefreshTimeout  = 30 * time.Second
	textSendQueueSize       = 16
	textSendQueueTimeout    = 5 * time.Second
	textSendTimeout         = 90 * time.Second
	mediaSendQueueSize      = 16
	mediaSendQueueTimeout   = 5 * time.Second
	mediaSendTimeout        = 10 * time.Minute
	readReceiptQueueSize    = 16
	readReceiptQueueTimeout = 5 * time.Second
	readReceiptTimeout      = 30 * time.Second
	reactionQueueSize       = 16
	reactionQueueTimeout    = 5 * time.Second
	reactionSendTimeout     = 90 * time.Second
	presenceQueueSize       = 32
	presenceSendTimeout     = 5 * time.Second
	mediaDownloadWorkers    = 2
	mediaDownloadQueueSize  = 16
	mediaDownloadTimeout    = 5 * time.Minute
)

type textSendRequest struct {
	Context context.Context
	ChatID  string
	Body    string
	Quote   *store.Message
	Result  chan<- textSendQueuedResult
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
	Quote       *store.Message
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

func runLiveWhatsApp(
	ctx context.Context,
	env Environment,
	updates chan<- ui.LiveUpdate,
	historyRequests <-chan string,
	textSendRequests <-chan textSendRequest,
	mediaSendRequests <-chan mediaSendRequest,
	readReceiptRequests <-chan readReceiptRequest,
	reactionRequests <-chan reactionRequest,
	presenceRequests <-chan presenceRequest,
	presenceSubscribeRequests <-chan presenceSubscribeRequest,
	mediaDownloadRequests <-chan mediaDownloadRequest,
	activeChatUpdates <-chan string,
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
			mediaDownloadWorker(ctx, env.Store, live, env.Paths.MediaDir, downloadJobs)
		}()
	}
	defer func() {
		close(downloadJobs)
		downloadWG.Wait()
	}()

	ingestor := whatsapp.Ingestor{Store: env.Store}
	historyInflight := map[string]time.Time{}
	metadataResults := refreshChatMetadata(ctx, live)
	activeChatID := ""
	online := true
	for {
		select {
		case event, ok := <-events:
			if !ok {
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{ConnectionState: ui.ConnectionOffline})
				return
			}
			if event.Kind == whatsapp.EventConnectionState {
				online = event.Connection.State == whatsapp.ConnectionOnline
				sendLiveUpdate(ctx, updates, liveUpdateForConnectionEvent(event.Connection))
				continue
			}
			if event.Kind == whatsapp.EventPresenceUpdate {
				sendLiveUpdate(ctx, updates, liveUpdateForPresenceEvent(event.Presence))
				continue
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
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{
					Refresh:         true,
					Status:          historyStatusLine(event.History),
					HistoryChatID:   event.History.ChatID,
					HistoryMessages: event.History.Messages,
				})
				continue
			}
			if isHistoricalImportEvent(event) {
				continue
			}
			if note, ok := buildNotification(context.Background(), env.Store, activeChatID, result); ok {
				queueNotification(notificationJobs, note)
			}
			sendLiveUpdate(ctx, updates, ui.LiveUpdate{Refresh: true})
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
		case chatID, ok := <-activeChatUpdates:
			if !ok {
				activeChatUpdates = nil
				continue
			}
			activeChatID = chatID
		case <-ctx.Done():
			return
		}
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
	chatJID, err := whatsapp.NormalizeSendChatJID(request.ChatID)
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
			completeQueuedTextSend(ctx, db, live, updates, message, body, request.Quote)
		}()
		return
	}
	completeQueuedTextSend(ctx, db, live, updates, message, body, request.Quote)
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

func completeQueuedTextSend(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, message store.Message, body string, quote *store.Message) {
	sendCtx, cancel := context.WithTimeout(ctx, textSendTimeout)
	defer cancel()
	request := whatsapp.TextSendRequest{
		ChatJID:  message.ChatJID,
		Body:     body,
		RemoteID: message.RemoteID,
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
	chatJID, err := whatsapp.NormalizeSendChatJID(request.ChatID)
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
			completeQueuedMediaSend(ctx, db, live, updates, message, attachment, request.Quote)
		}()
		return
	}
	completeQueuedMediaSend(ctx, db, live, updates, message, attachment, request.Quote)
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

func completeQueuedMediaSend(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, message store.Message, attachment ui.Attachment, quote *store.Message) {
	sendCtx, cancel := context.WithTimeout(ctx, mediaSendTimeout)
	defer cancel()
	request := whatsapp.MediaSendRequest{
		ChatJID:   message.ChatJID,
		LocalPath: attachment.LocalPath,
		FileName:  attachment.FileName,
		MIMEType:  attachment.MIMEType,
		Caption:   message.Body,
		RemoteID:  message.RemoteID,
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

func mediaSendStatus(result whatsapp.SendResult) string {
	if notice := strings.TrimSpace(result.Notice); notice != "" {
		return "sent attachment; " + notice
	}
	return "sent attachment"
}

func retryMediaSendRequest(ctx context.Context, db *store.Store, message store.Message, result chan mediaSendQueuedResult) (mediaSendRequest, error) {
	attachment, err := retryAttachmentForMessage(message)
	if err != nil {
		return mediaSendRequest{}, err
	}
	request := mediaSendRequest{
		Context:     ctx,
		ChatID:      retryChatID(message),
		Body:        strings.TrimSpace(message.Body),
		Attachments: []ui.Attachment{attachment},
		Result:      result,
	}
	quote, err := retryQuoteForMessage(ctx, db, message)
	if err != nil {
		return mediaSendRequest{}, err
	}
	request.Quote = quote
	return request, nil
}

func retryAttachmentForMessage(message store.Message) (ui.Attachment, error) {
	if !message.IsOutgoing {
		return ui.Attachment{}, fmt.Errorf("retry needs an outgoing message")
	}
	if strings.TrimSpace(message.Status) != "failed" {
		return ui.Attachment{}, fmt.Errorf("retry needs a failed message")
	}
	if len(message.Media) == 0 {
		return ui.Attachment{}, fmt.Errorf("retry needs a media attachment")
	}
	if len(message.Media) > 1 {
		return ui.Attachment{}, fmt.Errorf("only one attachment per message is supported")
	}
	item := message.Media[0]
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
	if _, err := whatsapp.NormalizeSendChatJID(chatJID); err != nil {
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

func handlePresenceRequest(ctx context.Context, live WhatsAppLiveSession, online bool, request presenceRequest) {
	if live == nil || !online || strings.TrimSpace(request.ChatID) == "" {
		return
	}
	presenceCtx, cancel := context.WithTimeout(ctx, presenceSendTimeout)
	defer cancel()
	_ = live.SendChatPresence(presenceCtx, request.ChatID, request.Composing)
}

func handlePresenceSubscribeRequest(ctx context.Context, live WhatsAppLiveSession, online bool, request presenceSubscribeRequest) {
	if live == nil || !online || strings.TrimSpace(request.ChatID) == "" {
		return
	}
	presenceCtx, cancel := context.WithTimeout(ctx, presenceSendTimeout)
	defer cancel()
	_ = live.SubscribePresence(presenceCtx, request.ChatID)
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

func mediaDownloadWorker(ctx context.Context, db *store.Store, live WhatsAppLiveSession, mediaDir string, jobs <-chan mediaDownloadRequest) {
	for {
		select {
		case request, ok := <-jobs:
			if !ok {
				return
			}
			media, err := downloadRemoteMedia(ctx, db, live, mediaDir, request)
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

func downloadRemoteMedia(ctx context.Context, db *store.Store, live WhatsAppLiveSession, mediaDir string, request mediaDownloadRequest) (store.MediaMetadata, error) {
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

	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		return failMediaDownload(ctx, db, mediaItem, fmt.Errorf("create media cache dir: %w", err))
	}
	finalPath := mediaCachePath(mediaDir, messageID, mediaItem)
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

	tmp, err := os.CreateTemp(mediaDir, "download-*.tmp")
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

func mediaFileExtension(mediaItem store.MediaMetadata) string {
	if ext := strings.ToLower(filepath.Ext(strings.TrimSpace(mediaItem.FileName))); validMediaExtension(ext) {
		return ext
	}
	if exts, _ := mime.ExtensionsByType(strings.TrimSpace(mediaItem.MIMEType)); len(exts) > 0 && validMediaExtension(exts[0]) {
		return exts[0]
	}
	return ".bin"
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

func isHistoricalImportEvent(event whatsapp.Event) bool {
	switch event.Kind {
	case whatsapp.EventChatUpsert:
		return event.Chat.Historical
	case whatsapp.EventMessageUpsert:
		return event.Message.Historical
	case whatsapp.EventMediaMetadata:
		return event.Media.Historical
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
	return &clone
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
		fmt.Sprintf("database path: %s", env.Paths.DatabaseFile),
		fmt.Sprintf("session path: %s", env.Paths.SessionFile),
		fmt.Sprintf("session status: %s", sessionStatus),
		fmt.Sprintf("editor: %s", env.Config.Editor),
		fmt.Sprintf("preview max: %dx%d delay=%dms", env.Config.PreviewMaxWidth, env.Config.PreviewMaxHeight, env.Config.PreviewDelayMS),
		fmt.Sprintf("downloads dir: %s", env.Config.DownloadsDir),
		fmt.Sprintf("leader key: %s", env.Config.LeaderKey),
		fmt.Sprintf("emoji mode: %s -> %s (TERM=%s UTF-8=%s)", emojiMode, resolvedEmojiMode, emptyAsAuto(os.Getenv("TERM")), yesNo(config.LocaleLooksUTF8())),
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
