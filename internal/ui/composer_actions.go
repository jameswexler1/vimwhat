package ui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"slices"
	"strings"
	"time"
	"unicode"
	"vimwhat/internal/store"
)

func (m Model) beginReplyToFocusedMessage() (tea.Model, tea.Cmd) {
	message, ok := m.focusedMessage()
	if !ok {
		m.status = "no message selected"
		return m, nil
	}
	return m.beginInsert(&message)
}

func (m Model) startComposeEditor() (tea.Model, tea.Cmd) {
	if len(m.chats) == 0 || m.currentChat().ID == "" {
		m.status = "no chat selected"
		return m, nil
	}
	if m.editTarget != nil {
		m.status = "editor compose unavailable while editing a message"
		return m, nil
	}
	if m.composeInEditor == nil {
		m.status = "editor compose unavailable"
		return m, nil
	}
	chatID := m.currentChat().ID
	initial := m.draftsByChat[chatID]
	if m.mode == ModeInsert {
		initial = m.composer
	}
	m.mode = ModeInsert
	m.focus = FocusMessages
	m.composer = initial
	m.composerSelectAll = false
	m.composerMentions = slices.Clone(m.composerMentionsByChat[chatID])
	m.clearMentionState()
	cmd := m.composeInEditor(chatID, initial)
	if cmd == nil {
		m.status = "editor compose unavailable"
		return m, nil
	}
	m.sendOwnPresence(chatID, false)
	m.terminalOwnerActive = true
	overlayCmd := m.clearOverlayForTerminalOwnerCmd()
	sixelCmd := m.clearSixelForTerminalOwnerCmd()
	m.status = "opening editor"
	return m, sequenceCmds(overlayCmd, sixelCmd, cmd)
}

func (m Model) beginEditFocusedMessage() (tea.Model, tea.Cmd) {
	message, ok := m.focusedMessage()
	if !ok {
		m.status = "no message selected"
		return m, nil
	}
	if err := m.validateEditTarget(message); err != nil {
		m.status = fmt.Sprintf("edit unavailable: %v", err)
		return m, nil
	}
	m.mode = ModeInsert
	m.focus = FocusMessages
	m.composer = message.Body
	m.composerSelectAll = false
	m.attachments = nil
	m.replyTo = nil
	target := message
	m.editTarget = &target
	activateCmd := m.handleCurrentChatActivated()
	m.sendOwnPresence(m.currentChat().ID, true)
	m.status = "editing message"
	return m, batchCmds(activateCmd, ownPresenceIdleCmd(m.currentChat().ID, m.ownPresenceGeneration))
}

func (m Model) handleComposerEdited(msg ComposerEditedMsg) (Model, tea.Cmd) {
	chatID := strings.TrimSpace(msg.ChatID)
	if chatID == "" {
		chatID = m.currentChat().ID
	}
	if msg.Err != nil {
		m.terminalOwnerActive = false
		if chatID == m.currentChat().ID {
			m.mode = ModeInsert
			m.focus = FocusMessages
		}
		m.status = fmt.Sprintf("editor failed: %v", msg.Err)
		return m, nil
	}

	m.terminalOwnerActive = false
	m.clearMentionState()
	delete(m.composerMentionsByChat, chatID)
	if chatID == m.currentChat().ID {
		m.mode = ModeInsert
		m.focus = FocusMessages
		m.composer = msg.Body
		m.composerSelectAll = false
		m.composerMentions = nil
	}
	m.localSetDraft(chatID, msg.Body)
	if strings.TrimSpace(msg.Body) == "" {
		m.status = "draft cleared from editor"
	} else {
		m.status = "draft loaded from editor"
	}
	return m, m.saveDraftCmd(chatID, msg.Body)
}

func (m Model) beginInsert(quote *store.Message) (tea.Model, tea.Cmd) {
	if len(m.chats) == 0 || m.currentChat().ID == "" {
		m.status = "no chat selected"
		return m, nil
	}
	m.mode = ModeInsert
	m.focus = FocusMessages
	m.restoreComposerDraft(m.currentChat().ID)
	if len(m.composerMentions) == 0 {
		m.composerMentions = slices.Clone(m.composerMentionsByChat[m.currentChat().ID])
	}
	m.clearMentionState()
	if quote != nil {
		quoted := *quote
		m.replyTo = &quoted
		m.status = "replying"
	}
	m.editTarget = nil
	activateCmd := m.handleCurrentChatActivated()
	m.sendOwnPresence(m.currentChat().ID, true)
	return m, batchCmds(activateCmd, ownPresenceIdleCmd(m.currentChat().ID, m.ownPresenceGeneration))
}

