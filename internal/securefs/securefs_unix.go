//go:build !windows

package securefs

import (
	"fmt"
	"os"
)

func repairPrivatePath(path string, wantDir bool, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if wantDir && !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	}
	if !wantDir && !info.Mode().IsRegular() {
		return fmt.Errorf("path is not a regular file")
	}
	if info.Mode().Perm() != mode {
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
	}
	return nil
}

func privatePathStatus(path string, wantDir bool, mode os.FileMode) Status {
	status := Status{
		Path: path,
		Kind: pathKind(wantDir),
		Want: mode,
	}
	if path == "" {
		status.Warning = "path is empty"
		return status
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return status
	}
	if err != nil {
		status.Warning = err.Error()
		return status
	}
	status.Exists = true
	status.Mode = info.Mode().Perm()
	if info.Mode()&os.ModeSymlink != 0 {
		status.Warning = "symlink; target permissions not checked"
		return status
	}
	if wantDir && !info.IsDir() {
		status.Warning = "not a directory"
		return status
	}
	if !wantDir && !info.Mode().IsRegular() {
		status.Warning = "not a regular file"
		return status
	}
	if status.Mode != mode {
		status.Warning = fmt.Sprintf("mode %04o, want %04o", status.Mode, mode)
		return status
	}
	status.OK = true
	return status
}

func pathKind(wantDir bool) string {
	if wantDir {
		return "dir"
	}
	return "file"
}
