package ui

import (
	"context"
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

type clipboardCopiedMsg struct {
	Count int
	Err   error
}

type mediaPreviewReadyMsg struct {
	Key     string
	Request media.PreviewRequest
	Preview media.Preview
}

type mediaDownloadedMsg struct {
	MessageID string
	Media     store.MediaMetadata
	Err       error
}

type mediaSavedMsg struct {
	MessageID string
	Media     store.MediaMetadata
	Path      string
	Err       error
}

type mediaOverlayMsg struct {
	Err error
}

type MediaOpenFinishedMsg struct {
	Path string
	Err  error
}

type Options struct {
	Paths           config.Paths
	Config          config.Config
	PreviewReport   media.Report
	Snapshot        store.Snapshot
	PersistMessage  func(chatID, body string, attachments []Attachment) (store.Message, error)
	LoadMessages    func(chatID string, limit int) ([]store.Message, error)
	SaveDraft       func(chatID, body string) error
	SearchChats     func(query string) ([]store.Chat, error)
	SearchMessages  func(chatID, query string, limit int) ([]store.Message, error)
	CopyToClipboard func(text string) error
	PickAttachment  func() tea.Cmd
	OpenMedia       func(media store.MediaMetadata) tea.Cmd
	DeleteMessage   func(messageID string) error
	SaveMedia       func(media store.MediaMetadata) error
	DownloadMedia   func(message store.Message, media store.MediaMetadata) (store.MediaMetadata, error)
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
	previewCache     map[string]media.Preview
	previewInflight  map[string]bool
	previewRequested map[string]bool
	overlay          *media.OverlayManager
	suppressOverlay  bool
	paths            config.Paths
	config           config.Config
	status           string
	commandLine      string
	searchLine       string
	composer         string
	attachments      []Attachment
	lastSearch       string
	lastSearchFocus  Focus
	activeSearch     string
	searchChatSource []store.Chat
	searchMatches    []int
	searchIndex      int
	messageFilter    string
	unfilteredByChat map[string][]store.Message
	pendingCount     int
	leaderPending    bool
	yankRegister     string
	quitting         bool
	compactLayout    bool
	infoPaneVisible  bool
	helpVisible      bool
	unreadOnly       bool
	pinnedFirst      bool
	commandHistory   []string
	searchHistory    []string
	deleteConfirmID  string
	persistMessage   func(chatID, body string, attachments []Attachment) (store.Message, error)
	loadMessages     func(chatID string, limit int) ([]store.Message, error)
	saveDraft        func(chatID, body string) error
	searchChats      func(query string) ([]store.Chat, error)
	searchMessages   func(chatID, query string, limit int) ([]store.Message, error)
	copyToClipboard  func(text string) error
	pickAttachment   func() tea.Cmd
	openMedia        func(media store.MediaMetadata) tea.Cmd
	deleteMessage    func(messageID string) error
	saveMedia        func(media store.MediaMetadata) error
	downloadMedia    func(message store.Message, media store.MediaMetadata) (store.MediaMetadata, error)
}

const messageLoadLimit = 200

func Run(opts Options) error {
	p := tea.NewProgram(NewModel(opts), tea.WithAltScreen())
	final, err := p.Run()
	if closer, ok := final.(interface{ Close() error }); ok {
		if closeErr := closer.Close(); err == nil {
			err = closeErr
		}
	}
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
		previewCache:     map[string]media.Preview{},
		previewInflight:  map[string]bool{},
		previewRequested: map[string]bool{},
		paths:            opts.Paths,
		config:           normalizeConfig(opts.Config),
		status:           "ready",
		pinnedFirst:      true,
		persistMessage:   opts.PersistMessage,
		loadMessages:     opts.LoadMessages,
		saveDraft:        opts.SaveDraft,
		searchChats:      opts.SearchChats,
		searchMessages:   opts.SearchMessages,
		copyToClipboard:  opts.CopyToClipboard,
		pickAttachment:   opts.PickAttachment,
		openMedia:        opts.OpenMedia,
		deleteMessage:    opts.DeleteMessage,
		saveMedia:        opts.SaveMedia,
		downloadMedia:    opts.DownloadMedia,
		unfilteredByChat: map[string][]store.Message{},
	}
}

func normalizeConfig(cfg config.Config) config.Config {
	if cfg.LeaderKey == "" {
		cfg.LeaderKey = "space"
	}
	if cfg.DownloadsDir == "" {
		cfg.DownloadsDir = config.Default(config.Paths{}).DownloadsDir
	}
	return cfg
}

