package app

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"vimwhat/internal/config"
)

func clearLocalState(env Environment) error {
	var errs []error

	if env.Store != nil {
		if err := env.Store.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close store: %w", err))
		}
	}

	for _, path := range []string{env.Paths.DatabaseFile, env.Paths.SessionFile} {
		if err := removeSQLiteArtifacts(path); err != nil {
			errs = append(errs, err)
		}
	}

	for _, path := range uniqueNonEmptyPaths(
		env.Paths.AvatarCacheDir,
		env.Paths.MediaDir,
		env.Paths.PreviewCacheDir,
		env.Paths.LegacyMediaDir(),
		env.Paths.LegacyPreviewCacheDir(),
	) {
		if err := os.RemoveAll(path); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", path, err))
		}
	}

	if err := removeFileIfExists(env.Paths.LogFile); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func removeSQLiteArtifacts(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	var errs []error
	for _, target := range uniqueNonEmptyPaths(path, path+"-wal", path+"-shm") {
		if err := removeFileIfExists(target); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func removeFileIfExists(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func uniqueNonEmptyPaths(paths ...string) []string {
	seen := make(map[string]struct{}, len(paths))
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		unique = append(unique, path)
	}
	return unique
}

func scrubManagedMediaPaths(paths config.Paths, item *storeMediaPaths) bool {
	if item == nil {
		return false
	}
	changed := false
	if paths.IsManagedCachePath(item.LocalPath) && !mediaPathAvailable(item.LocalPath) {
		item.LocalPath = ""
		changed = true
	}
	if paths.IsManagedCachePath(item.ThumbnailPath) && !mediaPathAvailable(item.ThumbnailPath) {
		item.ThumbnailPath = ""
		changed = true
	}
	return changed
}

type storeMediaPaths struct {
	LocalPath     string
	ThumbnailPath string
}
