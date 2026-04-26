//go:build !windows

package media

func platformSafeSaveFileName(name string) string {
	return name
}
