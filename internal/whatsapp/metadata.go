package whatsapp

import (
	"context"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"

	"vimwhat/internal/store"
)

func (c *Client) RefreshChatMetadata(ctx context.Context) ([]Event, error) {
	if c == nil || c.client == nil {
		return nil, ErrClientNotOpen
	}

	var out []Event
	groups, groupErr := c.client.GetJoinedGroups(ctx)
	for _, group := range groups {
		if group == nil || group.JID.IsEmpty() {
			continue
		}
		out = append(out, normalizeFullGroupInfoEvent(group, true)...)
	}

	if c.client.Store != nil && c.client.Store.Contacts != nil {
		contacts, err := c.client.Store.Contacts.GetAllContacts(ctx)
		if err == nil {
			for jid, info := range contacts {
				if event, ok := c.cachedContactEvent(ctx, jid, info); ok {
					out = append(out, event)
				}
			}
		} else if groupErr == nil {
			groupErr = err
		}
	}

	return out, groupErr
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
	canonicalChatJID, _ := c.canonicalChatIdentity(ctx, jid, contactPhoneJID(phone))
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
