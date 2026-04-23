package ui

import (
	"os"
	"strings"
	"time"

	"vimwhat/internal/config"
	"vimwhat/internal/store"
)

func normalizeManagedMediaMetadata(paths config.Paths, item store.MediaMetadata) (store.MediaMetadata, bool) {
	changed := false
	if paths.IsManagedCachePath(item.LocalPath) && !localMediaPathAvailable(item.LocalPath) {
		item.LocalPath = ""
		changed = true
	}
	if paths.IsManagedCachePath(item.ThumbnailPath) && !localMediaPathAvailable(item.ThumbnailPath) {
		item.ThumbnailPath = ""
		changed = true
	}
	if changed && strings.TrimSpace(item.LocalPath) == "" && strings.TrimSpace(item.DownloadState) == "downloaded" {
		item.DownloadState = "remote"
	}
	return item, changed
}

func (m *Model) repairManagedMediaCache(message store.Message, item store.MediaMetadata) (store.Message, store.MediaMetadata, error) {
	repaired, changed := normalizeManagedMediaMetadata(m.paths, item)
	if !changed {
		return message, item, nil
	}
	if repaired.MessageID == "" {
		repaired.MessageID = message.ID
	}
	repaired.UpdatedAt = time.Now()
	if updated, _, updatedMessage := m.updateLoadedMedia(message.ID, repaired); updated {
		message = updatedMessage
	}
	if m.saveMedia != nil {
		if err := m.saveMedia(repaired); err != nil {
			return message, repaired, err
		}
	}
	return message, repaired, nil
}

func localMediaPathAvailable(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}
