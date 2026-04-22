package app

import (
	"bytes"
	"context"
	"errors"
	"io"
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
	historyErr      error
	connectErr      error
	subscribeErr    error
	connectCalled   bool
	subscribeCalled bool
}

type fakeHistoryRequest struct {
	anchor whatsapp.HistoryAnchor
	limit  int
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
		runLiveWhatsApp(ctx, env, updates, historyRequests)
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
