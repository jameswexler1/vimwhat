package app

import (
	"context"
	"strings"
	"time"

	"vimwhat/internal/config"
	"vimwhat/internal/store"
)

func repairManagedMediaMetadata(ctx context.Context, db *store.Store, paths config.Paths, item store.MediaMetadata) (store.MediaMetadata, bool, error) {
	if strings.TrimSpace(item.MessageID) == "" {
		return item, false, nil
	}

	pathsOnly := storeMediaPaths{
		LocalPath:     item.LocalPath,
		ThumbnailPath: item.ThumbnailPath,
	}
	localPathBefore := strings.TrimSpace(pathsOnly.LocalPath)
	changed := scrubManagedMediaPaths(paths, &pathsOnly)
	if !changed {
		return item, false, nil
	}

	item.LocalPath = pathsOnly.LocalPath
	item.ThumbnailPath = pathsOnly.ThumbnailPath
	if localPathBefore != "" && item.LocalPath == "" && db != nil {
		if _, ok, err := db.MediaDownloadDescriptor(ctx, item.MessageID); err != nil {
			return item, false, err
		} else if ok && strings.TrimSpace(item.DownloadState) == "downloaded" {
			item.DownloadState = "remote"
		}
	}
	item.UpdatedAt = time.Now()
	if db != nil {
		if err := db.UpsertMediaMetadata(ctx, item); err != nil {
			return item, false, err
		}
	}
	return item, true, nil
}
