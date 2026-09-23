package whatsapp

import (
	"context"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

type cachedSettingsFixture struct {
	wastore.ChatSettingsStore
	settings map[types.JID]types.LocalChatSettings
}

func (f cachedSettingsFixture) GetChatSettings(_ context.Context, jid types.JID) (types.LocalChatSettings, error) {
	return f.settings[jid], nil
}

func TestMetadataReconcilesCachedNamesAndSettingsWithoutNetwork(t *testing.T) {
	jid := types.NewJID("123", types.DefaultUserServer)
	for _, tc := range []struct {
		name  string
		until time.Time
		muted bool
	}{
		{"forever", wastore.MutedForever, true},
		{"future", time.Now().Add(time.Hour), true},
		{"expired", time.Now().Add(-time.Hour), false},
		{"unmuted", time.Time{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{client: &whatsmeow.Client{Store: &wastore.Device{
				Contacts: fakeContactStore{contacts: map[types.JID]types.ContactInfo{jid: {FullName: "Alice"}}},
				ChatSettings: cachedSettingsFixture{settings: map[types.JID]types.LocalChatSettings{jid: {
					Found: true, Pinned: true, MutedUntil: tc.until,
				}}},
			}}}
			got, err := client.cachedContacts(context.Background())
			if err != nil || len(got) != 2 {
				t.Fatalf("events = %+v, err = %v", got, err)
			}
			if got[0].Contact.DisplayName != "Alice" || !got[1].Chat.Pinned || got[1].Chat.Muted != tc.muted {
				t.Fatalf("cached metadata = %+v", got)
			}
			if tc.until == wastore.MutedForever && !got[1].Chat.MutedUntil.IsZero() {
				t.Fatal("forever mute must not become an expired timestamp")
			}
		})
	}
}
