package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vimwhat/internal/config"
	"vimwhat/internal/store"
)

const cliCommandTimeout = 5 * time.Minute

func runMedia(env Environment, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "open" {
		fmt.Fprintln(stderr, "usage: vimwhat media open <message-id>")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), cliCommandTimeout)
	defer cancel()

	if err := runMediaOpen(ctx, env, args[1], stdout); err != nil {
		fmt.Fprintf(stderr, "vimwhat: media open: %v\n", err)
		return 1
	}
	return 0
}

func runExport(env Environment, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "chat" {
		fmt.Fprintln(stderr, "usage: vimwhat export chat <jid>")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), cliCommandTimeout)
	defer cancel()

	path, err := exportChatMarkdown(ctx, env, args[1])
	if err != nil {
		fmt.Fprintf(stderr, "vimwhat: export chat: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}

func runMediaOpen(ctx context.Context, env Environment, messageID string, stdout io.Writer) error {
	if env.Store == nil {
		return fmt.Errorf("store is unavailable")
	}

	message, ok, err := env.Store.MessageByID(ctx, messageID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("message %q not found", messageID)
	}
	if len(message.Media) == 0 {
		return fmt.Errorf("message %q has no media attachment", messageID)
	}
	if len(message.Media) != 1 {
		return fmt.Errorf("message %q has %d media attachments; only one is supported", messageID, len(message.Media))
	}

	item := message.Media[0]
	if strings.TrimSpace(item.MessageID) == "" {
		item.MessageID = message.ID
	}
	repaired, _, err := repairManagedMediaMetadata(ctx, env.Store, env.Paths, item)
	if err != nil {
		return err
	}
	item = repaired
	if !mediaPathAvailable(item.LocalPath) {
		if _, ok, err := env.Store.MediaDownloadDescriptor(ctx, message.ID); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("media is not available locally and cannot be fetched")
		}

		status, err := checkWhatsAppSession(ctx, env)
		if err != nil {
			return fmt.Errorf("check whatsapp session: %w", err)
		}
		if !status.Paired {
			return fmt.Errorf("media is not downloaded locally and WhatsApp is not paired")
		}

		live, err := openWhatsAppLiveSession(ctx, env)
		if err != nil {
			return fmt.Errorf("open whatsapp session: %w", err)
		}
		defer live.Close()

		if err := live.Connect(ctx); err != nil {
			return fmt.Errorf("connect whatsapp session: %w", err)
		}

		item, err = downloadRemoteMedia(ctx, env.Store, live, env.Paths, mediaDownloadRequest{
			Message: message,
			Media:   item,
		})
		if err != nil {
			return err
		}
	}

	cmd, path, err := mediaOpenCommand(env.Config, item)
	if err != nil {
		return err
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("open media: %w", err)
	}
	_, err = fmt.Fprintln(stdout, path)
	return err
}

func openWhatsAppLiveSession(ctx context.Context, env Environment) (WhatsAppLiveSession, error) {
	session, err := openWhatsAppSession(ctx, env)
	if err != nil {
		return nil, err
	}
	live, ok := session.(WhatsAppLiveSession)
	if !ok {
		_ = session.Close()
		return nil, fmt.Errorf("whatsapp live session unavailable")
	}
	return live, nil
}

func exportChatMarkdown(ctx context.Context, env Environment, jid string) (string, error) {
	if env.Store == nil {
		return "", fmt.Errorf("store is unavailable")
	}

	chat, ok, err := env.Store.ChatByJID(ctx, jid)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("chat %q not found", jid)
	}

	messages, err := env.Store.ListAllMessages(ctx, chat.ID)
	if err != nil {
		return "", err
	}

	exportedAt := time.Now()
	name := fmt.Sprintf("vimwhat-chat-%s-%s.md", sanitizeFilenameComponent(chat.JID), exportedAt.Format("20060102-150405"))
	path, err := writeExportFile(exportDownloadsDir(env), name, []byte(renderMarkdownExport(chat, messages, exportedAt)))
	if err != nil {
		return "", err
	}
	return path, nil
}

func exportDownloadsDir(env Environment) string {
	if dir := strings.TrimSpace(env.Config.DownloadsDir); dir != "" {
		return dir
	}
	return config.Default(env.Paths).DownloadsDir
}

