package ui

import (
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
