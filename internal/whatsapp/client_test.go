package whatsapp

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waAdv"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
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

func TestHistoryAnchorMessageInfo(t *testing.T) {
	when := time.Unix(1_700_000_000, 0)
	info, err := historyAnchorMessageInfo(HistoryAnchor{
		ChatJID:   "12345@s.whatsapp.net",
		MessageID: "ABC123",
		IsFromMe:  true,
		Timestamp: when,
	})
	if err != nil {
		t.Fatalf("historyAnchorMessageInfo() error = %v", err)
	}
	if info.Chat.String() != "12345@s.whatsapp.net" ||
		info.ID != "ABC123" ||
		!info.IsFromMe ||
		!info.Timestamp.Equal(when) {
		t.Fatalf("history anchor info = %+v", info)
	}
}

func TestForwardedMessageFromPayloadMarksTextForwarded(t *testing.T) {
	payload, err := proto.Marshal(&waE2E.Message{Conversation: proto.String("hello")})
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}

	message, err := ForwardedMessageFromPayload(payload)
	if err != nil {
		t.Fatalf("ForwardedMessageFromPayload() error = %v", err)
	}
	text := message.GetExtendedTextMessage()
	if text == nil || text.GetText() != "hello" || message.GetConversation() != "" {
		t.Fatalf("forwarded text message = %+v", message)
	}
	contextInfo := text.GetContextInfo()
	if contextInfo == nil || !contextInfo.GetIsForwarded() || contextInfo.GetForwardingScore() != 1 {
		t.Fatalf("forward context = %+v", contextInfo)
	}
}

func TestMessageFromForwardPayloadCanSkipForwardMetadata(t *testing.T) {
	payload, err := proto.Marshal(&waE2E.Message{Conversation: proto.String("hello")})
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}

	message, err := messageFromForwardPayload(payload, false)
	if err != nil {
		t.Fatalf("messageFromForwardPayload() error = %v", err)
	}
	if message.GetConversation() != "hello" || message.GetExtendedTextMessage() != nil {
		t.Fatalf("self forward text message = %+v", message)
	}
}

func TestMessageFromForwardPayloadStripsExistingForwardMetadata(t *testing.T) {
	payload, err := proto.Marshal(&waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: proto.String("hello"),
			ContextInfo: &waE2E.ContextInfo{
				IsForwarded:     proto.Bool(true),
				ForwardingScore: proto.Uint32(3),
				StanzaID:        proto.String("quoted-1"),
			},
		},
	})
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}

	message, err := messageFromForwardPayload(payload, false)
	if err != nil {
		t.Fatalf("messageFromForwardPayload() error = %v", err)
	}
	contextInfo := message.GetExtendedTextMessage().GetContextInfo()
	if contextInfo == nil || contextInfo.GetIsForwarded() || contextInfo.GetForwardingScore() != 0 || contextInfo.GetStanzaID() != "quoted-1" {
		t.Fatalf("self forward context = %+v", contextInfo)
	}
}

func TestChatAvatarProfilePictureParamsRequestsFullImage(t *testing.T) {
	params := chatAvatarProfilePictureParams(" avatar-1 ")
	if params.Preview {
		t.Fatal("chatAvatarProfilePictureParams() requested preview image, want full image")
	}
	if params.ExistingID != "avatar-1" {
		t.Fatalf("ExistingID = %q, want trimmed avatar id", params.ExistingID)
	}
}

func TestStickerSyncUsesAllAppStatePatchNames(t *testing.T) {
	if !slices.Equal(stickerAppStatePatchNames[:], appstate.AllPatchNames[:]) {
		t.Fatalf("sticker app-state patches = %#v, want all WhatsApp patch names %#v", stickerAppStatePatchNames, appstate.AllPatchNames)
	}
}

func TestMediaDownloadValidationLengthRequiresPlaintextSHA(t *testing.T) {
	withoutPlaintextSHA := MediaDownloadDescriptor{FileLength: 123}
	if !mediaDownloadNeedsBufferedWrite(withoutPlaintextSHA) {
		t.Fatal("mediaDownloadNeedsBufferedWrite() = false, want true without plaintext SHA")
	}
	if got := mediaDownloadValidationLength(withoutPlaintextSHA); got != -1 {
		t.Fatalf("mediaDownloadValidationLength(without SHA) = %d, want -1", got)
	}

	withPlaintextSHA := MediaDownloadDescriptor{
		FileSHA256: []byte("01234567890123456789012345678901"),
		FileLength: 123,
	}
	if mediaDownloadNeedsBufferedWrite(withPlaintextSHA) {
		t.Fatal("mediaDownloadNeedsBufferedWrite() = true, want false with plaintext SHA")
	}
	if got := mediaDownloadValidationLength(withPlaintextSHA); got != 123 {
		t.Fatalf("mediaDownloadValidationLength(with SHA) = %d, want 123", got)
	}
}

func TestMediaDownloadSourcesPreferURLBeforeDirectPath(t *testing.T) {
	descriptor := MediaDownloadDescriptor{
		URL:        " https://mmg.whatsapp.net/sticker ",
		DirectPath: " /v/t62.15575-24/sticker.enc ",
	}
	got := mediaDownloadSources(descriptor)
	want := []mediaDownloadSource{mediaDownloadSourceURL, mediaDownloadSourceDirectPath}
	if !slices.Equal(got, want) {
		t.Fatalf("mediaDownloadSources() = %#v, want %#v", got, want)
	}

	webOnly := mediaDownloadSources(MediaDownloadDescriptor{
		URL: "https://web.whatsapp.net/media",
	})
	if len(webOnly) != 0 {
		t.Fatalf("mediaDownloadSources(web URL) = %#v, want no usable source", webOnly)
	}
}

func TestMediaDownloadDescriptorForSourceIsolatesDownloadSource(t *testing.T) {
	descriptor := MediaDownloadDescriptor{
		URL:        "https://mmg.whatsapp.net/sticker",
		DirectPath: "/v/t62.15575-24/sticker.enc",
	}

	urlDescriptor := mediaDownloadDescriptorForSource(descriptor, mediaDownloadSourceURL)
	if urlDescriptor.URL == "" || urlDescriptor.DirectPath != "" {
		t.Fatalf("url descriptor = %+v, want URL-only", urlDescriptor)
	}
	directDescriptor := mediaDownloadDescriptorForSource(descriptor, mediaDownloadSourceDirectPath)
	if directDescriptor.URL != "" || directDescriptor.DirectPath == "" {
		t.Fatalf("direct descriptor = %+v, want direct-path-only", directDescriptor)
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