func (m Model) Close() error {
	if m.overlay == nil {
		return nil
	}
	return m.overlay.Close()
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
		return m.withPreviewCmd(nil)
	case clipboardCopiedMsg:
		if msg.Err != nil {
			m.status = fmt.Sprintf("yanked %d message(s); clipboard failed: %v", msg.Count, msg.Err)
		} else {
			m.status = fmt.Sprintf("yanked %d message(s) to clipboard", msg.Count)
		}
		return m, nil
	case AttachmentPickedMsg:
		return m.handlePickedAttachment(msg)
	case mediaPreviewReadyMsg:
		updated, cmd := m.handleMediaPreviewReady(msg)
		if next, ok := updated.(Model); ok {
			return next.withPreviewCmd(cmd)
		}
		return updated, cmd
	case mediaDownloadedMsg:
		updated, cmd := m.handleMediaDownloaded(msg)
		if next, ok := updated.(Model); ok {
			return next.withPreviewCmd(cmd)
		}
		return updated, cmd
	case mediaSavedMsg:
		return m.handleMediaSaved(msg)
	case MediaOpenFinishedMsg:
		if msg.Err != nil {
			m.status = fmt.Sprintf("open failed: %s", shortError(msg.Err))
		} else {
			m.status = fmt.Sprintf("opened media: %s", msg.Path)
		}
		return m.withPreviewCmd(nil)
	case mediaOverlayMsg:
		if msg.Err != nil {
			m.status = fmt.Sprintf("overlay failed: %s", shortError(msg.Err))
		}
		return m, nil
	case tea.KeyMsg:
		updated, cmd := m.handleKey(msg)
		if next, ok := updated.(Model); ok {
			return next.withPreviewCmd(cmd)
		}
		return updated, cmd
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		m.quitting = true
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

	if m.leaderPending {
		return m.handleLeaderKey(msg)
	}
	if keyMatchesLeader(msg, m.config.LeaderKey) {
		m.leaderPending = true
		m.pendingCount = 0
		m.status = fmt.Sprintf("leader: %s", leaderDisplay(m.config.LeaderKey))
		return m, nil
	}
	if m.captureCount(msg) {
		return m, nil
	}
	count := m.consumeCount()

	switch msg.String() {
	case "q":
		m.quitting = true
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
		} else if m.focus == FocusMessages || m.focus == FocusPreview {
			return m.activateFocusedMediaPreview()
		}
	case "o":
		return m.openFocusedMedia()
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

func (m Model) handleLeaderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.leaderPending = false
	if msg.Type == tea.KeyEsc {
		m.status = "leader cancelled"
		return m, nil
	}
	switch msg.String() {
	case "s":
		return m.saveFocusedMedia()
	default:
		m.status = fmt.Sprintf("unknown leader key: %s", msg.String())
		return m, nil
	}
}

