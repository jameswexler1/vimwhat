package ui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"vimwhat/internal/config"
)

func keyTokenFromMsg(msg tea.KeyMsg) string {
	if msg.Type == tea.KeySpace {
		return "space"
	}
	raw := msg.String()
	if raw == " " {
		return "space"
	}
	token, err := config.ParseKeyToken(raw)
	if err != nil {
		return raw
	}
	return token
}

func (m Model) keyMatches(msg tea.KeyMsg, binding string) bool {
	tokens := strings.Fields(binding)
	if len(tokens) != 1 {
		return false
	}
	return keyTokenFromMsg(msg) == tokens[0]
}

func (m Model) keyTokenMatches(token, binding string) bool {
	tokens := strings.Fields(binding)
	if len(tokens) != 1 {
		return false
	}
	return token == tokens[0]
}

func shiftedEnterTokenFromMsg(msg tea.Msg) (string, bool) {
	stringer, ok := msg.(interface{ String() string })
	if !ok {
		return "", false
	}
	seq, ok := unknownCSISequenceFromString(stringer.String())
	if !ok {
		return "", false
	}
	switch seq {
	case "13;2u", "13;2~", "27;2;13~":
		return "shift+enter", true
	default:
		return "", false
	}
}

func unknownCSISequenceFromString(value string) (string, bool) {
	if strings.HasPrefix(value, "\x1b[") {
		return strings.TrimPrefix(value, "\x1b["), true
	}
	const prefix = "?CSI["
	const suffix = "]?"
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) {
		return "", false
	}
	fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(value, prefix), suffix))
	if len(fields) == 0 {
		return "", false
	}
	var out strings.Builder
	for _, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil || value < 0 || value > 255 {
			return "", false
		}
		out.WriteByte(byte(value))
	}
	return out.String(), true
}

func (m Model) keyMatchesLeaderStart(msg tea.KeyMsg) bool {
	if !m.hasNormalLeaderBindings() {
		return false
	}
	leader := strings.TrimSpace(m.config.LeaderKey)
	if leader == "" {
		leader = "space"
	}
	return keyTokenFromMsg(msg) == leader
}

func (m Model) hasNormalLeaderBindings() bool {
	for _, binding := range config.KeymapBindings(m.config.Keymap) {
		if binding.Mode != config.KeyModeNormal {
			continue
		}
		if bindingStartsWithLeader(binding.Value) {
			return true
		}
	}
	return false
}

func bindingStartsWithLeader(binding string) bool {
	tokens := strings.Fields(binding)
	return len(tokens) > 1 && tokens[0] == "leader"
}

func leaderBindingMatch(binding string, sequence []string) (exact bool, prefix bool) {
	tokens := strings.Fields(binding)
	if len(tokens) < 2 || tokens[0] != "leader" {
		return false, false
	}
	tail := tokens[1:]
	if len(sequence) > len(tail) {
		return false, false
	}
	for i := range sequence {
		if sequence[i] != tail[i] {
			return false, false
		}
	}
	if len(sequence) == len(tail) {
		return true, false
	}
	return false, true
}

func displayBinding(binding, leader string) string {
	tokens := strings.Fields(binding)
	if len(tokens) == 0 {
		return ""
	}
	if tokens[0] == "leader" {
		display := "<leader>"
		if strings.TrimSpace(leader) != "" && !strings.EqualFold(strings.TrimSpace(leader), "space") {
			display = leaderDisplay(leader)
		}
		return display + strings.Join(tokens[1:], "")
	}
	return strings.Join(tokens, " ")
}

func displayBindings(leader string, bindings ...string) string {
	var labels []string
	for _, binding := range bindings {
		label := displayBinding(binding, leader)
		if label != "" {
			labels = append(labels, label)
		}
	}
	return strings.Join(labels, "/")
}
