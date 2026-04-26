//go:build !windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
)

func platformPathRoots(home string) (pathRoots, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return pathRoots{}, fmt.Errorf("resolve config dir: %w", err)
	}

	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return pathRoots{}, fmt.Errorf("resolve cache dir: %w", err)
	}

	dataRoot := os.Getenv("XDG_DATA_HOME")
	if dataRoot == "" {
		dataRoot = filepath.Join(home, ".local", "share")
	}

	return pathRoots{
		ConfigDir: filepath.Join(configRoot, appName),
		DataDir:   filepath.Join(dataRoot, appName),
		CacheDir:  filepath.Join(cacheRoot, appName),
	}, nil
}

func platformComparablePath(path string) string {
	return path
}
