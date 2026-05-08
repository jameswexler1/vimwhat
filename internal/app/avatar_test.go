package app

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vimwhat/internal/whatsapp"
)

func TestDownloadChatAvatarUsesContentVersionedCachePath(t *testing.T) {
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/one", "/one-again":
			body = "avatar-one"
		case "/two":
			body = "avatar-two"
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Status:     "404 Not Found",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"image/png"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	cacheDir := t.TempDir()
	firstPath, err := downloadChatAvatar(context.Background(), cacheDir, "chat-1", whatsapp.ChatAvatarResult{
		URL:     "https://avatar.test/one",
		Changed: true,
	})
	if err != nil {
		t.Fatalf("downloadChatAvatar(first) error = %v", err)
	}
	repeatedPath, err := downloadChatAvatar(context.Background(), cacheDir, "chat-1", whatsapp.ChatAvatarResult{
		URL:     "https://avatar.test/one-again",
		Changed: true,
	})
	if err != nil {
		t.Fatalf("downloadChatAvatar(repeated) error = %v", err)
	}
	secondPath, err := downloadChatAvatar(context.Background(), cacheDir, "chat-1", whatsapp.ChatAvatarResult{
		URL:     "https://avatar.test/two",
		Changed: true,
	})
	if err != nil {
		t.Fatalf("downloadChatAvatar(second) error = %v", err)
	}

	if firstPath != repeatedPath {
		t.Fatalf("same avatar bytes produced paths %q and %q, want same path", firstPath, repeatedPath)
	}
	if firstPath == secondPath {
		t.Fatalf("changed avatar bytes reused path %q", firstPath)
	}
	if filepath.Ext(firstPath) != ".png" || filepath.Ext(secondPath) != ".png" {
		t.Fatalf("avatar paths = %q and %q, want .png extensions", firstPath, secondPath)
	}
	got, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatalf("ReadFile(secondPath) error = %v", err)
	}
	if string(got) != "avatar-two" {
		t.Fatalf("stored second avatar = %q, want avatar-two", string(got))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
