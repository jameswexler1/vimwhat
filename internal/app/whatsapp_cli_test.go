package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"vimwhat/internal/config"
	"vimwhat/internal/store"
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
			return WhatsAppSessionStatus{Label: "not configured", LoggedIn: false}, nil
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
			return WhatsAppSessionStatus{Label: "logged in (123@s.whatsapp.net)", LoggedIn: true}, nil
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
			return WhatsAppSessionStatus{Label: "logged in (123@s.whatsapp.net)", LoggedIn: true}, nil
		},
	}

	var out bytes.Buffer
	printDoctor(env, &out)
	if !strings.Contains(out.String(), "session status: logged in (123@s.whatsapp.net)") {
		t.Fatalf("doctor output = %q, want logged-in session status", out.String())
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
