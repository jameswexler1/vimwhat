package config

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	KeyModeGlobal  = "global"
	KeyModeHelp    = "help"
	KeyModeNormal  = "normal"
	KeyModeInsert  = "insert"
	KeyModeVisual  = "visual"
	KeyModeCommand = "command"
	KeyModeSearch  = "search"
)

type Keymap struct {
	GlobalQuit string

	HelpClose    string
	HelpCloseAlt string

	NormalCancel            string
	NormalQuit              string
	NormalHelp              string
	NormalInsert            string
	NormalReply             string
	NormalRetryFailedMedia  string
	NormalVisual            string
	NormalCommand           string
	NormalSearch            string
	NormalFocusNext         string
	NormalFocusPrevious     string
	NormalFocusLeft         string
	NormalFocusRightOrReply string
	NormalMoveDown          string
	NormalMoveUp            string
	NormalGoTop             string
	NormalGoBottom          string
	NormalOpen              string
	NormalOpenMedia         string
	NormalSearchNext        string
	NormalSearchPrevious    string
	NormalToggleUnread      string
	NormalTogglePinned      string
	NormalSaveMedia         string
	NormalUnloadPreviews    string

	InsertAttach           string
	InsertRemoveAttachment string
	InsertNewline          string
	InsertNewlineAlt       string
	InsertCancel           string
	InsertSend             string
	InsertBackspace        string
	InsertBackspaceAlt     string

	VisualCancel   string
	VisualMoveDown string
	VisualMoveUp   string
	VisualYank     string

	CommandCancel       string
	CommandRun          string
	CommandBackspace    string
	CommandBackspaceAlt string

	SearchCancel       string
	SearchRun          string
	SearchBackspace    string
	SearchBackspaceAlt string
}

type KeyBinding struct {
	Name  string
	Mode  string
	Value string
}

func DefaultKeymap() Keymap {
	return Keymap{
		GlobalQuit: "ctrl+c",

		HelpClose:    "esc",
		HelpCloseAlt: "?",

		NormalCancel:            "esc",
		NormalQuit:              "q",
		NormalHelp:              "?",
		NormalInsert:            "i",
		NormalReply:             "r",
		NormalRetryFailedMedia:  "R",
		NormalVisual:            "v",
		NormalCommand:           ":",
		NormalSearch:            "/",
		NormalFocusNext:         "tab",
		NormalFocusPrevious:     "shift+tab",
		NormalFocusLeft:         "h",
		NormalFocusRightOrReply: "l",
		NormalMoveDown:          "j",
		NormalMoveUp:            "k",
		NormalGoTop:             "g",
		NormalGoBottom:          "G",
		NormalOpen:              "enter",
		NormalOpenMedia:         "o",
		NormalSearchNext:        "n",
		NormalSearchPrevious:    "N",
		NormalToggleUnread:      "u",
		NormalTogglePinned:      "p",
		NormalSaveMedia:         "leader s",
		NormalUnloadPreviews:    "leader h f",

		InsertAttach:           "ctrl+f",
		InsertRemoveAttachment: "ctrl+x",
		InsertNewline:          "ctrl+j",
		InsertNewlineAlt:       "alt+enter",
		InsertCancel:           "esc",
		InsertSend:             "enter",
		InsertBackspace:        "backspace",
		InsertBackspaceAlt:     "ctrl+h",

		VisualCancel:   "esc",
		VisualMoveDown: "j",
		VisualMoveUp:   "k",
		VisualYank:     "y",

		CommandCancel:       "esc",
		CommandRun:          "enter",
		CommandBackspace:    "backspace",
		CommandBackspaceAlt: "ctrl+h",

		SearchCancel:       "esc",
		SearchRun:          "enter",
		SearchBackspace:    "backspace",
		SearchBackspaceAlt: "ctrl+h",
	}
}