func renderMarkdownExport(chat store.Chat, messages []store.Message, exportedAt time.Time) string {
	var body bytes.Buffer
	title := strings.TrimSpace(chat.Title)
	if title == "" {
		title = strings.TrimSpace(chat.JID)
	}
	if title == "" {
		title = chat.ID
	}

	fmt.Fprintf(&body, "# Vimwhat Chat Export\n\n")
	fmt.Fprintf(&body, "- Chat: %s\n", title)
	fmt.Fprintf(&body, "- JID: `%s`\n", strings.TrimSpace(chat.JID))
	fmt.Fprintf(&body, "- Exported: %s\n", exportedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(&body, "- Scope: local SQLite history only\n")

	if len(messages) == 0 {
		fmt.Fprintf(&body, "\n_No messages exported._\n")
		return body.String()
	}

	byID := make(map[string]store.Message, len(messages))
	for _, message := range messages {
		byID[message.ID] = message
	}

	for _, message := range messages {
		fmt.Fprintf(&body, "\n## %s %s\n", message.Timestamp.Format("2006-01-02 15:04:05"), exportSenderLabel(message))
		if status := strings.TrimSpace(message.Status); status != "" && status != "sent" {
			fmt.Fprintf(&body, "Status: %s\n", status)
		}
		if summary := quotedSummaryForExport(message, byID); summary != "" {
			fmt.Fprintf(&body, "Reply: %s\n", summary)
		}
		if text := strings.TrimSpace(message.Body); text != "" {
			fmt.Fprintf(&body, "%s\n", strings.TrimRight(message.Body, "\n"))
		}
		for _, item := range message.Media {
			fmt.Fprintf(&body, "Attachment: %s (%s, %s)\n", mediaLabelForExport(item), emptyAsUnknown(item.MIMEType), emptyAsUnknown(item.DownloadState))
			if mediaPathAvailable(item.LocalPath) {
				fmt.Fprintf(&body, "Local file: `%s`\n", item.LocalPath)
			}
		}
	}

	return body.String()
}

func exportSenderLabel(message store.Message) string {
	if message.IsOutgoing {
		return "Me"
	}
	for _, value := range []string{message.Sender, message.SenderJID, message.ChatJID, message.ChatID} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "Unknown"
}

func quotedSummaryForExport(message store.Message, byID map[string]store.Message) string {
	if quotedID := strings.TrimSpace(message.QuotedMessageID); quotedID != "" {
		if quoted, ok := byID[quotedID]; ok {
			summary := exportSenderLabel(quoted)
			if preview := exportMessagePreview(quoted); preview != "" {
				return summary + ": " + preview
			}
			return summary
		}
	}
	return strings.TrimSpace(message.QuotedRemoteID)
}

func exportMessagePreview(message store.Message) string {
	if text := strings.TrimSpace(firstLineForExport(message.Body)); text != "" {
		return text
	}
	if len(message.Media) > 0 {
		return mediaLabelForExport(message.Media[0])
	}
	return ""
}

func mediaLabelForExport(item store.MediaMetadata) string {
	for _, value := range []string{item.FileName, item.MIMEType} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "media"
}

func emptyAsUnknown(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "unknown"
}

func firstLineForExport(value string) string {
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func sanitizeFilenameComponent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "chat"
	}

	var out strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out.WriteRune(r)
			lastUnderscore = false
		case r == '.', r == '-':
			out.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				out.WriteByte('_')
				lastUnderscore = true
			}
		}
	}

	result := strings.Trim(out.String(), "_.-")
	if result == "" {
		return "chat"
	}
	return result
}

func writeExportFile(downloadsDir, name string, data []byte) (string, error) {
	if strings.TrimSpace(downloadsDir) == "" {
		return "", fmt.Errorf("downloads dir is empty")
	}
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		return "", fmt.Errorf("create downloads dir: %w", err)
	}

	for index := 0; ; index++ {
		target := filepath.Join(downloadsDir, collisionNameForExport(name, index))
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", fmt.Errorf("create export file: %w", err)
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			return "", fmt.Errorf("write export file: %w", err)
		}
		if err := file.Close(); err != nil {
			return "", fmt.Errorf("close export file: %w", err)
		}
		return target, nil
	}
}

func collisionNameForExport(name string, index int) string {
	if index == 0 {
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem = "export"
	}
	return fmt.Sprintf("%s-%d%s", stem, index, ext)
}
