package ui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"slices"
	"strings"
	"vimwhat/internal/store"
)

func (m Model) loadMessagesCmd(chatID string, limit int, showLatest, activate bool) tea.Cmd {
	loadMessages := m.loadMessages
	if loadMessages == nil || strings.TrimSpace(chatID) == "" {
		return nil
	}
	return func() tea.Msg {
		messages, err := loadMessages(chatID, limit)
		return messagesLoadedMsg{ChatID: chatID, Messages: messages, ShowLatest: showLatest, Activate: activate, Err: err}
	}
}

func (m Model) loadOlderMessagesCmd(chatID string, before store.Message, limit int) tea.Cmd {
	loadOlder := m.loadOlderMessages
	if loadOlder == nil || strings.TrimSpace(chatID) == "" {
		return nil
	}
	anchorID := before.ID
	return func() tea.Msg {
		messages, err := loadOlder(chatID, before, limit)
		return olderMessagesLoadedMsg{ChatID: chatID, AnchorID: anchorID, Messages: messages, Err: err}
	}
}

func (m Model) requestHistoryCmd(chatID, contextLabel string) tea.Cmd {
	requestHistory := m.requestHistory
	if requestHistory == nil || strings.TrimSpace(chatID) == "" {
		return nil
	}
	return func() tea.Msg {
		return historyRequestedMsg{ChatID: chatID, Context: contextLabel, Err: requestHistory(chatID)}
	}
}

func (m Model) handleMessagesLoaded(msg messagesLoadedMsg) (Model, tea.Cmd) {
	delete(m.messageLoadInflight, msg.ChatID)
	if msg.Err != nil {
		m.status = fmt.Sprintf("load messages failed: %v", msg.Err)
		return m, nil
	}
	m.messagesByChat[msg.ChatID] = slices.Clone(msg.Messages)
	m.historyDetached[msg.ChatID] = false
	if msg.ChatID == m.currentChat().ID {
		m.messageCursor = clamp(m.messageCursor, 0, max(0, len(msg.Messages)-1))
		m.messageScrollTop = clamp(m.messageScrollTop, 0, max(0, len(msg.Messages)-1))
		if msg.ShowLatest {
			m.showCurrentChatLatest()
		}
		if msg.Activate {
			m.focus = FocusMessages
			return m, m.handleCurrentChatActivated()
		}
	}
	return m, nil
}

func (m Model) handleOlderMessagesLoaded(msg olderMessagesLoadedMsg) (Model, tea.Cmd) {
	delete(m.olderMessagesInflight, msg.ChatID)
	if msg.Err != nil {
		m.status = fmt.Sprintf("load older messages failed: %v", msg.Err)
		return m, nil
	}
	messages := m.messagesByChat[msg.ChatID]
	if len(messages) > 0 && messages[0].ID != msg.AnchorID {
		m.status = "older load ignored; message list changed"
		return m, nil
	}
	if len(msg.Messages) > 0 {
		combined := make([]store.Message, 0, len(msg.Messages)+len(messages))
		combined = append(combined, msg.Messages...)
		combined = append(combined, messages...)
		m.messagesByChat[msg.ChatID] = slices.Clone(combined[:min(len(combined), maxHistoryWindow)])
		m.historyDetached[msg.ChatID] = true
		m.addMessageLimit(msg.ChatID, len(msg.Messages))
		if msg.ChatID == m.currentChat().ID {
			m.messageCursor = len(msg.Messages) - 1
			m.messageScrollTop = m.messageCursor
			m.pauseOverlays(true, false)
		}
		m.status = fmt.Sprintf("loaded %d older local message(s)", len(msg.Messages))
		if target := m.pendingQuoteByChat[msg.ChatID]; target != "" && msg.ChatID == m.currentChat().ID && m.focusMessageByID(target) {
			delete(m.pendingQuoteByChat, msg.ChatID)
			m.status = "jumped to quote"
		}
		return m, nil
	}
	return m.startHistoryRequest(msg.ChatID, "history")
}

func (m Model) handleHistoryRequested(msg historyRequestedMsg) Model {
	delete(m.historyRequestInflight, msg.ChatID)
	if msg.Err != nil {
		switch msg.Context {
		case "quote":
			m.status = fmt.Sprintf("quote not loaded; history request failed: %v", msg.Err)
		default:
			m.status = fmt.Sprintf("history request failed: %v", msg.Err)
		}
		return m
	}
	if m.historyRequestedByChat == nil {
		m.historyRequestedByChat = map[string]bool{}
	}
	m.historyRequestedByChat[msg.ChatID] = true
	if msg.Context == "quote" {
		m.status = "quote not loaded; requested older history"
	} else {
		m.status = "requested older history"
	}
	return m
}

