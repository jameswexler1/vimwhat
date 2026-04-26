//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func platformPathRoots(home string) (pathRoots, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return pathRoots{}, fmt.Errorf("resolve config dir: %w", err)
	}

	localRoot, err := localAppDataRoot(home)
	if err != nil {
		return pathRoots{}, err
	}

	return pathRoots{
		ConfigDir: filepath.Join(configRoot, appName),
		DataDir:   filepath.Join(localRoot, appName, "data"),
		CacheDir:  filepath.Join(localRoot, appName, "cache"),
	}, nil
}

func localAppDataRoot(home string) (string, error) {
	if value := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); value != "" {
		return value, nil
	}
	if cacheRoot, err := os.UserCacheDir(); err == nil && strings.TrimSpace(cacheRoot) != "" {
		return cacheRoot, nil
	}
	if strings.TrimSpace(home) != "" {
		return filepath.Join(home, "AppData", "Local"), nil
	}
	return "", fmt.Errorf("resolve local app data dir")
}

func platformComparablePath(path string) string {
	return strings.ToLower(path)
}
