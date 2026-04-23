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
	historyErr      error
	downloadErr     error
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
		runLiveWhatsApp(ctx, env, updates, historyRequests, make(chan mediaDownloadRequest, 16))
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
		runLiveWhatsApp(ctx, env, updates, make(chan string, 16), make(chan mediaDownloadRequest, 16))
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
