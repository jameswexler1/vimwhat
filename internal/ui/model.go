package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"maybewhats/internal/config"
	"maybewhats/internal/media"
	"maybewhats/internal/store"
)

type Mode string

const (
	ModeNormal  Mode = "normal"
	ModeInsert  Mode = "insert"
	ModeVisual  Mode = "visual"
	ModeCommand Mode = "command"
	ModeSearch  Mode = "search"
)

type Focus string

const (
	FocusChats    Focus = "chats"
	FocusMessages Focus = "messages"
	FocusPreview  Focus = "preview"
)

type Options struct {
	Paths          config.Paths
	Config         config.Config
	PreviewReport  media.Report
	Snapshot       store.Snapshot
	PersistMessage func(chatID, body string) (store.Message, error)
	LoadMessages   func(chatID string, limit int) ([]store.Message, error)
	SaveDraft      func(chatID, body string) error
	SearchChats    func(query string) ([]store.Chat, error)
	SearchMessages func(chatID, query string, limit int) ([]store.Message, error)
}

type Model struct {
	width            int
	height           int
	mode             Mode
	focus            Focus
	allChats         []store.Chat
	chats            []store.Chat
	messagesByChat   map[string][]store.Message
	draftsByChat     map[string]string
	activeChat       int
	chatScrollTop    int
	messageCursor    int
	messageScrollTop int
	visualAnchor     int
	previewReport    media.Report
	paths            config.Paths
	config           config.Config
	status           string
	commandLine      string
	searchLine       string
	composer         string
	lastSearch       string
	lastSearchFocus  Focus
	activeSearch     string
	searchChatSource []store.Chat
	searchMatches    []int
	searchIndex      int
	messageFilter    string
	unfilteredByChat map[string][]store.Message
	pendingCount     int
	yankRegister     string
	compactLayout    bool
	infoPaneVisible  bool
	helpVisible      bool
	unreadOnly       bool
	pinnedFirst      bool
	commandHistory   []string
	searchHistory    []string
	persistMessage   func(chatID, body string) (store.Message, error)
	loadMessages     func(chatID string, limit int) ([]store.Message, error)
	saveDraft        func(chatID, body string) error
	searchChats      func(query string) ([]store.Chat, error)
	searchMessages   func(chatID, query string, limit int) ([]store.Message, error)
}

const messageLoadLimit = 200

