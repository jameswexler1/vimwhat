package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Editor              string
	PreviewBackend      string
	NotificationCommand string
	ClipboardCommand    string
	FilePickerCommand   string
	DownloadsDir        string
}

func Load(paths Paths) (Config, error) {
	cfg := Default(paths)

	data, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	if err := parseSimpleTOML(string(data), &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", paths.ConfigFile, err)
	}

	return cfg, nil
}

func Default(paths Paths) Config {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
	}

	downloadsDir := filepath.Join(mustHomeDir(), "Downloads")

	return Config{
		Editor:            editor,
		PreviewBackend:    "auto",
		FilePickerCommand: "yazi --chooser-file {chooser}",
		DownloadsDir:      downloadsDir,
	}
}

func mustHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func parseSimpleTOML(input string, cfg *Config) error {
	scanner := bufio.NewScanner(strings.NewReader(input))
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			return fmt.Errorf("line %d: nested sections are not supported yet", lineNo)
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("line %d: expected key = value", lineNo)
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = stripComment(value)
		parsed, err := parseValue(value)
		if err != nil {
			return fmt.Errorf("line %d: %w", lineNo, err)
		}

		switch key {
		case "editor":
			cfg.Editor = parsed
		case "preview_backend":
			cfg.PreviewBackend = parsed
		case "notification_command":
			cfg.NotificationCommand = parsed
		case "clipboard_command":
			cfg.ClipboardCommand = parsed
		case "file_picker_command":
			cfg.FilePickerCommand = parsed
		case "downloads_dir":
			cfg.DownloadsDir = expandPath(parsed)
		default:
			return fmt.Errorf("line %d: unknown key %q", lineNo, key)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func stripComment(value string) string {
	inQuote := false
	var b strings.Builder

	for _, r := range value {
		switch r {
		case '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case '#':
			if !inQuote {
				return strings.TrimSpace(b.String())
			}
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}

	return strings.TrimSpace(b.String())
}

func parseValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}

	if strings.HasPrefix(value, "\"") {
		if !strings.HasSuffix(value, "\"") || len(value) < 2 {
			return "", fmt.Errorf("unterminated quoted string")
		}
		return strings.Trim(value, "\""), nil
	}

	return value, nil
}

func expandPath(value string) string {
	if value == "" {
		return value
	}

	if strings.HasPrefix(value, "~/") {
		return filepath.Join(mustHomeDir(), strings.TrimPrefix(value, "~/"))
	}

	return os.ExpandEnv(value)
}