func NormalizeKeymap(input Keymap) Keymap {
	defaults := DefaultKeymap()

	if input.GlobalQuit == "" {
		input.GlobalQuit = defaults.GlobalQuit
	}
	if input.HelpClose == "" {
		input.HelpClose = defaults.HelpClose
	}
	if input.HelpCloseAlt == "" {
		input.HelpCloseAlt = defaults.HelpCloseAlt
	}
	if input.NormalCancel == "" {
		input.NormalCancel = defaults.NormalCancel
	}
	if input.NormalQuit == "" {
		input.NormalQuit = defaults.NormalQuit
	}
	if input.NormalHelp == "" {
		input.NormalHelp = defaults.NormalHelp
	}
	if input.NormalInsert == "" {
		input.NormalInsert = defaults.NormalInsert
	}
	if input.NormalReply == "" {
		input.NormalReply = defaults.NormalReply
	}
	if input.NormalRetryFailedMedia == "" {
		input.NormalRetryFailedMedia = defaults.NormalRetryFailedMedia
	}
	if input.NormalVisual == "" {
		input.NormalVisual = defaults.NormalVisual
	}
	if input.NormalCommand == "" {
		input.NormalCommand = defaults.NormalCommand
	}
	if input.NormalSearch == "" {
		input.NormalSearch = defaults.NormalSearch
	}
	if input.NormalFocusNext == "" {
		input.NormalFocusNext = defaults.NormalFocusNext
	}
	if input.NormalFocusPrevious == "" {
		input.NormalFocusPrevious = defaults.NormalFocusPrevious
	}
	if input.NormalFocusLeft == "" {
		input.NormalFocusLeft = defaults.NormalFocusLeft
	}
	if input.NormalFocusRightOrReply == "" {
		input.NormalFocusRightOrReply = defaults.NormalFocusRightOrReply
	}
	if input.NormalMoveDown == "" {
		input.NormalMoveDown = defaults.NormalMoveDown
	}
	if input.NormalMoveUp == "" {
		input.NormalMoveUp = defaults.NormalMoveUp
	}
	if input.NormalGoTop == "" {
		input.NormalGoTop = defaults.NormalGoTop
	}
	if input.NormalGoBottom == "" {
		input.NormalGoBottom = defaults.NormalGoBottom
	}
	if input.NormalOpen == "" {
		input.NormalOpen = defaults.NormalOpen
	}
	if input.NormalOpenMedia == "" {
		input.NormalOpenMedia = defaults.NormalOpenMedia
	}
	if input.NormalSearchNext == "" {
		input.NormalSearchNext = defaults.NormalSearchNext
	}
	if input.NormalSearchPrevious == "" {
		input.NormalSearchPrevious = defaults.NormalSearchPrevious
	}
	if input.NormalToggleUnread == "" {
		input.NormalToggleUnread = defaults.NormalToggleUnread
	}
	if input.NormalTogglePinned == "" {
		input.NormalTogglePinned = defaults.NormalTogglePinned
	}
	if input.NormalSaveMedia == "" {
		input.NormalSaveMedia = defaults.NormalSaveMedia
	}
	if input.NormalUnloadPreviews == "" {
		input.NormalUnloadPreviews = defaults.NormalUnloadPreviews
	}
	if input.InsertAttach == "" {
		input.InsertAttach = defaults.InsertAttach
	}
	if input.InsertRemoveAttachment == "" {
		input.InsertRemoveAttachment = defaults.InsertRemoveAttachment
	}
	if input.InsertNewline == "" {
		input.InsertNewline = defaults.InsertNewline
	}
	if input.InsertNewlineAlt == "" {
		input.InsertNewlineAlt = defaults.InsertNewlineAlt
	}
	if input.InsertCancel == "" {
		input.InsertCancel = defaults.InsertCancel
	}
	if input.InsertSend == "" {
		input.InsertSend = defaults.InsertSend
	}
	if input.InsertBackspace == "" {
		input.InsertBackspace = defaults.InsertBackspace
	}
	if input.InsertBackspaceAlt == "" {
		input.InsertBackspaceAlt = defaults.InsertBackspaceAlt
	}
	if input.VisualCancel == "" {
		input.VisualCancel = defaults.VisualCancel
	}
	if input.VisualMoveDown == "" {
		input.VisualMoveDown = defaults.VisualMoveDown
	}
	if input.VisualMoveUp == "" {
		input.VisualMoveUp = defaults.VisualMoveUp
	}
	if input.VisualYank == "" {
		input.VisualYank = defaults.VisualYank
	}
	if input.CommandCancel == "" {
		input.CommandCancel = defaults.CommandCancel
	}
	if input.CommandRun == "" {
		input.CommandRun = defaults.CommandRun
	}
	if input.CommandBackspace == "" {
		input.CommandBackspace = defaults.CommandBackspace
	}
	if input.CommandBackspaceAlt == "" {
		input.CommandBackspaceAlt = defaults.CommandBackspaceAlt
	}
	if input.SearchCancel == "" {
		input.SearchCancel = defaults.SearchCancel
	}
	if input.SearchRun == "" {
		input.SearchRun = defaults.SearchRun
	}
	if input.SearchBackspace == "" {
		input.SearchBackspace = defaults.SearchBackspace
	}
	if input.SearchBackspaceAlt == "" {
		input.SearchBackspaceAlt = defaults.SearchBackspaceAlt
	}

	return input
}