func Run(opts Options) error {
	p := tea.NewProgram(NewModel(opts), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func NewModel(opts Options) Model {
	chats := slices.Clone(opts.Snapshot.Chats)
	activeChat := 0

	for i, chat := range chats {
		if chat.ID == opts.Snapshot.ActiveChatID {
			activeChat = i
			break
		}
	}

	return Model{
		mode:             ModeNormal,
		focus:            FocusChats,
		allChats:         slices.Clone(chats),
		chats:            chats,
		messagesByChat:   cloneMessages(opts.Snapshot.MessagesByChat),
		draftsByChat:     cloneDrafts(opts.Snapshot.DraftsByChat),
		activeChat:       activeChat,
		previewReport:    opts.PreviewReport,
		paths:            opts.Paths,
		config:           opts.Config,
		status:           "ready",
		pinnedFirst:      true,
		persistMessage:   opts.PersistMessage,
		loadMessages:     opts.LoadMessages,
		saveDraft:        opts.SaveDraft,
		searchChats:      opts.SearchChats,
		searchMessages:   opts.SearchMessages,
		unfilteredByChat: map[string][]store.Message{},
	}
}

func cloneMessages(input map[string][]store.Message) map[string][]store.Message {
	out := make(map[string][]store.Message, len(input))
	for key, messages := range input {
		out[key] = slices.Clone(messages)
	}
	return out
}

func cloneDrafts(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, draft := range input {
		out[key] = draft
	}
	return out
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.compactLayout = msg.Width < 110
		if (!m.infoPaneVisible || m.compactLayout) && m.focus == FocusPreview {
			m.focus = FocusMessages
		}
		m.keepActiveChatVisible()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}

	switch m.mode {
	case ModeInsert:
		return m.updateInsert(msg)
	case ModeCommand:
		return m.updateCommand(msg)
	case ModeSearch:
		return m.updateSearch(msg)
	case ModeVisual:
		return m.updateVisual(msg)
	default:
		return m.updateNormal(msg)
	}
}

func (m Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.helpVisible {
		switch msg.Type {
		case tea.KeyEsc:
			m.helpVisible = false
			m.status = "help closed"
			return m, nil
		}
		if msg.String() == "?" {
			m.helpVisible = false
			m.status = "help closed"
			return m, nil
		}
		return m, nil
	}

	if m.captureCount(msg) {
		return m, nil
	}
	count := m.consumeCount()

	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "?":
		m.helpVisible = true
		m.status = "help"
	case "i":
		if len(m.chats) == 0 {
			m.status = "no chat selected"
			return m, nil
		}
		m.mode = ModeInsert
		m.focus = FocusMessages
		m.composer = m.draftsByChat[m.currentChat().ID]
		m.status = "insert mode"
	case "v":
		if len(m.currentMessages()) == 0 {
			m.status = "no messages to select"
			return m, nil
		}
		m.mode = ModeVisual
		m.focus = FocusMessages
		m.visualAnchor = m.messageCursor
		m.status = "visual mode"
	case ":":
		m.mode = ModeCommand
		m.commandLine = ""
		m.status = "command mode"
	case "/":
		m.mode = ModeSearch
		m.searchLine = ""
		m.status = "search mode"
	case "tab":
		m.cycleFocus(1)
	case "shift+tab":
		m.cycleFocus(-1)
	case "h":
		m.moveFocus(-1)
	case "l":
		m.moveFocus(1)
	case "j":
		m.moveCursor(count)
	case "k":
		m.moveCursor(-count)
	case "g":
		if m.focus == FocusMessages {
			m.messageCursor = 0
			m.messageScrollTop = 0
		} else {
			m.activeChat = 0
			m.chatScrollTop = 0
			if err := m.ensureCurrentMessagesLoaded(); err != nil {
				m.status = fmt.Sprintf("load messages failed: %v", err)
				return m, nil
			}
			m.messageCursor = 0
			m.messageScrollTop = 0
		}
	case "G":
		if m.focus == FocusMessages {
			if messageCount := len(m.currentMessages()); messageCount > 0 {
				target := messageCount - 1
				if count > 1 {
					target = count - 1
				}
				m.messageCursor = clamp(target, 0, messageCount-1)
				m.messageScrollTop = m.messageCursor
			}
		} else {
			if chatCount := len(m.chats); chatCount > 0 {
				target := chatCount - 1
				if count > 1 {
					target = count - 1
				}
				m.activeChat = clamp(target, 0, chatCount-1)
				m.keepActiveChatVisible()
				if err := m.ensureCurrentMessagesLoaded(); err != nil {
					m.status = fmt.Sprintf("load messages failed: %v", err)
					return m, nil
				}
				m.messageCursor = 0
				m.messageScrollTop = 0
			}
		}
	case "enter":
		if m.focus == FocusChats {
			if len(m.chats) == 0 {
				m.status = "no chat selected"
				return m, nil
			}
			m.focus = FocusMessages
			if err := m.ensureCurrentMessagesLoaded(); err != nil {
				m.status = fmt.Sprintf("load messages failed: %v", err)
				return m, nil
			}
			m.messageCursor = max(0, len(m.currentMessages())-1)
			m.messageScrollTop = m.messageCursor
			m.status = fmt.Sprintf("opened %s", m.currentChat().Title)
		}
	case "n":
		m.advanceSearch(1)
	case "N":
		m.advanceSearch(-1)
	case "u":
		if err := m.setUnreadOnly(!m.unreadOnly); err != nil {
			m.status = fmt.Sprintf("filter failed: %v", err)
			return m, nil
		}
	case "p":
		if err := m.setPinnedFirst(!m.pinnedFirst); err != nil {
			m.status = fmt.Sprintf("sort failed: %v", err)
			return m, nil
		}
	default:
		return m, nil
	}

	return m, nil
}

