//go:build !windows

package securefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePrivateDirCreatesAndRepairsMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatalf("EnsurePrivateDir() error = %v", err)
	}

	assertMode(t, dir, PrivateDirMode)
}

func TestEnsurePrivateFileCreatesAndRepairsModeWithoutChangingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite3")

	if err := EnsurePrivateFile(path); err != nil {
		t.Fatalf("EnsurePrivateFile(create) error = %v", err)
	}
	assertMode(t, path, PrivateFileMode)

	if err := os.WriteFile(path, []byte("state"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	if err := EnsurePrivateFile(path); err != nil {
		t.Fatalf("EnsurePrivateFile(repair) error = %v", err)
	}

	assertMode(t, path, PrivateFileMode)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "state" {
		t.Fatalf("file content = %q, want preserved content", string(data))
	}
}

func TestRepairSQLiteArtifactsRepairsExistingSidecarsAndIgnoresMissing(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "state.sqlite3")
	for _, path := range []string{db, db + "-wal"} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("Chmod(%q) error = %v", path, err)
		}
	}

	if err := RepairSQLiteArtifacts(db); err != nil {
		t.Fatalf("RepairSQLiteArtifacts() error = %v", err)
	}

	assertMode(t, db, PrivateFileMode)
	assertMode(t, db+"-wal", PrivateFileMode)
	if _, err := os.Stat(db + "-shm"); !os.IsNotExist(err) {
		t.Fatalf("shm stat error = %v, want missing", err)
	}
}

func TestPrivateStatusWarnsForPermissiveMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("config"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}

	status := PrivateFileStatus(path)
	if status.OK || status.Warning == "" {
		t.Fatalf("PrivateFileStatus() = %+v, want warning", status)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
