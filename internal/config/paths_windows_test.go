//go:build windows

package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePathsUsesWindowsAppDataRoots(t *testing.T) {
	home := t.TempDir()
	roaming := filepath.Join(home, "AppData", "Roaming")
	local := filepath.Join(home, "AppData", "Local")
	t.Setenv("APPDATA", roaming)
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("USERPROFILE", home)

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}

	if !strings.HasPrefix(paths.ConfigFile, filepath.Join(roaming, "vimwhat")) {
		t.Fatalf("ConfigFile = %q, want under roaming AppData", paths.ConfigFile)
	}
	if paths.DataDir != filepath.Join(local, "vimwhat", "data") {
		t.Fatalf("DataDir = %q", paths.DataDir)
	}
	if paths.CacheDir != filepath.Join(local, "vimwhat", "cache") {
		t.Fatalf("CacheDir = %q", paths.CacheDir)
	}
}

func TestIsManagedCachePathIsCaseInsensitiveOnWindows(t *testing.T) {
	root := filepath.Join(`C:\Users\Alice\AppData\Local`, "vimwhat")
	paths := Paths{
		CacheDir:        filepath.Join(root, "cache"),
		AvatarCacheDir:  filepath.Join(root, "cache", "avatars"),
		TransientDir:    filepath.Join(`C:\Users\Alice\AppData\Local\Temp`, "vimwhat-u-test"),
		MediaDir:        filepath.Join(`C:\Users\Alice\AppData\Local\Temp`, "vimwhat-u-test", "media"),
		PreviewCacheDir: filepath.Join(`C:\Users\Alice\AppData\Local\Temp`, "vimwhat-u-test", "preview"),
	}

	path := strings.ToUpper(filepath.Join(paths.MediaDir, "photo.jpg"))
	if !paths.IsManagedCachePath(path) {
		t.Fatalf("IsManagedCachePath(%q) = false, want true", path)
	}
}
