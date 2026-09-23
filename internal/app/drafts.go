package app

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"vimwhat/internal/securefs"
	"vimwhat/internal/store"
)

func persistComposerDraft(env Environment, chatID string, draft store.ComposerDraft) error {
	// Clipboard attachments in the transient cache must survive a reboot too.
	for i, item := range draft.Media {
		if item.LocalPath == "" || !env.Paths.IsManagedCachePath(item.LocalPath) {
			continue
		}
		path, err := retainDraftAttachment(env.Paths.DataDir, item.LocalPath)
		if err != nil {
			return err
		}
		draft.Media[i].LocalPath = path
	}
	ctx, cancel := uiStoreWriteContext()
	defer cancel()
	return env.Store.SaveComposerDraft(ctx, chatID, draft)
}

func retainDraftAttachment(dataDir, source string) (string, error) {
	dir := filepath.Join(dataDir, "attachments")
	if err := securefs.EnsurePrivateDir(dir); err != nil {
		return "", err
	}
	src, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("retain draft attachment: %w", err)
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("draft attachment must be a regular file")
	}
	tmp, err := os.CreateTemp(dir, "draft-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, hash), src)
	closeErr := tmp.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	target := filepath.Join(dir, fmt.Sprintf("%x%s", hash.Sum(nil), filepath.Ext(source)))
	if err := os.Rename(tmp.Name(), target); err != nil {
		return "", err
	}
	return target, nil
}
