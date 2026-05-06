//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePathsUsesTempForTransientMediaCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}

	if !strings.HasPrefix(paths.DatabaseFile, paths.DataDir) {
		t.Fatalf("DatabaseFile = %q, want under %q", paths.DatabaseFile, paths.DataDir)
	}
	if !strings.HasPrefix(paths.SessionFile, paths.DataDir) {
		t.Fatalf("SessionFile = %q, want under %q", paths.SessionFile, paths.DataDir)
	}
	if !strings.HasPrefix(paths.LogFile, paths.CacheDir) {
		t.Fatalf("LogFile = %q, want under %q", paths.LogFile, paths.CacheDir)
	}
	if !strings.HasPrefix(paths.AvatarCacheDir, paths.CacheDir) {
		t.Fatalf("AvatarCacheDir = %q, want under %q", paths.AvatarCacheDir, paths.CacheDir)
	}
	if !strings.HasPrefix(paths.TransientDir, os.TempDir()) {
		t.Fatalf("TransientDir = %q, want under temp dir %q", paths.TransientDir, os.TempDir())
	}
	if !strings.HasPrefix(paths.MediaDir, paths.TransientDir) {
		t.Fatalf("MediaDir = %q, want under %q", paths.MediaDir, paths.TransientDir)
	}
	if !strings.HasPrefix(paths.PreviewCacheDir, paths.TransientDir) {
		t.Fatalf("PreviewCacheDir = %q, want under %q", paths.PreviewCacheDir, paths.TransientDir)
	}
	if paths.MediaDir == filepath.Join(paths.CacheDir, "media") {
		t.Fatalf("MediaDir = %q, want transient cache path instead of XDG cache media dir", paths.MediaDir)
	}
}

func TestPathsEnsureCreatesTransientDirectories(t *testing.T) {
	root := t.TempDir()
	paths := Paths{
		ConfigDir:       filepath.Join(root, "config"),
		DataDir:         filepath.Join(root, "data"),
		CacheDir:        filepath.Join(root, "cache"),
		AvatarCacheDir:  filepath.Join(root, "cache", "avatars"),
		TransientDir:    filepath.Join(root, "transient"),
		MediaDir:        filepath.Join(root, "transient", "media"),
		PreviewCacheDir: filepath.Join(root, "transient", "preview"),
	}

	if err := paths.Ensure(); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	for _, dir := range []struct {
		path string
		perm os.FileMode
	}{
		{path: paths.ConfigDir, perm: 0o700},
		{path: paths.DataDir, perm: 0o700},
		{path: paths.CacheDir, perm: 0o755},
		{path: paths.AvatarCacheDir, perm: 0o700},
		{path: paths.TransientDir, perm: 0o700},
		{path: paths.MediaDir, perm: 0o700},
		{path: paths.PreviewCacheDir, perm: 0o700},
	} {
		info, err := os.Stat(dir.path)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", dir.path, err)
		}
		if !info.IsDir() {
			t.Fatalf("%q is not a directory", dir.path)
		}
		if got := info.Mode().Perm(); got != dir.perm {
			t.Fatalf("%q mode = %04o, want %04o", dir.path, got, dir.perm)
		}
	}
}

func TestIsManagedCachePathRecognizesCurrentAndLegacyCacheRoots(t *testing.T) {
	root := t.TempDir()
	paths := Paths{
		CacheDir:        filepath.Join(root, "cache"),
		AvatarCacheDir:  filepath.Join(root, "cache", "avatars"),
		TransientDir:    filepath.Join(root, "transient"),
		MediaDir:        filepath.Join(root, "transient", "media"),
		PreviewCacheDir: filepath.Join(root, "transient", "preview"),
	}

	tests := []struct {
		path string
		want bool
	}{
		{path: filepath.Join(paths.AvatarCacheDir, "avatar.png"), want: true},
		{path: filepath.Join(paths.MediaDir, "wa-photo.jpg"), want: true},
		{path: filepath.Join(paths.PreviewCacheDir, "thumbs", "abc.jpg"), want: true},
		{path: filepath.Join(paths.LegacyMediaDir(), "wa-photo.jpg"), want: true},
		{path: filepath.Join(paths.LegacyPreviewCacheDir(), "thumbs", "abc.jpg"), want: true},
		{path: filepath.Join(root, "Downloads", "saved.jpg"), want: false},
	}

	for _, test := range tests {
		if got := paths.IsManagedCachePath(test.path); got != test.want {
			t.Fatalf("IsManagedCachePath(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}
