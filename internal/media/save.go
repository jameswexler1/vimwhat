package media

import (
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

type SaveRequest struct {
	SourcePath   string
	FileName     string
	MIMEType     string
	DownloadsDir string
}

func SaveLocalFile(req SaveRequest) (string, error) {
	if strings.TrimSpace(req.SourcePath) == "" {
		return "", fmt.Errorf("media is not downloaded")
	}
	info, err := os.Stat(req.SourcePath)
	if err != nil {
		return "", fmt.Errorf("stat media: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("media path is a directory")
	}
	if strings.TrimSpace(req.DownloadsDir) == "" {
		return "", fmt.Errorf("downloads dir is empty")
	}
	if err := os.MkdirAll(req.DownloadsDir, 0o755); err != nil {
		return "", fmt.Errorf("create downloads dir: %w", err)
	}

	name := saveFileName(req)
	for index := 0; ; index++ {
		target := filepath.Join(req.DownloadsDir, collisionName(name, index))
		if err := copyExclusive(req.SourcePath, target, info.Mode().Perm()); err == nil {
			return target, nil
		} else if !os.IsExist(err) {
			return "", err
		}
	}
}

func saveFileName(req SaveRequest) string {
	name := filepath.Base(strings.TrimSpace(req.FileName))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = filepath.Base(req.SourcePath)
	}
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "media"
	}
	if filepath.Ext(name) == "" {
		if exts, err := mime.ExtensionsByType(strings.ToLower(strings.TrimSpace(req.MIMEType))); err == nil && len(exts) > 0 {
			name += exts[0]
		}
	}
	return name
}

func collisionName(name string, index int) string {
	if index == 0 {
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem = "media"
	}
	return fmt.Sprintf("%s-%d%s", stem, index, ext)
}

func copyExclusive(source, target string, perm os.FileMode) error {
	src, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open media: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		if os.IsExist(err) {
			return err
		}
		return fmt.Errorf("create saved media: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy media: %w", err)
	}
	return nil
}