func (m Model) updateInsert(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "alt+enter" || msg.String() == "ctrl+j" {
		m.composer += "\n"
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		if err := m.persistCurrentDraft(); err != nil {
			m.status = fmt.Sprintf("save draft failed: %v", err)
			return m, nil
		}
		m.mode = ModeNormal
		m.status = "normal mode"
	case tea.KeyEnter:
		body := strings.TrimSpace(m.composer)
		if body == "" {
			m.status = "empty message"
			m.composer = ""
			return m, nil
		}
		chatID := m.currentChat().ID
		if chatID == "" {
			m.status = "no active chat"
			m.mode = ModeNormal
			return m, nil
		}

		message := store.Message{
			ID:         fmt.Sprintf("local-%d", time.Now().UnixNano()),
			ChatID:     chatID,
			Sender:     "me",
			Body:       body,
			Timestamp:  time.Now(),
			IsOutgoing: true,
		}
		if m.persistMessage != nil {
			persisted, err := m.persistMessage(chatID, body)
			if err != nil {
				m.status = fmt.Sprintf("send failed: %v", err)
				return m, nil
			}
			message = persisted
		}
		m.messagesByChat[chatID] = append(m.messagesByChat[chatID], message)
		if base, ok := m.unfilteredByChat[chatID]; ok {
			m.unfilteredByChat[chatID] = append(base, message)
		}
		m.messageCursor = len(m.messagesByChat[chatID]) - 1
		m.messageScrollTop = m.messageCursor
		m.composer = ""
		if err := m.setDraft(chatID, ""); err != nil {
			m.status = fmt.Sprintf("clear draft failed: %v", err)
			return m, nil
		}
		m.mode = ModeInsert
		m.focus = FocusMessages
		m.status = "message queued"
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.composer = trimLastRune(m.composer)
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.composer += msg.String()
		}
	}

	return m, nil
}

func (m Model) updateCommand(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = ModeNormal
		m.commandLine = ""
		m.status = "normal mode"
	case tea.KeyEnter:
		cmd := strings.TrimSpace(m.commandLine)
		m.commandLine = ""
		m.mode = ModeNormal
		if cmd != "" {
			m.commandHistory = append(m.commandHistory, cmd)
		}
		return m.executeCommand(cmd)
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.commandLine = trimLastRune(m.commandLine)
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.commandLine += msg.String()
		}
	}

	return m, nil
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = ModeNormal
		m.searchLine = ""
		m.status = "normal mode"
	case tea.KeyEnter:
		m.lastSearch = m.searchLine
		m.lastSearchFocus = m.focus
		if strings.TrimSpace(m.lastSearch) == "" {
			m.clearSearch()
			m.searchLine = ""
			m.mode = ModeNormal
			m.status = "search cleared"
			return m, nil
		}
		m.activeSearch = strings.TrimSpace(m.lastSearch)
		m.searchHistory = append(m.searchHistory, m.activeSearch)
		m.rebuildSearchMatches()
		m.advanceSearch(1)
		m.mode = ModeNormal
		if len(m.searchMatches) > 0 {
			m.status = fmt.Sprintf("search: %s", m.lastSearch)
		}
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.searchLine = trimLastRune(m.searchLine)
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.searchLine += msg.String()
		}
	}

	return m, nil
}

func (m Model) updateVisual(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = ModeNormal
		m.status = "normal mode"
	case tea.KeyRunes:
		switch msg.String() {
		case "j":
			m.moveCursor(1)
		case "k":
			m.moveCursor(-1)
		case "y":
			selected := m.selectedMessages()
			var parts []string
			for _, message := range selected {
				parts = append(parts, message.Body)
			}
			m.yankRegister = strings.Join(parts, "\n")
			m.mode = ModeNormal
			m.status = fmt.Sprintf("yanked %d message(s)", len(selected))
		}
	}

	return m, nil
}

func (m *Model) cycleFocus(delta int) {
	order := []Focus{FocusChats, FocusMessages}
	if m.infoPaneVisible && !m.compactLayout {
		order = append(order, FocusPreview)
	}

	index := 0
	for i, focus := range order {
		if focus == m.focus {
			index = i
			break
		}
	}

	index = (index + delta + len(order)) % len(order)
	m.focus = order[index]
	if m.focus == FocusMessages {
		if err := m.ensureCurrentMessagesLoaded(); err != nil {
			m.status = fmt.Sprintf("load messages failed: %v", err)
			return
		}
	}
	m.status = fmt.Sprintf("focus: %s", m.focus)
}

