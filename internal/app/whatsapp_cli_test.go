package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"vimwhat/internal/config"
	"vimwhat/internal/notify"
	"vimwhat/internal/store"
	"vimwhat/internal/ui"
	"vimwhat/internal/whatsapp"
)

type fakeWhatsAppSession struct {
	loggedIn     bool
	loginCodes   []string
	loginErr     error
	logoutErr    error
	loginCalled  bool
	logoutCalled bool
	closeCalled  bool
}

func (s *fakeWhatsAppSession) IsLoggedIn() bool {
	return s.loggedIn
}

func (s *fakeWhatsAppSession) Login(_ context.Context, handleQR func(string)) error {
	s.loginCalled = true
	for _, code := range s.loginCodes {
		handleQR(code)
	}
	if s.loginErr == nil {
		s.loggedIn = true
	}
	return s.loginErr
}

func (s *fakeWhatsAppSession) Logout(context.Context) error {
	s.logoutCalled = true
	if s.logoutErr == nil {
		s.loggedIn = false
	}
	return s.logoutErr
}

func (s *fakeWhatsAppSession) Close() error {
	s.closeCalled = true
	return nil
}

type fakeLiveWhatsAppSession struct {
	fakeWhatsAppSession
	events            chan whatsapp.Event
	historyRequests   chan fakeHistoryRequest
	downloads         chan fakeDownloadRequest
	sends             chan fakeSendRequest
	mediaSends        chan whatsapp.MediaSendRequest
	stickerSends      chan whatsapp.StickerSendRequest
	forwards          chan whatsapp.ForwardMessageRequest
	readReceipts      chan []whatsapp.ReadReceiptTarget
	reactions         chan whatsapp.ReactionSendRequest
	deletes           chan whatsapp.DeleteForEveryoneRequest
	edits             chan whatsapp.EditMessageRequest
	presences         chan fakePresenceRequest
	subscriptions     chan string
	historyErr        error
	downloadErr       error
	sendErr           error
	readErr           error
	reactionErr       error
	deleteErr         error
	editErr           error
	presenceErr       error
	canonicalErr      error
	stickerSyncErr    error
	downloadErrByPath map[string]error
	generatedID       string
	connectErr        error
	subscribeErr      error
	connectCalled     bool
	subscribeCalled   bool
	canonicalChatID   map[string]string
	stickerSync       []whatsapp.Event
	stickerSyncCalls  int
}

type fakeHistoryRequest struct {
	anchor whatsapp.HistoryAnchor
	limit  int
}

type fakeDownloadRequest struct {
	descriptor whatsapp.MediaDownloadDescriptor
	targetPath string
}

type fakeSendRequest struct {
	request whatsapp.TextSendRequest
}

type fakePresenceRequest struct {
	chatID    string
	composing bool
}

type fakeMetadataLiveWhatsAppSession struct {
	*fakeLiveWhatsAppSession
	metadataEvents []whatsapp.Event
	metadataErr    error
}

type fakeNotifier struct {
	notifications chan notify.Notification
	err           error
	report        notify.Report
}

func (s *fakeLiveWhatsAppSession) Connect(context.Context) error {
	s.connectCalled = true
	if s.connectErr == nil {
		s.loggedIn = true
	}
	return s.connectErr
}

func (s *fakeLiveWhatsAppSession) SubscribeEvents(context.Context) (<-chan whatsapp.Event, error) {
	s.subscribeCalled = true
	return s.events, s.subscribeErr
}

func (s *fakeLiveWhatsAppSession) RequestHistoryBefore(_ context.Context, anchor whatsapp.HistoryAnchor, limit int) error {
	if s.historyRequests != nil {
		s.historyRequests <- fakeHistoryRequest{anchor: anchor, limit: limit}
	}
	return s.historyErr
}

func (s *fakeLiveWhatsAppSession) SyncRecentStickers(context.Context) ([]whatsapp.Event, error) {
	s.stickerSyncCalls++
	return append([]whatsapp.Event(nil), s.stickerSync...), s.stickerSyncErr
}

func (s *fakeLiveWhatsAppSession) GenerateMessageID() string {
	if s.generatedID != "" {
		return s.generatedID
	}
	return "remote-generated"
}

func (s *fakeLiveWhatsAppSession) CanonicalChatJID(_ context.Context, chatID string) (string, error) {
	if s.canonicalErr != nil {
		return "", s.canonicalErr
	}
	normalized, err := whatsapp.NormalizeSendChatJID(chatID)
	if err != nil {
		if s.canonicalChatID != nil {
			if mapped, ok := s.canonicalChatID[chatID]; ok && strings.TrimSpace(mapped) != "" {
				return mapped, nil
			}
		}
		return chatID, nil
	}
	if s.canonicalChatID != nil {
		if mapped, ok := s.canonicalChatID[chatID]; ok && strings.TrimSpace(mapped) != "" {
			return mapped, nil
		}
		if mapped, ok := s.canonicalChatID[normalized]; ok && strings.TrimSpace(mapped) != "" {
			return mapped, nil
		}
	}
	return normalized, nil
}

func (s *fakeLiveWhatsAppSession) SendText(_ context.Context, request whatsapp.TextSendRequest) (whatsapp.SendResult, error) {
	if s.sends != nil {
		s.sends <- fakeSendRequest{request: request}
	}
	if s.sendErr != nil {
		return whatsapp.SendResult{}, s.sendErr
	}
	if request.RemoteID == "" {
		request.RemoteID = s.GenerateMessageID()
	}
	return whatsapp.SendResult{
		MessageID: whatsapp.LocalMessageID(request.ChatJID, request.RemoteID),
		RemoteID:  request.RemoteID,
		Status:    "sent",
		Timestamp: time.Now(),
	}, nil
}

func (s *fakeLiveWhatsAppSession) SendMedia(_ context.Context, request whatsapp.MediaSendRequest) (whatsapp.SendResult, error) {
	if s.mediaSends != nil {
		s.mediaSends <- request
	}
	if s.sendErr != nil {
		return whatsapp.SendResult{}, s.sendErr
	}
	if request.RemoteID == "" {
		request.RemoteID = s.GenerateMessageID()
	}
	return whatsapp.SendResult{
		MessageID: whatsapp.LocalMessageID(request.ChatJID, request.RemoteID),
		RemoteID:  request.RemoteID,
		Status:    "sent",
		Timestamp: time.Now(),
	}, nil
}

func (s *fakeLiveWhatsAppSession) SendSticker(_ context.Context, request whatsapp.StickerSendRequest) (whatsapp.SendResult, error) {
	if s.stickerSends != nil {
		s.stickerSends <- request
	}
	if s.sendErr != nil {
		return whatsapp.SendResult{}, s.sendErr
	}
	if request.RemoteID == "" {
		request.RemoteID = s.GenerateMessageID()
	}
	return whatsapp.SendResult{
		MessageID: whatsapp.LocalMessageID(request.ChatJID, request.RemoteID),
		RemoteID:  request.RemoteID,
		Status:    "sent",
		Timestamp: time.Now(),
	}, nil
}

func (s *fakeLiveWhatsAppSession) ForwardMessage(_ context.Context, request whatsapp.ForwardMessageRequest) (whatsapp.SendResult, error) {
	if s.forwards != nil {
		s.forwards <- request
	}
	if s.sendErr != nil {
		return whatsapp.SendResult{}, s.sendErr
	}
	if request.RemoteID == "" {
		request.RemoteID = s.GenerateMessageID()
	}
	return whatsapp.SendResult{
		MessageID: whatsapp.LocalMessageID(request.ChatJID, request.RemoteID),
		RemoteID:  request.RemoteID,
		Status:    "sent",
		Timestamp: time.Now(),
	}, nil
}

func (s *fakeLiveWhatsAppSession) MarkRead(_ context.Context, targets []whatsapp.ReadReceiptTarget) error {
	if s.readReceipts != nil {
		s.readReceipts <- targets
	}
	return s.readErr
}

func (s *fakeLiveWhatsAppSession) SendReaction(_ context.Context, request whatsapp.ReactionSendRequest) (whatsapp.SendResult, error) {
	if s.reactions != nil {
		s.reactions <- request
	}
	if s.reactionErr != nil {
		return whatsapp.SendResult{}, s.reactionErr
	}
	remoteID := request.RemoteID
	if remoteID == "" {
		remoteID = s.GenerateMessageID()
	}
	return whatsapp.SendResult{
		MessageID: whatsapp.LocalMessageID(request.ChatJID, remoteID),
		RemoteID:  remoteID,
		Status:    "sent",
		Timestamp: time.Now(),
	}, nil
}

func (s *fakeLiveWhatsAppSession) DeleteMessageForEveryone(_ context.Context, request whatsapp.DeleteForEveryoneRequest) (whatsapp.SendResult, error) {
	if s.deletes != nil {
		s.deletes <- request
	}
	if s.deleteErr != nil {
		return whatsapp.SendResult{}, s.deleteErr
	}
	return whatsapp.SendResult{
		MessageID: whatsapp.LocalMessageID(request.ChatJID, request.TargetRemoteID),
		RemoteID:  request.TargetRemoteID,
		Status:    "deleted",
		Timestamp: time.Now(),
	}, nil
}

func (s *fakeLiveWhatsAppSession) EditMessage(_ context.Context, request whatsapp.EditMessageRequest) (whatsapp.SendResult, error) {
	if s.edits != nil {
		s.edits <- request
	}
	if s.editErr != nil {
		return whatsapp.SendResult{}, s.editErr
	}
	return whatsapp.SendResult{
		MessageID: whatsapp.LocalMessageID(request.ChatJID, request.TargetRemoteID),
		RemoteID:  request.TargetRemoteID,
		Status:    "edited",
		Timestamp: time.Now(),
	}, nil
}

func (s *fakeLiveWhatsAppSession) SendChatPresence(_ context.Context, chatID string, composing bool) error {
	if s.presences != nil {
		s.presences <- fakePresenceRequest{chatID: chatID, composing: composing}
	}
	return s.presenceErr
}

func (s *fakeLiveWhatsAppSession) SubscribePresence(_ context.Context, chatID string) error {
	if s.subscriptions != nil {
		s.subscriptions <- chatID
	}
	return s.presenceErr
}

func (s *fakeLiveWhatsAppSession) DownloadMedia(_ context.Context, descriptor whatsapp.MediaDownloadDescriptor, targetPath string) error {
	if s.downloads != nil {
		s.downloads <- fakeDownloadRequest{descriptor: descriptor, targetPath: targetPath}
	}
	if s.downloadErr != nil {
		return s.downloadErr
	}
	if s.downloadErrByPath != nil {
		if err := s.downloadErrByPath[descriptor.DirectPath]; err != nil {
			return err
		}
	}
	return os.WriteFile(targetPath, []byte("downloaded media"), 0o644)
}

func (s *fakeLiveWhatsAppSession) GetChatAvatar(context.Context, string, string) (whatsapp.ChatAvatarResult, error) {
	return whatsapp.ChatAvatarResult{}, nil
}

func (s *fakeMetadataLiveWhatsAppSession) RefreshChatMetadata(context.Context) ([]whatsapp.Event, error) {
	return s.metadataEvents, s.metadataErr
}

func (n *fakeNotifier) Notify(_ context.Context, note notify.Notification) error {
	if n.notifications != nil {
		n.notifications <- note
	}
	return n.err
}

func (n *fakeNotifier) Report() notify.Report {
	return n.report
}

func TestRunLoginRendersQRAndCompletes(t *testing.T) {
	session := &fakeWhatsAppSession{loginCodes: []string{"qr-code"}}
	var openedPath string
	env := Environment{
		Paths: config.Paths{SessionFile: "/tmp/vimwhat-session.sqlite3"},
		OpenWhatsAppSession: func(_ context.Context, path string) (WhatsAppSession, error) {
			openedPath = path
			return session, nil
		},
		RenderQR: func(w io.Writer, code string) error {
			_, err := w.Write([]byte("rendered:" + code + "\n"))
			return err
		},
	}

	var stdout, stderr bytes.Buffer
	if code := run(env, []string{"login"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run login exit = %d, stderr = %q", code, stderr.String())
	}
	if openedPath != env.Paths.SessionFile {
		t.Fatalf("opened path = %q, want %q", openedPath, env.Paths.SessionFile)
	}
	if !session.loginCalled {
		t.Fatalf("Login was not called")
	}
	if !session.closeCalled {
		t.Fatalf("Close was not called")
	}
	if got := stdout.String(); !strings.Contains(got, "rendered:qr-code") || !strings.Contains(got, "login complete") {
		t.Fatalf("stdout = %q, want rendered QR and completion", got)
	}
}

func TestRunLiveWhatsAppIngestsEventsAndRequestsRefresh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	session := &fakeLiveWhatsAppSession{
		events: make(chan whatsapp.Event, 4),
	}
	env := Environment{
		Paths: config.Paths{SessionFile: "/tmp/vimwhat-session.sqlite3"},
		Store: db,
		OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
			return session, nil
		},
	}
	updates := make(chan ui.LiveUpdate, 16)
	historyRequests := make(chan string, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runLiveWhatsApp(ctx, env, updates, historyRequests, make(chan textSendRequest, 16), make(chan mediaSendRequest, 16), make(chan readReceiptRequest, 16), make(chan reactionRequest, 16), make(chan deleteEveryoneRequest, 16), make(chan editMessageRequest, 16), make(chan forwardMessagesRequest, 16), make(chan presenceRequest, 16), make(chan presenceSubscribeRequest, 16), make(chan mediaDownloadRequest, 16), make(chan stickerSyncRequest, 16), make(chan string, 16), make(chan bool, 16), make(chan []string, 16))
	}()

	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.ConnectionState == ui.ConnectionOnline
	})

	when := time.Unix(1_700_000_000, 0)
	session.events <- whatsapp.Event{
		Kind: whatsapp.EventChatUpsert,
		Chat: whatsapp.ChatEvent{
			ID:            "chat-1",
			JID:           "chat-1@s.whatsapp.net",
			Title:         "Alice",
			Kind:          "direct",
			LastMessageAt: when,
		},
	}
	session.events <- whatsapp.Event{
		Kind: whatsapp.EventMessageUpsert,
		Message: whatsapp.MessageEvent{
			ID:        "chat-1/msg-1",
			RemoteID:  "msg-1",
			ChatID:    "chat-1",
			ChatJID:   "chat-1@s.whatsapp.net",
			Sender:    "Alice",
			SenderJID: "alice@s.whatsapp.net",
			Body:      "hello from live",
			Timestamp: when,
			Status:    "received",
		},
	}

	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Refresh
	})

	messages := waitForStoredMessages(t, db, "chat-1", 1)
	if len(messages) != 1 || messages[0].Body != "hello from live" {
		t.Fatalf("messages = %+v", messages)
	}
	if !session.connectCalled || !session.subscribeCalled {
		t.Fatalf("connect/subscribe called = %v/%v", session.connectCalled, session.subscribeCalled)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runLiveWhatsApp did not stop after cancellation")
	}
}