func KeymapBindings(k Keymap) []KeyBinding {
	return []KeyBinding{
		{Name: "key_global_quit", Mode: KeyModeGlobal, Value: k.GlobalQuit},
		{Name: "key_help_close", Mode: KeyModeHelp, Value: k.HelpClose},
		{Name: "key_help_close_alt", Mode: KeyModeHelp, Value: k.HelpCloseAlt},
		{Name: "key_normal_cancel", Mode: KeyModeNormal, Value: k.NormalCancel},
		{Name: "key_normal_quit", Mode: KeyModeNormal, Value: k.NormalQuit},
		{Name: "key_normal_help", Mode: KeyModeNormal, Value: k.NormalHelp},
		{Name: "key_normal_insert", Mode: KeyModeNormal, Value: k.NormalInsert},
		{Name: "key_normal_reply", Mode: KeyModeNormal, Value: k.NormalReply},
		{Name: "key_normal_retry_failed_media", Mode: KeyModeNormal, Value: k.NormalRetryFailedMedia},
		{Name: "key_normal_visual", Mode: KeyModeNormal, Value: k.NormalVisual},
		{Name: "key_normal_command", Mode: KeyModeNormal, Value: k.NormalCommand},
		{Name: "key_normal_search", Mode: KeyModeNormal, Value: k.NormalSearch},
		{Name: "key_normal_focus_next", Mode: KeyModeNormal, Value: k.NormalFocusNext},
		{Name: "key_normal_focus_previous", Mode: KeyModeNormal, Value: k.NormalFocusPrevious},
		{Name: "key_normal_focus_left", Mode: KeyModeNormal, Value: k.NormalFocusLeft},
		{Name: "key_normal_focus_right_or_reply", Mode: KeyModeNormal, Value: k.NormalFocusRightOrReply},
		{Name: "key_normal_move_down", Mode: KeyModeNormal, Value: k.NormalMoveDown},
		{Name: "key_normal_move_up", Mode: KeyModeNormal, Value: k.NormalMoveUp},
		{Name: "key_normal_go_top", Mode: KeyModeNormal, Value: k.NormalGoTop},
		{Name: "key_normal_go_bottom", Mode: KeyModeNormal, Value: k.NormalGoBottom},
		{Name: "key_normal_open", Mode: KeyModeNormal, Value: k.NormalOpen},
		{Name: "key_normal_open_media", Mode: KeyModeNormal, Value: k.NormalOpenMedia},
		{Name: "key_normal_search_next", Mode: KeyModeNormal, Value: k.NormalSearchNext},
		{Name: "key_normal_search_previous", Mode: KeyModeNormal, Value: k.NormalSearchPrevious},
		{Name: "key_normal_toggle_unread", Mode: KeyModeNormal, Value: k.NormalToggleUnread},
		{Name: "key_normal_toggle_pinned", Mode: KeyModeNormal, Value: k.NormalTogglePinned},
		{Name: "key_normal_save_media", Mode: KeyModeNormal, Value: k.NormalSaveMedia},
		{Name: "key_normal_unload_previews", Mode: KeyModeNormal, Value: k.NormalUnloadPreviews},
		{Name: "key_insert_attach", Mode: KeyModeInsert, Value: k.InsertAttach},
		{Name: "key_insert_remove_attachment", Mode: KeyModeInsert, Value: k.InsertRemoveAttachment},
		{Name: "key_insert_newline", Mode: KeyModeInsert, Value: k.InsertNewline},
		{Name: "key_insert_newline_alt", Mode: KeyModeInsert, Value: k.InsertNewlineAlt},
		{Name: "key_insert_cancel", Mode: KeyModeInsert, Value: k.InsertCancel},
		{Name: "key_insert_send", Mode: KeyModeInsert, Value: k.InsertSend},
		{Name: "key_insert_backspace", Mode: KeyModeInsert, Value: k.InsertBackspace},
		{Name: "key_insert_backspace_alt", Mode: KeyModeInsert, Value: k.InsertBackspaceAlt},
		{Name: "key_visual_cancel", Mode: KeyModeVisual, Value: k.VisualCancel},
		{Name: "key_visual_move_down", Mode: KeyModeVisual, Value: k.VisualMoveDown},
		{Name: "key_visual_move_up", Mode: KeyModeVisual, Value: k.VisualMoveUp},
		{Name: "key_visual_yank", Mode: KeyModeVisual, Value: k.VisualYank},
		{Name: "key_command_cancel", Mode: KeyModeCommand, Value: k.CommandCancel},
		{Name: "key_command_run", Mode: KeyModeCommand, Value: k.CommandRun},
		{Name: "key_command_backspace", Mode: KeyModeCommand, Value: k.CommandBackspace},
		{Name: "key_command_backspace_alt", Mode: KeyModeCommand, Value: k.CommandBackspaceAlt},
		{Name: "key_search_cancel", Mode: KeyModeSearch, Value: k.SearchCancel},
		{Name: "key_search_run", Mode: KeyModeSearch, Value: k.SearchRun},
		{Name: "key_search_backspace", Mode: KeyModeSearch, Value: k.SearchBackspace},
		{Name: "key_search_backspace_alt", Mode: KeyModeSearch, Value: k.SearchBackspaceAlt},
	}
}