func (m *Model) moveFocus(delta int) {
	if delta < 0 {
		switch m.focus {
		case FocusMessages:
			m.focus = FocusChats
		case FocusPreview:
			m.focus = FocusMessages
		}
	} else {
		switch m.focus {
		case FocusChats:
			m.focus = FocusMessages
			if err := m.ensureCurrentMessagesLoaded(); err != nil {
				m.status = fmt.Sprintf("load messages failed: %v", err)
				return
			}
		case FocusMessages:
			if m.infoPaneVisible && !m.compactLayout {
				m.focus = FocusPreview
			}
		}
	}
}

func (m *Model) moveCursor(delta int) {
	switch m.focus {
	case FocusChats:
		if len(m.chats) == 0 {
			return
		}
		m.activeChat = clamp(m.activeChat+delta, 0, len(m.chats)-1)
		m.keepActiveChatVisible()
		if err := m.ensureCurrentMessagesLoaded(); err != nil {
			m.status = fmt.Sprintf("load messages failed: %v", err)
			return
		}
		m.messageCursor = clamp(m.messageCursor, 0, max(0, len(m.currentMessages())-1))
		m.messageScrollTop = clamp(m.messageScrollTop, 0, max(0, len(m.currentMessages())-1))
	case FocusMessages:
		if len(m.currentMessages()) == 0 {
			return
		}
		previous := m.messageCursor
		m.messageCursor = clamp(m.messageCursor+delta, 0, len(m.currentMessages())-1)
		m.keepMessageCursorNearViewport(previous)
	}
}

func (m Model) executeCommand(cmd string) (tea.Model, tea.Cmd) {
	switch {
	case cmd == "":
		m.status = "command cancelled"
	case cmd == "q" || cmd == "quit":
		return m, tea.Quit
	case cmd == "help":
		m.helpVisible = true
		m.status = "help"
	case cmd == "focus chats":
		m.focus = FocusChats
		m.status = "focus: chats"
	case cmd == "focus messages":
		m.focus = FocusMessages
		if err := m.ensureCurrentMessagesLoaded(); err != nil {
			m.status = fmt.Sprintf("load messages failed: %v", err)
			return m, nil
		}
		m.status = "focus: messages"
	case cmd == "focus preview":
		if m.compactLayout {
			m.status = "info pane hidden in compact layout"
		} else {
			m.infoPaneVisible = true
			m.focus = FocusPreview
			m.status = "focus: info"
		}
	case cmd == "preview" || cmd == "preview toggle" || cmd == "info" || cmd == "info toggle":
		if m.compactLayout {
			m.status = "info pane hidden in compact layout"
			break
		}
		m.infoPaneVisible = !m.infoPaneVisible
		if !m.infoPaneVisible && m.focus == FocusPreview {
			m.focus = FocusMessages
		}
		m.status = fmt.Sprintf("info pane: %s", onOff(m.infoPaneVisible))
	case cmd == "preview show" || cmd == "info show":
		if m.compactLayout {
			m.status = "info pane hidden in compact layout"
			break
		}
		m.infoPaneVisible = true
		m.status = "info pane: on"
	case cmd == "preview hide" || cmd == "info hide":
		m.infoPaneVisible = false
		if m.focus == FocusPreview {
			m.focus = FocusMessages
		}
		m.status = "info pane: off"
	case cmd == "preview-backend auto":
		m.previewReport = media.Detect("auto")
		m.status = fmt.Sprintf("preview backend: %s", m.previewReport.Selected)
	case cmd == "clear-search" || cmd == "search clear":
		m.clearSearch()
		m.status = "search cleared"
	case cmd == "filter unread":
		if err := m.setUnreadOnly(true); err != nil {
			m.status = fmt.Sprintf("filter failed: %v", err)
			break
		}
	case cmd == "filter all":
		if err := m.setUnreadOnly(false); err != nil {
			m.status = fmt.Sprintf("filter failed: %v", err)
			break
		}
	case cmd == "filter clear" || cmd == "filter messages clear":
		if err := m.clearMessageFilter(); err != nil {
			m.status = fmt.Sprintf("filter failed: %v", err)
			break
		}
	case strings.HasPrefix(cmd, "filter messages "):
		query := strings.TrimSpace(strings.TrimPrefix(cmd, "filter messages "))
		if query == "" || query == "clear" {
			if err := m.clearMessageFilter(); err != nil {
				m.status = fmt.Sprintf("filter failed: %v", err)
			}
			break
		}
		if err := m.applyMessageFilter(query); err != nil {
			m.status = fmt.Sprintf("filter failed: %v", err)
			break
		}
	case cmd == "sort pinned":
		if err := m.setPinnedFirst(true); err != nil {
			m.status = fmt.Sprintf("sort failed: %v", err)
			break
		}
	case cmd == "sort recent":
		if err := m.setPinnedFirst(false); err != nil {
			m.status = fmt.Sprintf("sort failed: %v", err)
			break
		}
	default:
		m.status = fmt.Sprintf("unknown command: %s", cmd)
	}

	return m, nil
}

