//go:build linux

package app

import (
	"os"
	"path/filepath"
	"testing"

	"vimwhat/internal/config"
	"vimwhat/internal/store"
)

func TestDefaultOpenerFallsBackButExplicitCommandIsStrict(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	cfg := config.Default(config.Paths{})
	for _, mime := range []string{"image/png", "video/mp4", "application/pdf"} {
		item := store.MediaMetadata{LocalPath: "/tmp/file with spaces", MIMEType: mime}
		cmd, _, err := mediaOpenCommand(cfg, item)
		if err != nil || filepath.Base(cmd.Path) != "xdg-open" || len(cmd.Args) != 2 {
			t.Fatalf("fallback: %v %v", cmd, err)
		}
	}
	cfg.ImageViewerCommand = "missing-viewer {path}"
	if _, _, err := mediaOpenCommand(cfg, store.MediaMetadata{LocalPath: "/tmp/image", MIMEType: "image/png"}); err == nil {
		t.Fatal("explicit missing command silently fell back")
	}
}
