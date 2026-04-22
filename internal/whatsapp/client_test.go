package whatsapp

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionURIUsesModernCSQLitePragmas(t *testing.T) {
	raw := SessionURI(filepath.Join("/tmp", "vimwhat session.sqlite3"))
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if parsed.Scheme != "file" {
		t.Fatalf("scheme = %q, want file", parsed.Scheme)
	}

	pragmas := parsed.Query()["_pragma"]
	want := map[string]bool{
		"foreign_keys=on":   false,
		"busy_timeout=5000": false,
		"journal_mode=WAL":  false,
	}
	for _, pragma := range pragmas {
		if _, ok := want[pragma]; ok {
			want[pragma] = true
		}
	}
	for pragma, found := range want {
		if !found {
			t.Fatalf("SessionURI pragmas = %#v, missing %q", pragmas, pragma)
		}
	}
}

func TestCheckSessionStatusMissingDoesNotCreateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.sqlite3")

	status, err := CheckSessionStatus(context.Background(), path)
	if err != nil {
		t.Fatalf("CheckSessionStatus() error = %v", err)
	}
	if status.State != SessionMissing {
		t.Fatalf("status.State = %q, want %q", status.State, SessionMissing)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("session file stat error = %v, want not exist", err)
	}
}

func TestOpenSessionCreatesUnpairedStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite3")

	client, err := OpenSession(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenSession() error = %v", err)
	}
	if client.IsLoggedIn() {
		t.Fatalf("new session reports logged in")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	status, err := CheckSessionStatus(context.Background(), path)
	if err != nil {
		t.Fatalf("CheckSessionStatus() error = %v", err)
	}
	if status.State != SessionUnpaired {
		t.Fatalf("status.State = %q, want %q", status.State, SessionUnpaired)
	}
}