func (m Model) updateInsert(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.composerVersion++
	keys := m.config.Keymap
	if m.keyMatches(msg, keys.InsertSelectAll) {
		m.clearMentionState()
		m.composerSelectAll = m.composer != ""
		return m, nil
	}
	if m.keyMatches(msg, keys.InsertDelete) {
		// The inline composer currently keeps its caret at the end, so Delete
		// only has work to do when text is selected.
		m.deleteComposerSelection()
		return m, nil
	}
	if m.composerSelectAll && m.keyMatches(msg, keys.InsertCancel) {
		m.composerSelectAll = false
		return m, nil
	}
	if updated, cmd, handled := m.handleMentionKey(msg); handled {
		return updated, cmd
	}
	if m.keyMatches(msg, keys.InsertAttach) {
		if m.editTarget != nil {
			m.status = "attachments cannot be used while editing"
			return m, nil
		}
		return m.startAttachmentPicker()
	}
	if m.keyMatches(msg, keys.InsertPasteImage) {
		if m.editTarget != nil {
			m.status = "attachments cannot be used while editing"
			return m, nil
		}
		return m.startClipboardAttachmentPaste()
	}
	if m.keyMatches(msg, keys.InsertRemoveAttachment) {
		if m.editTarget != nil {
			m.status = "attachments cannot be used while editing"
			return m, nil
		}
		if len(m.attachments) == 0 {
			m.status = "no staged attachments"
			return m, nil
		}
		removed := m.attachments[len(m.attachments)-1]
		m.attachments = m.attachments[:len(m.attachments)-1]
		m.status = fmt.Sprintf("removed attachment: %s", removed.FileName)
		return m, nil
	}
	if m.keyMatches(msg, keys.InsertNewline) || m.keyMatches(msg, keys.InsertNewlineAlt) {
		m.clearMentionState()
		m.appendComposerText("\n")
		m.sendOwnPresence(m.currentChat().ID, true)
		return m, ownPresenceIdleCmd(m.currentChat().ID, m.ownPresenceGeneration)
	}

	switch {
	case m.keyMatches(msg, keys.InsertCancel):
		if m.editTarget != nil {
			chatID := m.currentChat().ID
			m.composer = ""
			m.composerSelectAll = false
			m.composerMentions = nil
			m.clearMentionState()
			m.attachments = nil
			m.replyTo = nil
			m.editTarget = nil
			m.sendOwnPresence(chatID, false)
			m.mode = ModeNormal
			m.status = "edit cancelled"
			return m, nil
		}
		m.sendOwnPresence(m.currentChat().ID, false)
		m.clearMentionState()
		m.mode = ModeNormal
		return m, m.persistCurrentDraft()
	case m.keyMatches(msg, keys.InsertSend):
		if m.editTarget != nil {
			return m.submitEditedMessage()
		}
		body := strings.TrimSpace(m.composer)
		if body == "" && len(m.attachments) == 0 {
			m.status = "empty message"
			m.composer = ""
			m.composerSelectAll = false
			return m, nil
		}
		chatID := m.currentChat().ID
		if chatID == "" {
			m.status = "no active chat"
			m.mode = ModeNormal
			return m, nil
		}
		if err := m.validateAttachmentsForSend(body); err != nil {
			m.status = err.Error()
			return m, nil
		}
		if len(m.attachments) > 0 && m.blockAttachments {
			m.status = "attachment send is not implemented yet"
			m.localSetDraft(chatID, m.composer)
			return m, m.saveDraftCmd(chatID, m.composer)
		}
		if m.requireOnlineForSend && !m.whatsAppReady() {
			m.status = "sending needs WhatsApp online and ready"
			m.localSetDraft(chatID, m.composer)
			return m, m.saveDraftCmd(chatID, m.composer)
		}
		if m.blockSending {
			m.status = "sending is not implemented yet"
			m.localSetDraft(chatID, m.composer)
			return m, m.saveDraftCmd(chatID, m.composer)
		}

		message := store.Message{
			ID:         fmt.Sprintf("local-%d", time.Now().UnixNano()),
			ChatID:     chatID,
			Sender:     "me",
			Body:       body,
			Timestamp:  time.Now(),
			IsOutgoing: true,
			Mentions:   m.mentionsForSend(body),
		}
		if m.replyTo != nil {
			message.QuotedMessageID = m.replyTo.ID
			message.QuotedRemoteID = m.replyTo.RemoteID
		}
		message.Media = m.mediaForLocalMessage(message.ID, m.attachments)
		attachments := slices.Clone(m.attachments)
		draftBody := m.composer
		var persistCmd tea.Cmd
		if m.persistMessage != nil {
			request := OutgoingMessage{
				ChatID:      chatID,
				Body:        body,
				Attachments: slices.Clone(attachments),
				Mentions:    slices.Clone(message.Mentions),
			}
			if m.replyTo != nil {
				quote := *m.replyTo
				request.Quote = &quote
			}
			m.outgoingMessageInflight[message.ID] = true
			persistCmd = m.persistOutgoingMessageCmd(message.ID, chatID, draftBody, attachments, request)
		}
		if len(message.Media) == 0 && len(m.attachments) > 0 {
			message.Media = m.mediaForLocalMessage(message.ID, m.attachments)
		}
		m.appendMessageToChat(chatID, message)
		m.messageCursor = len(m.messagesByChat[chatID]) - 1
		m.messageScrollTop = m.messageCursor
		m.composer = ""
		m.composerSelectAll = false
		m.composerMentions = nil
		delete(m.composerMentionsByChat, chatID)
		m.clearMentionState()
		m.attachments = nil
		m.replyTo = nil
		m.sendOwnPresence(chatID, false)
		m.localSetDraft(chatID, "")
		m.mode = ModeInsert
		m.focus = FocusMessages
		m.status = "message queued"
		if persistCmd != nil {
			return m, persistCmd
		}
		return m, m.saveDraftCmd(chatID, "")
	case m.keyMatches(msg, keys.InsertBackspace) || m.keyMatches(msg, keys.InsertBackspaceAlt):
		m.backspaceComposer()
	default:
		if text := keyText(msg); text != "" {
			mentionCmd := m.appendComposerText(text)
			m.sendOwnPresence(m.currentChat().ID, true)
			return m, batchCmds(mentionCmd, ownPresenceIdleCmd(m.currentChat().ID, m.ownPresenceGeneration))
		}
	}

	return m, nil
}

