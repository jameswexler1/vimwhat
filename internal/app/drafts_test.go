package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"vimwhat/internal/config"
	"vimwhat/internal/store"
)

func TestRetainedDraftAttachmentSurvivesTransientRemoval(t *testing.T) {
	source := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(source, []byte("image data"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err := retainDraftAttachment(t.TempDir(), source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "image data" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
}

func TestDraftSaverReusesRetainedAttachmentAndLogoutRemovesIt(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{DataDir: filepath.Join(root, "data"), MediaDir: filepath.Join(root, "cache")}
	if err := os.MkdirAll(paths.MediaDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.DataDir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(paths.DataDir, "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.UpsertChat(ctx, store.Chat{ID: "chat", Title: "Alice"}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(paths.MediaDir, "image.png")
	if err := os.WriteFile(source, []byte("image data"), 0600); err != nil {
		t.Fatal(err)
	}
	draft := store.ComposerDraft{Media: []store.MediaMetadata{{LocalPath: source}}}
	save := newComposerDraftSaver(Environment{Paths: paths, Store: db})
	if err := save("chat", draft); err != nil {
		t.Fatal(err)
	}
	drafts, err := db.ListComposerDrafts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	retained := drafts["chat"].Media[0].LocalPath
	before, err := os.Stat(retained)
	if err != nil {
		t.Fatal(err)
	}
	draft.Body = "caption"
	if err := save("chat", draft); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(retained)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("unchanged attachment was recopied on typing")
	}
	if draft.Media[0].LocalPath != source {
		t.Fatal("save mutated caller media")
	}
	if err := clearLocalState(Environment{Paths: paths}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(retained); !os.IsNotExist(err) {
		t.Fatalf("logout retained private attachment: %v", err)
	}
}
