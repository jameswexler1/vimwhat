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

	"vimwhat/internal/config"
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
	events          chan whatsapp.Event
	historyRequests chan fakeHistoryRequest
	downloads       chan fakeDownloadRequest
	sends           chan fakeSendRequest
	mediaSends      chan whatsapp.MediaSendRequest
	readReceipts    chan []whatsapp.ReadReceiptTarget
	reactions       chan whatsapp.ReactionSendRequest
	presences       chan fakePresenceRequest
	subscriptions   chan string
	historyErr      error
	downloadErr     error
	sendErr         error
	readErr         error
	reactionErr     error
	presenceErr     error
	generatedID     string
	connectErr      error
	subscribeErr    error
	connectCalled   bool
	subscribeCalled bool
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

func (s *fakeLiveWhatsAppSession) GenerateMessageID() string {
	if s.generatedID != "" {
		return s.generatedID
	}
	return "remote-generated"
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
	return os.WriteFile(targetPath, []byte("downloaded media"), 0o644)
}

func (s *fakeMetadataLiveWhatsAppSession) RefreshChatMetadata(context.Context) ([]whatsapp.Event, error) {
	return s.metadataEvents, s.metadataErr
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
		runLiveWhatsApp(ctx, env, updates, historyRequests, make(chan textSendRequest, 16), make(chan mediaSendRequest, 16), make(chan readReceiptRequest, 16), make(chan reactionRequest, 16), make(chan presenceRequest, 16), make(chan presenceSubscribeRequest, 16), make(chan mediaDownloadRequest, 16))
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
		runLiveWhatsApp(ctx, env, updates, make(chan string, 16), make(chan textSendRequest, 16), make(chan mediaSendRequest, 16), make(chan readReceiptRequest, 16), make(chan reactionRequest, 16), make(chan presenceRequest, 16), make(chan presenceSubscribeRequest, 16), make(chan mediaDownloadRequest, 16))
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
		Body:   "hello live",
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
	if request.request.ChatJID != chatJID || request.request.Body != "hello live" || request.request.RemoteID != "remote-1" {
		t.Fatalf("send request = %+v, want jid/body/remote id", request)
	}
	messages := waitForStoredMessages(t, db, chatJID, 1)
	if len(messages) != 1 || messages[0].ID != queued.Message.ID || messages[0].Status != "sent" {
		t.Fatalf("stored messages = %+v, want one sent queued message", messages)
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
		Body:   "caption",
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
	if request.ChatJID != chatJID || request.Caption != "caption" || request.LocalPath != attachmentPath || request.RemoteID != "remote-1" {
		t.Fatalf("media request = %+v, want jid/caption/path/remote id", request)
	}
	messages := waitForStoredMessages(t, db, chatJID, 1)
	if len(messages) != 1 || messages[0].ID != queued.Message.ID || messages[0].Status != "sent" || len(messages[0].Media) != 1 {
		t.Fatalf("stored messages = %+v, want one sent media message", messages)
	}
	if messages[0].Media[0].LocalPath != attachmentPath || messages[0].Media[0].DownloadState != "downloaded" {
		t.Fatalf("stored media = %+v, want local downloaded attachment", messages[0].Media[0])
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
		Body:            "retry caption",
		Timestamp:       time.Unix(1_700_000_100, 0),
		IsOutgoing:      true,
		Status:          "failed",
		QuotedMessageID: quoted.ID,
		QuotedRemoteID:  quoted.RemoteID,
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
	if sent.ChatJID != chatJID || sent.Caption != "retry caption" || sent.LocalPath != attachmentPath {
		t.Fatalf("media request = %+v, want chat/caption/path preserved", sent)
	}
	if sent.QuotedRemoteID != quoted.RemoteID || sent.QuotedSenderJID != quoted.SenderJID || sent.QuotedMessageBody != quoted.Body {
		t.Fatalf("retry quote = %+v, want quoted remote id/sender/body", sent)
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
	media, err := downloadRemoteMedia(ctx, db, session, filepath.Join(t.TempDir(), "media"), mediaDownloadRequest{
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

	_, err = downloadRemoteMedia(ctx, db, &fakeLiveWhatsAppSession{}, filepath.Join(t.TempDir(), "media"), mediaDownloadRequest{
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

	_, err = downloadRemoteMedia(ctx, db, &fakeLiveWhatsAppSession{downloadErr: errors.New("boom")}, filepath.Join(t.TempDir(), "media"), mediaDownloadRequest{
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

func TestRunLogoutDoesNotOpenMissingSession(t *testing.T) {
	var openCalled bool
	env := Environment{
		Paths: config.Paths{SessionFile: "/tmp/vimwhat-session.sqlite3"},
		OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
			openCalled = true
			return nil, errors.New("unexpected open")
		},
		CheckWhatsAppSession: func(context.Context, string) (WhatsAppSessionStatus, error) {
			return WhatsAppSessionStatus{Label: "not configured", Paired: false}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	if code := run(env, []string{"logout"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run logout exit = %d, stderr = %q", code, stderr.String())
	}
	if openCalled {
		t.Fatalf("session was opened for a missing login")
	}
	if !strings.Contains(stdout.String(), "not logged in") {
		t.Fatalf("stdout = %q, want not logged in", stdout.String())
	}
}

func TestRunLogoutLogsOutPairedSession(t *testing.T) {
	session := &fakeWhatsAppSession{loggedIn: true}
	env := Environment{
		Paths: config.Paths{SessionFile: "/tmp/vimwhat-session.sqlite3"},
		OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) {
			return session, nil
		},
		CheckWhatsAppSession: func(context.Context, string) (WhatsAppSessionStatus, error) {
			return WhatsAppSessionStatus{Label: "paired locally (123@s.whatsapp.net)", Paired: true}, nil
		},
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
	if !strings.Contains(stdout.String(), "logged out") {
		t.Fatalf("stdout = %q, want logged out", stdout.String())
	}
}

func TestPrintDoctorUsesWhatsAppSessionStatus(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer db.Close()

	env := Environment{
		Paths: config.Paths{
			SessionFile:  "/tmp/vimwhat-session.sqlite3",
			ConfigFile:   "/tmp/config.toml",
			DataDir:      "/tmp/data",
			CacheDir:     "/tmp/cache",
			DatabaseFile: "/tmp/state.sqlite3",
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