const mentionCandidateLimit = 8

func (m Model) handleMentionKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if !m.mentionActive {
		return m, nil, false
	}
	keys := m.config.Keymap
	switch {
	case m.keyMatches(msg, keys.InsertCancel):
		m.clearMentionState()
		m.status = "mention cancelled"
		return m, nil, true
	case m.keyMatches(msg, keys.InsertSend) || m.keyMatches(msg, keys.InsertMentionSelectAlt):
		if len(m.mentionCandidates) == 0 {
			m.clearMentionState()
			m.status = "no mention match"
			return m, nil, true
		}
		m.completeMention()
		m.sendOwnPresence(m.currentChat().ID, true)
		return m, ownPresenceIdleCmd(m.currentChat().ID, m.ownPresenceGeneration), true
	case m.keyMatches(msg, keys.InsertMentionMoveDown):
		m.moveMentionCursor(1)
		return m, nil, true
	case m.keyMatches(msg, keys.InsertMentionMoveUp):
		m.moveMentionCursor(-1)
		return m, nil, true
	case m.keyMatches(msg, keys.InsertBackspace) || m.keyMatches(msg, keys.InsertBackspaceAlt):
		m.backspaceComposer()
		mentionCmd := m.updateActiveMentionFromComposer()
		m.sendOwnPresence(m.currentChat().ID, true)
		return m, batchCmds(mentionCmd, ownPresenceIdleCmd(m.currentChat().ID, m.ownPresenceGeneration)), true
	default:
		if text := keyText(msg); text != "" {
			mentionCmd := m.appendComposerText(text)
			mentionCmd = batchCmds(mentionCmd, m.updateActiveMentionFromComposer())
			m.sendOwnPresence(m.currentChat().ID, true)
			return m, batchCmds(mentionCmd, ownPresenceIdleCmd(m.currentChat().ID, m.ownPresenceGeneration)), true
		}
		return m, nil, true
	}
}

func (m *Model) appendComposerText(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	m.deleteComposerSelection()
	start := len(m.composer)
	m.composer += text
	if text == "@" && m.canStartMention() {
		return m.startMention(start)
	}
	m.pruneComposerMentions()
	return nil
}

func keyText(msg tea.KeyMsg) string {
	switch msg.Type {
	case tea.KeyRunes:
		return string(msg.Runes)
	case tea.KeySpace:
		return " "
	default:
		return ""
	}
}

func (m *Model) backspaceComposer() {
	if m.deleteComposerSelection() {
		return
	}
	m.composer = trimLastCluster(m.composer)
	m.pruneComposerMentions()
}

// Selection is transient and applies only to text, never attachments or replies.
func (m *Model) deleteComposerSelection() bool {
	if !m.composerSelectAll {
		return false
	}
	m.composerSelectAll = false
	m.composer = ""
	m.composerMentions = nil
	m.clearMentionState()
	return true
}