func (m *Model) rebuildSearchMatches() {
	query := strings.ToLower(strings.TrimSpace(m.lastSearch))
	m.searchMatches = nil
	m.searchIndex = -1
	if query == "" {
		return
	}

	switch m.lastSearchFocus {
	case FocusChats:
		for i, chat := range m.chats {
			if strings.Contains(strings.ToLower(chat.Title), query) {
				m.searchMatches = append(m.searchMatches, i)
			}
		}
	case FocusMessages, FocusPreview:
		for i, message := range m.currentMessages() {
			if strings.Contains(strings.ToLower(message.Body), query) {
				m.searchMatches = append(m.searchMatches, i)
			}
		}
	}
}

func (m *Model) runStoreSearch() error {
	query := strings.TrimSpace(m.lastSearch)

	switch m.lastSearchFocus {
	case FocusChats:
		if m.searchChats == nil {
			return nil
		}
		source := m.allChats
		if query == "" {
			m.activeSearch = ""
		} else {
			chats, err := m.searchChats(query)
			if err != nil {
				return err
			}
			source = chats
		}
		m.searchChatSource = slices.Clone(source)
		return m.applyChatView(source, "")
	case FocusMessages, FocusPreview:
		if m.searchMessages == nil {
			return nil
		}
		chatID := m.currentChat().ID
		if chatID == "" {
			return nil
		}
		messages, err := m.searchMessages(chatID, query, 200)
		if err != nil {
			return err
		}
		m.messagesByChat[chatID] = messages
		m.messageCursor = 0
		m.messageScrollTop = 0
	}

	return nil
}

func (m *Model) advanceSearch(delta int) {
	if strings.TrimSpace(m.lastSearch) == "" {
		m.status = "no active search"
		return
	}
	if m.lastSearchFocus != m.focus && !(m.lastSearchFocus == FocusPreview && m.focus == FocusMessages) {
		m.status = "search belongs to another pane"
		return
	}
	if len(m.searchMatches) == 0 {
		m.rebuildSearchMatches()
	}
	if len(m.searchMatches) == 0 {
		m.status = fmt.Sprintf("no matches for %q", m.lastSearch)
		return
	}

	if m.searchIndex == -1 {
		if delta < 0 {
			m.searchIndex = len(m.searchMatches) - 1
		} else {
			m.searchIndex = 0
		}
	} else {
		m.searchIndex = (m.searchIndex + delta + len(m.searchMatches)) % len(m.searchMatches)
	}

	target := m.searchMatches[m.searchIndex]
	if m.lastSearchFocus == FocusChats {
		m.activeChat = target
		m.keepActiveChatVisible()
		if err := m.ensureCurrentMessagesLoaded(); err != nil {
			m.status = fmt.Sprintf("load messages failed: %v", err)
			return
		}
		m.messageCursor = 0
		m.messageScrollTop = 0
	} else {
		m.messageCursor = target
		m.messageScrollTop = target
	}
}

func (m *Model) ensureCurrentMessagesLoaded() error {
	chatID := m.currentChat().ID
	if chatID == "" {
		return nil
	}
	if _, ok := m.messagesByChat[chatID]; ok {
		return nil
	}
	if m.loadMessages == nil {
		m.messagesByChat[chatID] = nil
		return nil
	}

	messages, err := m.loadMessages(chatID, messageLoadLimit)
	if err != nil {
		return err
	}
	m.messagesByChat[chatID] = slices.Clone(messages)
	return nil
}

