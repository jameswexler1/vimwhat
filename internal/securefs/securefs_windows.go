//go:build windows

package securefs

import (
	"fmt"
	"os"
)

func repairPrivatePath(path string, wantDir bool, _ os.FileMode) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if wantDir && !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	}
	if !wantDir && !info.Mode().IsRegular() {
		return fmt.Errorf("path is not a regular file")
	}
	return nil
}

func privatePathStatus(path string, wantDir bool, mode os.FileMode) Status {
	status := Status{
		Path:          path,
		Kind:          pathKind(wantDir),
		Want:          mode,
		NotApplicable: true,
	}
	if path == "" {
		status.Warning = "path is empty"
		return status
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return status
	}
	if err != nil {
		status.Warning = err.Error()
		return status
	}
	status.Exists = true
	status.Mode = info.Mode().Perm()
	if wantDir && !info.IsDir() {
		status.Warning = "not a directory"
		return status
	}
	if !wantDir && !info.Mode().IsRegular() {
		status.Warning = "not a regular file"
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
