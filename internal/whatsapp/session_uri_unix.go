//go:build linux

package whatsapp

import "path/filepath"

func sessionURIPath(sessionPath string) string {
	return filepath.ToSlash(filepath.Clean(sessionPath))
}
