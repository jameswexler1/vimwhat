//go:build windows

package ui

import (
	"strings"
	"testing"

	"vimwhat/internal/media"
	"vimwhat/internal/store"
)

func TestSixelMediaPreviewDoesNotRenderPayloadInView(t *testing.T) {
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendSixel,
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{
					ID:     "m-1",
					ChatID: "chat-1",
					Sender: "Alice",
					Body:   "photo",
					Media: []store.MediaMetadata{{
						MessageID:     "m-1",
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						LocalPath:     "/tmp/photo.jpg",
						DownloadState: "downloaded",
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20
	message := model.messagesByChat["chat-1"][0]
	request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
	if !ok {
		t.Fatal("previewRequestForMedia() returned false")
	}
	payload := "\x1bPqfake-sixel\x1b\\"
	model.previewCache[media.PreviewKey(request)] = media.Preview{
		Key:             media.PreviewKey(request),
		MessageID:       "m-1",
		Kind:            media.KindImage,
		Backend:         media.BackendSixel,
		RenderedBackend: media.BackendSixel,
		Display:         media.PreviewDisplaySixel,
		Width:           request.Width,
		Height:          request.Height,
		Lines:           []string{payload},
	}

	view := model.renderMessages(80, 12)
	if strings.Contains(view, payload) || strings.Contains(view, "\x1bP") {
		t.Fatalf("renderMessages leaked sixel payload into View()\n%q", view)
	}
	placements := model.visibleSixelMediaPlacements()
	if len(placements) != 1 || len(placements[0].Payload) != 1 || placements[0].Payload[0] != payload {
		t.Fatalf("visibleSixelMediaPlacements() = %+v, want one payload placement", placements)
	}
}