func TestRunLiveWhatsAppSyncsStickersAfterConnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	session := &fakeLiveWhatsAppSession{
		events:      make(chan whatsapp.Event, 1),
		downloads:   make(chan fakeDownloadRequest, 1),
		stickerSync: []whatsapp.Event{recentStickerSyncEvent("startup-favorite-sticker", "/v/t62.15575-24/startup.enc")},
	}
	env := Environment{
		Paths: config.Paths{
			SessionFile:  "/tmp/vimwhat-session.sqlite3",
			TransientDir: filepath.Join(t.TempDir(), "transient"),
		},
		Store: db,
		OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
			return session, nil
		},
	}
	updates := make(chan ui.LiveUpdate, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runLiveWhatsApp(ctx, env, updates, make(chan string, 16), make(chan textSendRequest, 16), make(chan mediaSendRequest, 16), make(chan readReceiptRequest, 16), make(chan reactionRequest, 16), make(chan deleteEveryoneRequest, 16), make(chan editMessageRequest, 16), make(chan forwardMessagesRequest, 16), make(chan presenceRequest, 16), make(chan presenceSubscribeRequest, 16), make(chan mediaDownloadRequest, 16), make(chan stickerSyncRequest, 16), make(chan string, 16), make(chan bool, 16), make(chan []string, 16))
	}()

	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.ConnectionState == ui.ConnectionOnline
	})
	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Refresh && strings.Contains(update.Status, "synced 1")
	})
	if session.stickerSyncCalls != 1 {
		t.Fatalf("sticker sync calls = %d, want startup sync once", session.stickerSyncCalls)
	}
	download := <-session.downloads
	if download.descriptor.Kind != "sticker" || download.targetPath == "" {
		t.Fatalf("download request = %+v, want startup sticker cache download", download)
	}
	recent, ok, err := db.RecentSticker(ctx, "startup-favorite-sticker")
	if err != nil {
		t.Fatalf("RecentSticker() error = %v", err)
	}
	if !ok || recent.LocalPath == "" || !mediaPathAvailable(recent.LocalPath) {
		t.Fatalf("recent sticker = %+v ok=%v, want cached startup favorite", recent, ok)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runLiveWhatsApp did not stop after cancellation")
	}
}

func TestRunLiveWhatsAppBatchesOfflineSyncRefreshAndNotifications(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	session := &fakeLiveWhatsAppSession{
		events: make(chan whatsapp.Event, 8),
	}
	notifier := &fakeNotifier{
		notifications: make(chan notify.Notification, 4),
		report:        notify.Report{Selected: notify.BackendCommand},
	}
	env := Environment{
		Paths: config.Paths{SessionFile: "/tmp/vimwhat-session.sqlite3"},
		Store: db,
		OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
			return session, nil
		},
		OpenNotifier: func(config.Config) notify.Notifier {
			return notifier
		},
	}

	updates := make(chan ui.LiveUpdate, 16)
	activeChat := make(chan string, 1)
	appFocus := make(chan bool, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runLiveWhatsApp(ctx, env, updates, make(chan string, 16), make(chan textSendRequest, 16), make(chan mediaSendRequest, 16), make(chan readReceiptRequest, 16), make(chan reactionRequest, 16), make(chan deleteEveryoneRequest, 16), make(chan editMessageRequest, 16), make(chan forwardMessagesRequest, 16), make(chan presenceRequest, 16), make(chan presenceSubscribeRequest, 16), make(chan mediaDownloadRequest, 16), make(chan stickerSyncRequest, 16), activeChat, appFocus, make(chan []string, 16))
	}()

	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.ConnectionState == ui.ConnectionOnline
	})
	activeChat <- "other-chat"
	appFocus <- true

	session.events <- whatsapp.Event{
		Kind: whatsapp.EventOfflineSync,
		Offline: whatsapp.OfflineSyncEvent{
			Active:   true,
			Total:    2,
			Messages: 1,
			Receipts: 1,
		},
	}
	start := waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Sync != nil && update.Sync.Active
	})
	if start.Sync.Total != 2 || start.Sync.Messages != 1 || start.Refresh {
		t.Fatalf("offline sync start update = %+v", start)
	}

	when := time.Unix(1_700_000_000, 0)
	session.events <- whatsapp.Event{
		Kind: whatsapp.EventChatUpsert,
		Chat: whatsapp.ChatEvent{
			ID:            "chat-1",
			JID:           "chat-1@s.whatsapp.net",
			Title:         "Alice",
			Kind:          "direct",
			LastMessageAt: when,
		},
	}
	session.events <- whatsapp.Event{
		Kind: whatsapp.EventMessageUpsert,
		Message: whatsapp.MessageEvent{
			ID:                  "chat-1/msg-1",
			RemoteID:            "msg-1",
			ChatID:              "chat-1",
			ChatJID:             "chat-1@s.whatsapp.net",
			Sender:              "Alice",
			SenderJID:           "alice@s.whatsapp.net",
			Body:                "missed message",
			NotificationPreview: "missed message",
			Timestamp:           when,
			Status:              "received",
		},
	}
	_ = waitForStoredMessages(t, db, "chat-1", 1)
	assertNoLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Refresh
	})

	session.events <- whatsapp.Event{
		Kind: whatsapp.EventOfflineSync,
		Offline: whatsapp.OfflineSyncEvent{
			Completed: true,
			Total:     2,
			Processed: 2,
		},
	}
	completed := waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Sync != nil && update.Sync.Completed
	})
	if !completed.Refresh || completed.Sync.Processed != 2 || completed.Sync.Total != 2 {
		t.Fatalf("offline sync completed update = %+v", completed)
	}
	assertNotificationCount(t, notifier.notifications, 0)

	session.events <- whatsapp.Event{
		Kind: whatsapp.EventChatUpsert,
		Chat: whatsapp.ChatEvent{
			ID:            "chat-2",
			JID:           "chat-2@s.whatsapp.net",
			Title:         "Bob",
			Kind:          "direct",
			LastMessageAt: when.Add(time.Minute),
		},
	}
	session.events <- whatsapp.Event{
		Kind: whatsapp.EventMessageUpsert,
		Message: whatsapp.MessageEvent{
			ID:                  "chat-2/msg-1",
			RemoteID:            "msg-2",
			ChatID:              "chat-2",
			ChatJID:             "chat-2@s.whatsapp.net",
			Sender:              "Bob",
			SenderJID:           "bob@s.whatsapp.net",
			Body:                "normal live message",
			NotificationPreview: "normal live message",
			Timestamp:           when.Add(time.Minute),
			Status:              "received",
		},
	}
	note := waitForNotification(t, notifier.notifications)
	if note.Title != "Bob" || note.Body != "normal live message" {
		t.Fatalf("notification after sync = %+v, want Bob/normal live message", note)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runLiveWhatsApp did not stop after cancellation")
	}
}

func TestRunLiveWhatsAppResumesNotificationsAfterOfflineSyncTimeout(t *testing.T) {
	prevInactivity := offlineSyncInactivity
	prevMaxDuration := offlineSyncMaxDuration
	prevProgressEvery := offlineSyncProgressEvery
	offlineSyncInactivity = time.Hour
	offlineSyncMaxDuration = 250 * time.Millisecond
	offlineSyncProgressEvery = time.Hour
	t.Cleanup(func() {
		offlineSyncInactivity = prevInactivity
		offlineSyncMaxDuration = prevMaxDuration
		offlineSyncProgressEvery = prevProgressEvery
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	session := &fakeLiveWhatsAppSession{
		events: make(chan whatsapp.Event, 8),
	}
	notifier := &fakeNotifier{
		notifications: make(chan notify.Notification, 4),
		report:        notify.Report{Selected: notify.BackendCommand},
	}
	env := Environment{
		Paths: config.Paths{SessionFile: "/tmp/vimwhat-session.sqlite3"},
		Store: db,
		OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
			return session, nil
		},
		OpenNotifier: func(config.Config) notify.Notifier {
			return notifier
		},
	}

	updates := make(chan ui.LiveUpdate, 16)
	activeChat := make(chan string, 1)
	appFocus := make(chan bool, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runLiveWhatsApp(ctx, env, updates, make(chan string, 16), make(chan textSendRequest, 16), make(chan mediaSendRequest, 16), make(chan readReceiptRequest, 16), make(chan reactionRequest, 16), make(chan deleteEveryoneRequest, 16), make(chan editMessageRequest, 16), make(chan forwardMessagesRequest, 16), make(chan presenceRequest, 16), make(chan presenceSubscribeRequest, 16), make(chan mediaDownloadRequest, 16), make(chan stickerSyncRequest, 16), activeChat, appFocus, make(chan []string, 16))
	}()

	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.ConnectionState == ui.ConnectionOnline
	})
	activeChat <- "other-chat"
	appFocus <- true

	when := time.Unix(1_700_000_000, 0)
	session.events <- whatsapp.Event{
		Kind: whatsapp.EventOfflineSync,
		Offline: whatsapp.OfflineSyncEvent{
			Active:   true,
			Total:    10,
			Messages: 1,
		},
	}
	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Sync != nil && update.Sync.Active
	})

	session.events <- whatsapp.Event{
		Kind: whatsapp.EventChatUpsert,
		Chat: whatsapp.ChatEvent{
			ID:            "chat-1",
			JID:           "chat-1@s.whatsapp.net",
			Title:         "Alice",
			Kind:          "direct",
			LastMessageAt: when,
		},
	}
	session.events <- whatsapp.Event{
		Kind: whatsapp.EventMessageUpsert,
		Message: whatsapp.MessageEvent{
			ID:                  "chat-1/msg-1",
			RemoteID:            "msg-1",
			ChatID:              "chat-1",
			ChatJID:             "chat-1@s.whatsapp.net",
			Sender:              "Alice",
			SenderJID:           "alice@s.whatsapp.net",
			Body:                "suppressed during stuck sync",
			NotificationPreview: "suppressed during stuck sync",
			Timestamp:           when,
			Status:              "received",
		},
	}
	_ = waitForStoredMessages(t, db, "chat-1", 1)
	timedOut := waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Sync != nil && update.Sync.Completed && strings.Contains(update.Status, "timed out")
	})
	if !timedOut.Refresh {
		t.Fatalf("timeout update = %+v, want refresh", timedOut)
	}
	assertNotificationCount(t, notifier.notifications, 0)

	session.events <- whatsapp.Event{
		Kind: whatsapp.EventChatUpsert,
		Chat: whatsapp.ChatEvent{
			ID:            "chat-2",
			JID:           "chat-2@s.whatsapp.net",
			Title:         "Bob",
			Kind:          "direct",
			LastMessageAt: when.Add(time.Minute),
		},
	}
	session.events <- whatsapp.Event{
		Kind: whatsapp.EventMessageUpsert,
		Message: whatsapp.MessageEvent{
			ID:                  "chat-2/msg-1",
			RemoteID:            "msg-2",
			ChatID:              "chat-2",
			ChatJID:             "chat-2@s.whatsapp.net",
			Sender:              "Bob",
			SenderJID:           "bob@s.whatsapp.net",
			Body:                "notify after timeout",
			NotificationPreview: "notify after timeout",
			Timestamp:           when.Add(time.Minute),
			Status:              "received",
		},
	}
	note := waitForNotification(t, notifier.notifications)
	if note.Title != "Bob" || note.Body != "notify after timeout" {
		t.Fatalf("notification after timeout = %+v, want Bob/notify after timeout", note)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runLiveWhatsApp did not stop after cancellation")
	}
}

