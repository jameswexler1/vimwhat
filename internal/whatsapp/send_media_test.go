package whatsapp

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow"
)

func TestLoadLocalMediaDetailsClassifiesImageAndReadsDimensions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	details, kind, err := loadLocalMediaDetails(MediaSendRequest{LocalPath: path})
	if err != nil {
		t.Fatalf("loadLocalMediaDetails() error = %v", err)
	}
	if kind != outgoingMediaImage {
		t.Fatalf("kind = %q, want %q", kind, outgoingMediaImage)
	}
	if details.FileName != "photo.png" || details.MIMEType != "image/png" {
		t.Fatalf("details = %+v, want png file metadata", details)
	}
	if details.Width != 2 || details.Height != 3 {
		t.Fatalf("dimensions = %dx%d, want 2x3", details.Width, details.Height)
	}
}

func TestOutgoingKindForFileFallsBackToDocument(t *testing.T) {
	kind := outgoingKindForFile("application/octet-stream", "notes.bin")
	if kind != outgoingMediaDocument {
		t.Fatalf("kind = %q, want %q", kind, outgoingMediaDocument)
	}
	if got := mediaTypeForOutgoingKind(kind); got != whatsmeow.MediaDocument {
		t.Fatalf("mediaTypeForOutgoingKind() = %q, want %q", got, whatsmeow.MediaDocument)
	}
}

func TestMediaMessageFromUploadIncludesReplyContext(t *testing.T) {
	client := &Client{}
	message := client.mediaMessageFromUpload(outgoingMediaDocument, localMediaDetails{
		FileName: "report.pdf",
		MIMEType: "application/pdf",
	}, "caption", whatsmeow.UploadResponse{
		URL:           "https://example.com/file",
		DirectPath:    "/v/t62.7118-24/file",
		MediaKey:      []byte{1},
		FileSHA256:    []byte{2},
		FileEncSHA256: []byte{3},
		FileLength:    42,
	}, MediaSendRequest{
		QuotedRemoteID:    "quoted-1",
		QuotedSenderJID:   "12345@s.whatsapp.net",
		QuotedMessageBody: "hello there",
	})

	document := message.GetDocumentMessage()
	if document == nil {
		t.Fatalf("message = %+v, want document message", message)
	}
	if document.GetCaption() != "caption" || document.GetFileName() != "report.pdf" || document.GetMimetype() != "application/pdf" {
		t.Fatalf("document = %+v, want caption and file metadata", document)
	}
	contextInfo := document.GetContextInfo()
	if contextInfo == nil || contextInfo.GetStanzaID() != "quoted-1" || contextInfo.GetParticipant() != "12345@s.whatsapp.net" {
		t.Fatalf("contextInfo = %+v, want stanza id and participant", contextInfo)
	}
	if quoted := contextInfo.GetQuotedMessage(); quoted.GetConversation() != "hello there" {
		t.Fatalf("quoted message = %+v, want conversation body", quoted)
	}
}
