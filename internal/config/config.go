package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Config struct {
	Editor                     string
	PreviewBackend             string
	EmojiMode                  string
	IndicatorNormal            string
	IndicatorInsert            string
	IndicatorVisual            string
	IndicatorCommand           string
	IndicatorSearch            string
	NotificationBackend        string
	NotificationCommand        string
	ClipboardCommand           string
	ClipboardImagePasteCommand string
	ClipboardImageCopyCommand  string
	FilePickerCommand          string
	ImageViewerCommand         string
	VideoPlayerCommand         string
	AudioPlayerCommand         string
	FileOpenerCommand          string
	LeaderKey                  string
	Keymap                     Keymap
	PreviewMaxWidth            int
	PreviewMaxHeight           int
	PreviewDelayMS             int
	DownloadsDir               string
}

const (
	EmojiModeAuto   = "auto"
	EmojiModeFull   = "full"
	EmojiModeCompat = "compat"
	IndicatorPywal  = "pywal"
)

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
	downloadsDir := filepath.Join(mustHomeDir(), "Downloads")

	return Config{
		Editor:              platformDefaultEditor(),
		PreviewBackend:      "auto",
		EmojiMode:           EmojiModeAuto,
		IndicatorNormal:     IndicatorPywal,
		IndicatorInsert:     IndicatorPywal,
		IndicatorVisual:     IndicatorPywal,
		IndicatorCommand:    IndicatorPywal,
		IndicatorSearch:     IndicatorPywal,
		NotificationBackend: "auto",
		FilePickerCommand:   platformDefaultFilePickerCommand(),
		ImageViewerCommand:  platformDefaultImageViewerCommand(),
		VideoPlayerCommand:  platformDefaultVideoPlayerCommand(),
		AudioPlayerCommand:  platformDefaultAudioPlayerCommand(),
		FileOpenerCommand:   platformDefaultFileOpenerCommand(),
		LeaderKey:           "space",
		Keymap:              DefaultKeymap(),
		PreviewMaxWidth:     67,
		PreviewMaxHeight:    18,
		PreviewDelayMS:      80,
		DownloadsDir:        downloadsDir,
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
		if strings.HasPrefix(key, "key_") {
			if err := SetKeyBinding(&cfg.Keymap, key, parsed); err != nil {
				return fmt.Errorf("line %d: %s: %w", lineNo, key, err)
			}
			continue
		}

		switch key {
		case "editor":
			cfg.Editor = parsed
		case "preview_backend":
			cfg.PreviewBackend = parsed
		case "emoji_mode":
			cfg.EmojiMode, err = parseEmojiMode(parsed)
			if err != nil {
				return fmt.Errorf("line %d: emoji_mode: %w", lineNo, err)
			}
		case "indicator_normal":
			cfg.IndicatorNormal, err = parseModeIndicator(parsed)
			if err != nil {
				return fmt.Errorf("line %d: indicator_normal: %w", lineNo, err)
			}
		case "indicator_insert":
			cfg.IndicatorInsert, err = parseModeIndicator(parsed)
			if err != nil {
				return fmt.Errorf("line %d: indicator_insert: %w", lineNo, err)
			}
		case "indicator_visual":
			cfg.IndicatorVisual, err = parseModeIndicator(parsed)
			if err != nil {
				return fmt.Errorf("line %d: indicator_visual: %w", lineNo, err)
			}
		case "indicator_command":
			cfg.IndicatorCommand, err = parseModeIndicator(parsed)
			if err != nil {
				return fmt.Errorf("line %d: indicator_command: %w", lineNo, err)
			}
		case "indicator_search":
			cfg.IndicatorSearch, err = parseModeIndicator(parsed)
			if err != nil {
				return fmt.Errorf("line %d: indicator_search: %w", lineNo, err)
			}
		case "notification_backend":
			cfg.NotificationBackend, err = parseNotificationBackend(parsed)
			if err != nil {
				return fmt.Errorf("line %d: notification_backend: %w", lineNo, err)
			}
		case "notification_command":
			cfg.NotificationCommand = parsed
		case "clipboard_command":
			cfg.ClipboardCommand = parsed
		case "clipboard_image_paste_command":
			cfg.ClipboardImagePasteCommand = parsed
		case "clipboard_image_copy_command":
			cfg.ClipboardImageCopyCommand = parsed
		case "file_picker_command":
			cfg.FilePickerCommand = parsed
		case "image_viewer_command":
			cfg.ImageViewerCommand = parsed
		case "video_player_command":
			cfg.VideoPlayerCommand = parsed
		case "audio_player_command":
			cfg.AudioPlayerCommand = parsed
		case "file_opener_command":
			cfg.FileOpenerCommand = parsed
		case "leader_key":
			cfg.LeaderKey, err = ParseLeaderKey(parsed)
			if err != nil {
				return fmt.Errorf("line %d: leader_key: %w", lineNo, err)
			}
		case "preview_max_width":
			cfg.PreviewMaxWidth, err = parsePositiveInt(parsed)
			if err != nil {
				return fmt.Errorf("line %d: preview_max_width: %w", lineNo, err)
			}
		case "preview_max_height":
			cfg.PreviewMaxHeight, err = parsePositiveInt(parsed)
			if err != nil {
				return fmt.Errorf("line %d: preview_max_height: %w", lineNo, err)
			}
		case "preview_delay_ms":
			cfg.PreviewDelayMS, err = parsePositiveInt(parsed)
			if err != nil {
				return fmt.Errorf("line %d: preview_delay_ms: %w", lineNo, err)
			}
		case "downloads_dir":
			cfg.DownloadsDir = expandPath(parsed)
		default:
			return fmt.Errorf("line %d: unknown key %q", lineNo, key)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if err := ValidateKeymap(*cfg); err != nil {
		return err
	}

	return nil
}

func ResolveEmojiMode(mode string) string {
	return ResolveEmojiModeForEnv(mode, os.Getenv("TERM"), os.Getenv("LC_ALL"), os.Getenv("LC_CTYPE"), os.Getenv("LANG"))
}

func ResolveEmojiModeForEnv(mode, term, lcAll, lcCtype, lang string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case EmojiModeFull:
		return EmojiModeFull
	case EmojiModeCompat:
		return EmojiModeCompat
	case "", EmojiModeAuto:
		if terminalPrefersCompatEmoji(term) || platformPrefersCompatEmoji(term) || !LocaleLooksUTF8ForEnv(lcAll, lcCtype, lang) {
			return EmojiModeCompat
		}
		return EmojiModeFull
	default:
		return EmojiModeCompat
	}
}