func TestHandleDeleteEveryoneRequestSendsRemoteRevokeBeforeLocalDelete(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.UpsertChat(ctx, store.Chat{ID: "12345@s.whatsapp.net", JID: "12345@s.whatsapp.net", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	message := store.Message{
		ID:         "12345@s.whatsapp.net/remote-1",
		RemoteID:   "remote-1",
		ChatID:     "12345@s.whatsapp.net",
		ChatJID:    "12345@s.whatsapp.net",
		Sender:     "me",
		SenderJID:  "me",
		Body:       "delete me",
		Timestamp:  time.Unix(1_700_000_000, 0),
		IsOutgoing: true,
		Status:     "sent",
	}
	if err := db.AddMessage(ctx, message); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{deletes: make(chan whatsapp.DeleteForEveryoneRequest, 1)}
	updates := make(chan ui.LiveUpdate, 4)
	result := make(chan deleteEveryoneResult, 1)
	handleDeleteEveryoneRequest(ctx, db, session, updates, nil, true, deleteEveryoneRequest{
		Message: message,
		Result:  result,
	})

	gotResult := <-result
	if gotResult.Err != nil || gotResult.MessageID != message.ID {
		t.Fatalf("delete result = %+v", gotResult)
	}
	gotRequest := <-session.deletes
	if gotRequest.ChatJID != "12345@s.whatsapp.net" || gotRequest.TargetRemoteID != "remote-1" {
		t.Fatalf("delete request = %+v", gotRequest)
	}
	messages, err := db.ListMessages(ctx, message.ChatID, 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages after delete = %+v, want none", messages)
	}
}

func TestHandleEditMessageRequestUsesProtocolAndUpdatesLocalMessage(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.UpsertChat(ctx, store.Chat{ID: "12345@s.whatsapp.net", JID: "12345@s.whatsapp.net", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	message := store.Message{
		ID:         "12345@s.whatsapp.net/remote-1",
		RemoteID:   "remote-1",
		ChatID:     "12345@s.whatsapp.net",
		ChatJID:    "12345@s.whatsapp.net",
		Sender:     "me",
		SenderJID:  "me",
		Body:       "old text",
		Timestamp:  time.Unix(1_700_000_000, 0),
		IsOutgoing: true,
		Status:     "sent",
	}
	if err := db.AddMessage(ctx, message); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{edits: make(chan whatsapp.EditMessageRequest, 1)}
	updates := make(chan ui.LiveUpdate, 4)
	result := make(chan editMessageResult, 1)
	handleEditMessageRequest(ctx, db, session, updates, nil, true, editMessageRequest{
		Message: message,
		Body:    "new text",
		Result:  result,
	})

	gotResult := <-result
	if gotResult.Err != nil || gotResult.MessageID != message.ID || gotResult.Body != "new text" {
		t.Fatalf("edit result = %+v", gotResult)
	}
	gotRequest := <-session.edits
	if gotRequest.ChatJID != "12345@s.whatsapp.net" || gotRequest.TargetRemoteID != "remote-1" || gotRequest.Body != "new text" {
		t.Fatalf("edit request = %+v", gotRequest)
	}
	messages, err := db.ListMessages(ctx, message.ChatID, 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "new text" || messages[0].EditedAt.IsZero() {
		t.Fatalf("messages after edit = %+v", messages)
	}
}

func TestHandleForwardMessagesRequestQueuesForwardedMessages(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	sourceChat := store.Chat{ID: "11111@s.whatsapp.net", JID: "11111@s.whatsapp.net", Title: "Alice"}
	recipient := store.Chat{ID: "22222@s.whatsapp.net", JID: "22222@s.whatsapp.net", Title: "Bob"}
	for _, chat := range []store.Chat{sourceChat, recipient} {
		if err := db.UpsertChat(ctx, chat); err != nil {
			t.Fatalf("UpsertChat(%s) error = %v", chat.ID, err)
		}
	}
	source := store.Message{
		ID:        "11111@s.whatsapp.net/source-1",
		RemoteID:  "source-1",
		ChatID:    sourceChat.ID,
		ChatJID:   sourceChat.JID,
		Sender:    "Alice",
		SenderJID: "11111@s.whatsapp.net",
		Body:      "forward this",
		Timestamp: time.Unix(1_700_000_000, 0),
		Status:    "sent",
	}
	if err := db.AddMessage(ctx, source); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}
	payload, err := proto.Marshal(&waE2E.Message{Conversation: proto.String("forward this")})
	if err != nil {
		t.Fatalf("marshal forward payload error = %v", err)
	}
	if err := db.UpsertMessagePayload(ctx, store.MessagePayload{
		MessageID: source.ID,
		Payload:   payload,
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertMessagePayload() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{
		forwards:    make(chan whatsapp.ForwardMessageRequest, 1),
		generatedID: "forward-1",
	}
	updates := make(chan ui.LiveUpdate, 4)
	result := make(chan forwardMessagesResult, 1)
	handleForwardMessagesRequest(ctx, db, session, updates, nil, true, forwardMessagesRequest{
		Messages:   []store.Message{source},
		Recipients: []store.Chat{recipient},
		Result:     result,
	})

	gotResult := <-result
	if gotResult.Err != nil || gotResult.Sent != 1 || gotResult.Skipped != 0 || gotResult.Failed != 0 {
		t.Fatalf("forward result = %+v", gotResult)
	}
	gotRequest := <-session.forwards
	if gotRequest.ChatJID != recipient.JID || gotRequest.RemoteID != "forward-1" || !bytes.Equal(gotRequest.Payload, payload) || !gotRequest.MarkForwarded {
		t.Fatalf("forward request = %+v", gotRequest)
	}
	messages, err := db.ListMessages(ctx, recipient.ID, 10)
	if err != nil {
		t.Fatalf("ListMessages(recipient) error = %v", err)
	}
	if len(messages) != 1 || messages[0].Body != source.Body || messages[0].Status != "sent" || !messages[0].IsOutgoing {
		t.Fatalf("recipient messages = %+v", messages)
	}
}

func TestHandleForwardMessagesRequestDoesNotMarkOutgoingSourcesForwarded(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	sourceChat := store.Chat{ID: "11111@s.whatsapp.net", JID: "11111@s.whatsapp.net", Title: "Alice"}
	recipient := store.Chat{ID: "22222@s.whatsapp.net", JID: "22222@s.whatsapp.net", Title: "Bob"}
	for _, chat := range []store.Chat{sourceChat, recipient} {
		if err := db.UpsertChat(ctx, chat); err != nil {
			t.Fatalf("UpsertChat(%s) error = %v", chat.ID, err)
		}
	}
	source := store.Message{
		ID:         "11111@s.whatsapp.net/source-1",
		RemoteID:   "source-1",
		ChatID:     sourceChat.ID,
		ChatJID:    sourceChat.JID,
		Sender:     "me",
		SenderJID:  "me",
		Body:       "send again",
		Timestamp:  time.Unix(1_700_000_000, 0),
		IsOutgoing: true,
		Status:     "sent",
	}
	if err := db.AddMessage(ctx, source); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}
	payload, err := proto.Marshal(&waE2E.Message{Conversation: proto.String("send again")})
	if err != nil {
		t.Fatalf("marshal forward payload error = %v", err)
	}
	if err := db.UpsertMessagePayload(ctx, store.MessagePayload{
		MessageID: source.ID,
		Payload:   payload,
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertMessagePayload() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{
		forwards:    make(chan whatsapp.ForwardMessageRequest, 1),
		generatedID: "forward-1",
	}
	result := make(chan forwardMessagesResult, 1)
	handleForwardMessagesRequest(ctx, db, session, make(chan ui.LiveUpdate, 4), nil, true, forwardMessagesRequest{
		Messages:   []store.Message{source},
		Recipients: []store.Chat{recipient},
		Result:     result,
	})

	gotResult := <-result
	if gotResult.Err != nil || gotResult.Sent != 1 || gotResult.Skipped != 0 || gotResult.Failed != 0 {
		t.Fatalf("forward result = %+v", gotResult)
	}
	gotRequest := <-session.forwards
	if gotRequest.MarkForwarded {
		t.Fatalf("outgoing source forward request was marked forwarded: %+v", gotRequest)
	}
}

func TestRunLiveWhatsAppRefreshesChatMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.UpsertChat(ctx, store.Chat{
		ID:          "12345-678@g.us",
		JID:         "12345-678@g.us",
		Title:       "12345-678",
		TitleSource: store.ChatTitleSourceJID,
		Kind:        "group",
	}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}

	session := &fakeMetadataLiveWhatsAppSession{
		fakeLiveWhatsAppSession: &fakeLiveWhatsAppSession{
			events: make(chan whatsapp.Event, 4),
		},
		metadataEvents: []whatsapp.Event{{
			Kind: whatsapp.EventChatUpsert,
			Chat: whatsapp.ChatEvent{
				ID:          "12345-678@g.us",
				JID:         "12345-678@g.us",
				Title:       "Project Group",
				TitleSource: store.ChatTitleSourceGroupSubject,
				Kind:        "group",
			},
		}},
	}
	env := Environment{
		Paths: config.Paths{SessionFile: "/tmp/vimwhat-session.sqlite3"},
		Store: db,
		OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
			return session, nil
		},
	}
	updates := make(chan ui.LiveUpdate, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runLiveWhatsApp(ctx, env, updates, make(chan string, 16), make(chan textSendRequest, 16), make(chan mediaSendRequest, 16), make(chan readReceiptRequest, 16), make(chan reactionRequest, 16), make(chan deleteEveryoneRequest, 16), make(chan editMessageRequest, 16), make(chan forwardMessagesRequest, 16), make(chan presenceRequest, 16), make(chan presenceSubscribeRequest, 16), make(chan mediaDownloadRequest, 16), make(chan stickerSyncRequest, 16), make(chan string, 16), make(chan bool, 16), make(chan []string, 16))
	}()

	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Refresh
	})
	chats, err := db.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].Title != "Project Group" || chats[0].TitleSource != store.ChatTitleSourceGroupSubject {
		t.Fatalf("chats after metadata = %+v, want Project Group/group_subject", chats)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runLiveWhatsApp did not stop after cancellation")
	}
}

func TestRunLiveWhatsAppNotifiesInactiveIncomingMessageWhileFocused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	session := &fakeLiveWhatsAppSession{
		events: make(chan whatsapp.Event, 4),
	}
	notifier := &fakeNotifier{
		notifications: make(chan notify.Notification, 2),
		report:        notify.Report{Selected: notify.BackendCommand},
	}
	env := Environment{
		Paths: config.Paths{SessionFile: "/tmp/vimwhat-session.sqlite3"},
		Store: db,
		OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
			return session, nil
		},
		OpenNotifier: func(config.Config) notify.Notifier {
			return notifier
		},
	}

	updates := make(chan ui.LiveUpdate, 16)
	activeChat := make(chan string, 2)
	appFocus := make(chan bool, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runLiveWhatsApp(ctx, env, updates, make(chan string, 16), make(chan textSendRequest, 16), make(chan mediaSendRequest, 16), make(chan readReceiptRequest, 16), make(chan reactionRequest, 16), make(chan deleteEveryoneRequest, 16), make(chan editMessageRequest, 16), make(chan forwardMessagesRequest, 16), make(chan presenceRequest, 16), make(chan presenceSubscribeRequest, 16), make(chan mediaDownloadRequest, 16), make(chan stickerSyncRequest, 16), activeChat, appFocus, make(chan []string, 16))
	}()

	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.ConnectionState == ui.ConnectionOnline
	})
	activeChat <- "other-chat"
	appFocus <- true

	when := time.Unix(1_700_000_000, 0)
	session.events <- whatsapp.Event{
		Kind: whatsapp.EventChatUpsert,
		Chat: whatsapp.ChatEvent{
			ID:            "chat-1",
			JID:           "chat-1@s.whatsapp.net",
			Title:         "Alice",
			Kind:          "direct",
			LastMessageAt: when,
		},
	}
	session.events <- whatsapp.Event{
		Kind: whatsapp.EventMessageUpsert,
		Message: whatsapp.MessageEvent{
			ID:                  "chat-1/msg-1",
			RemoteID:            "msg-1",
			ChatID:              "chat-1",
			ChatJID:             "chat-1@s.whatsapp.net",
			Sender:              "Alice",
			SenderJID:           "alice@s.whatsapp.net",
			Body:                "hello from live",
			NotificationPreview: "hello from live",
			Timestamp:           when,
			Status:              "received",
		},
	}

	note := waitForNotification(t, notifier.notifications)
	if note.Title != "Alice" || note.Body != "hello from live" {
		t.Fatalf("notification = %+v, want Alice/hello from live", note)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runLiveWhatsApp did not stop after cancellation")
	}
}

func TestRunLiveWhatsAppSuppressesActiveChatNotificationsOnlyWhenFocused(t *testing.T) {
	tests := []struct {
		name              string
		focusKnown        bool
		appFocused        bool
		wantNotifications int
	}{
		{
			name:              "focused window",
			focusKnown:        true,
			appFocused:        true,
			wantNotifications: 0,
		},
		{
			name:              "blurred window",
			focusKnown:        true,
			appFocused:        false,
			wantNotifications: 1,
		},
		{
			name:              "unknown focus state",
			wantNotifications: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
			if err != nil {
				t.Fatalf("store.Open() error = %v", err)
			}
			t.Cleanup(func() {
				_ = db.Close()
			})

			session := &fakeLiveWhatsAppSession{
				events: make(chan whatsapp.Event, 8),
			}
			notifier := &fakeNotifier{
				notifications: make(chan notify.Notification, 2),
				report:        notify.Report{Selected: notify.BackendCommand},
			}
			env := Environment{
				Paths: config.Paths{SessionFile: "/tmp/vimwhat-session.sqlite3"},
				Store: db,
				OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
					return session, nil
				},
				OpenNotifier: func(config.Config) notify.Notifier {
					return notifier
				},
			}

			updates := make(chan ui.LiveUpdate, 16)
			activeChat := make(chan string, 2)
			appFocus := make(chan bool, 2)
			done := make(chan struct{})
			go func() {
				defer close(done)
				runLiveWhatsApp(ctx, env, updates, make(chan string, 16), make(chan textSendRequest, 16), make(chan mediaSendRequest, 16), make(chan readReceiptRequest, 16), make(chan reactionRequest, 16), make(chan deleteEveryoneRequest, 16), make(chan editMessageRequest, 16), make(chan forwardMessagesRequest, 16), make(chan presenceRequest, 16), make(chan presenceSubscribeRequest, 16), make(chan mediaDownloadRequest, 16), make(chan stickerSyncRequest, 16), activeChat, appFocus, make(chan []string, 16))
			}()

			waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
				return update.ConnectionState == ui.ConnectionOnline
			})
			activeChat <- "chat-1"
			if test.focusKnown {
				appFocus <- test.appFocused
			}

			when := time.Unix(1_700_000_000, 0)
			session.events <- whatsapp.Event{
				Kind: whatsapp.EventChatUpsert,
				Chat: whatsapp.ChatEvent{
					ID:            "chat-1",
					JID:           "chat-1@s.whatsapp.net",
					Title:         "Alice",
					Kind:          "direct",
					LastMessageAt: when,
				},
			}
			session.events <- whatsapp.Event{
				Kind: whatsapp.EventMessageUpsert,
				Message: whatsapp.MessageEvent{
					ID:                  "chat-1/msg-1",
					RemoteID:            "msg-1",
					ChatID:              "chat-1",
					ChatJID:             "chat-1@s.whatsapp.net",
					Sender:              "Alice",
					SenderJID:           "alice@s.whatsapp.net",
					Body:                "hello",
					NotificationPreview: "hello",
					Timestamp:           when,
					Status:              "received",
				},
			}

			waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
				return update.Refresh
			})

			assertNotificationCount(t, notifier.notifications, test.wantNotifications)

			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("runLiveWhatsApp did not stop after cancellation")
			}
		})
	}
}

