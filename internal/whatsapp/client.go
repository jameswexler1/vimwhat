package whatsapp

import (
	"context"
	"errors"
	"time"
)

var ErrNotImplemented = errors.New("whatsapp integration not implemented yet")

type QRHandler func(code string)

type Adapter interface {
	Connect(ctx context.Context) error
	Login(ctx context.Context, handleQR QRHandler) error
	Logout(ctx context.Context) error
	SendText(ctx context.Context, chatJID, body string) (SendResult, error)
	SubscribeEvents(ctx context.Context) (<-chan Event, error)
	RequestRecentHistory(ctx context.Context, chatJID string, limit int) error
}

type Client struct{}

func (Client) Connect(context.Context) error {
	return ErrNotImplemented
}

func (Client) Login(context.Context, QRHandler) error {
	return ErrNotImplemented
}

func (Client) Logout(context.Context) error {
	return ErrNotImplemented
}

func (Client) SendText(context.Context, string, string) (SendResult, error) {
	return SendResult{}, ErrNotImplemented
}

func (Client) SubscribeEvents(context.Context) (<-chan Event, error) {
	return nil, ErrNotImplemented
}

func (Client) RequestRecentHistory(context.Context, string, int) error {
	return ErrNotImplemented
}

type SendResult struct {
	MessageID string
	RemoteID  string
	Status    string
}

type EventKind string

const (
	EventChatUpsert    EventKind = "chat_upsert"
	EventMessageUpsert EventKind = "message_upsert"
	EventReceiptUpdate EventKind = "receipt_update"
	EventMediaMetadata EventKind = "media_metadata"
)

type Event struct {
	Kind    EventKind
	Chat    ChatEvent
	Message MessageEvent
	Receipt ReceiptEvent
	Media   MediaEvent
}

type ChatEvent struct {
	ID            string
	JID           string
	Title         string
	Kind          string
	Unread        int
	Pinned        bool
	Muted         bool
	LastMessageAt time.Time
}

type MessageEvent struct {
	ID              string
	RemoteID        string
	ChatID          string
	ChatJID         string
	Sender          string
	SenderJID       string
	Body            string
	Timestamp       time.Time
	IsOutgoing      bool
	Status          string
	QuotedMessageID string
	QuotedRemoteID  string
}

type ReceiptEvent struct {
	MessageID string
	ChatID    string
	Status    string
}

type MediaEvent struct {
	MessageID     string
	MIMEType      string
	FileName      string
	SizeBytes     int64
	LocalPath     string
	ThumbnailPath string
	DownloadState string
	UpdatedAt     time.Time
}
