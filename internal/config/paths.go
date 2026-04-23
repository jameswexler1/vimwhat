package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const appName = "vimwhat"

type Paths struct {
	ConfigDir       string
	DataDir         string
	CacheDir        string
	TransientDir    string
	ConfigFile      string
	DatabaseFile    string
	SessionFile     string
	LogFile         string
	AvatarCacheDir  string
	MediaDir        string
	PreviewCacheDir string
}

func ResolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home dir: %w", err)
	}

	configRoot, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve config dir: %w", err)
	}

	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve cache dir: %w", err)
	}

	dataRoot := os.Getenv("XDG_DATA_HOME")
	if dataRoot == "" {
		dataRoot = filepath.Join(home, ".local", "share")
	}

	configDir := filepath.Join(configRoot, appName)
	dataDir := filepath.Join(dataRoot, appName)
	cacheDir := filepath.Join(cacheRoot, appName)
	transientDir := filepath.Join(os.TempDir(), transientDirName(home))

	return Paths{
		ConfigDir:       configDir,
		DataDir:         dataDir,
		CacheDir:        cacheDir,
		TransientDir:    transientDir,
		ConfigFile:      filepath.Join(configDir, "config.toml"),
		DatabaseFile:    filepath.Join(dataDir, "state.sqlite3"),
		SessionFile:     filepath.Join(dataDir, "whatsapp-session.sqlite3"),
		LogFile:         filepath.Join(cacheDir, "vimwhat.log"),
		AvatarCacheDir:  filepath.Join(cacheDir, "avatars"),
		MediaDir:        filepath.Join(transientDir, "media"),
		PreviewCacheDir: filepath.Join(transientDir, "preview"),
	}, nil
}

func (p Paths) Ensure() error {
	dirs := []struct {
		path string
		perm os.FileMode
	}{
		{path: p.ConfigDir, perm: 0o755},
		{path: p.DataDir, perm: 0o755},
		{path: p.CacheDir, perm: 0o755},
		{path: p.AvatarCacheDir, perm: 0o700},
		{path: p.TransientDir, perm: 0o700},
		{path: p.MediaDir, perm: 0o700},
		{path: p.PreviewCacheDir, perm: 0o700},
	}

	for _, dir := range dirs {
		if strings.TrimSpace(dir.path) == "" {
			continue
		}
		if err := os.MkdirAll(dir.path, dir.perm); err != nil {
			return fmt.Errorf("create %s: %w", dir.path, err)
		}
	}

	return nil
}

func (p Paths) LegacyMediaDir() string {
	if strings.TrimSpace(p.CacheDir) == "" {
		return ""
	}
	return filepath.Join(p.CacheDir, "media")
}

func (p Paths) LegacyPreviewCacheDir() string {
	if strings.TrimSpace(p.CacheDir) == "" {
		return ""
	}
	return filepath.Join(p.CacheDir, "preview")
}

func (p Paths) IsManagedCachePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	for _, root := range []string{p.AvatarCacheDir, p.MediaDir, p.PreviewCacheDir, p.LegacyMediaDir(), p.LegacyPreviewCacheDir()} {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if pathWithinRoot(path, root) {
			return true
		}
	}
	return false
}

func transientDirName(home string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(home)))
	return appName + "-u-" + hex.EncodeToString(sum[:])[:12]
}

func pathWithinRoot(path, root string) bool {
	cleanPath := filepath.Clean(path)
	cleanRoot := filepath.Clean(root)
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
