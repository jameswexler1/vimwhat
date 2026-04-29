package whatsapp

import (
	"context"
	"fmt"
	"image"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

var probeAudioDurationSeconds = defaultProbeAudioDurationSeconds

type MediaSendRequest struct {
	ChatJID           string
	LocalPath         string
	FileName          string
	MIMEType          string
	Caption           string
	RemoteID          string
	MentionedJIDs     []string
	QuotedRemoteID    string
	QuotedSenderJID   string
	QuotedMessageBody string
}

type outgoingMediaKind string

const (
	outgoingMediaImage    outgoingMediaKind = "image"
	outgoingMediaVideo    outgoingMediaKind = "video"
	outgoingMediaAudio    outgoingMediaKind = "audio"
	outgoingMediaDocument outgoingMediaKind = "document"
)

type localMediaDetails struct {
	LocalPath       string
	FileName        string
	MIMEType        string
	SizeBytes       uint64
	Width           uint32
	Height          uint32
	DurationSeconds uint32
	TransportKind   outgoingMediaKind
	SendNotice      string
}

func (c *Client) SendMedia(ctx context.Context, request MediaSendRequest) (SendResult, error) {
	if c == nil || c.client == nil {
		return SendResult{}, ErrClientNotOpen
	}
	normalizedChatJID, err := NormalizeSendChatJID(request.ChatJID)
	if err != nil {
		return SendResult{}, err
	}
	to, err := types.ParseJID(normalizedChatJID)
	if err != nil {
		return SendResult{}, fmt.Errorf("parse send chat jid: %w", err)
	}
	details, kind, err := loadLocalMediaDetails(ctx, request)
	if err != nil {
		return SendResult{}, err
	}
	caption := strings.TrimSpace(request.Caption)
	if kind == outgoingMediaAudio && caption != "" {
		return SendResult{}, fmt.Errorf("audio attachments do not support captions")
	}
	remoteID := strings.TrimSpace(request.RemoteID)
	if remoteID == "" {
		remoteID = c.GenerateMessageID()
	}
	if remoteID == "" {
		return SendResult{}, fmt.Errorf("generate message id failed")
	}

	file, err := os.Open(details.LocalPath)
	if err != nil {
		return SendResult{}, fmt.Errorf("open media file: %w", err)
	}
	defer file.Close()

	uploadType := mediaTypeForOutgoingKind(details.TransportKind)
	upload, err := c.client.UploadReader(ctx, file, nil, uploadType)
	if err != nil {
		return SendResult{}, fmt.Errorf("upload whatsapp media: %w", err)
	}

	message := c.mediaMessageFromUpload(details.TransportKind, details, caption, upload, request)
	resp, err := c.client.SendMessage(ctx, to, message, whatsmeow.SendRequestExtra{ID: types.MessageID(remoteID)})
	if err != nil {
		return SendResult{}, fmt.Errorf("send whatsapp media: %w", err)
	}
	if resp.ID != "" {
		remoteID = string(resp.ID)
	}
	timestamp := resp.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	return SendResult{
		MessageID: LocalMessageID(normalizedChatJID, remoteID),
		RemoteID:  remoteID,
		Status:    "sent",
		Timestamp: timestamp,
		Notice:    details.SendNotice,
	}, nil
}

func loadLocalMediaDetails(ctx context.Context, request MediaSendRequest) (localMediaDetails, outgoingMediaKind, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	localPath := strings.TrimSpace(request.LocalPath)
	if localPath == "" {
		return localMediaDetails{}, "", fmt.Errorf("media local path is required")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return localMediaDetails{}, "", fmt.Errorf("stat media file: %w", err)
	}
	if info.IsDir() {
		return localMediaDetails{}, "", fmt.Errorf("media path is a directory")
	}

	fileName := strings.TrimSpace(request.FileName)
	if fileName == "" {
		fileName = info.Name()
	}
	mimeType := normalizeOutgoingMIMEType(strings.TrimSpace(request.MIMEType), localPath, fileName)
	kind := outgoingKindForFile(mimeType, fileName)

	details := localMediaDetails{
		LocalPath:     localPath,
		FileName:      fileName,
		MIMEType:      mimeType,
		SizeBytes:     uint64(info.Size()),
		TransportKind: kind,
	}
	if kind == outgoingMediaImage {
		width, height := imageDimensions(localPath)
		details.Width = width
		details.Height = height
	}
	if kind == outgoingMediaAudio {
		durationSeconds, probeErr := probeAudioDurationSeconds(ctx, localPath)
		if probeErr == nil && durationSeconds > 0 {
			details.DurationSeconds = durationSeconds
		} else {
			details.TransportKind = outgoingMediaDocument
			details.SendNotice = "audio metadata unavailable; sent as document"
		}
	}
	return details, kind, nil
}

func defaultProbeAudioDurationSeconds(ctx context.Context, localPath string) (uint32, error) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return 0, err
	}
	output, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		localPath,
	).Output()
	if err != nil {
		return 0, err
	}
	durationText := strings.TrimSpace(string(output))
	if durationText == "" || durationText == "N/A" {
		return 0, fmt.Errorf("audio duration is unavailable")
	}
	duration, err := strconv.ParseFloat(durationText, 64)
	if err != nil {
		return 0, fmt.Errorf("parse audio duration %q: %w", durationText, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("audio duration must be positive")
	}
	seconds := uint32(duration)
	if duration > float64(seconds) {
		seconds++
	}
	if seconds == 0 {
		seconds = 1
	}
	return seconds, nil
}

