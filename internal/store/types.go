package store

import "time"

type Chat struct {
	ID            string
	JID           string
	Title         string
	Kind          string
	Unread        int
	Pinned        bool
	Muted         bool
	HasDraft      bool
	LastPreview   string
	LastMessageAt time.Time
}

type Message struct {
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
	DeletedAt       time.Time
	DeletedReason   string
	Media           []MediaMetadata
}

type Contact struct {
	JID             string
	DisplayName     string
	NotifyName      string
	Phone           string
	AvatarPath      string
	AvatarThumbPath string
	AvatarUpdatedAt time.Time
	UpdatedAt       time.Time
}

type MediaMetadata struct {
	MessageID     string
	MIMEType      string
	FileName      string
	SizeBytes     int64
	LocalPath     string
	ThumbnailPath string
	DownloadState string
	UpdatedAt     time.Time
}

type MediaDownloadDescriptor struct {
	MessageID     string
	Kind          string
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
	FileLength    int64
	UpdatedAt     time.Time
}

type UISnapshot struct {
	Kind      string
	Name      string
	ChatID    string
	Value     string
	UpdatedAt time.Time
}

type Snapshot struct {
	Chats          []Chat
	MessagesByChat map[string][]Message
	DraftsByChat   map[string]string
	ActiveChatID   string
}

type Stats struct {
	Chats      int
	Messages   int
	Drafts     int
	Contacts   int
	MediaItems int
	Migrations int
}
