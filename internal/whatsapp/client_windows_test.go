//go:build windows

package whatsapp

import (
	"net/url"
	"testing"
)

func TestSessionURIUsesWindowsFileURIPath(t *testing.T) {
	raw := SessionURI(`C:\Users\Alice\AppData\Local\vimwhat\data\whatsapp session.sqlite3`)
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if parsed.Scheme != "file" {
		t.Fatalf("scheme = %q, want file", parsed.Scheme)
	}
	if parsed.Path != `/C:/Users/Alice/AppData/Local/vimwhat/data/whatsapp session.sqlite3` {
		t.Fatalf("path = %q", parsed.Path)
	}
}
