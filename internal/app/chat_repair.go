package app

import (
	"context"
	"strings"

	"vimwhat/internal/store"
	"vimwhat/internal/whatsapp"
)

type chatCanonicalizer interface {
	CanonicalChatJID(context.Context, string) (string, error)
}

func canonicalizeLiveChatID(ctx context.Context, live WhatsAppLiveSession, chatID string) (string, error) {
	normalized, err := whatsapp.NormalizeSendChatJID(chatID)
	if err != nil {
		return "", err
	}
	if live == nil {
		return normalized, nil
	}
	canonical, err := live.CanonicalChatJID(ctx, normalized)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(canonical) == "" {
		return normalized, nil
	}
	return canonical, nil
}

func canonicalizeHistoryChatID(ctx context.Context, live WhatsAppLiveSession, chatID string) (string, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return "", nil
	}
	canonical, err := canonicalizeLiveChatID(ctx, live, chatID)
	if err == nil {
		return canonical, nil
	}
	if _, normalizeErr := whatsapp.NormalizeSendChatJID(chatID); normalizeErr == nil {
		return "", err
	}
	return chatID, nil
}

func repairCanonicalDirectChats(ctx context.Context, db *store.Store, canonicalizer chatCanonicalizer) error {
	if db == nil || canonicalizer == nil {
		return nil
	}
	chats, err := db.ListChats(ctx)
	if err != nil {
		return err
	}
	for _, chat := range chats {
		if chat.Kind != "direct" {
			continue
		}
		canonicalID, err := canonicalizer.CanonicalChatJID(ctx, chat.ID)
		if err != nil {
			continue
		}
		canonicalID = strings.TrimSpace(canonicalID)
		if canonicalID == "" || canonicalID == chat.ID {
			continue
		}
		if err := db.MergeChatAlias(ctx, canonicalID, chat.ID); err != nil {
			return err
		}
	}
	return nil
}

func runPreStartCanonicalRepair(ctx context.Context, env Environment) error {
	status, err := checkWhatsAppSession(ctx, env)
	if err != nil || !status.Paired {
		return nil
	}
	session, err := openWhatsAppSession(ctx, env)
	if err != nil {
		return err
	}
	defer session.Close()
	canonicalizer, ok := session.(chatCanonicalizer)
	if !ok {
		return nil
	}
	return repairCanonicalDirectChats(ctx, env.Store, canonicalizer)
}

func mergeEventChatAliases(ctx context.Context, db *store.Store, event whatsapp.ChatEvent) ([]string, error) {
	if db == nil || strings.TrimSpace(event.ID) == "" {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(event.AliasIDs))
	var merged []string
	for _, aliasID := range event.AliasIDs {
		aliasID = strings.TrimSpace(aliasID)
		if aliasID == "" || aliasID == event.ID {
			continue
		}
		if _, ok := seen[aliasID]; ok {
			continue
		}
		seen[aliasID] = struct{}{}
		if err := db.MergeChatAlias(ctx, event.ID, aliasID); err != nil {
			return merged, err
		}
		merged = append(merged, aliasID)
	}
	return merged, nil
}