func (m *Model) reloadCurrentMessages() error {
	chatID := m.currentChat().ID
	if chatID == "" {
		return nil
	}
	if m.loadMessages == nil {
		return nil
	}
	messages, err := m.loadMessages(chatID, messageLoadLimit)
	if err != nil {
		return err
	}
	m.messagesByChat[chatID] = slices.Clone(messages)
	m.messageCursor = clamp(m.messageCursor, 0, max(0, len(messages)-1))
	m.messageScrollTop = clamp(m.messageScrollTop, 0, max(0, len(messages)-1))
	return nil
}

func (m *Model) clearSearch() {
	m.lastSearch = ""
	m.searchLine = ""
	m.activeSearch = ""
	m.searchMatches = nil
	m.searchIndex = -1
	m.lastSearchFocus = ""
	m.searchChatSource = nil
}

func (m *Model) applyMessageFilter(query string) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return m.clearMessageFilter()
	}

	chatID := m.currentChat().ID
	if chatID == "" {
		return nil
	}
	source := m.filterSource(chatID)
	m.unfilteredByChat[chatID] = slices.Clone(source)

	var filtered []store.Message
	if m.searchMessages != nil {
		messages, err := m.searchMessages(chatID, query, messageLoadLimit)
		if err != nil {
			return err
		}
		filtered = slices.Clone(messages)
	} else {
		lowerQuery := strings.ToLower(query)
		for _, message := range source {
			if strings.Contains(strings.ToLower(message.Body), lowerQuery) {
				filtered = append(filtered, message)
			}
		}
	}

	m.messagesByChat[chatID] = filtered
	m.messageFilter = query
	m.messageCursor = 0
	m.messageScrollTop = 0
	m.status = fmt.Sprintf("message filter: %s", query)
	return nil
}

func (m *Model) clearMessageFilter() error {
	chatID := m.currentChat().ID
	if chatID == "" {
		m.messageFilter = ""
		return nil
	}
	if base, ok := m.unfilteredByChat[chatID]; ok {
		m.messagesByChat[chatID] = slices.Clone(base)
		delete(m.unfilteredByChat, chatID)
	} else if err := m.reloadCurrentMessages(); err != nil {
		return err
	}
	m.messageFilter = ""
	m.messageCursor = clamp(m.messageCursor, 0, max(0, len(m.currentMessages())-1))
	m.messageScrollTop = clamp(m.messageScrollTop, 0, max(0, len(m.currentMessages())-1))
	m.status = "message filter cleared"
	return nil
}

func (m Model) filterSource(chatID string) []store.Message {
	if base, ok := m.unfilteredByChat[chatID]; ok {
		return slices.Clone(base)
	}
	return slices.Clone(m.messagesByChat[chatID])
}

func (m *Model) captureCount(msg tea.KeyMsg) bool {
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return false
	}
	r := msg.Runes[0]
	if r < '0' || r > '9' {
		return false
	}
	if r == '0' && m.pendingCount == 0 {
		return false
	}
	m.pendingCount = m.pendingCount*10 + int(r-'0')
	m.status = fmt.Sprintf("count: %d", m.pendingCount)
	return true
}

func (m *Model) consumeCount() int {
	if m.pendingCount <= 0 {
		return 1
	}
	count := m.pendingCount
	m.pendingCount = 0
	return count
}

func (m *Model) setUnreadOnly(enabled bool) error {
	m.unreadOnly = enabled
	if err := m.applyChatView(m.currentChatSource(), m.currentChat().ID); err != nil {
		return err
	}
	m.status = fmt.Sprintf("unread filter: %s", onOff(enabled))
	return nil
}

func (m *Model) setPinnedFirst(enabled bool) error {
	m.pinnedFirst = enabled
	if err := m.applyChatView(m.currentChatSource(), m.currentChat().ID); err != nil {
		return err
	}
	if enabled {
		m.status = "sort: pinned"
	} else {
		m.status = "sort: recent"
	}
	return nil
}

func (m Model) currentChatSource() []store.Chat {
	if len(m.searchChatSource) == 0 {
		return m.allChats
	}
	return m.searchChatSource
}

func (m *Model) applyChatView(source []store.Chat, preferredChatID string) error {
	chats := slices.Clone(source)
	if m.unreadOnly {
		chats = filterUnread(chats)
	}
	sortChats(chats, m.pinnedFirst)
	m.chats = chats

	if len(m.chats) == 0 {
		m.activeChat = 0
		m.chatScrollTop = 0
		m.messageCursor = 0
		m.messageScrollTop = 0
		return nil
	}

	m.activeChat = indexOfChat(m.chats, preferredChatID)
	if m.activeChat == -1 {
		m.activeChat = 0
	}
	m.keepActiveChatVisible()
	m.messageCursor = 0
	m.messageScrollTop = 0
	return m.ensureCurrentMessagesLoaded()
}

