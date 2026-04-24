package whatsapp

import (
	"context"
	"strings"

	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

func (c *Client) CanonicalChatJID(ctx context.Context, chatJID string) (string, error) {
	normalized, err := NormalizeSendChatJID(chatJID)
	if err != nil {
		return "", err
	}
	jid, err := types.ParseJID(normalized)
	if err != nil {
		return "", err
	}
	canonical, _ := c.canonicalChatIdentity(ctx, jid, types.EmptyJID)
	return canonical.String(), nil
}

func (c *Client) canonicalChatIdentity(ctx context.Context, primary, alternate types.JID) (types.JID, []string) {
	primary = canonicalizableChatJID(primary)
	alternate = canonicalizableChatJID(alternate)
	if primary.IsEmpty() {
		primary = alternate
		alternate = types.EmptyJID
	}
	if primary.IsEmpty() {
		return types.EmptyJID, nil
	}
	if !supportedChat(primary) || primary.Server == types.GroupServer {
		return primary, nil
	}

	canonical := primary
	switch {
	case primary.Server == types.HiddenUserServer:
		canonical = primary
	case alternate.Server == types.HiddenUserServer:
		canonical = alternate
	case primary.Server == types.DefaultUserServer:
		if lid := c.lookupLIDForPN(ctx, primary); !lid.IsEmpty() {
			canonical = lid
		}
	case alternate.Server == types.DefaultUserServer:
		if lid := c.lookupLIDForPN(ctx, alternate); !lid.IsEmpty() {
			canonical = lid
		}
	}

	var aliases []string
	aliases = appendCanonicalAlias(aliases, primary, canonical)
	aliases = appendCanonicalAlias(aliases, alternate, canonical)
	if canonical.Server == types.HiddenUserServer {
		aliases = appendCanonicalAlias(aliases, c.lookupPNForLID(ctx, canonical), canonical)
	}
	return canonical, aliases
}

func canonicalizableChatJID(jid types.JID) types.JID {
	if jid.IsEmpty() {
		return types.EmptyJID
	}
	return jid.ToNonAD()
}

func (c *Client) lookupLIDForPN(ctx context.Context, pn types.JID) types.JID {
	pn = canonicalizableChatJID(pn)
	if pn.IsEmpty() || pn.Server != types.DefaultUserServer {
		return types.EmptyJID
	}
	lidStore := c.lidStore()
	if lidStore == nil {
		return types.EmptyJID
	}
	lid, err := lidStore.GetLIDForPN(ctx, pn)
	if err != nil {
		return types.EmptyJID
	}
	return canonicalizableChatJID(lid)
}

func (c *Client) lookupPNForLID(ctx context.Context, lid types.JID) types.JID {
	lid = canonicalizableChatJID(lid)
	if lid.IsEmpty() || lid.Server != types.HiddenUserServer {
		return types.EmptyJID
	}
	lidStore := c.lidStore()
	if lidStore == nil {
		return types.EmptyJID
	}
	pn, err := lidStore.GetPNForLID(ctx, lid)
	if err != nil {
		return types.EmptyJID
	}
	return canonicalizableChatJID(pn)
}

func (c *Client) lidStore() wastore.LIDStore {
	if c == nil {
		return nil
	}
	if c.client != nil && c.client.Store != nil && c.client.Store.LIDs != nil {
		return c.client.Store.LIDs
	}
	if c.container != nil && c.container.LIDMap != nil {
		return c.container.LIDMap
	}
	return nil
}

func appendCanonicalAlias(existing []string, candidate, canonical types.JID) []string {
	candidate = canonicalizableChatJID(candidate)
	if candidate.IsEmpty() {
		return existing
	}
	value := candidate.String()
	if value == "" || value == canonical.String() {
		return existing
	}
	for _, current := range existing {
		if current == value {
			return existing
		}
	}
	return append(existing, value)
}

func directChatAlternateJID(info types.MessageInfo) types.JID {
	if info.Chat.Server == types.GroupServer {
		return types.EmptyJID
	}
	if info.IsFromMe {
		if !info.RecipientAlt.IsEmpty() {
			return info.RecipientAlt
		}
		if !info.SenderAlt.IsEmpty() {
			return info.SenderAlt
		}
		return types.EmptyJID
	}
	if !info.SenderAlt.IsEmpty() {
		return info.SenderAlt
	}
	return info.RecipientAlt
}

func canonicalDirectSenderJID(chatID string, info types.MessageInfo) string {
	if info.IsFromMe {
		return "me"
	}
	if info.Chat.Server != types.GroupServer && strings.TrimSpace(chatID) != "" {
		return chatID
	}
	if !info.Sender.IsEmpty() {
		return info.Sender.ToNonAD().String()
	}
	return ""
}
