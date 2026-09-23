package ui

import (
	"fmt"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"vimwhat/internal/store"
)

const maxHistoryWindow = 400
const maxCachedChats = 8

type quoteWindowLoadedMsg struct {
	ChatID, TargetID string
	Messages         []store.Message
	Err              error
}
type newerWindowLoadedMsg struct {
	ChatID, AnchorID string
	Messages         []store.Message
	Err              error
}

func (m Model) quoteWindowCmd(chatID, targetID string) tea.Cmd {
	load := m.loadMessagesAround
	return func() tea.Msg {
		messages, err := load(chatID, targetID, messageLoadLimit)
		return quoteWindowLoadedMsg{chatID, targetID, messages, err}
	}
}

func (m Model) handleQuoteWindow(msg quoteWindowLoadedMsg) (Model, tea.Cmd) {
	if m.pendingQuoteByChat[msg.ChatID] != msg.TargetID {
		return m, nil
	}
	if msg.Err != nil {
		m.status = fmt.Sprintf("quote load failed: %v", msg.Err)
		return m, nil
	}
	if len(msg.Messages) == 0 {
		return m.startHistoryRequest(msg.ChatID, "quote")
	}
	m.messagesByChat[msg.ChatID] = slices.Clone(msg.Messages)
	m.historyDetached[msg.ChatID] = true
	if msg.ChatID == m.currentChat().ID && m.focusMessageByID(msg.TargetID) {
		delete(m.pendingQuoteByChat, msg.ChatID)
		m.status = "jumped to quote"
	}
	return m, nil
}

func (m *Model) loadNewerWindow() tea.Cmd {
	id := m.currentChat().ID
	messages := m.currentMessages()
	if m.loadNewerMessages == nil || len(messages) == 0 || m.messageLoadInflight[id] {
		return nil
	}
	anchor := messages[len(messages)-1]
	load := m.loadNewerMessages
	m.messageLoadInflight[id] = true
	return func() tea.Msg {
		messages, err := load(id, anchor, historyPageSize)
		return newerWindowLoadedMsg{id, anchor.ID, messages, err}
	}
}

func (m Model) handleNewerWindow(msg newerWindowLoadedMsg) Model {
	delete(m.messageLoadInflight, msg.ChatID)
	if msg.Err != nil {
		m.status = fmt.Sprintf("load newer messages failed: %v", msg.Err)
		return m
	}
	messages := m.messagesByChat[msg.ChatID]
	if len(messages) == 0 || messages[len(messages)-1].ID != msg.AnchorID {
		return m
	}
	if len(msg.Messages) == 0 {
		m.historyDetached[msg.ChatID] = false
		return m
	}
	combined := append(slices.Clone(messages), msg.Messages...)
	drop := max(0, len(combined)-maxHistoryWindow)
	m.messagesByChat[msg.ChatID] = slices.Clone(combined[drop:])
	if msg.ChatID == m.currentChat().ID {
		m.messageCursor = max(0, len(messages)-drop)
		m.messageScrollTop = m.messageCursor
	}
	return m
}

func (m *Model) boundHistoryCache() {
	active := m.currentChat().ID
	m.historyClock++
	m.historyLastUsed[active] = m.historyClock
	for id, messages := range m.messagesByChat {
		if len(messages) > maxHistoryWindow {
			if m.historyDetached[id] {
				m.messagesByChat[id] = slices.Clone(messages[:maxHistoryWindow])
			} else {
				drop := len(messages) - maxHistoryWindow
				m.messagesByChat[id] = slices.Clone(messages[drop:])
				if id == active {
					m.messageCursor = max(0, m.messageCursor-drop)
					m.messageScrollTop = max(0, m.messageScrollTop-drop)
				}
			}
		}
	}
	for len(m.messagesByChat) > maxCachedChats {
		oldest := ""
		for id := range m.messagesByChat {
			if id != active && (oldest == "" || m.historyLastUsed[id] < m.historyLastUsed[oldest]) {
				oldest = id
			}
		}
		if oldest == "" {
			break
		}
		delete(m.messagesByChat, oldest)
		delete(m.unfilteredByChat, oldest)
		delete(m.messageLimitsByChat, oldest)
		delete(m.historyLastUsed, oldest)
		delete(m.historyDetached, oldest)
	}
	for id, messages := range m.unfilteredByChat {
		if len(messages) > maxHistoryWindow {
			m.unfilteredByChat[id] = slices.Clone(messages[len(messages)-maxHistoryWindow:])
		}
	}
}
