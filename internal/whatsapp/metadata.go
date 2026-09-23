package whatsapp

import (
	"context"
	"errors"
	"strings"
	"time"

	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"

	"vimwhat/internal/store"
)

func (c *Client) RefreshChatMetadata(ctx context.Context) ([]Event, error) {
	if c == nil || c.client == nil {
		return nil, ErrClientNotOpen
	}

	// Read cached contacts before a potentially slow remote group request.
	out, contactErr := c.cachedContacts(ctx)
	groups, groupErr := c.client.GetJoinedGroups(ctx)
	for _, group := range groups {
		if group == nil || group.JID.IsEmpty() {
			continue
		}
		out = append(out, c.normalizeFullGroupInfoEvent(ctx, group, true)...)
		settings, err := c.cachedChatSettings(ctx, group.JID)
		out = append(out, settings...)
		groupErr = errors.Join(groupErr, err)
	}

	return out, errors.Join(contactErr, groupErr)
}

func (c *Client) cachedContacts(ctx context.Context) ([]Event, error) {
	var out []Event
	if c.client.Store != nil && c.client.Store.Contacts != nil {
		contacts, err := c.client.Store.Contacts.GetAllContacts(ctx)
		if err == nil {
			for jid, info := range contacts {
				if event, ok := c.cachedContactEvent(ctx, jid, info); ok {
					out = append(out, event)
				}
				settings, err := c.cachedChatSettings(ctx, jid)
				out = append(out, settings...)
				if err != nil {
					return out, err
				}
			}
		} else {
			return out, err
		}
	}

	return out, nil
}

// Incremental protocol sync may have no new pin/mute events (for example
// after pairing or an interrupted app import). Reconcile its saved settings
// along with names so cached conversations do not silently lose their mute.
func (c *Client) cachedChatSettings(ctx context.Context, jid types.JID) ([]Event, error) {
	if c.client.Store == nil || c.client.Store.ChatSettings == nil || !supportedChat(jid) {
		return nil, nil
	}
	canonical, aliases := c.canonicalChatIdentity(ctx, jid, types.EmptyJID)
	settings, err := c.client.Store.ChatSettings.GetChatSettings(ctx, canonical)
	if err != nil {
		return nil, err
	}
	if !settings.Found && canonical != jid {
		settings, err = c.client.Store.ChatSettings.GetChatSettings(ctx, jid)
	}
	if err != nil || !settings.Found {
		return nil, err
	}
	muted := settings.MutedUntil == wastore.MutedForever || settings.MutedUntil.After(time.Now())
	until := settings.MutedUntil
	if !muted || until == wastore.MutedForever {
		until = time.Time{}
	}
	return []Event{{Kind: EventChatUpsert, Chat: ChatEvent{
		ID: canonical.String(), JID: canonical.String(), AliasIDs: aliases,
		Kind: chatKind(canonical), Title: historyConversationTitle(nil, canonical),
		TitleSource: historyConversationTitleSource(nil, canonical),
		Pinned:      settings.Pinned, PinnedKnown: true, Muted: muted, MutedKnown: true,
		MutedUntil: until, Historical: true,
	}}}, nil
}

func (c *Client) cachedContactEvent(ctx context.Context, jid types.JID, info types.ContactInfo) (Event, bool) {
	if jid.IsEmpty() {
		return Event{}, false
	}
	displayName := firstNonEmpty(info.FullName, info.FirstName, info.BusinessName)
	notifyName := strings.TrimSpace(info.PushName)
	if displayName == "" && notifyName == "" && strings.TrimSpace(info.RedactedPhone) == "" {
		return Event{}, false
	}
	phone := strings.TrimSpace(info.RedactedPhone)
	if phone == "" && jid.Server == types.DefaultUserServer {
		phone = jid.User
	}
	canonicalChatJID, aliases := c.canonicalChatIdentity(ctx, jid, contactPhoneJID(phone))
	if canonicalChatJID.IsEmpty() {
		canonicalChatJID = canonicalizableChatJID(jid)
	}
	source := store.ChatTitleSourceContactDisplay
	if displayName == "" {
		source = store.ChatTitleSourcePushName
	}
	return Event{
		Kind: EventContactUpsert,
		Contact: ContactEvent{
			AliasIDs:    aliases,
			JID:         jid.String(),
			ChatID:      canonicalChatJID.String(),
			DisplayName: displayName,
			NotifyName:  notifyName,
			Phone:       phone,
			UpdatedAt:   time.Now(),
			TitleSource: source,
		},
	}, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
