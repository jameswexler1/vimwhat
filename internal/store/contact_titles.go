package store

import (
	"context"
	"strings"
)

// Called with titleMu held. Contacts do not create conversations.
func (s *Store) resolveContactTitle(ctx context.Context, chat Chat, aliases []string) (Chat, error) {
	if chat.Kind != "direct" {
		return chat, nil
	}
	seen := map[string]bool{}
	for _, jid := range append([]string{chat.ID, chat.JID}, aliases...) {
		if jid == "" || seen[jid] {
			continue
		}
		seen[jid] = true
		contact, err := s.Contact(ctx, jid)
		if err != nil {
			return chat, err
		}
		candidate := chat
		candidate.Title = strings.TrimSpace(contact.DisplayName)
		candidate.TitleSource = ChatTitleSourceContactDisplay
		if candidate.Title == "" {
			candidate.Title = strings.TrimSpace(contact.NotifyName)
			candidate.TitleSource = ChatTitleSourcePushName
		}
		if shouldReplaceChatTitle(chat, candidate) {
			chat.Title, chat.TitleSource = candidate.Title, candidate.TitleSource
		}
	}
	return chat, nil
}