func normalizeOutgoingMIMEType(mimeType, localPath, fileName string) string {
	if mimeType != "" {
		return mimeType
	}
	if guessed := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName))); guessed != "" {
		return guessed
	}
	file, err := os.Open(localPath)
	if err == nil {
		defer file.Close()
		var buf [512]byte
		n, readErr := file.Read(buf[:])
		if readErr == nil || (readErr != nil && n > 0) {
			return http.DetectContentType(buf[:n])
		}
	}
	return "application/octet-stream"
}

func outgoingKindForFile(mimeType, fileName string) outgoingMediaKind {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	fileName = strings.ToLower(strings.TrimSpace(fileName))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return outgoingMediaImage
	case strings.HasPrefix(mimeType, "video/"):
		return outgoingMediaVideo
	case strings.HasPrefix(mimeType, "audio/"):
		return outgoingMediaAudio
	case strings.HasSuffix(fileName, ".jpg"),
		strings.HasSuffix(fileName, ".jpeg"),
		strings.HasSuffix(fileName, ".png"),
		strings.HasSuffix(fileName, ".gif"),
		strings.HasSuffix(fileName, ".webp"):
		return outgoingMediaImage
	case strings.HasSuffix(fileName, ".mp4"),
		strings.HasSuffix(fileName, ".mov"),
		strings.HasSuffix(fileName, ".mkv"),
		strings.HasSuffix(fileName, ".webm"):
		return outgoingMediaVideo
	case strings.HasSuffix(fileName, ".ogg"),
		strings.HasSuffix(fileName, ".opus"),
		strings.HasSuffix(fileName, ".mp3"),
		strings.HasSuffix(fileName, ".m4a"),
		strings.HasSuffix(fileName, ".aac"),
		strings.HasSuffix(fileName, ".wav"),
		strings.HasSuffix(fileName, ".flac"),
		strings.HasSuffix(fileName, ".oga"):
		return outgoingMediaAudio
	default:
		return outgoingMediaDocument
	}
}

func mediaTypeForOutgoingKind(kind outgoingMediaKind) whatsmeow.MediaType {
	switch kind {
	case outgoingMediaImage:
		return whatsmeow.MediaImage
	case outgoingMediaVideo:
		return whatsmeow.MediaVideo
	case outgoingMediaAudio:
		return whatsmeow.MediaAudio
	default:
		return whatsmeow.MediaDocument
	}
}

func imageDimensions(path string) (uint32, uint32) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0
	}
	if config.Width <= 0 || config.Height <= 0 {
		return 0, 0
	}
	return uint32(config.Width), uint32(config.Height)
}

func (c *Client) mediaMessageFromUpload(kind outgoingMediaKind, details localMediaDetails, caption string, upload whatsmeow.UploadResponse, request MediaSendRequest) *waE2E.Message {
	contextInfo := c.messageContextInfo(request.QuotedRemoteID, request.QuotedSenderJID, request.QuotedMessageBody, request.MentionedJIDs)
	switch kind {
	case outgoingMediaImage:
		image := &waE2E.ImageMessage{
			URL:           proto.String(upload.URL),
			DirectPath:    proto.String(upload.DirectPath),
			MediaKey:      upload.MediaKey,
			FileSHA256:    upload.FileSHA256,
			FileEncSHA256: upload.FileEncSHA256,
			FileLength:    proto.Uint64(upload.FileLength),
			Mimetype:      proto.String(details.MIMEType),
			Caption:       proto.String(caption),
			ContextInfo:   contextInfo,
		}
		if details.Width > 0 {
			image.Width = proto.Uint32(details.Width)
		}
		if details.Height > 0 {
			image.Height = proto.Uint32(details.Height)
		}
		return &waE2E.Message{ImageMessage: image}
	case outgoingMediaVideo:
		video := &waE2E.VideoMessage{
			URL:           proto.String(upload.URL),
			DirectPath:    proto.String(upload.DirectPath),
			MediaKey:      upload.MediaKey,
			FileSHA256:    upload.FileSHA256,
			FileEncSHA256: upload.FileEncSHA256,
			FileLength:    proto.Uint64(upload.FileLength),
			Mimetype:      proto.String(details.MIMEType),
			Caption:       proto.String(caption),
			ContextInfo:   contextInfo,
		}
		return &waE2E.Message{VideoMessage: video}
	case outgoingMediaAudio:
		audio := &waE2E.AudioMessage{
			URL:           proto.String(upload.URL),
			DirectPath:    proto.String(upload.DirectPath),
			MediaKey:      upload.MediaKey,
			FileSHA256:    upload.FileSHA256,
			FileEncSHA256: upload.FileEncSHA256,
			FileLength:    proto.Uint64(upload.FileLength),
			Mimetype:      proto.String(details.MIMEType),
			ContextInfo:   contextInfo,
			PTT:           proto.Bool(false),
		}
		if details.DurationSeconds > 0 {
			audio.Seconds = proto.Uint32(details.DurationSeconds)
		}
		return &waE2E.Message{AudioMessage: audio}
	default:
		document := &waE2E.DocumentMessage{
			URL:           proto.String(upload.URL),
			DirectPath:    proto.String(upload.DirectPath),
			MediaKey:      upload.MediaKey,
			FileSHA256:    upload.FileSHA256,
			FileEncSHA256: upload.FileEncSHA256,
			FileLength:    proto.Uint64(upload.FileLength),
			Mimetype:      proto.String(details.MIMEType),
			FileName:      proto.String(details.FileName),
			Caption:       proto.String(caption),
			ContextInfo:   contextInfo,
		}
		return &waE2E.Message{DocumentMessage: document}
	}
}
