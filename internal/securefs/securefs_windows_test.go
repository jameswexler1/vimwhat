//go:build windows

package securefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePrivateDirAndFileCreatePathsOnWindows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatalf("EnsurePrivateDir() error = %v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("state dir stat = %v, info = %+v", err, info)
	}

	path := filepath.Join(dir, "state.sqlite3")
	if err := EnsurePrivateFile(path); err != nil {
		t.Fatalf("EnsurePrivateFile() error = %v", err)
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("state file stat = %v, info = %+v", err, info)
	}

	status := PrivateFileStatus(path)
	if !status.OK || !status.NotApplicable {
		t.Fatalf("PrivateFileStatus() = %+v, want ok ACL-based status", status)
	}
}
