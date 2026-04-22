package whatsapp

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/types"
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

func TestDeviceSaveThenIdentityInsertSatisfiesForeignKey(t *testing.T) {
	ctx := context.Background()
	client, err := OpenSession(ctx, filepath.Join(t.TempDir(), "session.sqlite3"))
	if err != nil {
		t.Fatalf("OpenSession() error = %v", err)
	}
	defer client.Close()

	jid := types.NewJID("12345", types.DefaultUserServer)
	jid.Device = 12
	lid := types.NewJID("67890", types.HiddenUserServer)
	lid.Device = 12
	client.client.Store.ID = &jid
	client.client.Store.LID = lid
	client.client.Store.Account = testAccount()
	if err := client.client.Store.Save(ctx); err != nil {
		t.Fatalf("Store.Save() error = %v", err)
	}

	var key [32]byte
	if err := client.client.Store.Identities.PutIdentity(ctx, lid.SignalAddress().String(), key); err != nil {
		t.Fatalf("PutIdentity() error = %v", err)
	}
}

func TestResetSessionReinitializesStoresAfterRejectedSession(t *testing.T) {
	ctx := context.Background()
	client, err := OpenSession(ctx, filepath.Join(t.TempDir(), "session.sqlite3"))
	if err != nil {
		t.Fatalf("OpenSession() error = %v", err)
	}
	defer client.Close()

	oldJID := types.NewJID("11111", types.DefaultUserServer)
	oldJID.Device = 8
	client.client.Store.ID = &oldJID
	client.client.Store.LID = types.NewJID("22222", types.HiddenUserServer)
	client.client.Store.Account = testAccount()
	if err := client.client.Store.Save(ctx); err != nil {
		t.Fatalf("Store.Save(old) error = %v", err)
	}

	if err := client.resetSession(ctx); err != nil {
		t.Fatalf("resetSession() error = %v", err)
	}

	newJID := types.NewJID("33333", types.DefaultUserServer)
	newJID.Device = 12
	newLID := types.NewJID("44444", types.HiddenUserServer)
	newLID.Device = 12
	client.client.Store.ID = &newJID
	client.client.Store.LID = newLID
	client.client.Store.Account = testAccount()
	if err := client.client.Store.Save(ctx); err != nil {
		t.Fatalf("Store.Save(new) error = %v", err)
	}

	var key [32]byte
	if err := client.client.Store.Identities.PutIdentity(ctx, newLID.SignalAddress().String(), key); err != nil {
		t.Fatalf("PutIdentity() after reset error = %v", err)
	}
}

func testAccount() *waAdv.ADVSignedDeviceIdentity {
	return &waAdv.ADVSignedDeviceIdentity{
		Details:             []byte("details"),
		AccountSignature:    make([]byte, 64),
		AccountSignatureKey: make([]byte, 32),
		DeviceSignature:     make([]byte, 64),
	}
}