func TestRunLiveWhatsAppSuppressesNotificationsForMutedAndDuplicateMessages(t *testing.T) {
	tests := []struct {
		name              string
		activeChatID      string
		chatMuted         bool
		message           whatsapp.MessageEvent
		repeatMessage     bool
		wantNotifications int
	}{
		{
			name:              "muted chat",
			activeChatID:      "other-chat",
			chatMuted:         true,
			message:           whatsapp.MessageEvent{ID: "chat-1/msg-1", RemoteID: "msg-1", ChatID: "chat-1", ChatJID: "chat-1@s.whatsapp.net", Sender: "Alice", SenderJID: "alice@s.whatsapp.net", Body: "hello", NotificationPreview: "hello", Timestamp: time.Unix(1_700_000_000, 0), Status: "received"},
			wantNotifications: 0,
		},
		{
			name:              "duplicate message",
			activeChatID:      "other-chat",
			message:           whatsapp.MessageEvent{ID: "chat-1/msg-1", RemoteID: "msg-1", ChatID: "chat-1", ChatJID: "chat-1@s.whatsapp.net", Sender: "Alice", SenderJID: "alice@s.whatsapp.net", Body: "hello", NotificationPreview: "hello", Timestamp: time.Unix(1_700_000_000, 0), Status: "received"},
			repeatMessage:     true,
			wantNotifications: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
			if err != nil {
				t.Fatalf("store.Open() error = %v", err)
			}
			t.Cleanup(func() {
				_ = db.Close()
			})

			session := &fakeLiveWhatsAppSession{
				events: make(chan whatsapp.Event, 8),
			}
			notifier := &fakeNotifier{
				notifications: make(chan notify.Notification, 4),
				report:        notify.Report{Selected: notify.BackendCommand},
			}
			env := Environment{
				Paths: config.Paths{SessionFile: "/tmp/vimwhat-session.sqlite3"},
				Store: db,
				OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
					return session, nil
				},
				OpenNotifier: func(config.Config) notify.Notifier {
					return notifier
				},
			}

			updates := make(chan ui.LiveUpdate, 16)
			activeChat := make(chan string, 2)
			appFocus := make(chan bool, 2)
			done := make(chan struct{})
			go func() {
				defer close(done)
				runLiveWhatsApp(ctx, env, updates, make(chan string, 16), make(chan textSendRequest, 16), make(chan mediaSendRequest, 16), make(chan readReceiptRequest, 16), make(chan reactionRequest, 16), make(chan deleteEveryoneRequest, 16), make(chan editMessageRequest, 16), make(chan forwardMessagesRequest, 16), make(chan presenceRequest, 16), make(chan presenceSubscribeRequest, 16), make(chan mediaDownloadRequest, 16), make(chan stickerSyncRequest, 16), activeChat, appFocus, make(chan []string, 16))
			}()

			waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
				return update.ConnectionState == ui.ConnectionOnline
			})
			activeChat <- test.activeChatID
			appFocus <- true

			when := test.message.Timestamp
			session.events <- whatsapp.Event{
				Kind: whatsapp.EventChatUpsert,
				Chat: whatsapp.ChatEvent{
					ID:            "chat-1",
					JID:           "chat-1@s.whatsapp.net",
					Title:         "Alice",
					Kind:          "direct",
					Muted:         test.chatMuted,
					LastMessageAt: when,
				},
			}
			session.events <- whatsapp.Event{
				Kind:    whatsapp.EventMessageUpsert,
				Message: test.message,
			}
			if test.repeatMessage {
				session.events <- whatsapp.Event{
					Kind:    whatsapp.EventMessageUpsert,
					Message: test.message,
				}
			}

			waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
				return update.Refresh
			})
			if test.repeatMessage {
				waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
					return update.Refresh
				})
			}

			assertNotificationCount(t, notifier.notifications, test.wantNotifications)

			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("runLiveWhatsApp did not stop after cancellation")
			}
		})
	}
}

func TestRunLiveWhatsAppSuppressesHistoricalOutgoingAndReactionNotifications(t *testing.T) {
	tests := []struct {
		name  string
		event whatsapp.Event
	}{
		{
			name: "historical message",
			event: whatsapp.Event{
				Kind: whatsapp.EventMessageUpsert,
				Message: whatsapp.MessageEvent{
					ID:                  "chat-1/old-1",
					RemoteID:            "old-1",
					ChatID:              "chat-1",
					ChatJID:             "chat-1@s.whatsapp.net",
					Sender:              "Alice",
					SenderJID:           "alice@s.whatsapp.net",
					Body:                "old",
					NotificationPreview: "old",
					Timestamp:           time.Unix(1_700_000_000, 0),
					Status:              "received",
					Historical:          true,
				},
			},
		},
		{
			name: "outgoing message",
			event: whatsapp.Event{
				Kind: whatsapp.EventMessageUpsert,
				Message: whatsapp.MessageEvent{
					ID:                  "chat-1/out-1",
					RemoteID:            "out-1",
					ChatID:              "chat-1",
					ChatJID:             "chat-1@s.whatsapp.net",
					Sender:              "me",
					SenderJID:           "me",
					Body:                "sent",
					NotificationPreview: "sent",
					Timestamp:           time.Unix(1_700_000_000, 0),
					Status:              "sent",
					IsOutgoing:          true,
				},
			},
		},
		{
			name: "reaction update",
			event: whatsapp.Event{
				Kind: whatsapp.EventReactionUpdate,
				Reaction: whatsapp.ReactionEvent{
					MessageID: "chat-1/msg-1",
					ChatID:    "chat-1",
					SenderJID: "alice@s.whatsapp.net",
					Emoji:     "👍",
					Timestamp: time.Unix(1_700_000_000, 0),
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
			if err != nil {
				t.Fatalf("store.Open() error = %v", err)
			}
			t.Cleanup(func() {
				_ = db.Close()
			})

			session := &fakeLiveWhatsAppSession{
				events: make(chan whatsapp.Event, 4),
			}
			notifier := &fakeNotifier{
				notifications: make(chan notify.Notification, 2),
				report:        notify.Report{Selected: notify.BackendCommand},
			}
			env := Environment{
				Paths: config.Paths{SessionFile: "/tmp/vimwhat-session.sqlite3"},
				Store: db,
				OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
					return session, nil
				},
				OpenNotifier: func(config.Config) notify.Notifier {
					return notifier
				},
			}

			updates := make(chan ui.LiveUpdate, 16)
			done := make(chan struct{})
			go func() {
				defer close(done)
				runLiveWhatsApp(ctx, env, updates, make(chan string, 16), make(chan textSendRequest, 16), make(chan mediaSendRequest, 16), make(chan readReceiptRequest, 16), make(chan reactionRequest, 16), make(chan deleteEveryoneRequest, 16), make(chan editMessageRequest, 16), make(chan forwardMessagesRequest, 16), make(chan presenceRequest, 16), make(chan presenceSubscribeRequest, 16), make(chan mediaDownloadRequest, 16), make(chan stickerSyncRequest, 16), make(chan string, 16), make(chan bool, 16), make(chan []string, 16))
			}()

			waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
				return update.ConnectionState == ui.ConnectionOnline
			})

			session.events <- whatsapp.Event{
				Kind: whatsapp.EventChatUpsert,
				Chat: whatsapp.ChatEvent{
					ID:    "chat-1",
					JID:   "chat-1@s.whatsapp.net",
					Title: "Alice",
					Kind:  "direct",
				},
			}
			session.events <- test.event

			assertNotificationCount(t, notifier.notifications, 0)

			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("runLiveWhatsApp did not stop after cancellation")
			}
		})
	}
}

