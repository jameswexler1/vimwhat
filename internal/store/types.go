package store

import "time"

type Chat struct {
	ID            string
	Title         string
	Unread        int
	Pinned        bool
	Muted         bool
	HasDraft      bool
	LastMessageAt time.Time
}

type Message struct {
	ID         string
	ChatID     string
	Sender     string
	Body       string
	Timestamp  time.Time
	IsOutgoing bool
}

type Snapshot struct {
	Chats          []Chat
	MessagesByChat map[string][]Message
	DraftsByChat   map[string]string
	ActiveChatID   string
}

type Stats struct {
	Chats    int
	Messages int
	Drafts   int
}
