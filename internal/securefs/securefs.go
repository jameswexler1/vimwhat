package securefs

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	PrivateDirMode  os.FileMode = 0o700
	PrivateFileMode os.FileMode = 0o600
)

type Status struct {
	Path          string
	Kind          string
	Exists        bool
	Mode          os.FileMode
	Want          os.FileMode
	OK            bool
	Warning       string
	NotApplicable bool
}

func EnsurePrivateDir(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(path, PrivateDirMode); err != nil {
		return fmt.Errorf("create private dir %s: %w", path, err)
	}
	if err := repairPrivatePath(path, true, PrivateDirMode); err != nil {
		return fmt.Errorf("secure private dir %s: %w", path, err)
	}
	return nil
}

func EnsurePrivateFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, PrivateFileMode)
	if err == nil {
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("close private file %s: %w", path, closeErr)
		}
		return nil
	}
	if !os.IsExist(err) {
		return fmt.Errorf("create private file %s: %w", path, err)
	}
	if err := RepairPrivateFile(path); err != nil {
		return fmt.Errorf("secure private file %s: %w", path, err)
	}
	return nil
}

func RepairPrivateFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := repairPrivatePath(path, false, PrivateFileMode); err != nil {
		return fmt.Errorf("secure private file %s: %w", path, err)
	}
	return nil
}

func RepairSQLiteArtifacts(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	var errs []error
	for _, target := range []string{path, path + "-wal", path + "-shm"} {
		if err := RepairPrivateFileIfExists(target); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func RepairPrivateFileIfExists(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat private file %s: %w", path, err)
	}
	return RepairPrivateFile(path)
}

func PrivateDirStatus(path string) Status {
	return privatePathStatus(strings.TrimSpace(path), true, PrivateDirMode)
}

func PrivateFileStatus(path string) Status {
	return privatePathStatus(strings.TrimSpace(path), false, PrivateFileMode)
}
