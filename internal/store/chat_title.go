package store

import (
	"strings"
	"unicode"
)

const (
	ChatTitleSourceJID            = "jid"
	ChatTitleSourcePlaceholder    = "placeholder"
	ChatTitleSourcePushName       = "push_name"
	ChatTitleSourceHistoryName    = "history_name"
	ChatTitleSourceContactDisplay = "contact_display"
	ChatTitleSourceGroupSubject   = "group_subject"
)

func NormalizeChatTitleSource(source string) string {
	switch strings.TrimSpace(source) {
	case ChatTitleSourceJID:
		return ChatTitleSourceJID
	case ChatTitleSourcePlaceholder:
		return ChatTitleSourcePlaceholder
	case ChatTitleSourcePushName:
		return ChatTitleSourcePushName
	case ChatTitleSourceHistoryName:
		return ChatTitleSourceHistoryName
	case ChatTitleSourceContactDisplay:
		return ChatTitleSourceContactDisplay
	case ChatTitleSourceGroupSubject:
		return ChatTitleSourceGroupSubject
	default:
		return ""
	}
}

func (c Chat) DisplayTitle() string {
	title := strings.TrimSpace(c.Title)
	if title == "" {
		return "unknown"
	}
	if c.Kind == "group" && WeakChatTitle(title, c.Kind, c.JID, c.ID) {
		return "Unnamed group"
	}
	return title
}

func WeakChatTitle(title, kind, jid, id string) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return true
	}
	if strings.EqualFold(title, strings.TrimSpace(id)) ||
		strings.EqualFold(title, strings.TrimSpace(jid)) {
		return true
	}
	for _, candidate := range []string{id, jid} {
		if user := jidUserPart(candidate); user != "" && strings.EqualFold(title, user) {
			return true
		}
	}
	if kind == "group" {
		if strings.EqualFold(title, "Unnamed group") {
			return true
		}
		return phoneLikeTitle(title)
	}
	return false
}

func shouldReplaceChatTitle(existing, incoming Chat) bool {
	if strings.TrimSpace(incoming.Title) == "" {
		return false
	}
	source := NormalizeChatTitleSource(incoming.TitleSource)
	if source == "" {
		return true
	}
	if strings.TrimSpace(existing.Title) == "" {
		return true
	}
	if WeakChatTitle(existing.Title, existing.Kind, existing.JID, existing.ID) {
		return true
	}
	if weakChatTitleSource(source) {
		return false
	}
	existingSource := NormalizeChatTitleSource(existing.TitleSource)
	if existingSource == "" {
		return true
	}
	return chatTitleSourceRank(source) >= chatTitleSourceRank(existingSource)
}

func weakChatTitleSource(source string) bool {
	switch NormalizeChatTitleSource(source) {
	case ChatTitleSourceJID, ChatTitleSourcePlaceholder:
		return true
	default:
		return false
	}
}

func chatTitleSourceRank(source string) int {
	switch NormalizeChatTitleSource(source) {
	case ChatTitleSourceJID:
		return 0
	case ChatTitleSourcePlaceholder:
		return 1
	case ChatTitleSourcePushName:
		return 2
	case ChatTitleSourceHistoryName:
		return 3
	case ChatTitleSourceContactDisplay:
		return 4
	case ChatTitleSourceGroupSubject:
		return 5
	default:
		return -1
	}
}

func jidUserPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	user, _, ok := strings.Cut(value, "@")
	if !ok {
		return ""
	}
	return strings.TrimSpace(user)
}

func phoneLikeTitle(title string) bool {
	hasDigit := false
	for _, r := range title {
		switch {
		case unicode.IsLetter(r):
			return false
		case unicode.IsDigit(r):
			hasDigit = true
		case strings.ContainsRune(" +-().", r):
		default:
			return false
		}
	}
	return hasDigit
}