func SetKeyBinding(k *Keymap, name, value string) error {
	normalized, err := ParseKeyBinding(value)
	if err != nil {
		return err
	}

	switch name {
	case "key_global_quit":
		k.GlobalQuit = normalized
	case "key_help_close":
		k.HelpClose = normalized
	case "key_help_close_alt":
		k.HelpCloseAlt = normalized
	case "key_normal_cancel":
		k.NormalCancel = normalized
	case "key_normal_quit":
		k.NormalQuit = normalized
	case "key_normal_help":
		k.NormalHelp = normalized
	case "key_normal_insert":
		k.NormalInsert = normalized
	case "key_normal_reply":
		k.NormalReply = normalized
	case "key_normal_retry_failed_media":
		k.NormalRetryFailedMedia = normalized
	case "key_normal_visual":
		k.NormalVisual = normalized
	case "key_normal_command":
		k.NormalCommand = normalized
	case "key_normal_search":
		k.NormalSearch = normalized
	case "key_normal_focus_next":
		k.NormalFocusNext = normalized
	case "key_normal_focus_previous":
		k.NormalFocusPrevious = normalized
	case "key_normal_focus_left":
		k.NormalFocusLeft = normalized
	case "key_normal_focus_right_or_reply":
		k.NormalFocusRightOrReply = normalized
	case "key_normal_move_down":
		k.NormalMoveDown = normalized
	case "key_normal_move_up":
		k.NormalMoveUp = normalized
	case "key_normal_go_top":
		k.NormalGoTop = normalized
	case "key_normal_go_bottom":
		k.NormalGoBottom = normalized
	case "key_normal_open":
		k.NormalOpen = normalized
	case "key_normal_open_media":
		k.NormalOpenMedia = normalized
	case "key_normal_search_next":
		k.NormalSearchNext = normalized
	case "key_normal_search_previous":
		k.NormalSearchPrevious = normalized
	case "key_normal_toggle_unread":
		k.NormalToggleUnread = normalized
	case "key_normal_toggle_pinned":
		k.NormalTogglePinned = normalized
	case "key_normal_save_media":
		k.NormalSaveMedia = normalized
	case "key_normal_unload_previews":
		k.NormalUnloadPreviews = normalized
	case "key_insert_attach":
		k.InsertAttach = normalized
	case "key_insert_remove_attachment":
		k.InsertRemoveAttachment = normalized
	case "key_insert_newline":
		k.InsertNewline = normalized
	case "key_insert_newline_alt":
		k.InsertNewlineAlt = normalized
	case "key_insert_cancel":
		k.InsertCancel = normalized
	case "key_insert_send":
		k.InsertSend = normalized
	case "key_insert_backspace":
		k.InsertBackspace = normalized
	case "key_insert_backspace_alt":
		k.InsertBackspaceAlt = normalized
	case "key_visual_cancel":
		k.VisualCancel = normalized
	case "key_visual_move_down":
		k.VisualMoveDown = normalized
	case "key_visual_move_up":
		k.VisualMoveUp = normalized
	case "key_visual_yank":
		k.VisualYank = normalized
	case "key_command_cancel":
		k.CommandCancel = normalized
	case "key_command_run":
		k.CommandRun = normalized
	case "key_command_backspace":
		k.CommandBackspace = normalized
	case "key_command_backspace_alt":
		k.CommandBackspaceAlt = normalized
	case "key_search_cancel":
		k.SearchCancel = normalized
	case "key_search_run":
		k.SearchRun = normalized
	case "key_search_backspace":
		k.SearchBackspace = normalized
	case "key_search_backspace_alt":
		k.SearchBackspaceAlt = normalized
	default:
		return fmt.Errorf("unknown key binding %q", name)
	}

	return nil
}

