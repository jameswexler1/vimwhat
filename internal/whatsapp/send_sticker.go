package whatsapp

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

type StickerSendRequest struct {
	ChatJID            string
	LocalPath          string
	FileName           string
	MIMEType           string
	Width              uint32
	Height             uint32
	IsAnimated         bool
	IsLottie           bool
	AccessibilityLabel string
	RemoteID           string
	QuotedRemoteID     string
	QuotedSenderJID    string
	QuotedMessageBody  string
}

type localStickerDetails struct {
	LocalPath          string
	FileName           string
	MIMEType           string
	SizeBytes          uint64
	Width              uint32
	Height             uint32
	IsAnimated         bool
	IsLottie           bool
	AccessibilityLabel string
}

func (c *Client) SendSticker(ctx context.Context, request StickerSendRequest) (SendResult, error) {
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
	details, err := loadLocalStickerDetails(request)
	if err != nil {
		return SendResult{}, err
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
		return SendResult{}, fmt.Errorf("open sticker file: %w", err)
	}
	defer file.Close()

	upload, err := c.client.UploadReader(ctx, file, nil, whatsmeow.MediaImage)
	if err != nil {
		return SendResult{}, fmt.Errorf("upload whatsapp sticker: %w", err)
	}

	message := c.stickerMessageFromUpload(details, upload, request, time.Now())
	resp, err := c.client.SendMessage(ctx, to, message, whatsmeow.SendRequestExtra{ID: types.MessageID(remoteID)})
	if err != nil {
		return SendResult{}, fmt.Errorf("send whatsapp sticker: %w", err)
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
	}, nil
}

func loadLocalStickerDetails(request StickerSendRequest) (localStickerDetails, error) {
	localPath := strings.TrimSpace(request.LocalPath)
	if localPath == "" {
		return localStickerDetails{}, fmt.Errorf("sticker local path is required")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return localStickerDetails{}, fmt.Errorf("stat sticker file: %w", err)
	}
	if info.IsDir() {
		return localStickerDetails{}, fmt.Errorf("sticker path is a directory")
	}

	fileName := strings.TrimSpace(request.FileName)
	if fileName == "" {
		fileName = info.Name()
	}
	mimeType := normalizeOutgoingMIMEType(strings.TrimSpace(request.MIMEType), localPath, fileName)
	if mimeType == "application/octet-stream" {
		if guessed := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName))); guessed != "" {
			mimeType = guessed
		}
	}
	if request.IsLottie {
		return localStickerDetails{}, fmt.Errorf("lottie sticker send is not supported yet")
	}
	if !isSupportedStickerMIME(mimeType, fileName) {
		return localStickerDetails{}, fmt.Errorf("unsupported sticker MIME type %q", mimeType)
	}

	return localStickerDetails{
		LocalPath:          localPath,
		FileName:           fileName,
		MIMEType:           mimeType,
		SizeBytes:          uint64(info.Size()),
		Width:              request.Width,
		Height:             request.Height,
		IsAnimated:         request.IsAnimated,
		IsLottie:           request.IsLottie,
		AccessibilityLabel: strings.TrimSpace(request.AccessibilityLabel),
	}, nil
}

func isSupportedStickerMIME(mimeType, fileName string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	fileName = strings.ToLower(strings.TrimSpace(fileName))
	return mimeType == "image/webp" || strings.HasSuffix(fileName, ".webp")
}

func (c *Client) stickerMessageFromUpload(details localStickerDetails, upload whatsmeow.UploadResponse, request StickerSendRequest, sentAt time.Time) *waE2E.Message {
	if sentAt.IsZero() {
		sentAt = time.Now()
	}
	sticker := &waE2E.StickerMessage{
		URL:                proto.String(upload.URL),
		DirectPath:         proto.String(upload.DirectPath),
		MediaKey:           upload.MediaKey,
		FileSHA256:         upload.FileSHA256,
		FileEncSHA256:      upload.FileEncSHA256,
		FileLength:         proto.Uint64(upload.FileLength),
		Mimetype:           proto.String(details.MIMEType),
		IsAnimated:         proto.Bool(details.IsAnimated),
		IsLottie:           proto.Bool(details.IsLottie),
		StickerSentTS:      proto.Int64(sentAt.UnixMilli()),
		ContextInfo:        c.quoteContextInfo(request.QuotedRemoteID, request.QuotedSenderJID, request.QuotedMessageBody),
		AccessibilityLabel: proto.String(details.AccessibilityLabel),
	}
	if details.Width > 0 {
		sticker.Width = proto.Uint32(details.Width)
	}
	if details.Height > 0 {
		sticker.Height = proto.Uint32(details.Height)
	}
	return &waE2E.Message{StickerMessage: sticker}
}
