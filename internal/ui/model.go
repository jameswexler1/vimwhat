package ui

import (
	"fmt"
	"slices"
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
	SaveDraft      func(chatID, body string) error
	SearchChats    func(query string) ([]store.Chat, error)
	SearchMessages func(chatID, query string, limit int) ([]store.Message, error)
}

type Model struct {
	width           int
	height          int
	mode            Mode
	focus           Focus
	allChats        []store.Chat
	chats           []store.Chat
	messagesByChat  map[string][]store.Message
	draftsByChat    map[string]string
	activeChat      int
	messageCursor   int
	visualAnchor    int
	previewReport   media.Report
	paths           config.Paths
	config          config.Config
	status          string
	commandLine     string
	searchLine      string
	composer        string
	lastSearch      string
	lastSearchFocus Focus
	searchMatches   []int
	searchIndex     int
	yankRegister    string
	compactLayout   bool
	infoPaneVisible bool
	persistMessage  func(chatID, body string) (store.Message, error)
	saveDraft       func(chatID, body string) error
	searchChats     func(query string) ([]store.Chat, error)
	searchMessages  func(chatID, query string, limit int) ([]store.Message, error)
}

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
		mode:           ModeNormal,
		focus:          FocusChats,
		allChats:       slices.Clone(chats),
		chats:          chats,
		messagesByChat: cloneMessages(opts.Snapshot.MessagesByChat),
		draftsByChat:   cloneDrafts(opts.Snapshot.DraftsByChat),
		activeChat:     activeChat,
		previewReport:  opts.PreviewReport,
		paths:          opts.Paths,
		config:         opts.Config,
		status:         "ready",
		persistMessage: opts.PersistMessage,
		saveDraft:      opts.SaveDraft,
		searchChats:    opts.SearchChats,
		searchMessages: opts.SearchMessages,
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
	switch msg.String() {
	case "q":
		return m, tea.Quit
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
		m.moveCursor(1)
	case "k":
		m.moveCursor(-1)
	case "g":
		if m.focus == FocusMessages {
			m.messageCursor = 0
		} else {
			m.activeChat = 0
			m.messageCursor = 0
		}
	case "G":
		if m.focus == FocusMessages {
			if count := len(m.currentMessages()); count > 0 {
				m.messageCursor = count - 1
			}
		} else {
			if count := len(m.chats); count > 0 {
				m.activeChat = count - 1
				m.messageCursor = 0
			}
		}
	case "enter":
		if m.focus == FocusChats {
			if len(m.chats) == 0 {
				m.status = "no chat selected"
				return m, nil
			}
			m.focus = FocusMessages
			m.messageCursor = max(0, len(m.currentMessages())-1)
			m.status = fmt.Sprintf("opened %s", m.currentChat().Title)
		}
	case "n":
		m.advanceSearch(1)
	case "N":
		m.advanceSearch(-1)
	default:
		return m, nil
	}

	return m, nil
}

func (m Model) updateInsert(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		m.messageCursor = len(m.messagesByChat[chatID]) - 1
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
		if err := m.runStoreSearch(); err != nil {
			m.searchLine = ""
			m.mode = ModeNormal
			m.status = fmt.Sprintf("search failed: %v", err)
			return m, nil
		}
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
		m.messageCursor = clamp(m.messageCursor, 0, max(0, len(m.currentMessages())-1))
	case FocusMessages:
		if len(m.currentMessages()) == 0 {
			return
		}
		m.messageCursor = clamp(m.messageCursor+delta, 0, len(m.currentMessages())-1)
	}
}

func (m Model) executeCommand(cmd string) (tea.Model, tea.Cmd) {
	switch cmd {
	case "":
		m.status = "command cancelled"
	case "q", "quit":
		return m, tea.Quit
	case "help":
		m.status = "normal: hjkl/tab : / i v enter q"
	case "focus chats":
		m.focus = FocusChats
		m.status = "focus: chats"
	case "focus messages":
		m.focus = FocusMessages
		m.status = "focus: messages"
	case "focus preview":
		if m.compactLayout {
			m.status = "info pane hidden in compact layout"
		} else {
			m.infoPaneVisible = true
			m.focus = FocusPreview
			m.status = "focus: info"
		}
	case "preview", "preview toggle", "info", "info toggle":
		if m.compactLayout {
			m.status = "info pane hidden in compact layout"
			break
		}
		m.infoPaneVisible = !m.infoPaneVisible
		if !m.infoPaneVisible && m.focus == FocusPreview {
			m.focus = FocusMessages
		}
		m.status = fmt.Sprintf("info pane: %s", onOff(m.infoPaneVisible))
	case "preview show", "info show":
		if m.compactLayout {
			m.status = "info pane hidden in compact layout"
			break
		}
		m.infoPaneVisible = true
		m.status = "info pane: on"
	case "preview hide", "info hide":
		m.infoPaneVisible = false
		if m.focus == FocusPreview {
			m.focus = FocusMessages
		}
		m.status = "info pane: off"
	case "preview-backend auto":
		m.previewReport = media.Detect("auto")
		m.status = fmt.Sprintf("preview backend: %s", m.previewReport.Selected)
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
		if query == "" {
			m.chats = slices.Clone(m.allChats)
		} else {
			chats, err := m.searchChats(query)
			if err != nil {
				return err
			}
			m.chats = slices.Clone(chats)
		}
		m.activeChat = 0
		m.messageCursor = 0
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
		m.messageCursor = 0
	} else {
		m.messageCursor = target
	}
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
