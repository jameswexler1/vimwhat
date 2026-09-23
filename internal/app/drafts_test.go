package app

import (
	"os"
	"path/filepath"
	"testing"
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
