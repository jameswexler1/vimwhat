package whatsapp

import (
	"context"
	"errors"
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

	details, kind, err := loadLocalMediaDetails(context.Background(), MediaSendRequest{LocalPath: path})
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
	if details.TransportKind != outgoingMediaImage {
		t.Fatalf("transport kind = %q, want %q", details.TransportKind, outgoingMediaImage)
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

func TestLoadLocalMediaDetailsKeepsAudioTransportWhenProbeSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(path, []byte("audio-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	restore := stubAudioDurationProbe(t, func(context.Context, string) (uint32, error) {
		return 17, nil
	})
	defer restore()

	details, kind, err := loadLocalMediaDetails(context.Background(), MediaSendRequest{
		LocalPath: path,
		MIMEType:  "audio/ogg",
	})
	if err != nil {
		t.Fatalf("loadLocalMediaDetails() error = %v", err)
	}
	if kind != outgoingMediaAudio {
		t.Fatalf("kind = %q, want %q", kind, outgoingMediaAudio)
	}
	if details.TransportKind != outgoingMediaAudio || details.DurationSeconds != 17 {
		t.Fatalf("details = %+v, want audio transport with duration", details)
	}
	if details.SendNotice != "" {
		t.Fatalf("send notice = %q, want empty", details.SendNotice)
	}
}

func TestLoadLocalMediaDetailsDowngradesAudioWhenProbeFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(path, []byte("audio-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	restore := stubAudioDurationProbe(t, func(context.Context, string) (uint32, error) {
		return 0, errors.New("ffprobe failed")
	})
	defer restore()

	details, kind, err := loadLocalMediaDetails(context.Background(), MediaSendRequest{
		LocalPath: path,
		MIMEType:  "audio/ogg",
	})
	if err != nil {
		t.Fatalf("loadLocalMediaDetails() error = %v", err)
	}
	if kind != outgoingMediaAudio {
		t.Fatalf("kind = %q, want %q", kind, outgoingMediaAudio)
	}
	if details.TransportKind != outgoingMediaDocument {
		t.Fatalf("transport kind = %q, want %q", details.TransportKind, outgoingMediaDocument)
	}
	if details.DurationSeconds != 0 {
		t.Fatalf("duration = %d, want 0", details.DurationSeconds)
	}
	if details.SendNotice == "" {
		t.Fatal("send notice is empty, want fallback explanation")
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

func TestMediaMessageFromUploadIncludesMentionContext(t *testing.T) {
	client := &Client{}
	message := client.mediaMessageFromUpload(outgoingMediaImage, localMediaDetails{
		MIMEType: "image/png",
	}, "hi @Ana", whatsmeow.UploadResponse{
		URL:           "https://example.com/image",
		DirectPath:    "/v/t62.7118-24/image",
		MediaKey:      []byte{1},
		FileSHA256:    []byte{2},
		FileEncSHA256: []byte{3},
		FileLength:    42,
	}, MediaSendRequest{
		MentionedJIDs: []string{"222@s.whatsapp.net"},
	})

	image := message.GetImageMessage()
	if image == nil {
		t.Fatalf("message = %+v, want image message", message)
	}
	mentions := image.GetContextInfo().GetMentionedJID()
	if len(mentions) != 1 || mentions[0] != "222@s.whatsapp.net" {
		t.Fatalf("MentionedJID = %+v, want participant", mentions)
	}
}

func TestMediaMessageFromUploadBuildsAudioMessage(t *testing.T) {
	client := &Client{}
	message := client.mediaMessageFromUpload(outgoingMediaAudio, localMediaDetails{
		MIMEType:        "audio/ogg",
		DurationSeconds: 12,
	}, "", whatsmeow.UploadResponse{
		URL:           "https://example.com/audio",
		DirectPath:    "/v/t62.7118-24/audio",
		MediaKey:      []byte{1},
		FileSHA256:    []byte{2},
		FileEncSHA256: []byte{3},
		FileLength:    128,
	}, MediaSendRequest{})

	audio := message.GetAudioMessage()
	if audio == nil {
		t.Fatalf("message = %+v, want audio message", message)
	}
	if audio.GetMimetype() != "audio/ogg" {
		t.Fatalf("mimetype = %q, want audio/ogg", audio.GetMimetype())
	}
	if audio.GetSeconds() != 12 {
		t.Fatalf("seconds = %d, want 12", audio.GetSeconds())
	}
	if audio.GetPTT() {
		t.Fatal("audio.GetPTT() = true, want false")
	}
}

func stubAudioDurationProbe(t *testing.T, probe func(context.Context, string) (uint32, error)) func() {
	t.Helper()
	previous := probeAudioDurationSeconds
	probeAudioDurationSeconds = probe
	return func() {
		probeAudioDurationSeconds = previous
	}
}
