//go:build windows

package whatsapp

import (
	"path/filepath"
	"strings"
)

func sessionURIPath(sessionPath string) string {
	clean := filepath.ToSlash(filepath.Clean(sessionPath))
	if filepath.VolumeName(sessionPath) != "" && !strings.HasPrefix(clean, "/") {
		return "/" + clean
	}
	return clean
}
