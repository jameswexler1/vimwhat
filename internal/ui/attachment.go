package ui

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type Attachment struct {
	LocalPath     string
	FileName      string
	MIMEType      string
	SizeBytes     int64
	ThumbnailPath string
	DownloadState string
}

type AttachmentPickedMsg struct {
	Attachment Attachment
	Err        error
	Cancelled  bool
}

func AttachmentFromPath(path string) (Attachment, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Attachment{}, fmt.Errorf("attachment path is required")
	}

	info, err := os.Stat(path)
	if err != nil {
		return Attachment{}, fmt.Errorf("stat attachment: %w", err)
	}
	if info.IsDir() {
		return Attachment{}, fmt.Errorf("attachment path is a directory")
	}

	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mimeType == "" {
		mimeType = detectFileMIME(path)
	}

	return Attachment{
		LocalPath:     path,
		FileName:      info.Name(),
		MIMEType:      mimeType,
		SizeBytes:     info.Size(),
		DownloadState: "local_pending",
	}, nil
}

func detectFileMIME(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer file.Close()

	var buf [512]byte
	n, err := file.Read(buf[:])
	if err != nil && n == 0 {
		return "application/octet-stream"
	}

	return http.DetectContentType(buf[:n])
}
