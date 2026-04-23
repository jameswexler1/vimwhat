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
		TransientDir:    filepath.Join(root, "transient"),
		MediaDir:        filepath.Join(root, "transient", "media"),
		PreviewCacheDir: filepath.Join(root, "transient", "preview"),
	}

	if err := paths.Ensure(); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	for _, dir := range []string{paths.ConfigDir, paths.DataDir, paths.CacheDir, paths.TransientDir, paths.MediaDir, paths.PreviewCacheDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%q is not a directory", dir)
		}
	}
}

func TestIsManagedCachePathRecognizesCurrentAndLegacyCacheRoots(t *testing.T) {
	root := t.TempDir()
	paths := Paths{
		CacheDir:        filepath.Join(root, "cache"),
		TransientDir:    filepath.Join(root, "transient"),
		MediaDir:        filepath.Join(root, "transient", "media"),
		PreviewCacheDir: filepath.Join(root, "transient", "preview"),
	}

	tests := []struct {
		path string
		want bool
	}{
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