func ParseKeyBinding(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("must not be empty")
	}

	parts := strings.Fields(value)
	if len(parts) == 0 {
		return "", fmt.Errorf("must not be empty")
	}
	if len(parts) > 1 && !strings.EqualFold(parts[0], "leader") {
		return "", fmt.Errorf("multi-key bindings must start with leader")
	}

	normalized := make([]string, 0, len(parts))
	for i, part := range parts {
		if strings.EqualFold(part, "leader") {
			if i != 0 {
				return "", fmt.Errorf("leader may only appear at the start of a binding")
			}
			if len(parts) == 1 {
				return "", fmt.Errorf("leader binding requires at least one following key")
			}
			normalized = append(normalized, "leader")
			continue
		}
		token, err := ParseKeyToken(part)
		if err != nil {
			return "", err
		}
		normalized = append(normalized, token)
	}

	return strings.Join(normalized, " "), nil
}

func ParseKeyToken(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("key token must not be empty")
	}
	if value == " " || strings.EqualFold(value, "space") {
		return "space", nil
	}
	lower := strings.ToLower(value)
	switch lower {
	case "enter", "esc", "tab", "shift+tab", "backspace":
		return lower, nil
	case "leader":
		return "", fmt.Errorf("leader is only valid in key bindings, not as a key token")
	}
	if strings.HasPrefix(lower, "ctrl+") {
		key := strings.TrimPrefix(lower, "ctrl+")
		if utf8.RuneCountInString(key) == 1 && strings.TrimSpace(key) != "" {
			return "ctrl+" + key, nil
		}
		return "", fmt.Errorf("control key must look like ctrl+x")
	}
	if strings.HasPrefix(lower, "alt+") {
		key := strings.TrimPrefix(lower, "alt+")
		switch key {
		case "enter", "tab", "backspace", "esc":
			return "alt+" + key, nil
		default:
			if utf8.RuneCountInString(key) == 1 && strings.TrimSpace(key) != "" {
				return "alt+" + key, nil
			}
		}
		return "", fmt.Errorf("alt key must look like alt+x or alt+enter")
	}
	if utf8.RuneCountInString(value) == 1 && strings.TrimSpace(value) != "" {
		return value, nil
	}
	return "", fmt.Errorf("unsupported key token %q", value)
}