func TestHandleHistoryRequestUsesOldestAnchorAndCoalesces(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.UpsertChat(ctx, store.Chat{ID: "chat-1", JID: "chat-1@s.whatsapp.net", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	when := time.Unix(1_700_000_000, 0)
	if err := db.AddMessage(ctx, store.Message{
		ID:        "chat-1/oldest",
		RemoteID:  "oldest",
		ChatID:    "chat-1",
		ChatJID:   "chat-1@s.whatsapp.net",
		Sender:    "Alice",
		Body:      "oldest local",
		Timestamp: when,
	}); err != nil {
		t.Fatalf("AddMessage(oldest) error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{historyRequests: make(chan fakeHistoryRequest, 2)}
	updates := make(chan ui.LiveUpdate, 4)
	inflight := map[string]time.Time{}
	handleHistoryRequest(ctx, db, session, updates, inflight, true, "chat-1")

	request := <-session.historyRequests
	if request.limit != historyPageSize ||
		request.anchor.ChatJID != "chat-1@s.whatsapp.net" ||
		request.anchor.MessageID != "oldest" ||
		!request.anchor.Timestamp.Equal(when) {
		t.Fatalf("history request = %+v, want oldest anchor and page size", request)
	}

	handleHistoryRequest(ctx, db, session, updates, inflight, true, "chat-1")
	select {
	case duplicate := <-session.historyRequests:
		t.Fatalf("duplicate history request sent: %+v", duplicate)
	default:
	}
	update := waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return strings.Contains(update.Status, "already loading")
	})
	if update.Status == "" {
		t.Fatal("duplicate request did not produce status")
	}
}

func TestHandleHistoryRequestIgnoresLegacyTrueCursor(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.UpsertChat(ctx, store.Chat{ID: "chat-1", JID: "chat-1@s.whatsapp.net", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	when := time.Unix(1_700_000_000, 0)
	if err := db.AddMessage(ctx, store.Message{
		ID:        "chat-1/oldest",
		RemoteID:  "oldest",
		ChatID:    "chat-1",
		ChatJID:   "chat-1@s.whatsapp.net",
		Sender:    "Alice",
		Body:      "oldest local",
		Timestamp: when,
	}); err != nil {
		t.Fatalf("AddMessage(oldest) error = %v", err)
	}
	if err := db.SetSyncCursor(ctx, whatsapp.HistoryExhaustedCursor("chat-1"), "true"); err != nil {
		t.Fatalf("SetSyncCursor() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{historyRequests: make(chan fakeHistoryRequest, 1)}
	handleHistoryRequest(ctx, db, session, make(chan ui.LiveUpdate, 4), map[string]time.Time{}, true, "chat-1")

	select {
	case request := <-session.historyRequests:
		if request.anchor.MessageID != "oldest" {
			t.Fatalf("history request anchor = %+v, want oldest", request.anchor)
		}
	case <-time.After(time.Second):
		t.Fatal("legacy true cursor blocked history request")
	}
}

func TestHandleTextSendRequestPersistsSendingThenMarksSent(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	chatJID := "12345@s.whatsapp.net"
	if err := db.UpsertChat(ctx, store.Chat{ID: chatJID, JID: chatJID, Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{
		generatedID: "remote-1",
		sends:       make(chan fakeSendRequest, 1),
	}
	result := make(chan textSendQueuedResult, 1)
	updates := make(chan ui.LiveUpdate, 4)
	handleTextSendRequest(ctx, db, session, updates, nil, true, textSendRequest{
		ChatID: chatJID,
		Body:   "hello live @Ana",
		Mentions: []store.MessageMention{{
			JID:         "222@s.whatsapp.net",
			DisplayName: "Ana",
			StartByte:   11,
			EndByte:     15,
		}},
		Result: result,
	})

	queued := <-result
	if queued.Err != nil {
		t.Fatalf("queued send error = %v", queued.Err)
	}
	if queued.Message.ID != whatsapp.LocalMessageID(chatJID, "remote-1") ||
		queued.Message.RemoteID != "remote-1" ||
		queued.Message.Status != "sending" {
		t.Fatalf("queued message = %+v, want generated remote id and sending status", queued.Message)
	}

	request := <-session.sends
	if request.request.ChatJID != chatJID || request.request.Body != "hello live @222" || request.request.RemoteID != "remote-1" {
		t.Fatalf("send request = %+v, want jid/body/remote id", request)
	}
	if len(request.request.MentionedJIDs) != 1 || request.request.MentionedJIDs[0] != "222@s.whatsapp.net" {
		t.Fatalf("send mentions = %+v, want participant", request.request.MentionedJIDs)
	}
	messages := waitForStoredMessages(t, db, chatJID, 1)
	if len(messages) != 1 || messages[0].ID != queued.Message.ID || messages[0].Status != "sent" {
		t.Fatalf("stored messages = %+v, want one sent queued message", messages)
	}
	if len(messages[0].Mentions) != 1 || messages[0].Mentions[0].JID != "222@s.whatsapp.net" {
		t.Fatalf("stored mentions = %+v, want participant", messages[0].Mentions)
	}
	if messages[0].Body != "hello live @Ana" {
		t.Fatalf("stored body = %q, want friendly mention text", messages[0].Body)
	}
	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Refresh && strings.Contains(update.Status, "sent")
	})
}

func TestHandleTextSendRequestFailureMarksFailedAndRestoresDraft(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	chatJID := "12345@s.whatsapp.net"
	if err := db.UpsertChat(ctx, store.Chat{ID: chatJID, JID: chatJID, Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{
		generatedID: "remote-1",
		sendErr:     errors.New("boom"),
	}
	result := make(chan textSendQueuedResult, 1)
	updates := make(chan ui.LiveUpdate, 4)
	handleTextSendRequest(ctx, db, session, updates, nil, true, textSendRequest{
		ChatID: chatJID,
		Body:   "retry me",
		Result: result,
	})

	queued := <-result
	if queued.Err != nil {
		t.Fatalf("queued send error = %v", queued.Err)
	}
	messages := waitForStoredMessages(t, db, chatJID, 1)
	if messages[0].Status != "failed" {
		t.Fatalf("stored message status = %q, want failed", messages[0].Status)
	}
	draft, err := db.Draft(ctx, chatJID)
	if err != nil {
		t.Fatalf("Draft() error = %v", err)
	}
	if draft != "retry me" {
		t.Fatalf("draft = %q, want failed body restored", draft)
	}
	update := waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Refresh && strings.Contains(update.Status, "send failed")
	})
	if !strings.Contains(update.Status, "boom") {
		t.Fatalf("failure status = %q, want protocol error", update.Status)
	}
}

func TestHandleTextSendRequestRejectsInvalidChatJID(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	result := make(chan textSendQueuedResult, 1)
	handleTextSendRequest(ctx, db, &fakeLiveWhatsAppSession{}, make(chan ui.LiveUpdate, 1), nil, true, textSendRequest{
		ChatID: "chat-1",
		Body:   "hello",
		Result: result,
	})

	queued := <-result
	if queued.Err == nil || !strings.Contains(queued.Err.Error(), "unsupported send chat jid") {
		t.Fatalf("queued error = %v, want unsupported chat jid", queued.Err)
	}
}

func TestHandleMediaSendRequestPersistsAndCompletes(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	chatJID := "12345@s.whatsapp.net"
	if err := db.UpsertChat(ctx, store.Chat{ID: chatJID, JID: chatJID, Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	attachmentPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(attachmentPath, []byte("image-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{
		generatedID: "remote-1",
		mediaSends:  make(chan whatsapp.MediaSendRequest, 1),
	}
	result := make(chan mediaSendQueuedResult, 1)
	updates := make(chan ui.LiveUpdate, 4)
	handleMediaSendRequest(ctx, db, session, updates, nil, true, mediaSendRequest{
		ChatID: chatJID,
		Body:   "caption @Ana",
		Mentions: []store.MessageMention{{
			JID:         "222@s.whatsapp.net",
			DisplayName: "Ana",
			StartByte:   8,
			EndByte:     12,
		}},
		Attachments: []ui.Attachment{{
			LocalPath:     attachmentPath,
			FileName:      "photo.jpg",
			MIMEType:      "image/jpeg",
			DownloadState: "local_pending",
		}},
		Result: result,
	})

	queued := <-result
	if queued.Err != nil {
		t.Fatalf("queued media send error = %v", queued.Err)
	}
	request := <-session.mediaSends
	if request.ChatJID != chatJID || request.Caption != "caption @222" || request.LocalPath != attachmentPath || request.RemoteID != "remote-1" {
		t.Fatalf("media request = %+v, want jid/caption/path/remote id", request)
	}
	messages := waitForStoredMessages(t, db, chatJID, 1)
	if len(messages) != 1 || messages[0].ID != queued.Message.ID || messages[0].Status != "sent" || len(messages[0].Media) != 1 {
		t.Fatalf("stored messages = %+v, want one sent media message", messages)
	}
	if messages[0].Media[0].LocalPath != attachmentPath || messages[0].Media[0].DownloadState != "downloaded" {
		t.Fatalf("stored media = %+v, want local downloaded attachment", messages[0].Media[0])
	}
	if messages[0].Body != "caption @Ana" || len(messages[0].Mentions) != 1 {
		t.Fatalf("stored media body/mentions = %q %+v, want friendly mention", messages[0].Body, messages[0].Mentions)
	}
	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Refresh && strings.Contains(update.Status, "attachment")
	})
}

func TestHandleMediaSendRequestFailureMarksFailedAndRestoresCaptionDraft(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	chatJID := "12345@s.whatsapp.net"
	if err := db.UpsertChat(ctx, store.Chat{ID: chatJID, JID: chatJID, Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	attachmentPath := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(attachmentPath, []byte("pdf-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{
		generatedID: "remote-1",
		sendErr:     errors.New("boom"),
		mediaSends:  make(chan whatsapp.MediaSendRequest, 1),
	}
	result := make(chan mediaSendQueuedResult, 1)
	updates := make(chan ui.LiveUpdate, 4)
	handleMediaSendRequest(ctx, db, session, updates, nil, true, mediaSendRequest{
		ChatID: chatJID,
		Body:   "retry caption",
		Attachments: []ui.Attachment{{
			LocalPath: attachmentPath,
			FileName:  "report.pdf",
			MIMEType:  "application/pdf",
		}},
		Result: result,
	})

	queued := <-result
	if queued.Err != nil {
		t.Fatalf("queued media send error = %v", queued.Err)
	}
	<-session.mediaSends
	messages := waitForStoredMessages(t, db, chatJID, 1)
	if messages[0].Status != "failed" || len(messages[0].Media) != 1 {
		t.Fatalf("stored failed message = %+v", messages)
	}
	if messages[0].Media[0].LocalPath != attachmentPath || messages[0].Media[0].DownloadState != "downloaded" {
		t.Fatalf("stored media = %+v, want preserved local attachment", messages[0].Media[0])
	}
	draft, err := db.Draft(ctx, chatJID)
	if err != nil {
		t.Fatalf("Draft() error = %v", err)
	}
	if draft != "retry caption" {
		t.Fatalf("draft = %q, want failed caption restored", draft)
	}
	update := waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Refresh && strings.Contains(update.Status, "send failed")
	})
	if !strings.Contains(update.Status, "boom") {
		t.Fatalf("failure status = %q, want protocol error", update.Status)
	}
}

func TestHandleStickerSendRequestQueuesDedicatedStickerMessage(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	chatJID := "12345@s.whatsapp.net"
	if err := db.UpsertChat(ctx, store.Chat{ID: chatJID, JID: chatJID, Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	stickerPath := filepath.Join(t.TempDir(), "sticker.webp")
	if err := os.WriteFile(stickerPath, []byte("webp-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{
		generatedID:  "sticker-remote-1",
		stickerSends: make(chan whatsapp.StickerSendRequest, 1),
	}
	result := make(chan mediaSendQueuedResult, 1)
	updates := make(chan ui.LiveUpdate, 4)
	handleMediaSendRequest(ctx, db, session, updates, nil, true, mediaSendRequest{
		ChatID: chatJID,
		Body:   "ignored caption",
		Sticker: &store.RecentSticker{
			ID:        "recent-sticker-1",
			LocalPath: stickerPath,
			FileName:  "sticker.webp",
			MIMEType:  "image/webp",
			Width:     256,
			Height:    256,
		},
		Result: result,
	})

	queued := <-result
	if queued.Err != nil {
		t.Fatalf("queued sticker send error = %v", queued.Err)
	}
	request := <-session.stickerSends
	if request.ChatJID != chatJID || request.LocalPath != stickerPath || request.RemoteID != "sticker-remote-1" || request.Width != 256 || request.Height != 256 {
		t.Fatalf("sticker request = %+v, want dedicated sticker send", request)
	}
	messages := waitForStoredMessages(t, db, chatJID, 1)
	if len(messages) != 1 || messages[0].ID != queued.Message.ID || messages[0].Status != "sent" || messages[0].Body != "" || len(messages[0].Media) != 1 {
		t.Fatalf("stored sticker message = %+v", messages)
	}
	mediaItem := messages[0].Media[0]
	if mediaItem.Kind != "sticker" || mediaItem.LocalPath != stickerPath || mediaItem.DownloadState != "downloaded" {
		t.Fatalf("stored sticker media = %+v", mediaItem)
	}
	recent, ok, err := db.RecentSticker(ctx, "recent-sticker-1")
	if err != nil {
		t.Fatalf("RecentSticker() error = %v", err)
	}
	if !ok || recent.LocalPath != stickerPath || recent.LastUsedAt.IsZero() {
		t.Fatalf("recent sticker after send = %+v ok=%v", recent, ok)
	}
	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Refresh && strings.Contains(update.Status, "sticker")
	})
}

func TestHandleStickerSyncRequestFetchesAndCachesWhatsAppStickers(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	session := &fakeLiveWhatsAppSession{
		downloads: make(chan fakeDownloadRequest, 1),
		stickerSync: []whatsapp.Event{{
			Kind: whatsapp.EventRecentSticker,
			Sticker: whatsapp.RecentStickerEvent{
				ID:            "favorite-sticker-1",
				URL:           "https://mmg.whatsapp.net/sticker",
				DirectPath:    "/v/t62.15575-24/sticker.enc",
				MediaKey:      []byte("media-key"),
				FileEncSHA256: []byte("file-enc-sha"),
				MIMEType:      "image/webp",
				FileName:      "sticker.webp",
				LastUsedAt:    time.Unix(1_700_000_000, 0),
				IsFavorite:    true,
				UpdatedAt:     time.Unix(1_700_000_000, 0),
			},
		}},
	}
	paths := config.Paths{TransientDir: filepath.Join(t.TempDir(), "transient")}
	updates := make(chan ui.LiveUpdate, 4)
	result := make(chan stickerSyncResult, 1)

	handleStickerSyncRequest(ctx, db, session, paths, updates, nil, true, stickerSyncRequest{
		Context: ctx,
		Result:  result,
	})

	synced := <-result
	if synced.Err != nil || synced.Stickers != 1 {
		t.Fatalf("sticker sync result = %+v, want one cached sticker", synced)
	}
	if session.stickerSyncCalls != 1 {
		t.Fatalf("sticker sync calls = %d, want 1", session.stickerSyncCalls)
	}
	download := <-session.downloads
	if download.descriptor.Kind != "sticker" || download.descriptor.DirectPath == "" || download.targetPath == "" {
		t.Fatalf("download request = %+v, want sticker descriptor", download)
	}
	recent, ok, err := db.RecentSticker(ctx, "favorite-sticker-1")
	if err != nil {
		t.Fatalf("RecentSticker() error = %v", err)
	}
	if !ok || !recent.IsFavorite || recent.LocalPath == "" || !mediaPathAvailable(recent.LocalPath) {
		t.Fatalf("recent sticker = %+v ok=%v, want cached favorite", recent, ok)
	}
	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Refresh && strings.Contains(update.Status, "synced 1")
	})
}

func TestHandleStickerSyncRequestKeepsUsableStickersWhenOneDownloadFails(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	session := &fakeLiveWhatsAppSession{
		downloads: make(chan fakeDownloadRequest, 2),
		downloadErrByPath: map[string]error{
			"/v/t62.15575-24/bad.enc": errors.New("expired media"),
		},
		stickerSync: []whatsapp.Event{
			recentStickerSyncEvent("favorite-sticker-bad", "/v/t62.15575-24/bad.enc"),
			recentStickerSyncEvent("favorite-sticker-good", "/v/t62.15575-24/good.enc"),
		},
	}
	updates := make(chan ui.LiveUpdate, 4)
	result := make(chan stickerSyncResult, 1)

	handleStickerSyncRequest(ctx, db, session, config.Paths{TransientDir: filepath.Join(t.TempDir(), "transient")}, updates, nil, true, stickerSyncRequest{
		Context: ctx,
		Result:  result,
	})

	synced := <-result
	if synced.Err != nil || synced.Stickers != 1 {
		t.Fatalf("sticker sync result = %+v, want one usable sticker and no fatal error", synced)
	}
	bad, ok, err := db.RecentSticker(ctx, "favorite-sticker-bad")
	if err != nil {
		t.Fatalf("RecentSticker(bad) error = %v", err)
	}
	if !ok || bad.LocalPath != "" || !bad.IsFavorite {
		t.Fatalf("bad sticker = %+v ok=%v, want metadata-only favorite", bad, ok)
	}
	good, ok, err := db.RecentSticker(ctx, "favorite-sticker-good")
	if err != nil {
		t.Fatalf("RecentSticker(good) error = %v", err)
	}
	if !ok || good.LocalPath == "" || !mediaPathAvailable(good.LocalPath) {
		t.Fatalf("good sticker = %+v ok=%v, want cached sticker file", good, ok)
	}
	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Refresh && strings.Contains(update.Status, "synced 1") && strings.Contains(update.Status, "1 unavailable")
	})
}

func TestHandleStickerSyncRequestReportsReasonWhenAllDownloadsFail(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	session := &fakeLiveWhatsAppSession{
		downloads: make(chan fakeDownloadRequest, 2),
		downloadErrByPath: map[string]error{
			"/v/t62.15575-24/bad-1.enc": errors.New("expired media 1"),
			"/v/t62.15575-24/bad-2.enc": errors.New("expired media 2"),
		},
		stickerSync: []whatsapp.Event{
			recentStickerSyncEvent("favorite-sticker-bad-1", "/v/t62.15575-24/bad-1.enc"),
			recentStickerSyncEvent("favorite-sticker-bad-2", "/v/t62.15575-24/bad-2.enc"),
		},
	}
	updates := make(chan ui.LiveUpdate, 4)
	result := make(chan stickerSyncResult, 1)

	handleStickerSyncRequest(ctx, db, session, config.Paths{TransientDir: filepath.Join(t.TempDir(), "transient")}, updates, nil, true, stickerSyncRequest{
		Context: ctx,
		Result:  result,
	})

	synced := <-result
	if synced.Err == nil || synced.Stickers != 0 {
		t.Fatalf("sticker sync result = %+v, want concise fatal error with no cached stickers", synced)
	}
	wantPrefix := "no sticker files cached; 2 metadata record(s) synced, 2 download(s) failed"
	if got := synced.Err.Error(); !strings.HasPrefix(got, wantPrefix) || !strings.Contains(got, "expired media 1") {
		t.Fatalf("sticker sync error = %q, want prefix %q and first failure reason", got, wantPrefix)
	}
	for _, id := range []string{"favorite-sticker-bad-1", "favorite-sticker-bad-2"} {
		recent, ok, err := db.RecentSticker(ctx, id)
		if err != nil {
			t.Fatalf("RecentSticker(%s) error = %v", id, err)
		}
		if !ok || recent.LocalPath != "" || !recent.IsFavorite {
			t.Fatalf("recent sticker %s = %+v ok=%v, want metadata persisted without local file", id, recent, ok)
		}
	}
	update := waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Refresh && strings.Contains(update.Status, "sticker sync failed")
	})
	if !strings.Contains(update.Status, "expired media 1") || strings.Contains(update.Status, "cache sticker") {
		t.Fatalf("status = %q, want concise sync failure with first download reason", update.Status)
	}
}

func recentStickerSyncEvent(id, directPath string) whatsapp.Event {
	return whatsapp.Event{
		Kind: whatsapp.EventRecentSticker,
		Sticker: whatsapp.RecentStickerEvent{
			ID:            id,
			URL:           "https://mmg.whatsapp.net/sticker",
			DirectPath:    directPath,
			MediaKey:      []byte("media-key-" + id),
			FileEncSHA256: []byte("file-enc-sha-" + id),
			MIMEType:      "image/webp",
			FileName:      "sticker.webp",
			LastUsedAt:    time.Unix(1_700_000_000, 0),
			IsFavorite:    true,
			UpdatedAt:     time.Unix(1_700_000_000, 0),
		},
	}
}

func TestHandleMediaSendRequestRejectsAudioCaption(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	attachmentPath := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(attachmentPath, []byte("audio-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	result := make(chan mediaSendQueuedResult, 1)
	handleMediaSendRequest(ctx, db, &fakeLiveWhatsAppSession{}, make(chan ui.LiveUpdate, 1), nil, true, mediaSendRequest{
		ChatID: "12345@s.whatsapp.net",
		Body:   "not allowed",
		Attachments: []ui.Attachment{{
			LocalPath: attachmentPath,
			FileName:  "voice.ogg",
			MIMEType:  "audio/ogg",
		}},
		Result: result,
	})

	queued := <-result
	if queued.Err == nil || !strings.Contains(queued.Err.Error(), "do not support captions") {
		t.Fatalf("queued error = %v, want audio caption rejection", queued.Err)
	}
	messages, err := db.ListMessages(ctx, "12345@s.whatsapp.net", 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages = %+v, want no stored media send", messages)
	}
}

func TestRetryMediaSendRequestBuildsQueuedRetryFromFailedMessage(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	chatJID := "12345@s.whatsapp.net"
	if err := db.UpsertChat(ctx, store.Chat{ID: chatJID, JID: chatJID, Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	quoted := store.Message{
		ID:        whatsapp.LocalMessageID(chatJID, "quoted-1"),
		RemoteID:  "quoted-1",
		ChatID:    chatJID,
		ChatJID:   chatJID,
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Body:      "earlier context",
		Timestamp: time.Unix(1_700_000_000, 0),
	}
	if _, err := db.AddIncomingMessage(ctx, quoted); err != nil {
		t.Fatalf("AddIncomingMessage() error = %v", err)
	}

	attachmentPath := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(attachmentPath, []byte("pdf-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	failed := store.Message{
		ID:              whatsapp.LocalMessageID(chatJID, "failed-1"),
		RemoteID:        "failed-1",
		ChatID:          chatJID,
		ChatJID:         chatJID,
		Sender:          "me",
		SenderJID:       "me",
		Body:            "@Ana retry caption",
		Timestamp:       time.Unix(1_700_000_100, 0),
		IsOutgoing:      true,
		Status:          "failed",
		QuotedMessageID: quoted.ID,
		QuotedRemoteID:  quoted.RemoteID,
		Mentions: []store.MessageMention{{
			JID:         "222@s.whatsapp.net",
			DisplayName: "Ana",
			StartByte:   0,
			EndByte:     4,
		}},
		Media: []store.MediaMetadata{{
			MessageID:     whatsapp.LocalMessageID(chatJID, "failed-1"),
			FileName:      "report.pdf",
			MIMEType:      "application/pdf",
			LocalPath:     attachmentPath,
			SizeBytes:     9,
			DownloadState: "downloaded",
		}},
	}
	if err := db.AddMessageWithMedia(ctx, failed, failed.Media); err != nil {
		t.Fatalf("AddMessageWithMedia() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{
		generatedID: "retry-1",
		mediaSends:  make(chan whatsapp.MediaSendRequest, 1),
	}
	result := make(chan mediaSendQueuedResult, 1)
	request, err := retryMediaSendRequest(ctx, db, failed, result)
	if err != nil {
		t.Fatalf("retryMediaSendRequest() error = %v", err)
	}
	updates := make(chan ui.LiveUpdate, 4)
	handleMediaSendRequest(ctx, db, session, updates, nil, true, request)

	queued := <-result
	if queued.Err != nil {
		t.Fatalf("queued retry error = %v", queued.Err)
	}
	if queued.Message.ID == failed.ID {
		t.Fatal("queued retry reused the failed message id")
	}
	sent := <-session.mediaSends
	if sent.ChatJID != chatJID || sent.Caption != "@222 retry caption" || sent.LocalPath != attachmentPath {
		t.Fatalf("media request = %+v, want chat/caption/path preserved", sent)
	}
	if sent.QuotedRemoteID != quoted.RemoteID || sent.QuotedSenderJID != quoted.SenderJID || sent.QuotedMessageBody != quoted.Body {
		t.Fatalf("retry quote = %+v, want quoted remote id/sender/body", sent)
	}
	if len(sent.MentionedJIDs) != 1 || sent.MentionedJIDs[0] != "222@s.whatsapp.net" {
		t.Fatalf("retry mentions = %+v, want preserved mention", sent.MentionedJIDs)
	}

	messages := waitForStoredMessages(t, db, chatJID, 3)
	if len(messages) != 3 {
		t.Fatalf("messages = %+v, want quoted source plus original failed row plus retry row", messages)
	}
	if messages[1].ID != failed.ID || messages[1].Status != "failed" {
		t.Fatalf("original message = %+v, want unchanged failed row", messages[1])
	}
	if messages[2].ID != queued.Message.ID || messages[2].Status != "sent" {
		t.Fatalf("retry message = %+v, want new sent row", messages[2])
	}
	if len(messages[2].Mentions) != 1 || messages[2].Mentions[0].JID != "222@s.whatsapp.net" {
		t.Fatalf("retry message mentions = %+v, want preserved mention", messages[2].Mentions)
	}
}

func TestRetryMediaSendRequestUsesStickerSendForFailedSticker(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	chatJID := "12345@s.whatsapp.net"
	if err := db.UpsertChat(ctx, store.Chat{ID: chatJID, JID: chatJID, Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	stickerPath := filepath.Join(t.TempDir(), "sticker.webp")
	if err := os.WriteFile(stickerPath, []byte("webp-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	failed := store.Message{
		ID:         whatsapp.LocalMessageID(chatJID, "failed-sticker-1"),
		RemoteID:   "failed-sticker-1",
		ChatID:     chatJID,
		ChatJID:    chatJID,
		Sender:     "me",
		SenderJID:  "me",
		Timestamp:  time.Unix(1_700_000_100, 0),
		IsOutgoing: true,
		Status:     "failed",
		Media: []store.MediaMetadata{{
			MessageID:     whatsapp.LocalMessageID(chatJID, "failed-sticker-1"),
			Kind:          "sticker",
			FileName:      "sticker.webp",
			MIMEType:      "image/webp",
			LocalPath:     stickerPath,
			SizeBytes:     10,
			DownloadState: "downloaded",
		}},
	}
	if err := db.AddMessageWithMedia(ctx, failed, failed.Media); err != nil {
		t.Fatalf("AddMessageWithMedia() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{
		generatedID:  "retry-sticker-1",
		stickerSends: make(chan whatsapp.StickerSendRequest, 1),
	}
	result := make(chan mediaSendQueuedResult, 1)
	request, err := retryMediaSendRequest(ctx, db, failed, result)
	if err != nil {
		t.Fatalf("retryMediaSendRequest() error = %v", err)
	}
	if request.Sticker == nil {
		t.Fatal("retryMediaSendRequest() Sticker = nil, want sticker retry")
	}
	updates := make(chan ui.LiveUpdate, 4)
	handleMediaSendRequest(ctx, db, session, updates, nil, true, request)

	queued := <-result
	if queued.Err != nil {
		t.Fatalf("queued sticker retry error = %v", queued.Err)
	}
	sent := <-session.stickerSends
	if sent.ChatJID != chatJID || sent.LocalPath != stickerPath || sent.RemoteID != "retry-sticker-1" {
		t.Fatalf("sticker retry request = %+v", sent)
	}
	messages := waitForStoredMessages(t, db, chatJID, 2)
	if messages[0].ID != failed.ID || messages[0].Status != "failed" || messages[1].ID != queued.Message.ID || messages[1].Status != "sent" {
		t.Fatalf("messages after sticker retry = %+v", messages)
	}
}

func TestRetryMediaSendRequestRejectsMissingLocalFile(t *testing.T) {
	ctx := context.Background()
	request, err := retryMediaSendRequest(ctx, nil, store.Message{
		ID:         "failed-1",
		ChatID:     "12345@s.whatsapp.net",
		ChatJID:    "12345@s.whatsapp.net",
		Sender:     "me",
		IsOutgoing: true,
		Status:     "failed",
		Media: []store.MediaMetadata{{
			MessageID: "failed-1",
			FileName:  "missing.pdf",
			MIMEType:  "application/pdf",
			LocalPath: filepath.Join(t.TempDir(), "missing.pdf"),
		}},
	}, make(chan mediaSendQueuedResult, 1))
	if err == nil || !strings.Contains(err.Error(), "stat attachment") {
		t.Fatalf("retryMediaSendRequest() error = %v, want missing local file error", err)
	}
	_ = request
}

func TestHandleReadReceiptRequestMarksChatReadAfterProtocolSuccess(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	chatJID := "12345@s.whatsapp.net"
	if err := db.UpsertChat(ctx, store.Chat{ID: chatJID, JID: chatJID, Title: "Alice", Unread: 2}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	message := store.Message{
		ID:        whatsapp.LocalMessageID(chatJID, "msg-1"),
		RemoteID:  "msg-1",
		ChatID:    chatJID,
		ChatJID:   chatJID,
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Body:      "hello",
		Timestamp: time.Unix(1_700_000_000, 0),
	}
	if _, err := db.AddIncomingMessage(ctx, message); err != nil {
		t.Fatalf("AddIncomingMessage() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{readReceipts: make(chan []whatsapp.ReadReceiptTarget, 1)}
	result := make(chan error, 1)
	updates := make(chan ui.LiveUpdate, 4)
	handleReadReceiptRequest(ctx, db, session, updates, nil, true, readReceiptRequest{
		Chat:     store.Chat{ID: chatJID, JID: chatJID, Title: "Alice"},
		Messages: []store.Message{message},
		Result:   result,
	})

	if err := <-result; err != nil {
		t.Fatalf("queued read receipt error = %v", err)
	}
	targets := <-session.readReceipts
	if len(targets) != 1 || targets[0].RemoteID != "msg-1" || targets[0].ChatJID != chatJID {
		t.Fatalf("read targets = %+v", targets)
	}
	chats, err := db.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].Unread != 0 {
		t.Fatalf("chats after mark read = %+v, want unread cleared", chats)
	}
	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.ReadChatID == chatJID && update.Refresh
	})
}

func TestHandleReactionRequestUpdatesStore(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	chatJID := "12345@s.whatsapp.net"
	if err := db.UpsertChat(ctx, store.Chat{ID: chatJID, JID: chatJID, Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	message := store.Message{
		ID:        whatsapp.LocalMessageID(chatJID, "msg-1"),
		RemoteID:  "msg-1",
		ChatID:    chatJID,
		ChatJID:   chatJID,
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Body:      "hello",
		Timestamp: time.Unix(1_700_000_000, 0),
	}
	if err := db.AddMessage(ctx, message); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{
		generatedID: "reaction-1",
		reactions:   make(chan whatsapp.ReactionSendRequest, 1),
	}
	result := make(chan error, 1)
	updates := make(chan ui.LiveUpdate, 4)
	handleReactionRequest(ctx, db, session, updates, nil, true, reactionRequest{
		Message: message,
		Emoji:   "🔥",
		Result:  result,
	})

	if err := <-result; err != nil {
		t.Fatalf("queued reaction error = %v", err)
	}
	request := <-session.reactions
	if request.ChatJID != chatJID || request.TargetRemoteID != "msg-1" || request.Emoji != "🔥" {
		t.Fatalf("reaction request = %+v", request)
	}
	reactions, err := db.ListMessageReactions(ctx, message.ID)
	if err != nil {
		t.Fatalf("ListMessageReactions() error = %v", err)
	}
	if len(reactions) != 1 || reactions[0].Emoji != "🔥" || reactions[0].SenderJID != "me" {
		t.Fatalf("stored reactions = %+v", reactions)
	}
	waitForLiveUpdate(t, updates, func(update ui.LiveUpdate) bool {
		return update.Refresh && strings.Contains(update.Status, "reaction")
	})
}

func TestDownloadRemoteMediaWritesFileAndUpdatesMetadata(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.UpsertChat(ctx, store.Chat{ID: "chat-1", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	if err := db.AddMessage(ctx, store.Message{
		ID:        "chat-1/msg-1",
		ChatID:    "chat-1",
		Sender:    "Alice",
		Body:      "photo",
		Timestamp: time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}
	if err := db.UpsertMediaMetadataWithDownload(ctx, store.MediaMetadata{
		MessageID:     "chat-1/msg-1",
		MIMEType:      "image/jpeg",
		FileName:      "photo.jpg",
		DownloadState: "remote",
	}, &store.MediaDownloadDescriptor{
		Kind:          "image",
		DirectPath:    "/v/t62.7118-24/photo",
		MediaKey:      []byte{1},
		FileSHA256:    []byte{2},
		FileEncSHA256: []byte{3},
		FileLength:    16,
	}); err != nil {
		t.Fatalf("UpsertMediaMetadataWithDownload() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{downloads: make(chan fakeDownloadRequest, 1)}
	paths := config.Paths{MediaDir: filepath.Join(t.TempDir(), "media")}
	media, err := downloadRemoteMedia(ctx, db, session, paths, mediaDownloadRequest{
		Message: store.Message{ID: "chat-1/msg-1"},
		Media: store.MediaMetadata{
			MessageID:     "chat-1/msg-1",
			MIMEType:      "image/jpeg",
			FileName:      "photo.jpg",
			DownloadState: "remote",
		},
	})
	if err != nil {
		t.Fatalf("downloadRemoteMedia() error = %v", err)
	}
	if media.DownloadState != "downloaded" || media.LocalPath == "" || filepath.Ext(media.LocalPath) != ".jpg" {
		t.Fatalf("downloaded media = %+v", media)
	}
	data, err := os.ReadFile(media.LocalPath)
	if err != nil {
		t.Fatalf("ReadFile(downloaded) error = %v", err)
	}
	if string(data) != "downloaded media" {
		t.Fatalf("downloaded data = %q", data)
	}
	request := <-session.downloads
	if request.descriptor.Kind != "image" || request.descriptor.DirectPath == "" || request.targetPath == media.LocalPath {
		t.Fatalf("download request = %+v, want descriptor and temp target", request)
	}
	stored, err := db.MediaMetadata(ctx, "chat-1/msg-1")
	if err != nil {
		t.Fatalf("MediaMetadata() error = %v", err)
	}
	if stored.DownloadState != "downloaded" || stored.LocalPath != media.LocalPath {
		t.Fatalf("stored media = %+v, want downloaded local path", stored)
	}
}

func TestDownloadRemoteMediaReportsMissingDescriptor(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.UpsertChat(ctx, store.Chat{ID: "chat-1", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	if err := db.AddMessageWithMedia(ctx, store.Message{
		ID:        "chat-1/msg-1",
		ChatID:    "chat-1",
		Sender:    "Alice",
		Body:      "photo",
		Timestamp: time.Unix(1_700_000_000, 0),
	}, []store.MediaMetadata{{
		MIMEType:      "image/jpeg",
		FileName:      "photo.jpg",
		DownloadState: "remote",
	}}); err != nil {
		t.Fatalf("AddMessageWithMedia() error = %v", err)
	}

	_, err = downloadRemoteMedia(ctx, db, &fakeLiveWhatsAppSession{}, config.Paths{MediaDir: filepath.Join(t.TempDir(), "media")}, mediaDownloadRequest{
		Message: store.Message{ID: "chat-1/msg-1"},
		Media: store.MediaMetadata{
			MessageID:     "chat-1/msg-1",
			MIMEType:      "image/jpeg",
			FileName:      "photo.jpg",
			DownloadState: "remote",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "download details unavailable") {
		t.Fatalf("downloadRemoteMedia() error = %v, want missing descriptor", err)
	}
}

func TestDownloadRemoteMediaMarksFailedOnProtocolError(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.UpsertChat(ctx, store.Chat{ID: "chat-1", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	if err := db.AddMessage(ctx, store.Message{
		ID:        "chat-1/msg-1",
		ChatID:    "chat-1",
		Sender:    "Alice",
		Body:      "photo",
		Timestamp: time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}
	if err := db.UpsertMediaMetadataWithDownload(ctx, store.MediaMetadata{
		MessageID:     "chat-1/msg-1",
		MIMEType:      "image/jpeg",
		FileName:      "photo.jpg",
		DownloadState: "remote",
	}, &store.MediaDownloadDescriptor{
		Kind:       "image",
		DirectPath: "/v/t62.7118-24/photo",
	}); err != nil {
		t.Fatalf("UpsertMediaMetadataWithDownload() error = %v", err)
	}

	_, err = downloadRemoteMedia(ctx, db, &fakeLiveWhatsAppSession{downloadErr: errors.New("boom")}, config.Paths{MediaDir: filepath.Join(t.TempDir(), "media")}, mediaDownloadRequest{
		Message: store.Message{ID: "chat-1/msg-1"},
		Media: store.MediaMetadata{
			MessageID:     "chat-1/msg-1",
			MIMEType:      "image/jpeg",
			FileName:      "photo.jpg",
			DownloadState: "remote",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("downloadRemoteMedia() error = %v, want boom", err)
	}
	stored, err := db.MediaMetadata(ctx, "chat-1/msg-1")
	if err != nil {
		t.Fatalf("MediaMetadata() error = %v", err)
	}
	if stored.DownloadState != "failed" {
		t.Fatalf("DownloadState = %q, want failed", stored.DownloadState)
	}
}

func TestRunMediaOpenRepairsStaleManagedCacheAndDownloadsAgain(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ctx := context.Background()
	mediaDir := filepath.Join(t.TempDir(), "media")
	stalePath := filepath.Join(mediaDir, "missing-report.pdf")
	if err := db.UpsertChat(ctx, store.Chat{ID: "chat-1", JID: "docs@g.us", Title: "Docs", Kind: "group"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	if err := db.AddMessageWithMedia(ctx, store.Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		ChatJID:   "docs@g.us",
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1_700_200_000, 0),
	}, []store.MediaMetadata{{
		MessageID:     "m-1",
		MIMEType:      "application/pdf",
		FileName:      "report.pdf",
		LocalPath:     stalePath,
		DownloadState: "downloaded",
		UpdatedAt:     time.Unix(1_700_200_000, 0),
	}}); err != nil {
		t.Fatalf("AddMessageWithMedia() error = %v", err)
	}
	if err := db.UpsertMediaMetadataWithDownload(ctx, store.MediaMetadata{
		MessageID:     "m-1",
		MIMEType:      "application/pdf",
		FileName:      "report.pdf",
		LocalPath:     stalePath,
		DownloadState: "downloaded",
		UpdatedAt:     time.Unix(1_700_200_000, 0),
	}, &store.MediaDownloadDescriptor{
		MessageID:  "m-1",
		Kind:       "document",
		URL:        "https://example.invalid/report.pdf",
		DirectPath: "/v/t62.7118-24/example",
		MediaKey:   []byte("media-key"),
		FileLength: 32,
		UpdatedAt:  time.Unix(1_700_200_000, 0),
	}); err != nil {
		t.Fatalf("UpsertMediaMetadataWithDownload() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{downloads: make(chan fakeDownloadRequest, 1)}
	env := Environment{
		Store: db,
		Config: config.Config{
			FileOpenerCommand: "true {path}",
		},
		Paths: config.Paths{
			TransientDir: filepath.Join(t.TempDir(), "transient"),
			MediaDir:     mediaDir,
			SessionFile:  filepath.Join(t.TempDir(), "session.sqlite3"),
		},
		OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
			return session, nil
		},
		CheckWhatsAppSession: func(context.Context, string) (WhatsAppSessionStatus, error) {
			return WhatsAppSessionStatus{Label: "paired", Paired: true}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	if code := run(env, []string{"media", "open", "m-1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run media open exit = %d, stderr = %q", code, stderr.String())
	}
	request := <-session.downloads
	if request.descriptor.Kind != "document" {
		t.Fatalf("download descriptor = %+v, want document", request.descriptor)
	}
	stored, err := db.MediaMetadata(ctx, "m-1")
	if err != nil {
		t.Fatalf("MediaMetadata() error = %v", err)
	}
	if stored.LocalPath == "" || stored.LocalPath == stalePath || stored.DownloadState != "downloaded" {
		t.Fatalf("stored media = %+v, want repaired downloaded local path", stored)
	}
	if got := strings.TrimSpace(stdout.String()); got != stored.LocalPath {
		t.Fatalf("stdout path = %q, want %q", got, stored.LocalPath)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func waitForStoredMessages(t *testing.T, db *store.Store, chatID string, count int) []store.Message {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		messages, err := db.ListMessages(context.Background(), chatID, 10)
		if err != nil {
			t.Fatalf("ListMessages() error = %v", err)
		}
		if len(messages) >= count {
			return messages
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for %d stored message(s), got %d", count, len(messages))
		}
	}
}

func waitForLiveUpdate(t *testing.T, updates <-chan ui.LiveUpdate, match func(ui.LiveUpdate) bool) ui.LiveUpdate {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case update := <-updates:
			if match(update) {
				return update
			}
		case <-deadline:
			t.Fatal("timed out waiting for live update")
		}
	}
}

func assertNoLiveUpdate(t *testing.T, updates <-chan ui.LiveUpdate, match func(ui.LiveUpdate) bool) {
	t.Helper()
	deadline := time.After(150 * time.Millisecond)
	for {
		select {
		case update := <-updates:
			if match(update) {
				t.Fatalf("unexpected live update = %+v", update)
			}
		case <-deadline:
			return
		}
	}
}

func waitForNotification(t *testing.T, notifications <-chan notify.Notification) notify.Notification {
	t.Helper()
	select {
	case note := <-notifications:
		return note
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notification")
		return notify.Notification{}
	}
}

func assertNotificationCount(t *testing.T, notifications <-chan notify.Notification, want int) {
	t.Helper()
	deadline := time.After(250 * time.Millisecond)
	got := 0
	for {
		select {
		case <-notifications:
			got++
		case <-deadline:
			if got != want {
				t.Fatalf("notification count = %d, want %d", got, want)
			}
			return
		}
	}
}

func logoutTestEnvironment(t *testing.T) (Environment, string) {
	t.Helper()
	root := t.TempDir()
	paths := config.Paths{
		ConfigDir:       filepath.Join(root, "config"),
		DataDir:         filepath.Join(root, "data"),
		CacheDir:        filepath.Join(root, "cache"),
		TransientDir:    filepath.Join(root, "transient"),
		ConfigFile:      filepath.Join(root, "config", "config.toml"),
		DatabaseFile:    filepath.Join(root, "data", "state.sqlite3"),
		SessionFile:     filepath.Join(root, "data", "whatsapp-session.sqlite3"),
		LogFile:         filepath.Join(root, "cache", "vimwhat.log"),
		MediaDir:        filepath.Join(root, "transient", "media"),
		PreviewCacheDir: filepath.Join(root, "transient", "preview"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	downloadsDir := filepath.Join(root, "Downloads")
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(downloads) error = %v", err)
	}
	savedPath := filepath.Join(downloadsDir, "saved-report.pdf")
	if err := os.WriteFile(savedPath, []byte("saved"), 0o644); err != nil {
		t.Fatalf("WriteFile(saved) error = %v", err)
	}

	db, err := store.Open(paths.DatabaseFile)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	ctx := context.Background()
	if err := db.UpsertChat(ctx, store.Chat{ID: "chat-1", JID: "alice@s.whatsapp.net", Title: "Alice", Kind: "direct"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	if err := db.AddMessage(ctx, store.Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		ChatJID:   "alice@s.whatsapp.net",
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Body:      "hello",
		Timestamp: time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}
	if err := db.SaveDraft(ctx, "chat-1", "draft"); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}

	for _, file := range []string{
		paths.DatabaseFile + "-wal",
		paths.DatabaseFile + "-shm",
		paths.SessionFile,
		paths.SessionFile + "-wal",
		paths.SessionFile + "-shm",
		paths.LogFile,
		filepath.Join(paths.MediaDir, "cached.bin"),
		filepath.Join(paths.PreviewCacheDir, "thumb.jpg"),
		filepath.Join(paths.LegacyMediaDir(), "legacy.bin"),
		filepath.Join(paths.LegacyPreviewCacheDir(), "legacy-thumb.jpg"),
	} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(file), err)
		}
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", file, err)
		}
	}

	return Environment{
		Paths:  paths,
		Config: config.Config{DownloadsDir: downloadsDir},
		Store:  db,
	}, savedPath
}

func assertLocalStateCleared(t *testing.T, paths config.Paths, savedPath string) {
	t.Helper()
	for _, path := range []string{
		paths.DatabaseFile,
		paths.DatabaseFile + "-wal",
		paths.DatabaseFile + "-shm",
		paths.SessionFile,
		paths.SessionFile + "-wal",
		paths.SessionFile + "-shm",
		paths.LogFile,
		paths.MediaDir,
		paths.PreviewCacheDir,
		paths.LegacyMediaDir(),
		paths.LegacyPreviewCacheDir(),
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Stat(%q) error = %v, want not exists", path, err)
		}
	}
	if _, err := os.Stat(savedPath); err != nil {
		t.Fatalf("saved download missing after logout: %v", err)
	}
}

func assertFreshStateAfterLogout(t *testing.T, dbPath string) {
	t.Helper()
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() after logout error = %v", err)
	}
	defer db.Close()
	snapshot, err := db.LoadSnapshot(context.Background(), 50)
	if err != nil {
		t.Fatalf("LoadSnapshot() after logout error = %v", err)
	}
	if len(snapshot.Chats) != 0 || len(snapshot.MessagesByChat) != 0 || len(snapshot.DraftsByChat) != 0 || snapshot.ActiveChatID != "" {
		t.Fatalf("snapshot after logout = %+v, want empty first-use state", snapshot)
	}
}

func TestRunLoginSkipsAlreadyLoggedInSession(t *testing.T) {
	session := &fakeWhatsAppSession{loggedIn: true}
	env := Environment{
		Paths: config.Paths{SessionFile: "/tmp/vimwhat-session.sqlite3"},
		OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
			return session, nil
		},
		RenderQR: func(io.Writer, string) error {
			t.Fatalf("RenderQR called for already logged in session")
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	if code := run(env, []string{"login"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run login exit = %d, stderr = %q", code, stderr.String())
	}
	if session.loginCalled {
		t.Fatalf("Login was called for already logged in session")
	}
	if !strings.Contains(stdout.String(), "already logged in") {
		t.Fatalf("stdout = %q, want already logged in", stdout.String())
	}
}

func TestRunLogoutClearsLocalStateWithoutOpeningUnpairedSession(t *testing.T) {
	env, savedPath := logoutTestEnvironment(t)
	var openCalled bool
	env.OpenWhatsAppSession = func(context.Context, string) (WhatsAppSession, error) {
		openCalled = true
		return nil, errors.New("unexpected open")
	}
	env.CheckWhatsAppSession = func(context.Context, string) (WhatsAppSessionStatus, error) {
		return WhatsAppSessionStatus{Label: "not configured", Paired: false}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := run(env, []string{"logout"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run logout exit = %d, stderr = %q", code, stderr.String())
	}
	if openCalled {
		t.Fatalf("session was opened for an unpaired logout")
	}
	if !strings.Contains(stdout.String(), "local state cleared") {
		t.Fatalf("stdout = %q, want local state cleared", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	assertLocalStateCleared(t, env.Paths, savedPath)
	assertFreshStateAfterLogout(t, env.Paths.DatabaseFile)
}

func TestRunLogoutLogsOutPairedSessionAndClearsLocalState(t *testing.T) {
	env, savedPath := logoutTestEnvironment(t)
	session := &fakeWhatsAppSession{loggedIn: true}
	env.OpenWhatsAppSession = func(context.Context, string) (WhatsAppSession, error) {
		return session, nil
	}
	env.CheckWhatsAppSession = func(context.Context, string) (WhatsAppSessionStatus, error) {
		return WhatsAppSessionStatus{Label: "paired locally (123@s.whatsapp.net)", Paired: true}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := run(env, []string{"logout"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run logout exit = %d, stderr = %q", code, stderr.String())
	}
	if !session.logoutCalled {
		t.Fatalf("Logout was not called")
	}
	if !session.closeCalled {
		t.Fatalf("Close was not called")
	}
	if !strings.Contains(stdout.String(), "logged out; local state cleared") {
		t.Fatalf("stdout = %q, want logged out + local reset", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	assertLocalStateCleared(t, env.Paths, savedPath)
}

func TestRunLogoutWarnsOnRemoteFailureButStillClearsLocalState(t *testing.T) {
	env, savedPath := logoutTestEnvironment(t)
	session := &fakeWhatsAppSession{loggedIn: true, logoutErr: errors.New("boom")}
	env.OpenWhatsAppSession = func(context.Context, string) (WhatsAppSession, error) {
		return session, nil
	}
	env.CheckWhatsAppSession = func(context.Context, string) (WhatsAppSessionStatus, error) {
		return WhatsAppSessionStatus{Label: "paired locally (123@s.whatsapp.net)", Paired: true}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := run(env, []string{"logout"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run logout exit = %d, stderr = %q", code, stderr.String())
	}
	if !session.logoutCalled || !session.closeCalled {
		t.Fatalf("session logout/close = %v/%v, want true/true", session.logoutCalled, session.closeCalled)
	}
	if !strings.Contains(stdout.String(), "local state cleared") {
		t.Fatalf("stdout = %q, want local state cleared", stdout.String())
	}
	if !strings.Contains(stderr.String(), "remote logout failed") {
		t.Fatalf("stderr = %q, want remote logout warning", stderr.String())
	}
	assertLocalStateCleared(t, env.Paths, savedPath)
}

func TestPrintDoctorUsesWhatsAppSessionStatus(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer db.Close()

	env := Environment{
		Paths: config.Paths{
			SessionFile:     "/tmp/vimwhat-session.sqlite3",
			ConfigFile:      "/tmp/config.toml",
			DataDir:         "/tmp/data",
			CacheDir:        "/tmp/cache",
			TransientDir:    "/tmp/vimwhat-u-test",
			DatabaseFile:    "/tmp/state.sqlite3",
			MediaDir:        "/tmp/vimwhat-u-test/media",
			PreviewCacheDir: "/tmp/vimwhat-u-test/preview",
		},
		Store: db,
		CheckWhatsAppSession: func(context.Context, string) (WhatsAppSessionStatus, error) {
			return WhatsAppSessionStatus{Label: "paired locally (123@s.whatsapp.net)", Paired: true}, nil
		},
	}

	var out bytes.Buffer
	printDoctor(env, &out)
	if !strings.Contains(out.String(), "session status: paired locally (123@s.whatsapp.net)") {
		t.Fatalf("doctor output = %q, want paired-local session status", out.String())
	}
	if !strings.Contains(out.String(), "emoji mode:") {
		t.Fatalf("doctor output = %q, want emoji mode line", out.String())
	}
	for _, want := range []string{"transient dir: /tmp/vimwhat-u-test", "media cache dir: /tmp/vimwhat-u-test/media", "preview cache dir: /tmp/vimwhat-u-test/preview"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("doctor output = %q, want %q", out.String(), want)
		}
	}
	if !strings.Contains(out.String(), "requested notification backend:") || !strings.Contains(out.String(), "notification delivery path:") {
		t.Fatalf("doctor output = %q, want notification diagnostics", out.String())
	}
}

func TestRunMediaOpenUsesLocalDownloadedFile(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ctx := context.Background()
	if err := db.UpsertChat(ctx, store.Chat{ID: "chat-1", JID: "docs@g.us", Title: "Docs", Kind: "group"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	localPath := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(localPath, []byte("local media"), 0o644); err != nil {
		t.Fatalf("WriteFile(local media) error = %v", err)
	}
	if err := db.AddMessageWithMedia(ctx, store.Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		ChatJID:   "docs@g.us",
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1_700_200_000, 0),
	}, []store.MediaMetadata{{
		MessageID:     "m-1",
		MIMEType:      "text/plain",
		FileName:      "report.txt",
		LocalPath:     localPath,
		DownloadState: "downloaded",
		UpdatedAt:     time.Unix(1_700_200_000, 0),
	}}); err != nil {
		t.Fatalf("AddMessageWithMedia() error = %v", err)
	}

	env := Environment{
		Store:  db,
		Config: config.Config{FileOpenerCommand: "true {path}"},
	}

	var stdout, stderr bytes.Buffer
	if code := run(env, []string{"media", "open", "m-1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run media open exit = %d, stderr = %q", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != localPath {
		t.Fatalf("stdout = %q, want %q", got, localPath)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunMediaOpenDownloadsRemoteWhenNeeded(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ctx := context.Background()
	if err := db.UpsertChat(ctx, store.Chat{ID: "chat-1", JID: "docs@g.us", Title: "Docs", Kind: "group"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	if err := db.AddMessageWithMedia(ctx, store.Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		ChatJID:   "docs@g.us",
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1_700_200_000, 0),
	}, []store.MediaMetadata{{
		MessageID:     "m-1",
		MIMEType:      "application/pdf",
		FileName:      "report.pdf",
		DownloadState: "remote",
		UpdatedAt:     time.Unix(1_700_200_000, 0),
	}}); err != nil {
		t.Fatalf("AddMessageWithMedia() error = %v", err)
	}
	if err := db.UpsertMediaMetadataWithDownload(ctx, store.MediaMetadata{
		MessageID:     "m-1",
		MIMEType:      "application/pdf",
		FileName:      "report.pdf",
		DownloadState: "remote",
		UpdatedAt:     time.Unix(1_700_200_000, 0),
	}, &store.MediaDownloadDescriptor{
		MessageID:  "m-1",
		Kind:       "document",
		URL:        "https://example.invalid/report.pdf",
		DirectPath: "/v/t62.7118-24/example",
		MediaKey:   []byte("media-key"),
		FileLength: 32,
		UpdatedAt:  time.Unix(1_700_200_000, 0),
	}); err != nil {
		t.Fatalf("UpsertMediaMetadataWithDownload() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{downloads: make(chan fakeDownloadRequest, 1)}
	mediaDir := filepath.Join(t.TempDir(), "media")
	env := Environment{
		Store: db,
		Config: config.Config{
			FileOpenerCommand: "true {path}",
		},
		Paths: config.Paths{
			MediaDir:    mediaDir,
			SessionFile: filepath.Join(t.TempDir(), "session.sqlite3"),
		},
		OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
			return session, nil
		},
		CheckWhatsAppSession: func(context.Context, string) (WhatsAppSessionStatus, error) {
			return WhatsAppSessionStatus{Label: "paired", Paired: true}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	if code := run(env, []string{"media", "open", "m-1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run media open exit = %d, stderr = %q", code, stderr.String())
	}
	if !session.connectCalled || !session.closeCalled {
		t.Fatalf("session connect/close = %v/%v, want true/true", session.connectCalled, session.closeCalled)
	}
	request := <-session.downloads
	if request.descriptor.Kind != "document" {
		t.Fatalf("download descriptor = %+v, want document", request.descriptor)
	}
	stored, err := db.MediaMetadata(ctx, "m-1")
	if err != nil {
		t.Fatalf("MediaMetadata() error = %v", err)
	}
	if stored.LocalPath == "" || stored.DownloadState != "downloaded" {
		t.Fatalf("stored media = %+v, want downloaded local path", stored)
	}
	if got := strings.TrimSpace(stdout.String()); got != stored.LocalPath {
		t.Fatalf("stdout path = %q, want %q", got, stored.LocalPath)
	}
	if _, err := os.Stat(stored.LocalPath); err != nil {
		t.Fatalf("Stat(downloaded) error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunMediaOpenFailsWithoutDescriptor(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ctx := context.Background()
	if err := db.UpsertChat(ctx, store.Chat{ID: "chat-1", JID: "docs@g.us", Title: "Docs", Kind: "group"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	if err := db.AddMessageWithMedia(ctx, store.Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		ChatJID:   "docs@g.us",
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1_700_200_000, 0),
	}, []store.MediaMetadata{{
		MessageID:     "m-1",
		MIMEType:      "application/pdf",
		FileName:      "report.pdf",
		DownloadState: "remote",
		UpdatedAt:     time.Unix(1_700_200_000, 0),
	}}); err != nil {
		t.Fatalf("AddMessageWithMedia() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := run(Environment{
		Store:  db,
		Config: config.Config{FileOpenerCommand: "true {path}"},
	}, []string{"media", "open", "m-1"}, &stdout, &stderr); code != 1 {
		t.Fatalf("run media open exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "cannot be fetched") {
		t.Fatalf("stderr = %q, want missing descriptor error", stderr.String())
	}
}

func TestRunMediaOpenFailsWhenUnpairedForRemoteOnlyMedia(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ctx := context.Background()
	if err := db.UpsertChat(ctx, store.Chat{ID: "chat-1", JID: "docs@g.us", Title: "Docs", Kind: "group"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	if err := db.AddMessageWithMedia(ctx, store.Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		ChatJID:   "docs@g.us",
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1_700_200_000, 0),
	}, []store.MediaMetadata{{
		MessageID:     "m-1",
		MIMEType:      "application/pdf",
		FileName:      "report.pdf",
		DownloadState: "remote",
		UpdatedAt:     time.Unix(1_700_200_000, 0),
	}}); err != nil {
		t.Fatalf("AddMessageWithMedia() error = %v", err)
	}
	if err := db.UpsertMediaMetadataWithDownload(ctx, store.MediaMetadata{
		MessageID:     "m-1",
		MIMEType:      "application/pdf",
		FileName:      "report.pdf",
		DownloadState: "remote",
		UpdatedAt:     time.Unix(1_700_200_000, 0),
	}, &store.MediaDownloadDescriptor{
		MessageID:  "m-1",
		Kind:       "document",
		DirectPath: "/v/t62.7118-24/example",
		MediaKey:   []byte("media-key"),
		FileLength: 32,
		UpdatedAt:  time.Unix(1_700_200_000, 0),
	}); err != nil {
		t.Fatalf("UpsertMediaMetadataWithDownload() error = %v", err)
	}

	sessionOpened := false
	env := Environment{
		Store: db,
		Config: config.Config{
			FileOpenerCommand: "true {path}",
		},
		OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
			sessionOpened = true
			return &fakeLiveWhatsAppSession{}, nil
		},
		CheckWhatsAppSession: func(context.Context, string) (WhatsAppSessionStatus, error) {
			return WhatsAppSessionStatus{Label: "not paired", Paired: false}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	if code := run(env, []string{"media", "open", "m-1"}, &stdout, &stderr); code != 1 {
		t.Fatalf("run media open exit = %d, want 1", code)
	}
	if sessionOpened {
		t.Fatalf("session should not open for unpaired download")
	}
	if !strings.Contains(stderr.String(), "WhatsApp is not paired") {
		t.Fatalf("stderr = %q, want unpaired error", stderr.String())
	}
}

func TestRunExportChatWritesMarkdownFile(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ctx := context.Background()
	chat := store.Chat{ID: "chat-1", JID: "project@g.us", Title: "Project", Kind: "group"}
	if err := db.UpsertChat(ctx, chat); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}

	base := time.Unix(1_700_300_000, 0)
	if err := db.AddMessage(ctx, store.Message{
		ID:        "m-1",
		ChatID:    chat.ID,
		ChatJID:   chat.JID,
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Body:      "Hello from Alice\nwith details",
		Timestamp: base,
	}); err != nil {
		t.Fatalf("AddMessage(m-1) error = %v", err)
	}
	attachmentPath := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(attachmentPath, []byte("pdf"), 0o644); err != nil {
		t.Fatalf("WriteFile(attachment) error = %v", err)
	}
	if err := db.AddMessageWithMedia(ctx, store.Message{
		ID:              "m-2",
		ChatID:          chat.ID,
		ChatJID:         chat.JID,
		Sender:          "me",
		SenderJID:       "me@s.whatsapp.net",
		Timestamp:       base.Add(time.Minute),
		IsOutgoing:      true,
		Status:          "failed",
		QuotedMessageID: "m-1",
		QuotedRemoteID:  "remote-1",
	}, []store.MediaMetadata{{
		MessageID:     "m-2",
		MIMEType:      "application/pdf",
		FileName:      "report.pdf",
		LocalPath:     attachmentPath,
		DownloadState: "downloaded",
		UpdatedAt:     base.Add(time.Minute),
	}}); err != nil {
		t.Fatalf("AddMessageWithMedia(m-2) error = %v", err)
	}

	downloadsDir := filepath.Join(t.TempDir(), "downloads")
	env := Environment{
		Store: db,
		Config: config.Config{
			DownloadsDir: downloadsDir,
		},
	}

	var stdout, stderr bytes.Buffer
	if code := run(env, []string{"export", "chat", chat.JID}, &stdout, &stderr); code != 0 {
		t.Fatalf("run export chat exit = %d, stderr = %q", code, stderr.String())
	}
	exportPath := strings.TrimSpace(stdout.String())
	if exportPath == "" {
		t.Fatalf("stdout = %q, want export path", stdout.String())
	}
	if !strings.HasPrefix(exportPath, downloadsDir+string(os.PathSeparator)) {
		t.Fatalf("export path = %q, want under %q", exportPath, downloadsDir)
	}
	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("ReadFile(export) error = %v", err)
	}
	content := string(data)
	firstHeader := "## " + base.Format("2006-01-02 15:04:05") + " Alice"
	secondHeader := "## " + base.Add(time.Minute).Format("2006-01-02 15:04:05") + " Me"
	for _, want := range []string{
		"# Vimwhat Chat Export",
		"- Chat: Project",
		"- JID: `project@g.us`",
		"- Scope: local SQLite history only",
		firstHeader,
		"Hello from Alice",
		secondHeader,
		"Status: failed",
		"Reply: Alice: Hello from Alice",
		"Attachment: report.pdf (application/pdf, downloaded)",
		"Local file: `" + attachmentPath + "`",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("export content missing %q in:\n%s", want, content)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunExportChatHandlesEmptyAndMissingChats(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ctx := context.Background()
	if err := db.UpsertChat(ctx, store.Chat{ID: "chat-1", JID: "empty@g.us", Title: "Empty", Kind: "group"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}

	downloadsDir := filepath.Join(t.TempDir(), "downloads")
	env := Environment{
		Store: db,
		Config: config.Config{
			DownloadsDir: downloadsDir,
		},
	}

	var stdout, stderr bytes.Buffer
	if code := run(env, []string{"export", "chat", "empty@g.us"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run export chat exit = %d, stderr = %q", code, stderr.String())
	}
	exportPath := strings.TrimSpace(stdout.String())
	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("ReadFile(export) error = %v", err)
	}
	if !strings.Contains(string(data), "_No messages exported._") {
		t.Fatalf("export content = %q, want empty marker", string(data))
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(env, []string{"export", "chat", "missing@g.us"}, &stdout, &stderr); code != 1 {
		t.Fatalf("run export chat missing exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), `chat "missing@g.us" not found`) {
		t.Fatalf("stderr = %q, want missing chat error", stderr.String())
	}
}

func TestRenderTerminalQRRejectsEmptyCode(t *testing.T) {
	var out bytes.Buffer
	if err := renderTerminalQR(&out, " "); err == nil {
		t.Fatalf("renderTerminalQR() error = nil, want empty QR error")
	}
	if out.Len() != 0 {
		t.Fatalf("renderTerminalQR wrote %d bytes for empty QR", out.Len())
	}
}
