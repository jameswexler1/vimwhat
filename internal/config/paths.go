package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const appName = "vimwhat"

type Paths struct {
	ConfigDir       string
	DataDir         string
	CacheDir        string
	ConfigFile      string
	DatabaseFile    string
	SessionFile     string
	LogFile         string
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

	return Paths{
		ConfigDir:       configDir,
		DataDir:         dataDir,
		CacheDir:        cacheDir,
		ConfigFile:      filepath.Join(configDir, "config.toml"),
		DatabaseFile:    filepath.Join(dataDir, "state.sqlite3"),
		SessionFile:     filepath.Join(dataDir, "whatsapp-session.sqlite3"),
		LogFile:         filepath.Join(cacheDir, "vimwhat.log"),
		MediaDir:        filepath.Join(cacheDir, "media"),
		PreviewCacheDir: filepath.Join(cacheDir, "preview"),
	}, nil
}

func (p Paths) Ensure() error {
	dirs := []string{
		p.ConfigDir,
		p.DataDir,
		p.CacheDir,
		p.MediaDir,
		p.PreviewCacheDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	return nil
}
