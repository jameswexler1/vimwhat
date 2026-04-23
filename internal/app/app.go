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
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"vimwhat/internal/config"
	"vimwhat/internal/media"
	"vimwhat/internal/store"
	"vimwhat/internal/ui"
	"vimwhat/internal/whatsapp"
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

	initialConnection, liveEnabled := initialLiveConnectionState(env, context.Background())
	liveUpdates := make(chan ui.LiveUpdate, 64)
	historyRequests := make(chan string, 16)
	mediaDownloadRequests := make(chan mediaDownloadRequest, mediaDownloadQueueSize)
	var liveUpdateSource <-chan ui.LiveUpdate
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	if liveEnabled {
		liveUpdateSource = liveUpdates
		wg.Add(1)
		go func() {
			defer wg.Done()
			runLiveWhatsApp(ctx, env, liveUpdates, historyRequests, mediaDownloadRequests)
		}()
	}
	defer func() {
		cancel()
		wg.Wait()
		close(liveUpdates)
	}()

	opts := ui.Options{
		Paths:           env.Paths,
		Config:          env.Config,
		PreviewReport:   env.PreviewReport,
		Snapshot:        snapshot,
		ConnectionState: initialConnection,
		LiveUpdates:     liveUpdateSource,
		BlockSending:    liveEnabled,
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
	historyPageSize        = 50
	historyRequestTimeout  = 30 * time.Second
	metadataRefreshTimeout = 30 * time.Second
	mediaDownloadWorkers   = 2
	mediaDownloadQueueSize = 16
	mediaDownloadTimeout   = 5 * time.Minute
)

type mediaDownloadRequest struct {
	Message store.Message
	Media   store.MediaMetadata
	Result  chan<- mediaDownloadResult
}

type mediaDownloadResult struct {
	Media store.MediaMetadata
	Err   error
}

func runLiveWhatsApp(ctx context.Context, env Environment, updates chan<- ui.LiveUpdate, historyRequests <-chan string, mediaDownloadRequests <-chan mediaDownloadRequest) {
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
			if err := ingestor.Apply(ctx, event); err != nil {
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
			sendLiveUpdate(ctx, updates, ui.LiveUpdate{Refresh: true})
		case chatID, ok := <-historyRequests:
			if !ok {
				return
			}
			handleHistoryRequest(ctx, env.Store, live, updates, historyInflight, online, chatID)
		case result, ok := <-metadataResults:
			if ok {
				ingested := 0
				for _, event := range result.Events {
					if err := ingestor.Apply(ctx, event); err != nil {
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
