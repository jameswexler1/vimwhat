package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vimwhat/internal/securefs"
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

type pathRoots struct {
	ConfigDir string
	DataDir   string
	CacheDir  string
}

func ResolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home dir: %w", err)
	}

	roots, err := platformPathRoots(home)
	if err != nil {
		return Paths{}, err
	}

	configDir := roots.ConfigDir
	dataDir := roots.DataDir
	cacheDir := roots.CacheDir
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
	dirs := []string{
		p.ConfigDir,
		p.DataDir,
		p.CacheDir,
		p.AvatarCacheDir,
		p.TransientDir,
		p.MediaDir,
		p.PreviewCacheDir,
	}

	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if err := securefs.EnsurePrivateDir(dir); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
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
	cleanPath := platformComparablePath(filepath.Clean(path))
	cleanRoot := platformComparablePath(filepath.Clean(root))
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