func terminalPrefersCompatEmoji(term string) bool {
	term = strings.ToLower(strings.TrimSpace(term))
	return term == "dumb" || term == "st" || strings.HasPrefix(term, "st-")
}

func LocaleLooksUTF8() bool {
	return LocaleLooksUTF8ForEnv(os.Getenv("LC_ALL"), os.Getenv("LC_CTYPE"), os.Getenv("LANG"))
}

func LocaleLooksUTF8ForEnv(lcAll, lcCtype, lang string) bool {
	for _, value := range []string{lcAll, lcCtype, lang} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		upper := strings.ToUpper(value)
		return strings.Contains(upper, "UTF-8") || strings.Contains(upper, "UTF8")
	}
	return true
}

func parseEmojiMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case EmojiModeAuto:
		return EmojiModeAuto, nil
	case EmojiModeFull:
		return EmojiModeFull, nil
	case EmojiModeCompat:
		return EmojiModeCompat, nil
	default:
		return "", fmt.Errorf("must be %q, %q, or %q", EmojiModeAuto, EmojiModeFull, EmojiModeCompat)
	}
}

func parseModeIndicator(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, IndicatorPywal) {
		return IndicatorPywal, nil
	}
	if isHexColor(value) {
		return value, nil
	}
	return "", fmt.Errorf("must be %q or a hex color like \"#7ED7C1\"", IndicatorPywal)
}

func parseNotificationBackend(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "auto", nil
	case "none":
		return "none", nil
	case "command":
		return "command", nil
	case "linux-dbus":
		return "linux-dbus", nil
	case "macos-osascript":
		return "macos-osascript", nil
	case "windows-powershell":
		return "windows-powershell", nil
	default:
		return "", fmt.Errorf("must be %q, %q, %q, %q, %q, or %q", "auto", "none", "command", "linux-dbus", "macos-osascript", "windows-powershell")
	}
}

func isHexColor(value string) bool {
	if len(value) != 4 && len(value) != 7 {
		return false
	}
	if !strings.HasPrefix(value, "#") {
		return false
	}
	for _, r := range value[1:] {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}

func ParseLeaderKey(value string) (string, error) {
	if value == " " || strings.EqualFold(strings.TrimSpace(value), "space") {
		return "space", nil
	}
	if utf8.RuneCountInString(value) != 1 || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("must be \"space\" or a single key")
	}
	return value, nil
}

func parsePositiveInt(value string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	if parsed < 0 {
		return 0, fmt.Errorf("must be >= 0")
	}
	return parsed, nil
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
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return "", err
		}
		return parsed, nil
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