func ValidateKeymap(cfg Config) error {
	keymap := NormalizeKeymap(cfg.Keymap)
	leader, err := ParseLeaderKey(cfg.LeaderKey)
	if err != nil {
		return fmt.Errorf("leader_key: %w", err)
	}
	if isDigitKey(leader) {
		return fmt.Errorf("leader_key: digits are reserved for normal-mode counts")
	}

	modeBindings := map[string][]KeyBinding{}
	for _, binding := range KeymapBindings(keymap) {
		tokens := strings.Fields(binding.Value)
		if len(tokens) == 0 {
			return fmt.Errorf("%s: must not be empty", binding.Name)
		}
		if tokens[0] == "leader" && binding.Mode != KeyModeNormal {
			return fmt.Errorf("%s: leader sequences are only supported in normal mode", binding.Name)
		}
		if binding.Mode == KeyModeNormal {
			first := tokens[0]
			if first == "leader" {
				first = leader
			}
			if isDigitKey(first) {
				return fmt.Errorf("%s: digits are reserved for normal-mode counts", binding.Name)
			}
		}
		if binding.Mode == KeyModeGlobal {
			for _, mode := range []string{KeyModeHelp, KeyModeNormal, KeyModeInsert, KeyModeVisual, KeyModeCommand, KeyModeSearch} {
				modeBindings[mode] = append(modeBindings[mode], binding)
			}
			continue
		}
		modeBindings[binding.Mode] = append(modeBindings[binding.Mode], binding)
	}

	for mode, bindings := range modeBindings {
		if err := validateModeBindings(mode, bindings, leader); err != nil {
			return err
		}
	}

	return nil
}

func validateModeBindings(mode string, bindings []KeyBinding, leader string) error {
	for i := range bindings {
		left := expandedBindingTokens(bindings[i].Value, leader)
		for j := i + 1; j < len(bindings); j++ {
			right := expandedBindingTokens(bindings[j].Value, leader)
			if sameTokens(left, right) {
				return fmt.Errorf("%s and %s both bind %q in %s mode", bindings[i].Name, bindings[j].Name, displayTokens(left), mode)
			}
			if isPrefixTokens(left, right) || isPrefixTokens(right, left) {
				return fmt.Errorf("%s and %s conflict by prefix in %s mode", bindings[i].Name, bindings[j].Name, mode)
			}
		}
	}
	return nil
}

func expandedBindingTokens(binding, leader string) []string {
	tokens := strings.Fields(binding)
	out := make([]string, len(tokens))
	copy(out, tokens)
	if len(out) > 0 && out[0] == "leader" {
		out[0] = leader
	}
	return out
}

func sameTokens(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func isPrefixTokens(prefix, value []string) bool {
	if len(prefix) >= len(value) {
		return false
	}
	for i := range prefix {
		if prefix[i] != value[i] {
			return false
		}
	}
	return true
}

func displayTokens(tokens []string) string {
	return strings.Join(tokens, " ")
}

func isDigitKey(value string) bool {
	return len(value) == 1 && value[0] >= '0' && value[0] <= '9'
}