func (m *Model) keepActiveChatVisible() {
	m.chatScrollTop = adjustedChatScrollTop(m.chatScrollTop, m.activeChat, len(m.chats), visibleChatCellCount(m.chatPaneContentHeight()))
}

func (m Model) chatPaneContentHeight() int {
	if m.height <= 0 {
		return 0
	}
	inputHeight := m.inputHeight()
	bodyHeight := m.height - 1 - inputHeight
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	return panelContentHeight(m.panelStyle(FocusChats), bodyHeight)
}

func (m *Model) keepMessageCursorNearViewport(previous int) {
	messageCount := len(m.currentMessages())
	if messageCount == 0 {
		m.messageScrollTop = 0
		return
	}
	m.messageScrollTop = clamp(m.messageScrollTop, 0, messageCount-1)
	if m.messageCursor < m.messageScrollTop {
		m.messageScrollTop = m.messageCursor
		return
	}
	if m.messageCursor > previous && m.messageCursor > m.messageScrollTop+2 {
		m.messageScrollTop = m.messageCursor - 2
	}
}

func filterUnread(chats []store.Chat) []store.Chat {
	out := make([]store.Chat, 0, len(chats))
	for _, chat := range chats {
		if chat.Unread > 0 {
			out = append(out, chat)
		}
	}
	return out
}

func sortChats(chats []store.Chat, pinnedFirst bool) {
	sort.SliceStable(chats, func(i, j int) bool {
		left := chats[i]
		right := chats[j]
		if pinnedFirst && left.Pinned != right.Pinned {
			return left.Pinned
		}
		if !left.LastMessageAt.Equal(right.LastMessageAt) {
			return left.LastMessageAt.After(right.LastMessageAt)
		}
		return strings.ToLower(left.Title) < strings.ToLower(right.Title)
	})
}

func indexOfChat(chats []store.Chat, chatID string) int {
	if chatID == "" {
		return -1
	}
	for i, chat := range chats {
		if chat.ID == chatID {
			return i
		}
	}
	return -1
}

func (m Model) currentChat() store.Chat {
	if len(m.chats) == 0 || m.activeChat < 0 || m.activeChat >= len(m.chats) {
		return store.Chat{}
	}
	return m.chats[m.activeChat]
}

func (m Model) currentMessages() []store.Message {
	chatID := m.currentChat().ID
	if chatID == "" {
		return nil
	}
	return m.messagesByChat[chatID]
}

func (m Model) selectedMessages() []store.Message {
	messages := m.currentMessages()
	if len(messages) == 0 {
		return nil
	}

	start := min(m.visualAnchor, m.messageCursor)
	end := max(m.visualAnchor, m.messageCursor)
	return slices.Clone(messages[start : end+1])
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func (m *Model) persistCurrentDraft() error {
	chatID := m.currentChat().ID
	if chatID == "" {
		return nil
	}
	return m.setDraft(chatID, m.composer)
}

func (m *Model) setDraft(chatID, body string) error {
	if m.saveDraft != nil {
		if err := m.saveDraft(chatID, body); err != nil {
			return err
		}
	}

	if strings.TrimSpace(body) == "" {
		delete(m.draftsByChat, chatID)
		m.updateChatDraftFlag(chatID, false)
		return nil
	}

	m.draftsByChat[chatID] = body
	m.updateChatDraftFlag(chatID, true)
	return nil
}

func (m *Model) updateChatDraftFlag(chatID string, hasDraft bool) {
	for i := range m.allChats {
		if m.allChats[i].ID == chatID {
			m.allChats[i].HasDraft = hasDraft
			break
		}
	}
	for i := range m.chats {
		if m.chats[i].ID == chatID {
			m.chats[i].HasDraft = hasDraft
			break
		}
	}
}

func trimLastRune(value string) string {
	if value == "" {
		return value
	}
	_, size := utf8.DecodeLastRuneInString(value)
	if size == 0 {
		return ""
	}
	return value[:len(value)-size]
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}
