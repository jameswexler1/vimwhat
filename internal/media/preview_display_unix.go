//go:build !windows

package media

func platformSixelPreviewDisplay() PreviewDisplay {
	return PreviewDisplayText
}