func (m Model) canStartMention() bool {
	if m.editTarget != nil || m.searchMentionCandidates == nil {
		return false
	}
	chat := m.currentChat()
	return strings.TrimSpace(chat.ID) != "" && strings.EqualFold(strings.TrimSpace(chat.Kind), "group")
}

func (m *Model) startMention(start int) tea.Cmd {
	if start < 0 || start >= len(m.composer) {
		return nil
	}
	m.mentionActive = true
	m.mentionStart = start
	m.mentionQuery = ""
	m.mentionCursor = 0
	return m.refreshMentionCandidates()
}

func (m *Model) clearMentionState() {
	m.mentionActive = false
	m.mentionStart = 0
	m.mentionQuery = ""
	m.mentionCandidates = nil
	m.mentionCursor = 0
}

func (m *Model) updateActiveMentionFromComposer() tea.Cmd {
	if !m.mentionActive {
		return nil
	}
	if m.mentionStart < 0 || m.mentionStart >= len(m.composer) || m.composer[m.mentionStart] != '@' {
		m.clearMentionState()
		return nil
	}
	query := m.composer[m.mentionStart+1:]
	if strings.ContainsAny(query, "\n\r\t") {
		m.clearMentionState()
		return nil
	}
	m.mentionQuery = query
	m.mentionCursor = 0
	return m.refreshMentionCandidates()
}

func (m *Model) refreshMentionCandidates() tea.Cmd {
	m.mentionCandidates = nil
	if m.searchMentionCandidates == nil {
		return nil
	}
	chatID := m.currentChat().ID
	if strings.TrimSpace(chatID) == "" {
		return nil
	}
	m.mentionSearchGeneration++
	return m.mentionCandidatesCmd(m.mentionSearchGeneration, chatID, m.mentionQuery)
}

func (m *Model) moveMentionCursor(delta int) {
	if len(m.mentionCandidates) == 0 {
		m.mentionCursor = 0
		return
	}
	m.mentionCursor = clamp(m.mentionCursor+delta, 0, len(m.mentionCandidates)-1)
}

func (m *Model) completeMention() {
	if len(m.mentionCandidates) == 0 {
		return
	}
	if m.mentionStart < 0 || m.mentionStart > len(m.composer) {
		m.clearMentionState()
		return
	}
	candidate := m.mentionCandidates[clamp(m.mentionCursor, 0, len(m.mentionCandidates)-1)]
	display := mentionDisplayName(candidate)
	if display == "" {
		m.clearMentionState()
		return
	}
	prefix := m.composer[:m.mentionStart]
	text := mentionText(display)
	start := len(prefix)
	end := start + len(text)
	m.composer = prefix + text + " "
	m.composerMentions = append(m.validComposerMentions(), store.MessageMention{
		JID:         strings.TrimSpace(candidate.JID),
		DisplayName: display,
		StartByte:   start,
		EndByte:     end,
		UpdatedAt:   time.Now(),
	})
	m.clearMentionState()
}

func mentionDisplayName(candidate store.MentionCandidate) string {
	display := strings.Join(strings.Fields(candidate.DisplayName), " ")
	if display != "" {
		return display
	}
	return strings.TrimSpace(candidate.JID)
}

func mentionText(display string) string {
	return "@" + strings.TrimSpace(display)
}

func (m *Model) pruneComposerMentions() {
	m.composerMentions = m.validComposerMentions()
}

func (m Model) validComposerMentions() []store.MessageMention {
	if len(m.composerMentions) == 0 {
		return nil
	}
	out := make([]store.MessageMention, 0, len(m.composerMentions))
	for _, mention := range m.composerMentions {
		if !m.mentionStillPresent(mention) {
			continue
		}
		out = append(out, mention)
	}
	return out
}

func (m Model) mentionStillPresent(mention store.MessageMention) bool {
	if mention.StartByte < 0 || mention.EndByte <= mention.StartByte || mention.EndByte > len(m.composer) {
		return false
	}
	return m.composer[mention.StartByte:mention.EndByte] == mentionText(mention.DisplayName)
}

func (m Model) mentionsForSend(body string) []store.MessageMention {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	raw := m.composer
	leftTrimmed := strings.TrimLeftFunc(raw, unicode.IsSpace)
	offset := len(raw) - len(leftTrimmed)
	bodyEnd := offset + len(body)
	var mentions []store.MessageMention
	for _, mention := range m.validComposerMentions() {
		if mention.EndByte <= offset || mention.StartByte >= bodyEnd {
			continue
		}
		jid := strings.TrimSpace(mention.JID)
		if jid == "" {
			continue
		}
		mention.StartByte = max(0, mention.StartByte-offset)
		mention.EndByte = min(len(body), mention.EndByte-offset)
		mentions = append(mentions, mention)
	}
	return mentions
}