func (m Model) updateInsert(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+f" {
		return m.startAttachmentPicker()
	}
	if msg.String() == "ctrl+x" {
		if len(m.attachments) == 0 {
			m.status = "no staged attachments"
			return m, nil
		}
		removed := m.attachments[len(m.attachments)-1]
		m.attachments = m.attachments[:len(m.attachments)-1]
		m.status = fmt.Sprintf("removed attachment: %s", removed.FileName)
		return m, nil
	}
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
		if body == "" && len(m.attachments) == 0 {
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
		message.Media = m.mediaForLocalMessage(message.ID, m.attachments)
		if m.persistMessage != nil {
			persisted, err := m.persistMessage(chatID, body, slices.Clone(m.attachments))
			if err != nil {
				m.status = fmt.Sprintf("send failed: %v", err)
				return m, nil
			}
			message = persisted
		}
		if len(message.Media) == 0 && len(m.attachments) > 0 {
			message.Media = m.mediaForLocalMessage(message.ID, m.attachments)
		}
		m.messagesByChat[chatID] = append(m.messagesByChat[chatID], message)
		if base, ok := m.unfilteredByChat[chatID]; ok {
			m.unfilteredByChat[chatID] = append(base, message)
		}
		m.messageCursor = len(m.messagesByChat[chatID]) - 1
		m.messageScrollTop = m.messageCursor
		m.composer = ""
		m.attachments = nil
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
			if m.copyToClipboard == nil {
				m.status = fmt.Sprintf("yanked %d message(s) to register", len(selected))
				return m, nil
			}
			count := len(selected)
			text := m.yankRegister
			m.status = fmt.Sprintf("yanked %d message(s); copying clipboard", count)
			return m, func() tea.Msg {
				return clipboardCopiedMsg{Count: count, Err: m.copyToClipboard(text)}
			}
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
	case strings.HasPrefix(cmd, "preview-backend "):
		backend := strings.TrimSpace(strings.TrimPrefix(cmd, "preview-backend "))
		m.previewReport = media.Detect(backend)
		m.previewCache = map[string]media.Preview{}
		m.previewInflight = map[string]bool{}
		m.previewRequested = map[string]bool{}
		if m.previewReport.Selected == media.BackendUeberzugPP && m.overlay == nil {
			m.overlay = media.NewOverlayManager(m.previewReport.UeberzugPPOutput)
		}
		m.status = fmt.Sprintf("preview backend: %s", m.previewReport.Selected)
		return m, m.clearOverlayCmd()
	case cmd == "preview-cache clear" || cmd == "clear-preview-cache":
		m.previewCache = map[string]media.Preview{}
		m.previewInflight = map[string]bool{}
		m.previewRequested = map[string]bool{}
		m.status = "preview cache cleared"
		return m, m.clearOverlayCmd()
	case cmd == "media-preview" || cmd == "preview-media" || cmd == "media preview":
		return m.activateFocusedMediaPreview()
	case cmd == "media-open" || cmd == "media open":
		return m.openFocusedMedia()
	case cmd == "media-save" || cmd == "media save":
		return m.saveFocusedMedia()
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
	case cmd == "attach":
		return m.startAttachmentPicker()
	case strings.HasPrefix(cmd, "attach "):
		path := strings.TrimSpace(strings.TrimPrefix(cmd, "attach "))
		if err := m.stageAttachmentPath(path); err != nil {
			m.status = fmt.Sprintf("attach failed: %v", err)
			break
		}
	case cmd == "delete-message" || cmd == "delete message":
		m.armDeleteFocusedMessage()
	case cmd == "delete-message confirm" || cmd == "delete message confirm":
		if err := m.deleteConfirmedMessage(); err != nil {
			m.status = fmt.Sprintf("delete failed: %v", err)
			break
		}
	case cmd == "delete-message!" || cmd == "delete message!":
		m.armDeleteFocusedMessage()
		if err := m.deleteConfirmedMessage(); err != nil {
			m.status = fmt.Sprintf("delete failed: %v", err)
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

func (m Model) withPreviewCmd(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if m.quitting {
		return m, batchCmds(cmd, m.clearOverlayCmd())
	}
	next, previewCmd := m.queueRequestedPreviewCmd()
	if next.suppressOverlay {
		next.suppressOverlay = false
		return next, batchCmds(cmd, previewCmd, next.clearOverlayCmd())
	}
	overlayCmd := next.syncOverlayCmd()
	return next, batchCmds(cmd, previewCmd, overlayCmd)
}

func batchCmds(cmds ...tea.Cmd) tea.Cmd {
	var active []tea.Cmd
	for _, cmd := range cmds {
		if cmd != nil {
			active = append(active, cmd)
		}
	}
	if len(active) == 0 {
		return nil
	}
	if len(active) == 1 {
		return active[0]
	}
	return tea.Batch(active...)
}

func (m Model) queueRequestedPreviewCmd() (Model, tea.Cmd) {
	requests := m.requestedPreviewRequests()
	if len(requests) == 0 {
		return m, nil
	}
	if m.previewCache == nil {
		m.previewCache = map[string]media.Preview{}
	}
	if m.previewInflight == nil {
		m.previewInflight = map[string]bool{}
	}

	previewer := media.NewPreviewer(
		m.previewReport,
		m.paths.PreviewCacheDir,
		previewMaxWidth(m.config),
		previewMaxHeight(m.config),
	)
	var cmds []tea.Cmd
	for _, request := range requests {
		key := media.PreviewKey(request)
		if _, ok := m.previewCache[key]; ok || m.previewInflight[key] {
			continue
		}
		m.previewInflight[key] = true
		req := request
		cmds = append(cmds, func() tea.Msg {
			if delay := previewDelay(m.config); delay > 0 {
				time.Sleep(delay)
			}
			return mediaPreviewReadyMsg{
				Key:     media.PreviewKey(req),
				Request: req,
				Preview: previewer.Render(context.Background(), req),
			}
		})
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) syncOverlayCmd() tea.Cmd {
	if m.previewReport.Selected != media.BackendUeberzugPP {
		return m.clearOverlayCmd()
	}
	placement, ok := m.activeMediaPlacement()
	if !ok {
		return m.clearOverlayCmd()
	}
	if m.overlay == nil {
		m.overlay = media.NewOverlayManager(m.previewReport.UeberzugPPOutput)
	}
	manager := m.overlay
	return func() tea.Msg {
		return mediaOverlayMsg{Err: manager.Place(context.Background(), placement)}
	}
}

func (m Model) clearOverlayCmd() tea.Cmd {
	if m.overlay == nil {
		return nil
	}
	manager := m.overlay
	return func() tea.Msg {
		return mediaOverlayMsg{Err: manager.Remove(context.Background())}
	}
}

func (m Model) requestedPreviewRequests() []media.PreviewRequest {
	if m.width <= 0 || m.height <= 0 || m.previewReport.Selected == media.BackendNone {
		return nil
	}
	if len(m.previewRequested) == 0 {
		return nil
	}

	messages := m.currentMessages()
	if len(messages) == 0 {
		return nil
	}

	width, height := m.previewDimensions()
	if width <= 0 || height <= 0 {
		return nil
	}

	requests := make([]media.PreviewRequest, 0, 4)
	for _, message := range messages {
		for _, item := range message.Media {
			if !m.previewRequested[mediaActivationKey(message, item)] {
				continue
			}
			request, ok := m.previewRequestForMedia(message, item, width, height)
			if !ok {
				continue
			}
			if strings.TrimSpace(request.LocalPath) == "" && strings.TrimSpace(request.ThumbnailPath) == "" {
				continue
			}
			requests = append(requests, request)
			if len(requests) >= 4 {
				return requests
			}
			break
		}
	}
	return requests
}

func (m Model) previewRequestForMedia(message store.Message, item store.MediaMetadata, width, height int) (media.PreviewRequest, bool) {
	if media.MediaKind(item.MIMEType, item.FileName) == media.KindUnsupported {
		return media.PreviewRequest{}, false
	}
	if width <= 0 || height <= 0 {
		width, height = m.previewDimensions()
	}
	if width <= 0 || height <= 0 {
		return media.PreviewRequest{}, false
	}

	request := media.PreviewRequest{
		MessageID:     item.MessageID,
		MIMEType:      item.MIMEType,
		FileName:      item.FileName,
		LocalPath:     item.LocalPath,
		ThumbnailPath: item.ThumbnailPath,
		CacheDir:      m.paths.PreviewCacheDir,
		Backend:       m.previewReport.Selected,
		Width:         width,
		Height:        height,
	}
	if request.MessageID == "" {
		request.MessageID = message.ID
	}
	return request, true
}

func (m Model) activateFocusedMediaPreview() (tea.Model, tea.Cmd) {
	message, ok := m.focusedMessage()
	if !ok {
		m.status = "no message selected"
		return m, nil
	}
	item, ok := firstMessageMedia(message)
	if !ok {
		m.status = "no media on focused message"
		return m, nil
	}

	kind := media.MediaKind(item.MIMEType, item.FileName)
	if strings.TrimSpace(item.LocalPath) == "" && strings.TrimSpace(item.ThumbnailPath) == "" {
		if m.downloadMedia != nil {
			m.status = fmt.Sprintf("downloading media: %s", mediaDisplayName(item))
			return m, func() tea.Msg {
				downloaded, err := m.downloadMedia(message, item)
				return mediaDownloadedMsg{
					MessageID: message.ID,
					Media:     downloaded,
					Err:       err,
				}
			}
		}
		m.status = fmt.Sprintf("%s is not downloaded yet; WhatsApp media download is not implemented", mediaDisplayName(item))
		return m, nil
	}
	if kind == media.KindUnsupported {
		if strings.TrimSpace(item.LocalPath) != "" {
			return m.openFocusedMedia()
		}
		m.status = fmt.Sprintf("no inline preview for %s", mediaDisplayName(item))
		return m, nil
	}
	if m.previewReport.Selected == media.BackendNone || m.previewReport.Selected == media.BackendExternal {
		if strings.TrimSpace(item.LocalPath) != "" {
			return m.openFocusedMedia()
		}
		m.status = fmt.Sprintf("preview backend %s cannot render inline", m.previewReport.Selected)
		return m, nil
	}

	request, ok := m.previewRequestForMedia(message, item, 0, 0)
	if !ok {
		m.status = fmt.Sprintf("no inline preview for %s", mediaDisplayName(item))
		return m, nil
	}
	previewKey := media.PreviewKey(request)
	if preview, ok := m.previewCache[previewKey]; ok {
		if preview.Ready() {
			return m.openFocusedMedia()
		}
		delete(m.previewCache, previewKey)
	}
	if m.previewInflight != nil && m.previewInflight[previewKey] {
		m.status = fmt.Sprintf("rendering preview: %s", mediaDisplayName(item))
		return m, nil
	}
	if m.previewRequested == nil {
		m.previewRequested = map[string]bool{}
	}
	m.previewRequested[mediaActivationKey(message, item)] = true
	m.status = fmt.Sprintf("rendering preview: %s", mediaDisplayName(item))
	return m, nil
}

func (m Model) openFocusedMedia() (tea.Model, tea.Cmd) {
	message, item, ok := m.focusedMedia()
	if !ok {
		m.status = "no media on focused message"
		return m, nil
	}
	if strings.TrimSpace(item.LocalPath) == "" {
		if m.downloadMedia != nil {
			m.status = fmt.Sprintf("downloading media: %s", mediaDisplayName(item))
			return m, func() tea.Msg {
				downloaded, err := m.downloadMedia(message, item)
				if err == nil && strings.TrimSpace(downloaded.LocalPath) != "" && m.openMedia != nil {
					if openMsg := m.openMedia(downloaded)(); openMsg != nil {
						return openMsg
					}
				}
				return mediaDownloadedMsg{MessageID: message.ID, Media: downloaded, Err: err}
			}
		}
		m.status = fmt.Sprintf("%s is not downloaded yet; WhatsApp media download is not implemented", mediaDisplayName(item))
		return m, nil
	}
	if m.openMedia == nil {
		m.status = "media opener unavailable"
		return m, nil
	}
	m.status = fmt.Sprintf("opening media: %s", mediaDisplayName(item))
	m.suppressOverlay = true
	return m, batchCmds(m.clearOverlayCmd(), m.openMedia(item))
}

func (m Model) saveFocusedMedia() (tea.Model, tea.Cmd) {
	message, item, ok := m.focusedMedia()
	if !ok {
		m.status = "no media on focused message"
		return m, nil
	}
	if strings.TrimSpace(item.LocalPath) == "" {
		if m.downloadMedia != nil {
			m.status = fmt.Sprintf("downloading media: %s", mediaDisplayName(item))
			return m, func() tea.Msg {
				downloaded, err := m.downloadMedia(message, item)
				if err != nil {
					return mediaSavedMsg{MessageID: message.ID, Media: downloaded, Err: err}
				}
				path, err := saveMediaToDownloads(downloaded, m.config.DownloadsDir)
				return mediaSavedMsg{MessageID: message.ID, Media: downloaded, Path: path, Err: err}
			}
		}
		m.status = fmt.Sprintf("%s is not downloaded yet; WhatsApp media download is not implemented", mediaDisplayName(item))
		return m, nil
	}
	m.status = fmt.Sprintf("saving media: %s", mediaDisplayName(item))
	return m, func() tea.Msg {
		path, err := saveMediaToDownloads(item, m.config.DownloadsDir)
		return mediaSavedMsg{MessageID: message.ID, Media: item, Path: path, Err: err}
	}
}

func saveMediaToDownloads(item store.MediaMetadata, downloadsDir string) (string, error) {
	return media.SaveLocalFile(media.SaveRequest{
		SourcePath:   item.LocalPath,
		FileName:     item.FileName,
		MIMEType:     item.MIMEType,
		DownloadsDir: downloadsDir,
	})
}

func (m Model) previewDimensions() (int, int) {
	contentWidth := m.messagePaneContentWidth()
	if contentWidth <= 0 {
		return 0, 0
	}
	width := min(previewMaxWidth(m.config), max(10, mediaBubbleWidth(contentWidth)-4))
	height := previewMaxHeight(m.config)
	if geometry, ok := m.messagePaneGeometry(); ok {
		height = min(height, max(6, geometry.height-5))
	}
	if m.compactLayout && m.width < 72 {
		height = min(height, 12)
	}
	return width, height
}

func (m Model) messagePaneContentWidth() int {
	if m.width <= 0 {
		return 0
	}

	if m.compactLayout {
		style := m.panelStyle(FocusMessages)
		return panelContentWidth(style, m.width)
	}

	chatWidth := max(24, m.width/4)
	previewWidth := max(26, m.width/4)
	messageWidth := m.width - chatWidth
	if m.infoPaneVisible {
		messageWidth -= previewWidth
	}
	style := m.panelStyle(FocusMessages)
	return panelContentWidth(style, messageWidth)
}

func (m Model) handleMediaPreviewReady(msg mediaPreviewReadyMsg) (tea.Model, tea.Cmd) {
	if m.previewCache == nil {
		m.previewCache = map[string]media.Preview{}
	}
	if m.previewInflight != nil {
		delete(m.previewInflight, msg.Key)
	}
	m.previewCache[msg.Key] = msg.Preview
	if msg.Preview.Err != nil {
		m.status = fmt.Sprintf("preview failed: %s", shortError(msg.Preview.Err))
		return m, nil
	}
	if msg.Preview.Err == nil && (msg.Preview.SourceKind == media.SourceGeneratedThumbnail || msg.Preview.GeneratedThumbnail) && msg.Preview.ThumbnailPath != "" {
		if err := m.applyGeneratedThumbnail(msg.Request.MessageID, msg.Preview.ThumbnailPath); err != nil {
			m.status = fmt.Sprintf("preview metadata failed: %v", err)
		}
		updatedRequest := msg.Request
		updatedRequest.ThumbnailPath = msg.Preview.ThumbnailPath
		m.previewCache[media.PreviewKey(updatedRequest)] = msg.Preview
	}
	if msg.Preview.Ready() {
		m.status = fmt.Sprintf("preview ready: %s (%s %s %dx%d)", previewRequestName(msg.Request), msg.Preview.RenderedBackend, msg.Preview.SourceKind, msg.Preview.Width, msg.Preview.Height)
	}
	return m, nil
}

func (m Model) handleMediaDownloaded(msg mediaDownloadedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.status = fmt.Sprintf("download failed: %s", shortError(msg.Err))
		return m, nil
	}
	if strings.TrimSpace(msg.MessageID) == "" {
		m.status = "download failed: missing message id"
		return m, nil
	}
	if msg.Media.MessageID == "" {
		msg.Media.MessageID = msg.MessageID
	}
	if msg.Media.DownloadState == "" {
		msg.Media.DownloadState = "downloaded"
	}
	if strings.TrimSpace(msg.Media.LocalPath) == "" && strings.TrimSpace(msg.Media.ThumbnailPath) == "" {
		m.status = "download failed: media has no local file"
		return m, nil
	}

	var (
		updatedChatID  string
		updatedMessage store.Message
		updated        bool
	)
	for chatID, messages := range m.messagesByChat {
		replaced, ok, message := replaceMessageMedia(messages, msg.MessageID, msg.Media)
		if !ok {
			continue
		}
		m.messagesByChat[chatID] = replaced
		updatedChatID = chatID
		updatedMessage = message
		updated = true
		break
	}
	if !updated {
		m.status = "download failed: message is not loaded"
		return m, nil
	}
	if base, ok := m.unfilteredByChat[updatedChatID]; ok {
		replaced, _, _ := replaceMessageMedia(base, msg.MessageID, msg.Media)
		m.unfilteredByChat[updatedChatID] = replaced
	}
	if m.saveMedia != nil {
		if err := m.saveMedia(msg.Media); err != nil {
			m.status = fmt.Sprintf("download metadata failed: %v", err)
			return m, nil
		}
	}
	if m.previewRequested == nil {
		m.previewRequested = map[string]bool{}
	}
	m.previewRequested[mediaActivationKey(updatedMessage, msg.Media)] = true
	m.status = fmt.Sprintf("downloaded media; rendering preview: %s", mediaDisplayName(msg.Media))
	return m, nil
}

func (m Model) handleMediaSaved(msg mediaSavedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.status = fmt.Sprintf("save failed: %s", shortError(msg.Err))
		return m, nil
	}
	if msg.MessageID != "" && strings.TrimSpace(msg.Media.LocalPath) != "" {
		if updated, _, _ := m.updateLoadedMedia(msg.MessageID, msg.Media); updated && m.saveMedia != nil {
			if err := m.saveMedia(msg.Media); err != nil {
				m.status = fmt.Sprintf("save metadata failed: %v", err)
				return m, nil
			}
		}
	}
	m.status = fmt.Sprintf("saved media: %s", msg.Path)
	return m, nil
}

func (m *Model) updateLoadedMedia(messageID string, mediaItem store.MediaMetadata) (bool, string, store.Message) {
	for chatID, messages := range m.messagesByChat {
		replaced, ok, message := replaceMessageMedia(messages, messageID, mediaItem)
		if !ok {
			continue
		}
		m.messagesByChat[chatID] = replaced
		if base, ok := m.unfilteredByChat[chatID]; ok {
			replacedBase, _, _ := replaceMessageMedia(base, messageID, mediaItem)
			m.unfilteredByChat[chatID] = replacedBase
		}
		return true, chatID, message
	}
	return false, "", store.Message{}
}

func (m *Model) applyGeneratedThumbnail(messageID, thumbnailPath string) error {
	if messageID == "" || thumbnailPath == "" {
		return nil
	}
	for chatID, messages := range m.messagesByChat {
		for messageIndex := range messages {
			for mediaIndex := range messages[messageIndex].Media {
				if messages[messageIndex].Media[mediaIndex].MessageID != messageID {
					continue
				}
				m.messagesByChat[chatID][messageIndex].Media[mediaIndex].ThumbnailPath = thumbnailPath
				if m.saveMedia != nil {
					if err := m.saveMedia(m.messagesByChat[chatID][messageIndex].Media[mediaIndex]); err != nil {
						return err
					}
				}
				return nil
			}
		}
	}
	return nil
}

func previewMaxWidth(cfg config.Config) int {
	if cfg.PreviewMaxWidth <= 0 {
		return 67
	}
	return cfg.PreviewMaxWidth
}

func previewMaxHeight(cfg config.Config) int {
	if cfg.PreviewMaxHeight <= 0 {
		return 40
	}
	return cfg.PreviewMaxHeight
}

func previewDelay(cfg config.Config) time.Duration {
	if cfg.PreviewDelayMS <= 0 {
		return 0
	}
	return time.Duration(cfg.PreviewDelayMS) * time.Millisecond
}

func (m Model) handlePickedAttachment(msg AttachmentPickedMsg) (tea.Model, tea.Cmd) {
	if msg.Cancelled {
		m.mode = ModeInsert
		m.focus = FocusMessages
		m.status = "attachment picker cancelled"
		return m, nil
	}
	if msg.Err != nil {
		m.mode = ModeInsert
		m.focus = FocusMessages
		m.status = fmt.Sprintf("attach failed: %v", msg.Err)
		return m, nil
	}

	m.stageAttachment(msg.Attachment)
	return m, nil
}

func (m Model) startAttachmentPicker() (tea.Model, tea.Cmd) {
	if len(m.chats) == 0 || m.currentChat().ID == "" {
		m.status = "no chat selected"
		return m, nil
	}
	m.mode = ModeInsert
	m.focus = FocusMessages
	if m.pickAttachment == nil {
		m.status = "attachment picker unavailable; use :attach /path"
		return m, nil
	}

	m.status = "opening attachment picker"
	return m, m.pickAttachment()
}

func (m *Model) stageAttachmentPath(path string) error {
	if len(m.chats) == 0 || m.currentChat().ID == "" {
		return fmt.Errorf("no chat selected")
	}
	attachment, err := AttachmentFromPath(path)
	if err != nil {
		return err
	}
	m.mode = ModeInsert
	m.focus = FocusMessages
	m.stageAttachment(attachment)
	return nil
}

func (m *Model) stageAttachment(attachment Attachment) {
	if attachment.FileName == "" {
		attachment.FileName = attachment.LocalPath
	}
	if attachment.DownloadState == "" {
		attachment.DownloadState = "local_pending"
	}
	if len(m.attachments) > 0 {
		m.attachments = []Attachment{attachment}
		m.status = fmt.Sprintf("replaced attachment with %s", attachment.FileName)
		return
	}
	m.attachments = []Attachment{attachment}
	m.status = fmt.Sprintf("attached %s", attachment.FileName)
}

func (m Model) mediaForLocalMessage(messageID string, attachments []Attachment) []store.MediaMetadata {
	mediaItems := make([]store.MediaMetadata, 0, len(attachments))
	for _, attachment := range attachments {
		mediaItems = append(mediaItems, store.MediaMetadata{
			MessageID:     messageID,
			MIMEType:      attachment.MIMEType,
			FileName:      attachment.FileName,
			SizeBytes:     attachment.SizeBytes,
			LocalPath:     attachment.LocalPath,
			ThumbnailPath: attachment.ThumbnailPath,
			DownloadState: attachment.DownloadState,
			UpdatedAt:     time.Now(),
		})
	}
	return mediaItems
}

func (m *Model) armDeleteFocusedMessage() {
	message, ok := m.focusedMessage()
	if !ok {
		m.status = "no message selected"
		return
	}
	m.deleteConfirmID = message.ID
	m.status = "run :delete-message confirm to delete locally"
}

func (m *Model) deleteConfirmedMessage() error {
	message, ok := m.focusedMessage()
	if !ok {
		m.deleteConfirmID = ""
		return fmt.Errorf("no message selected")
	}
	if m.deleteConfirmID != message.ID {
		m.deleteConfirmID = ""
		return fmt.Errorf("delete confirmation expired")
	}
	if m.deleteMessage != nil {
		if err := m.deleteMessage(message.ID); err != nil {
			return err
		}
	}

	chatID := m.currentChat().ID
	m.messagesByChat[chatID] = removeMessageByID(m.messagesByChat[chatID], message.ID)
	if base, ok := m.unfilteredByChat[chatID]; ok {
		m.unfilteredByChat[chatID] = removeMessageByID(base, message.ID)
	}
	m.messageCursor = clamp(m.messageCursor, 0, max(0, len(m.currentMessages())-1))
	m.messageScrollTop = clamp(m.messageScrollTop, 0, max(0, len(m.currentMessages())-1))
	m.deleteConfirmID = ""
	m.rebuildSearchMatches()
	m.status = "deleted message locally"
	return nil
}

func (m Model) focusedMessage() (store.Message, bool) {
	messages := m.currentMessages()
	if len(messages) == 0 {
		return store.Message{}, false
	}
	return messages[clamp(m.messageCursor, 0, len(messages)-1)], true
}

func (m Model) focusedMedia() (store.Message, store.MediaMetadata, bool) {
	message, ok := m.focusedMessage()
	if !ok {
		return store.Message{}, store.MediaMetadata{}, false
	}
	item, ok := firstMessageMedia(message)
	return message, item, ok
}

func firstMessageMedia(message store.Message) (store.MediaMetadata, bool) {
	if len(message.Media) == 0 {
		return store.MediaMetadata{}, false
	}
	return message.Media[0], true
}

func replaceMessageMedia(messages []store.Message, messageID string, mediaItem store.MediaMetadata) ([]store.Message, bool, store.Message) {
	for index := range messages {
		if messages[index].ID != messageID {
			continue
		}
		if mediaItem.MessageID == "" {
			mediaItem.MessageID = messageID
		}
		if mediaItem.DownloadState == "" {
			mediaItem.DownloadState = "downloaded"
		}
		if len(messages[index].Media) == 0 {
			messages[index].Media = []store.MediaMetadata{mediaItem}
		} else {
			messages[index].Media[0] = mediaItem
		}
		return messages, true, messages[index]
	}
	return messages, false, store.Message{}
}

func mediaActivationKey(message store.Message, item store.MediaMetadata) string {
	messageID := strings.TrimSpace(item.MessageID)
	if messageID == "" {
		messageID = strings.TrimSpace(message.ID)
	}
	return strings.Join([]string{
		messageID,
		strings.ToLower(strings.TrimSpace(item.MIMEType)),
		strings.TrimSpace(item.FileName),
		strings.TrimSpace(item.LocalPath),
	}, "\x00")
}

func keyMatchesLeader(msg tea.KeyMsg, leader string) bool {
	leader = strings.TrimSpace(leader)
	if leader == "" {
		leader = "space"
	}
	if strings.EqualFold(leader, "space") {
		return msg.Type == tea.KeySpace || msg.String() == " " || msg.String() == "space"
	}
	return msg.Type == tea.KeyRunes && msg.String() == leader
}

func leaderDisplay(leader string) string {
	leader = strings.TrimSpace(leader)
	if leader == "" || strings.EqualFold(leader, "space") {
		return "space"
	}
	return leader
}

func mediaDisplayName(item store.MediaMetadata) string {
	name := strings.TrimSpace(item.FileName)
	if name == "" {
		name = strings.TrimSpace(item.LocalPath)
	}
	if name == "" {
		return "media"
	}
	return name
}

func previewRequestName(request media.PreviewRequest) string {
	name := strings.TrimSpace(request.FileName)
	if name == "" {
		name = strings.TrimSpace(request.LocalPath)
	}
	if name == "" {
		return "media"
	}
	return name
}

func shortError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	if text == "" {
		return "unknown error"
	}
	return truncateDisplay(text, 96)
}

func removeMessageByID(messages []store.Message, id string) []store.Message {
	out := messages[:0]
	for _, message := range messages {
		if message.ID != id {
			out = append(out, message)
		}
	}
	return out
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