func (m *Model) ensureCurrentMessagesLoaded(showLatest, activate bool) tea.Cmd {
	m.reportActiveChatChanged()
	chatID := m.currentChat().ID
	if chatID == "" {
		return nil
	}
	if _, ok := m.messagesByChat[chatID]; ok {
		var activateCmd tea.Cmd
		if showLatest {
			m.showCurrentChatLatest()
		}
		if activate {
			m.focus = FocusMessages
			activateCmd = m.handleCurrentChatActivated()
		}
		return activateCmd
	}
	if m.loadMessages == nil {
		m.messagesByChat[chatID] = nil
		return nil
	}
	if m.messageLoadInflight[chatID] {
		m.status = "messages loading"
		return nil
	}
	m.messageLoadInflight[chatID] = true
	m.status = "loading messages"
	return m.loadMessagesCmd(chatID, m.messageLimitForChat(chatID), showLatest, activate)
}

func (m *Model) reloadCurrentMessages() tea.Cmd {
	chatID := m.currentChat().ID
	if chatID == "" {
		return nil
	}
	if m.loadMessages == nil {
		return nil
	}
	if m.messageLoadInflight[chatID] {
		m.status = "messages loading"
		return nil
	}
	m.messageLoadInflight[chatID] = true
	m.status = "loading messages"
	return m.loadMessagesCmd(chatID, m.messageLimitForChat(chatID), false, false)
}

func (m *Model) loadOlderOrRequestHistory() tea.Cmd {
	chatID := m.currentChat().ID
	if chatID == "" {
		m.status = "no active chat"
		return nil
	}
	if strings.TrimSpace(m.messageFilter) != "" {
		m.status = "clear message filter to load older history"
		return nil
	}
	messages := m.currentMessages()
	if len(messages) == 0 {
		m.status = "history fetch needs a local message anchor"
		return nil
	}

	if m.loadOlderMessages != nil {
		if m.olderMessagesInflight[chatID] {
			m.status = "older messages loading"
			return nil
		}
		m.olderMessagesInflight[chatID] = true
		m.status = "loading older messages"
		return m.loadOlderMessagesCmd(chatID, messages[0], historyPageSize)
	}

	next, cmd := m.startHistoryRequest(chatID, "history")
	*m = next
	return cmd
}

func (m Model) startHistoryRequest(chatID, contextLabel string) (Model, tea.Cmd) {
	if !m.whatsAppReady() {
		m.status = "no older local messages; WhatsApp is not online"
		return m, nil
	}
	if m.requestHistory == nil {
		m.status = "remote history fetch unavailable"
		return m, nil
	}
	if m.historyRequestedByChat != nil && m.historyRequestedByChat[chatID] {
		m.status = "history already loading"
		return m, nil
	}
	if m.historyRequestInflight[chatID] {
		m.status = "history already loading"
		return m, nil
	}
	m.historyRequestInflight[chatID] = true
	m.status = "requested older history"
	return m, m.requestHistoryCmd(chatID, contextLabel)
}

func (m *Model) jumpToQuotedMessage() tea.Cmd {
	message, ok := m.focusedMessage()
	if !ok {
		m.status = "no message selected"
		return nil
	}
	targetID := strings.TrimSpace(message.QuotedMessageID)
	if targetID == "" {
		if strings.TrimSpace(message.QuotedRemoteID) == "" {
			m.status = "focused message is not a reply"
			return nil
		}
		targetID = message.ChatID + "/" + strings.TrimSpace(message.QuotedRemoteID)
	}
	if strings.TrimSpace(m.messageFilter) != "" {
		m.status = "clear message filter before quote jump"
		return nil
	}
	chatID := m.currentChat().ID
	if chatID == "" {
		m.status = "no active chat"
		return nil
	}
	if m.focusMessageByID(targetID) {
		m.status = "jumped to quote"
		return nil
	}
	m.pendingQuoteByChat[chatID] = targetID
	if m.loadMessagesAround != nil {
		m.status = "loading quoted history"
		return m.quoteWindowCmd(chatID, targetID)
	}
	messages := m.currentMessages()
	if len(messages) > 0 && m.loadOlderMessages != nil {
		if m.olderMessagesInflight[chatID] {
			m.status = "quote history already loading"
			return nil
		}
		m.olderMessagesInflight[chatID] = true
		m.status = "loading quoted history"
		return m.loadOlderMessagesCmd(chatID, messages[0], historyPageSize)
	}
	if m.whatsAppReady() && m.requestHistory != nil {
		next, cmd := m.startHistoryRequest(chatID, "quote")
		*m = next
		if cmd != nil {
			m.status = "quote not loaded; requested older history"
		}
		return cmd
	}
	m.status = "quoted message is not loaded"
	return nil
}

func (m *Model) focusMessageByID(messageID string) bool {
	for i, message := range m.currentMessages() {
		if message.ID != messageID {
			continue
		}
		m.messageCursor = i
		m.messageScrollTop = i
		m.focus = FocusMessages
		m.pauseOverlays(true, false)
		return true
	}
	return false
}
