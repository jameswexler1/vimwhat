package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"

	"vimwhat/internal/config"
	"vimwhat/internal/media"
	"vimwhat/internal/store"
	"vimwhat/internal/textmatch"
)

type Mode string

const (
	ModeNormal  Mode = "normal"
	ModeInsert  Mode = "insert"
	ModeVisual  Mode = "visual"
	ModeForward Mode = "forward"
	ModeCommand Mode = "command"
	ModeSearch  Mode = "search"
	ModeConfirm Mode = "confirm"
)

type Focus string

const (
	FocusChats    Focus = "chats"
	FocusMessages Focus = "messages"
	FocusPreview  Focus = "preview"
)

type ConnectionState string

const (
	ConnectionPaired       ConnectionState = "paired"
	ConnectionConnecting   ConnectionState = "connecting"
	ConnectionOnline       ConnectionState = "online"
	ConnectionReconnecting ConnectionState = "reconnecting"
	ConnectionOffline      ConnectionState = "offline"
	ConnectionLoggedOut    ConnectionState = "logged_out"
)

type LiveUpdate struct {
	ConnectionState ConnectionState
	Status          string
	Refresh         bool
	HistoryChatID   string
	HistoryMessages int
	ReadChatID      string
	PreferredChatID string
	Presence        PresenceUpdate
	Sync            *SyncProgressUpdate
}

type SyncProgressUpdate struct {
	Active         bool
	Completed      bool
	Title          string
	Subtitle       string
	Total          int
	Processed      int
	AppDataChanges int
	Messages       int
	Notifications  int
	Receipts       int
}

type PresenceUpdate struct {
	ChatID              string
	SenderJID           string
	Sender              string
	TypingChanged       bool
	Typing              bool
	ExpiresAt           time.Time
	AvailabilityChanged bool
	Online              bool
	LastSeen            time.Time
}

type OutgoingMessage struct {
	ChatID      string
	Body        string
	Attachments []Attachment
	Quote       *store.Message
	Mentions    []store.MessageMention
}

type ForwardMessagesRequest struct {
	Messages   []store.Message
	Recipients []store.Chat
}

type liveUpdateMsg struct {
	Update LiveUpdate
	OK     bool
}

type snapshotReloadedMsg struct {
	Snapshot     store.Snapshot
	ActiveChatID string
	Err          error
}

type refreshDebouncedMsg struct{}

type syncOverlayDoneMsg struct {
	Generation int
}

type presenceExpiredMsg struct {
	ChatID string
	At     time.Time
}

type ownPresenceIdleMsg struct {
	ChatID     string
	Generation int
}

type clipboardCopiedMsg struct {
	Count int
	Err   error
}

type ClipboardAttachmentPastedMsg struct {
	Attachment Attachment
	Err        error
}

type ClipboardImagePastedMsg = ClipboardAttachmentPastedMsg

type ClipboardImageCopiedMsg struct {
	MessageID string
	Media     store.MediaMetadata
	Err       error
}

type StickerPickedMsg struct {
	Sticker   store.RecentSticker
	Err       error
	Cancelled bool
}

type outgoingMessagePersistedMsg struct {
	TempID      string
	ChatID      string
	Message     store.Message
	DraftBody   string
	Attachments []Attachment
	Err         error
}

type draftSavedMsg struct {
	ChatID string
	Body   string
	Err    error
}

type markReadFinishedMsg struct {
	ChatID string
	Manual bool
	Err    error
}

type reactionFinishedMsg struct {
	Emoji string
	Err   error
}

type retryMessageFinishedMsg struct {
	Original store.Message
	Message  store.Message
	Err      error
}

type stickerSentMsg struct {
	ChatID  string
	Sticker store.RecentSticker
	Message store.Message
	Err     error
}

type messagesLoadedMsg struct {
	ChatID     string
	Messages   []store.Message
	ShowLatest bool
	Activate   bool
	Err        error
}

type olderMessagesLoadedMsg struct {
	ChatID   string
	AnchorID string
	Messages []store.Message
	Err      error
}

type historyRequestedMsg struct {
	ChatID  string
	Context string
	Err     error
}

type messageFilterAppliedMsg struct {
	Generation int
	ChatID     string
	Query      string
	Messages   []store.Message
	Err        error
}

type messageFilterClearedMsg struct {
	Generation int
	ChatID     string
	Messages   []store.Message
	Err        error
}

type mentionCandidatesLoadedMsg struct {
	Generation int
	ChatID     string
	Query      string
	Candidates []store.MentionCandidate
	Err        error
}

type mediaPreviewReadyMsg struct {
	Key        string
	Generation int
	Request    media.PreviewRequest
	Preview    media.Preview
}

const (
	chatAvatarPreviewMessagePrefix = "chat-avatar:"
	chatAvatarPreviewWidth         = 4
	chatAvatarPreviewHeight        = 2
)

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

type audioStartedMsg struct {
	Session    int
	MessageID  string
	Media      store.MediaMetadata
	Process    AudioProcess
	Downloaded bool
	Err        error
}

type audioFinishedMsg struct {
	Session   int
	MessageID string
	MediaKey  string
	Err       error
}

type mediaOverlayMsg struct {
	Signature string
	Err       error
}

type sixelOverlayMsg struct {
	Signature string
	Err       error
}

type overlayResumeMsg struct {
	Generation int
}

type terminalSizePolledMsg struct {
	Width  int
	Height int
	OK     bool
}

type MediaOpenFinishedMsg struct {
	MessageID string
	Media     store.MediaMetadata
	Path      string
	Err       error
}

type MessageDeletedForEveryoneMsg struct {
	MessageID string
	Err       error
}

type MessageEditedMsg struct {
	MessageID string
	Body      string
	EditedAt  time.Time
	Err       error
}

type ForwardMessagesFinishedMsg struct {
	Sent    int
	Skipped int
	Failed  int
	Err     error
}

type AudioProcess interface {
	Wait() error
	Stop() error
}

type newMessageState struct {
	FirstMessageID string
	NewCount       int
}

type Options struct {
	Paths                        config.Paths
	Config                       config.Config
	PreviewReport                media.Report
	Snapshot                     store.Snapshot
	ConnectionState              ConnectionState
	LiveUpdates                  <-chan LiveUpdate
	ReserveLastColumn            bool
	BlockSending                 bool
	BlockAttachments             bool
	RequireOnlineForSend         bool
	BlockLiveStartup             bool
	PersistMessage               func(OutgoingMessage) (store.Message, error)
	SendSticker                  func(chatID string, sticker store.RecentSticker) (store.Message, error)
	RetryMessage                 func(message store.Message) (store.Message, error)
	MarkRead                     func(chat store.Chat, messages []store.Message) error
	SendReaction                 func(message store.Message, emoji string) error
	SendPresence                 func(chatID string, composing bool) error
	SubscribePresence            func(chatID string) error
	LoadMessages                 func(chatID string, limit int) ([]store.Message, error)
	LoadOlderMessages            func(chatID string, before store.Message, limit int) ([]store.Message, error)
	RequestHistory               func(chatID string) error
	ReloadSnapshot               func(activeChatID string, limit int) (store.Snapshot, error)
	SaveDraft                    func(chatID, body string) error
	SearchChats                  func(query string) ([]store.Chat, error)
	SearchMessages               func(chatID, query string, limit int) ([]store.Message, error)
	SearchMentionCandidates      func(chatID, query string, limit int) ([]store.MentionCandidate, error)
	ForwardMessages              func(ForwardMessagesRequest) tea.Cmd
	CopyToClipboard              func(text string) error
	PasteAttachmentFromClipboard func() tea.Cmd
	PasteImageFromClipboard      func() tea.Cmd
	CopyImageToClipboard         func(media store.MediaMetadata) tea.Cmd
	PickAttachment               func() tea.Cmd
	PickSticker                  func() tea.Cmd
	OpenMedia                    func(media store.MediaMetadata) tea.Cmd
	OpenMediaDetached            func(media store.MediaMetadata) tea.Cmd
	StartAudio                   func(media store.MediaMetadata) (AudioProcess, error)
	DeleteMessage                func(messageID string) error
	DeleteMessageForEveryone     func(message store.Message) tea.Cmd
	EditMessage                  func(message store.Message, body string) tea.Cmd
	SaveMedia                    func(media store.MediaMetadata) error
	DownloadMedia                func(message store.Message, media store.MediaMetadata) (store.MediaMetadata, error)
	ActiveChatChanged            func(chatID string)
	AppFocusChanged              func(focused bool)
	VisibleChatsChanged          func(chatIDs []string)
	SixelWriter                  io.Writer
	PollTerminalSize             bool
}

type Model struct {
	width                        int
	height                       int
	reserveLastColumn            bool
	mode                         Mode
	focus                        Focus
	allChats                     []store.Chat
	chats                        []store.Chat
	messagesByChat               map[string][]store.Message
	draftsByChat                 map[string]string
	activeChat                   int
	chatScrollTop                int
	messageCursor                int
	messageScrollTop             int
	visualAnchor                 int
	previewReport                media.Report
	previewCache                 map[string]media.Preview
	previewInflight              map[string]bool
	previewRequested             map[string]bool
	previewGeneration            int
	inlineFallbackPrompt         bool
	inlineFallbackAccepted       bool
	inlineFallbackDeclined       bool
	overlay                      *media.OverlayManager
	overlaySignature             string
	overlaySyncPending           bool
	overlayPendingSignature      string
	overlayConsecutiveFailures   int
	sixel                        *media.SixelManager
	sixelWriter                  io.Writer
	sixelSignature               string
	sixelSyncPending             bool
	sixelPendingSignature        string
	mediaOverlayPaused           bool
	avatarOverlayPaused          bool
	overlayPauseGeneration       int
	overlayResumeQueued          int
	mediaDownloadInflight        map[string]bool
	audioProcess                 AudioProcess
	audioSession                 int
	audioMessageID               string
	audioMediaKey                string
	audioDisplayName             string
	paths                        config.Paths
	config                       config.Config
	status                       string
	connectionState              ConnectionState
	commandLine                  string
	searchLine                   string
	forwardQuery                 string
	forwardSearchActive          bool
	forwardSourceMessages        []store.Message
	forwardCandidates            []store.Chat
	forwardCursor                int
	forwardSelected              map[string]bool
	forwardSelectedOrder         []string
	confirmLine                  string
	composer                     string
	composerMentions             []store.MessageMention
	composerMentionsByChat       map[string][]store.MessageMention
	mentionActive                bool
	mentionStart                 int
	mentionQuery                 string
	mentionCandidates            []store.MentionCandidate
	mentionCursor                int
	attachments                  []Attachment
	lastSearch                   string
	lastSearchFocus              Focus
	activeSearch                 string
	searchChatSource             []store.Chat
	searchMatches                []int
	searchIndex                  int
	messageFilter                string
	unfilteredByChat             map[string][]store.Message
	newMessageStateByChat        map[string]newMessageState
	pendingCount                 int
	leaderPending                bool
	leaderSequence               string
	yankRegister                 string
	quitting                     bool
	compactLayout                bool
	infoPaneVisible              bool
	helpVisible                  bool
	unreadOnly                   bool
	pinnedFirst                  bool
	commandHistory               []string
	searchHistory                []string
	deleteConfirmID              string
	deleteForEveryoneConfirmID   string
	editTarget                   *store.Message
	replyTo                      *store.Message
	presenceByChat               map[string]PresenceUpdate
	readReceiptInflight          map[string]bool
	messageLoadInflight          map[string]bool
	olderMessagesInflight        map[string]bool
	historyRequestInflight       map[string]bool
	outgoingMessageInflight      map[string]bool
	presenceSubscribed           map[string]bool
	ownPresenceChatID            string
	ownPresenceComposing         bool
	ownPresenceGeneration        int
	filterGeneration             int
	mentionSearchGeneration      int
	persistMessage               func(OutgoingMessage) (store.Message, error)
	sendSticker                  func(chatID string, sticker store.RecentSticker) (store.Message, error)
	retryMessage                 func(message store.Message) (store.Message, error)
	markRead                     func(chat store.Chat, messages []store.Message) error
	sendReaction                 func(message store.Message, emoji string) error
	sendPresence                 func(chatID string, composing bool) error
	subscribePresence            func(chatID string) error
	loadMessages                 func(chatID string, limit int) ([]store.Message, error)
	loadOlderMessages            func(chatID string, before store.Message, limit int) ([]store.Message, error)
	requestHistory               func(chatID string) error
	reloadSnapshot               func(activeChatID string, limit int) (store.Snapshot, error)
	saveDraft                    func(chatID, body string) error
	searchChats                  func(query string) ([]store.Chat, error)
	searchMessages               func(chatID, query string, limit int) ([]store.Message, error)
	searchMentionCandidates      func(chatID, query string, limit int) ([]store.MentionCandidate, error)
	forwardMessages              func(ForwardMessagesRequest) tea.Cmd
	copyToClipboard              func(text string) error
	pasteAttachmentFromClipboard func() tea.Cmd
	copyImageToClipboard         func(media store.MediaMetadata) tea.Cmd
	pickAttachment               func() tea.Cmd
	pickSticker                  func() tea.Cmd
	openMedia                    func(media store.MediaMetadata) tea.Cmd
	openMediaDetached            func(media store.MediaMetadata) tea.Cmd
	startAudio                   func(media store.MediaMetadata) (AudioProcess, error)
	deleteMessage                func(messageID string) error
	deleteMessageForEveryone     func(message store.Message) tea.Cmd
	editMessage                  func(message store.Message, body string) tea.Cmd
	saveMedia                    func(media store.MediaMetadata) error
	downloadMedia                func(message store.Message, media store.MediaMetadata) (store.MediaMetadata, error)
	activeChatChanged            func(chatID string)
	lastReportedActiveChat       string
	appFocusKnown                bool
	appFocused                   bool
	appFocusChanged              func(focused bool)
	visibleChatsChanged          func(chatIDs []string)
	lastReportedVisibleChats     string
	liveUpdates                  <-chan LiveUpdate
	reloadInFlight               bool
	refreshQueued                bool
	refreshDebouncePending       bool
	refreshPreferredChatID       string
	blockSending                 bool
	blockAttachments             bool
	requireOnlineForSend         bool
	messageLimitsByChat          map[string]int
	historyRequestedByChat       map[string]bool
	syncOverlay                  syncOverlayState
	pollTerminalSize             bool
}

const messageLoadLimit = 200
const historyPageSize = 50
const refreshDebounceDuration = 75 * time.Millisecond
const syncOverlayCompleteDelay = 600 * time.Millisecond
const overlayResumeDelay = 80 * time.Millisecond
const terminalMediaSyncTimeout = 5 * time.Second
const sixelPaintDelay = 40 * time.Millisecond
const terminalSizePollInterval = 250 * time.Millisecond

type syncOverlayState struct {
	Visible        bool
	Active         bool
	Completed      bool
	Generation     int
	Title          string
	Subtitle       string
	Total          int
	Processed      int
	AppDataChanges int
	Messages       int
	Notifications  int
	Receipts       int
}

func initialSyncOverlay(blockLiveStartup bool) syncOverlayState {
	if !blockLiveStartup {
		return syncOverlayState{}
	}
	return syncOverlayState{
		Visible:  true,
		Active:   true,
		Title:    "Connecting to WhatsApp",
		Subtitle: "Checking for new chats and messages before the app becomes interactive.",
	}
}

func Run(opts Options) (err error) {
	report, restore := prepareTerminalOutput()
	defer func() {
		if restore != nil {
			if restoreErr := restore(); err == nil {
				err = restoreErr
			}
		}
	}()
	opts.ReserveLastColumn = opts.ReserveLastColumn || report.LastColumnGuard
	opts.PollTerminalSize = opts.PollTerminalSize || platformTerminalSizePollingEnabled()
	output := newLockedTerminalFile(os.Stdout)
	opts.SixelWriter = output
	options := append(programOptions(), tea.WithOutput(output))
	p := tea.NewProgram(NewModel(opts), options...)
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

	model := Model{
		reserveLastColumn:            opts.ReserveLastColumn,
		mode:                         ModeNormal,
		focus:                        FocusChats,
		allChats:                     slices.Clone(chats),
		chats:                        chats,
		messagesByChat:               cloneMessages(opts.Snapshot.MessagesByChat),
		draftsByChat:                 cloneDrafts(opts.Snapshot.DraftsByChat),
		activeChat:                   activeChat,
		previewReport:                opts.PreviewReport,
		previewCache:                 map[string]media.Preview{},
		previewInflight:              map[string]bool{},
		previewRequested:             map[string]bool{},
		sixelWriter:                  opts.SixelWriter,
		mediaDownloadInflight:        map[string]bool{},
		paths:                        opts.Paths,
		config:                       normalizeConfig(opts.Config),
		status:                       "ready",
		connectionState:              opts.ConnectionState,
		pinnedFirst:                  true,
		persistMessage:               opts.PersistMessage,
		sendSticker:                  opts.SendSticker,
		retryMessage:                 opts.RetryMessage,
		loadMessages:                 opts.LoadMessages,
		loadOlderMessages:            opts.LoadOlderMessages,
		requestHistory:               opts.RequestHistory,
		reloadSnapshot:               opts.ReloadSnapshot,
		saveDraft:                    opts.SaveDraft,
		searchChats:                  opts.SearchChats,
		searchMessages:               opts.SearchMessages,
		searchMentionCandidates:      opts.SearchMentionCandidates,
		forwardMessages:              opts.ForwardMessages,
		copyToClipboard:              opts.CopyToClipboard,
		pasteAttachmentFromClipboard: opts.PasteAttachmentFromClipboard,
		copyImageToClipboard:         opts.CopyImageToClipboard,
		pickAttachment:               opts.PickAttachment,
		pickSticker:                  opts.PickSticker,
		openMedia:                    opts.OpenMedia,
		openMediaDetached:            opts.OpenMediaDetached,
		startAudio:                   opts.StartAudio,
		deleteMessage:                opts.DeleteMessage,
		deleteMessageForEveryone:     opts.DeleteMessageForEveryone,
		editMessage:                  opts.EditMessage,
		saveMedia:                    opts.SaveMedia,
		downloadMedia:                opts.DownloadMedia,
		activeChatChanged:            opts.ActiveChatChanged,
		appFocusChanged:              opts.AppFocusChanged,
		visibleChatsChanged:          opts.VisibleChatsChanged,
		markRead:                     opts.MarkRead,
		sendReaction:                 opts.SendReaction,
		sendPresence:                 opts.SendPresence,
		subscribePresence:            opts.SubscribePresence,
		liveUpdates:                  opts.LiveUpdates,
		blockSending:                 opts.BlockSending,
		blockAttachments:             opts.BlockAttachments,
		requireOnlineForSend:         opts.RequireOnlineForSend,
		unfilteredByChat:             map[string][]store.Message{},
		newMessageStateByChat:        map[string]newMessageState{},
		composerMentionsByChat:       map[string][]store.MessageMention{},
		presenceByChat:               map[string]PresenceUpdate{},
		forwardSelected:              map[string]bool{},
		readReceiptInflight:          map[string]bool{},
		messageLoadInflight:          map[string]bool{},
		olderMessagesInflight:        map[string]bool{},
		historyRequestInflight:       map[string]bool{},
		outgoingMessageInflight:      map[string]bool{},
		presenceSubscribed:           map[string]bool{},
		messageLimitsByChat:          map[string]int{},
		historyRequestedByChat:       map[string]bool{},
		syncOverlay:                  initialSyncOverlay(opts.BlockLiveStartup),
		pollTerminalSize:             opts.PollTerminalSize,
	}
	if model.pasteAttachmentFromClipboard == nil {
		model.pasteAttachmentFromClipboard = opts.PasteImageFromClipboard
	}
	model.ensureSixelManager()
	model.reportActiveChatChanged()
	model.reportVisibleChatsChanged()
	return model
}

func normalizeConfig(cfg config.Config) config.Config {
	if cfg.LeaderKey == "" {
		cfg.LeaderKey = "space"
	}
	cfg.Keymap = config.NormalizeKeymap(cfg.Keymap)
	switch cfg.EmojiMode {
	case config.EmojiModeAuto, config.EmojiModeFull, config.EmojiModeCompat:
	default:
		cfg.EmojiMode = config.EmojiModeAuto
	}
	if strings.TrimSpace(cfg.IndicatorNormal) == "" {
		cfg.IndicatorNormal = config.IndicatorPywal
	}
	if strings.TrimSpace(cfg.IndicatorInsert) == "" {
		cfg.IndicatorInsert = config.IndicatorPywal
	}
	if strings.TrimSpace(cfg.IndicatorVisual) == "" {
		cfg.IndicatorVisual = config.IndicatorPywal
	}
	if strings.TrimSpace(cfg.IndicatorCommand) == "" {
		cfg.IndicatorCommand = config.IndicatorPywal
	}
	if strings.TrimSpace(cfg.IndicatorSearch) == "" {
		cfg.IndicatorSearch = config.IndicatorPywal
	}
	if strings.TrimSpace(cfg.NotificationBackend) == "" {
		cfg.NotificationBackend = "auto"
	}
	if cfg.DownloadsDir == "" {
		cfg.DownloadsDir = config.Default(config.Paths{}).DownloadsDir
	}
	return cfg
}

func (m Model) Close() error {
	var err error
	if m.audioProcess != nil {
		err = m.audioProcess.Stop()
	}
	if m.overlay != nil {
		if closeErr := m.overlay.Close(); err == nil {
			err = closeErr
		}
	}
	if m.sixel != nil {
		if closeErr := m.sixel.Close(); err == nil {
			err = closeErr
		}
	}
	return err
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
	return batchCmds(m.waitForLiveUpdateCmd(), m.terminalSizePollCmd())
}

func (m Model) waitForLiveUpdateCmd() tea.Cmd {
	if m.liveUpdates == nil {
		return nil
	}
	updates := m.liveUpdates
	return func() tea.Msg {
		update, ok := <-updates
		return liveUpdateMsg{Update: update, OK: ok}
	}
}

func (m Model) handleLiveUpdate(update LiveUpdate) (Model, tea.Cmd) {
	var cmds []tea.Cmd
	if update.ConnectionState != "" {
		previous := m.connectionState
		m.connectionState = update.ConnectionState
		if update.ConnectionState != ConnectionOnline {
			m.presenceSubscribed = map[string]bool{}
			m.presenceByChat = map[string]PresenceUpdate{}
		} else if previous != ConnectionOnline {
			m.subscribeCurrentChatPresence()
		}
	}
	if strings.TrimSpace(update.Status) != "" {
		m.status = update.Status
	}
	if update.Sync != nil {
		var syncCmd tea.Cmd
		m, syncCmd = m.handleSyncProgress(*update.Sync)
		cmds = append(cmds, syncCmd)
	}
	if update.HistoryChatID != "" && update.HistoryMessages > 0 {
		m.addMessageLimit(update.HistoryChatID, update.HistoryMessages)
	}
	if update.HistoryChatID != "" && m.historyRequestedByChat != nil {
		delete(m.historyRequestedByChat, update.HistoryChatID)
	}
	if update.ReadChatID != "" && m.readReceiptInflight != nil {
		delete(m.readReceiptInflight, update.ReadChatID)
	}
	if strings.TrimSpace(update.PreferredChatID) != "" {
		m.refreshPreferredChatID = strings.TrimSpace(update.PreferredChatID)
	}
	if update.Presence.ChatID != "" {
		if m.presenceByChat == nil {
			m.presenceByChat = map[string]PresenceUpdate{}
		}
		presence := m.mergePresenceUpdate(update.Presence)
		if presenceEmpty(presence) {
			delete(m.presenceByChat, update.Presence.ChatID)
		} else {
			m.presenceByChat[presence.ChatID] = presence
			if presence.Typing && !presence.ExpiresAt.IsZero() {
				cmds = append(cmds, presenceExpiryCmd(presence.ChatID, presence.ExpiresAt))
			}
		}
	}

	if update.Refresh && m.reloadSnapshot != nil {
		if m.reloadInFlight {
			m.refreshQueued = true
		} else if !m.refreshDebouncePending {
			m.refreshDebouncePending = true
			cmds = append(cmds, refreshDebounceCmd())
		}
	}
	cmds = append(cmds, m.waitForLiveUpdateCmd())
	return m, batchCmds(cmds...)
}

func (m Model) mergePresenceUpdate(update PresenceUpdate) PresenceUpdate {
	presence := m.presenceByChat[update.ChatID]
	presence.ChatID = update.ChatID
	if strings.TrimSpace(update.SenderJID) != "" {
		presence.SenderJID = update.SenderJID
	}
	if strings.TrimSpace(update.Sender) != "" {
		presence.Sender = update.Sender
	}
	if update.TypingChanged {
		presence.TypingChanged = true
		presence.Typing = update.Typing
		if update.Typing {
			presence.ExpiresAt = update.ExpiresAt
			if presence.ExpiresAt.IsZero() {
				presence.ExpiresAt = time.Now().Add(presenceTTL)
			}
		} else {
			presence.ExpiresAt = time.Time{}
		}
	}
	if update.AvailabilityChanged {
		presence.AvailabilityChanged = true
		presence.Online = update.Online
		presence.LastSeen = update.LastSeen
	}
	return presence
}

func presenceEmpty(presence PresenceUpdate) bool {
	return !presence.Typing && !presence.Online && presence.LastSeen.IsZero()
}

func (m Model) handleSyncProgress(update SyncProgressUpdate) (Model, tea.Cmd) {
	m.syncOverlay.Generation++
	m.syncOverlay.Visible = update.Active || update.Completed
	m.syncOverlay.Active = update.Active
	m.syncOverlay.Completed = update.Completed
	m.syncOverlay.Title = strings.TrimSpace(update.Title)
	m.syncOverlay.Subtitle = strings.TrimSpace(update.Subtitle)
	m.syncOverlay.Total = max(0, update.Total)
	m.syncOverlay.Processed = max(0, update.Processed)
	m.syncOverlay.AppDataChanges = max(0, update.AppDataChanges)
	m.syncOverlay.Messages = max(0, update.Messages)
	m.syncOverlay.Notifications = max(0, update.Notifications)
	m.syncOverlay.Receipts = max(0, update.Receipts)
	if m.syncOverlay.Total > 0 && m.syncOverlay.Processed > m.syncOverlay.Total {
		m.syncOverlay.Processed = m.syncOverlay.Total
	}
	if update.Active {
		m.leaderPending = false
		m.leaderSequence = ""
		if syncProgressShouldReplaceStatus(m.status) {
			m.status = syncProgressStatus(update, "syncing WhatsApp updates")
		}
		return m, nil
	}
	if update.Completed {
		if strings.TrimSpace(m.status) == "" || strings.Contains(strings.ToLower(m.status), "syncing") {
			m.status = syncProgressStatus(update, "sync complete")
		}
		generation := m.syncOverlay.Generation
		return m, syncOverlayDoneCmd(generation)
	}
	m.syncOverlay = syncOverlayState{}
	return m, nil
}

func syncProgressStatus(update SyncProgressUpdate, fallback string) string {
	if title := strings.TrimSpace(update.Title); title != "" {
		return title
	}
	return fallback
}

func syncProgressShouldReplaceStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "" ||
		status == "ready" ||
		strings.Contains(status, "syncing") ||
		strings.Contains(status, "updating local database") ||
		strings.Contains(status, "connecting to whatsapp")
}

func syncOverlayDoneCmd(generation int) tea.Cmd {
	return tea.Tick(syncOverlayCompleteDelay, func(time.Time) tea.Msg {
		return syncOverlayDoneMsg{Generation: generation}
	})
}

const presenceTTL = 6 * time.Second
const ownPresenceIdle = 5 * time.Second

func presenceExpiryCmd(chatID string, expiresAt time.Time) tea.Cmd {
	delay := time.Until(expiresAt)
	if delay < 0 {
		delay = 0
	}
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return presenceExpiredMsg{ChatID: chatID, At: expiresAt}
	})
}

func refreshDebounceCmd() tea.Cmd {
	return tea.Tick(refreshDebounceDuration, func(time.Time) tea.Msg {
		return refreshDebouncedMsg{}
	})
}

func overlayResumeCmd(generation int) tea.Cmd {
	return tea.Tick(overlayResumeDelay, func(time.Time) tea.Msg {
		return overlayResumeMsg{Generation: generation}
	})
}

var currentTerminalSize = platformTerminalSize

func (m Model) terminalSizePollCmd() tea.Cmd {
	if !m.pollTerminalSize {
		return nil
	}
	return tea.Tick(terminalSizePollInterval, func(time.Time) tea.Msg {
		width, height, ok := currentTerminalSize()
		return terminalSizePolledMsg{Width: width, Height: height, OK: ok}
	})
}

func (m Model) handleRefreshDebounced() (Model, tea.Cmd) {
	m.refreshDebouncePending = false
	if m.reloadSnapshot == nil {
		return m, nil
	}
	if m.reloadInFlight {
		m.refreshQueued = true
		return m, nil
	}
	m.reloadInFlight = true
	return m, m.reloadSnapshotCmd()
}

func (m Model) reloadSnapshotCmd() tea.Cmd {
	if m.reloadSnapshot == nil {
		return nil
	}
	activeChatID := m.currentChat().ID
	if strings.TrimSpace(m.refreshPreferredChatID) != "" {
		activeChatID = strings.TrimSpace(m.refreshPreferredChatID)
	}
	reload := m.reloadSnapshot
	limit := m.messageLimitForChat(activeChatID)
	return func() tea.Msg {
		snapshot, err := reload(activeChatID, limit)
		return snapshotReloadedMsg{
			Snapshot:     snapshot,
			ActiveChatID: activeChatID,
			Err:          err,
		}
	}
}

func (m Model) handleSnapshotReloaded(msg snapshotReloadedMsg) (Model, tea.Cmd) {
	m.reloadInFlight = false
	if msg.Err != nil {
		m.status = fmt.Sprintf("refresh failed: %v", msg.Err)
		return m, m.nextQueuedRefreshCmd()
	}
	preferredChatID := m.currentChat().ID
	if preferredChatID == "" {
		preferredChatID = msg.ActiveChatID
	}
	if err := m.applySnapshot(msg.Snapshot, preferredChatID, m.messageFilter); err != nil {
		m.status = fmt.Sprintf("refresh failed: %v", err)
		return m, m.nextQueuedRefreshCmd()
	}
	if m.terminalOverlayBackendActive() {
		m.pauseOverlays(true, true)
	}
	m.refreshPreferredChatID = ""
	var activateCmd tea.Cmd
	if m.focus == FocusMessages {
		activateCmd = m.handleCurrentChatActivated()
	}
	return m, batchCmds(activateCmd, m.nextQueuedRefreshCmd())
}

func (m *Model) nextQueuedRefreshCmd() tea.Cmd {
	if !m.refreshQueued || m.reloadSnapshot == nil {
		return nil
	}
	m.refreshQueued = false
	m.reloadInFlight = true
	return m.reloadSnapshotCmd()
}

func (m Model) persistOutgoingMessageCmd(tempID, chatID, draftBody string, attachments []Attachment, request OutgoingMessage) tea.Cmd {
	persist := m.persistMessage
	if persist == nil {
		return nil
	}
	attachments = slices.Clone(attachments)
	return func() tea.Msg {
		message, err := persist(request)
		return outgoingMessagePersistedMsg{
			TempID:      tempID,
			ChatID:      chatID,
			Message:     message,
			DraftBody:   draftBody,
			Attachments: attachments,
			Err:         err,
		}
	}
}

func (m Model) saveDraftCmd(chatID, body string) tea.Cmd {
	save := m.saveDraft
	if save == nil || strings.TrimSpace(chatID) == "" {
		return nil
	}
	return func() tea.Msg {
		return draftSavedMsg{ChatID: chatID, Body: body, Err: save(chatID, body)}
	}
}

func (m Model) markReadCmd(chat store.Chat, messages []store.Message, manual bool) tea.Cmd {
	markRead := m.markRead
	if markRead == nil {
		return nil
	}
	messages = slices.Clone(messages)
	chatID := chat.ID
	return func() tea.Msg {
		return markReadFinishedMsg{ChatID: chatID, Manual: manual, Err: markRead(chat, messages)}
	}
}

func (m Model) sendReactionCmd(message store.Message, emoji string) tea.Cmd {
	sendReaction := m.sendReaction
	if sendReaction == nil {
		return nil
	}
	return func() tea.Msg {
		return reactionFinishedMsg{Emoji: emoji, Err: sendReaction(message, emoji)}
	}
}

func (m Model) retryMessageCmd(message store.Message) tea.Cmd {
	retry := m.retryMessage
	if retry == nil {
		return nil
	}
	return func() tea.Msg {
		retried, err := retry(message)
		return retryMessageFinishedMsg{Original: message, Message: retried, Err: err}
	}
}

func (m Model) sendStickerCmd(chatID string, sticker store.RecentSticker) tea.Cmd {
	sendSticker := m.sendSticker
	if sendSticker == nil {
		return nil
	}
	return func() tea.Msg {
		message, err := sendSticker(chatID, sticker)
		return stickerSentMsg{ChatID: chatID, Sticker: sticker, Message: message, Err: err}
	}
}

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

func (m Model) searchMessagesFilterCmd(generation int, chatID, query string) tea.Cmd {
	searchMessages := m.searchMessages
	if searchMessages == nil || strings.TrimSpace(chatID) == "" {
		return nil
	}
	return func() tea.Msg {
		messages, err := searchMessages(chatID, query, messageLoadLimit)
		return messageFilterAppliedMsg{Generation: generation, ChatID: chatID, Query: query, Messages: messages, Err: err}
	}
}

func (m Model) reloadMessagesForFilterClearCmd(generation int, chatID string, limit int) tea.Cmd {
	loadMessages := m.loadMessages
	if loadMessages == nil || strings.TrimSpace(chatID) == "" {
		return nil
	}
	return func() tea.Msg {
		messages, err := loadMessages(chatID, limit)
		return messageFilterClearedMsg{Generation: generation, ChatID: chatID, Messages: messages, Err: err}
	}
}

func (m Model) mentionCandidatesCmd(generation int, chatID, query string) tea.Cmd {
	searchMentionCandidates := m.searchMentionCandidates
	if searchMentionCandidates == nil || strings.TrimSpace(chatID) == "" {
		return nil
	}
	return func() tea.Msg {
		candidates, err := searchMentionCandidates(chatID, query, mentionCandidateLimit)
		return mentionCandidatesLoadedMsg{Generation: generation, ChatID: chatID, Query: query, Candidates: candidates, Err: err}
	}
}

func (m Model) handleOutgoingMessagePersisted(msg outgoingMessagePersistedMsg) (Model, tea.Cmd) {
	delete(m.outgoingMessageInflight, msg.TempID)
	if msg.Err != nil {
		m.removeMessageByID(msg.ChatID, msg.TempID)
		m.mode = ModeInsert
		m.focus = FocusMessages
		m.composer = msg.DraftBody
		m.attachments = slices.Clone(msg.Attachments)
		m.status = fmt.Sprintf("send failed: %v", msg.Err)
		m.localSetDraft(msg.ChatID, msg.DraftBody)
		return m, m.saveDraftCmd(msg.ChatID, msg.DraftBody)
	}
	message := msg.Message
	if message.ID == "" {
		message.ID = msg.TempID
	}
	if message.ChatID == "" {
		message.ChatID = msg.ChatID
	}
	if len(message.Media) == 0 && len(msg.Attachments) > 0 {
		message.Media = m.mediaForLocalMessage(message.ID, msg.Attachments)
	}
	if !m.replaceMessageByID(msg.ChatID, msg.TempID, message) {
		m.appendMessageToChat(msg.ChatID, message)
	}
	if msg.ChatID == m.currentChat().ID {
		m.messageCursor = len(m.messagesByChat[msg.ChatID]) - 1
		m.messageScrollTop = m.messageCursor
	}
	m.rebuildSearchMatches()
	m.status = "message queued"
	return m, m.saveDraftCmd(msg.ChatID, "")
}

func (m Model) handleDraftSaved(msg draftSavedMsg) Model {
	if msg.Err == nil {
		return m
	}
	if strings.TrimSpace(msg.Body) == "" {
		m.status = fmt.Sprintf("clear draft failed: %v", msg.Err)
	} else {
		m.status = fmt.Sprintf("save draft failed: %v", msg.Err)
	}
	return m
}

func (m Model) handleMarkReadFinished(msg markReadFinishedMsg) Model {
	delete(m.readReceiptInflight, msg.ChatID)
	if msg.Err != nil {
		if msg.Manual {
			m.status = fmt.Sprintf("mark read failed: %v", msg.Err)
		}
		return m
	}
	if msg.Manual {
		m.status = "mark read queued"
	}
	return m
}

func (m Model) handleReactionFinished(msg reactionFinishedMsg) Model {
	if msg.Err != nil {
		m.status = fmt.Sprintf("reaction failed: %v", msg.Err)
		return m
	}
	if strings.TrimSpace(msg.Emoji) == "" {
		m.status = "reaction clear queued"
	} else {
		m.status = "reaction queued"
	}
	return m
}

func (m Model) handleRetryMessageFinished(msg retryMessageFinishedMsg) Model {
	if msg.Err != nil {
		m.status = fmt.Sprintf("retry failed: %v", msg.Err)
		return m
	}
	retried := msg.Message
	if len(retried.Media) == 0 && len(msg.Original.Media) == 1 {
		retried.Media = []store.MediaMetadata{msg.Original.Media[0]}
		retried.Media[0].MessageID = retried.ID
	}
	chatID := m.currentChat().ID
	if chatID == "" {
		chatID = retried.ChatID
	}
	if chatID != "" && retried.ID != "" {
		m.appendMessageToChat(chatID, retried)
		if chatID == m.currentChat().ID {
			m.messageCursor = len(m.messagesByChat[chatID]) - 1
			m.messageScrollTop = m.messageCursor
			m.focus = FocusMessages
		}
		m.rebuildSearchMatches()
	}
	m.status = "retry queued"
	return m
}

func (m Model) handleStickerSent(msg stickerSentMsg) Model {
	if msg.Err != nil {
		m.status = fmt.Sprintf("sticker send failed: %v", msg.Err)
		return m
	}
	message := msg.Message
	if message.ID == "" {
		m.status = "sticker queued"
		return m
	}
	if message.ChatID == "" {
		message.ChatID = msg.ChatID
	}
	m.appendMessageToChat(msg.ChatID, message)
	if msg.ChatID == m.currentChat().ID {
		m.messageCursor = len(m.messagesByChat[msg.ChatID]) - 1
		m.messageScrollTop = m.messageCursor
	}
	m.rebuildSearchMatches()
	m.status = "sticker queued"
	return m
}

func (m Model) handleMessagesLoaded(msg messagesLoadedMsg) (Model, tea.Cmd) {
	delete(m.messageLoadInflight, msg.ChatID)
	if msg.Err != nil {
		m.status = fmt.Sprintf("load messages failed: %v", msg.Err)
		return m, nil
	}
	m.messagesByChat[msg.ChatID] = slices.Clone(msg.Messages)
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
		m.messagesByChat[msg.ChatID] = combined
		m.addMessageLimit(msg.ChatID, len(msg.Messages))
		if msg.ChatID == m.currentChat().ID {
			m.messageCursor = len(msg.Messages) - 1
			m.messageScrollTop = m.messageCursor
			m.pauseOverlays(true, false)
		}
		m.status = fmt.Sprintf("loaded %d older local message(s)", len(msg.Messages))
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

func (m Model) handleMessageFilterApplied(msg messageFilterAppliedMsg) Model {
	if msg.Generation != m.filterGeneration || msg.ChatID != m.currentChat().ID || msg.Query != strings.TrimSpace(m.messageFilter) {
		return m
	}
	if msg.Err != nil {
		m.status = fmt.Sprintf("filter failed: %v", msg.Err)
		return m
	}
	m.messagesByChat[msg.ChatID] = slices.Clone(msg.Messages)
	m.messageCursor = 0
	m.messageScrollTop = 0
	m.status = fmt.Sprintf("message filter: %s", msg.Query)
	return m
}

func (m Model) handleMessageFilterCleared(msg messageFilterClearedMsg) Model {
	if msg.Generation != m.filterGeneration || msg.ChatID != m.currentChat().ID || strings.TrimSpace(m.messageFilter) != "" {
		return m
	}
	if msg.Err != nil {
		m.status = fmt.Sprintf("filter failed: %v", msg.Err)
		return m
	}
	m.messagesByChat[msg.ChatID] = slices.Clone(msg.Messages)
	m.messageCursor = clamp(m.messageCursor, 0, max(0, len(msg.Messages)-1))
	m.messageScrollTop = clamp(m.messageScrollTop, 0, max(0, len(msg.Messages)-1))
	m.status = "message filter cleared"
	return m
}

func (m Model) handleMentionCandidatesLoaded(msg mentionCandidatesLoadedMsg) Model {
	if msg.Generation != m.mentionSearchGeneration || !m.mentionActive || msg.ChatID != m.currentChat().ID || msg.Query != m.mentionQuery {
		return m
	}
	if msg.Err != nil {
		m.status = fmt.Sprintf("mention search failed: %v", msg.Err)
		return m
	}
	m.mentionCandidates = slices.Clone(msg.Candidates)
	if len(m.mentionCandidates) == 0 {
		m.mentionCursor = 0
		return m
	}
	m.mentionCursor = clamp(m.mentionCursor, 0, len(m.mentionCandidates)-1)
	return m
}

func (m *Model) appendMessageToChat(chatID string, message store.Message) {
	m.messagesByChat[chatID] = append(m.messagesByChat[chatID], message)
	if base, ok := m.unfilteredByChat[chatID]; ok {
		m.unfilteredByChat[chatID] = append(base, message)
	}
}

func (m *Model) replaceMessageByID(chatID, messageID string, replacement store.Message) bool {
	replaced := replaceMessageInSlice(m.messagesByChat[chatID], messageID, replacement)
	if base, ok := m.unfilteredByChat[chatID]; ok {
		if replaceMessageInSlice(base, messageID, replacement) {
			m.unfilteredByChat[chatID] = base
			replaced = true
		}
	}
	return replaced
}

func replaceMessageInSlice(messages []store.Message, messageID string, replacement store.Message) bool {
	for i := range messages {
		if messages[i].ID == messageID {
			messages[i] = replacement
			return true
		}
	}
	return false
}

func (m *Model) removeMessageByID(chatID, messageID string) {
	m.messagesByChat[chatID] = removeMessageFromSlice(m.messagesByChat[chatID], messageID)
	if base, ok := m.unfilteredByChat[chatID]; ok {
		m.unfilteredByChat[chatID] = removeMessageFromSlice(base, messageID)
	}
	if chatID == m.currentChat().ID {
		m.messageCursor = clamp(m.messageCursor, 0, max(0, len(m.messagesByChat[chatID])-1))
		m.messageScrollTop = clamp(m.messageScrollTop, 0, max(0, len(m.messagesByChat[chatID])-1))
	}
}

func removeMessageFromSlice(messages []store.Message, messageID string) []store.Message {
	out := messages[:0]
	for _, message := range messages {
		if message.ID != messageID {
			out = append(out, message)
		}
	}
	return out
}

func withPreviewResult(updated tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	next := updated.(Model)
	return next.withPreviewCmd(cmd)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if token, ok := shiftedEnterTokenFromMsg(msg); ok {
		updated, cmd := m.handleSpecialKeyToken(token)
		return withPreviewResult(updated, cmd)
	}

	switch msg := msg.(type) {
	case liveUpdateMsg:
		if !msg.OK {
			m.liveUpdates = nil
			return m, nil
		}
		next, cmd := m.handleLiveUpdate(msg.Update)
		return next.withPreviewCmd(cmd)
	case snapshotReloadedMsg:
		next, cmd := m.handleSnapshotReloaded(msg)
		return next.withPreviewCmd(cmd)
	case outgoingMessagePersistedMsg:
		next, cmd := m.handleOutgoingMessagePersisted(msg)
		return next.withPreviewCmd(cmd)
	case draftSavedMsg:
		return m.handleDraftSaved(msg), nil
	case markReadFinishedMsg:
		return m.handleMarkReadFinished(msg), nil
	case reactionFinishedMsg:
		return m.handleReactionFinished(msg), nil
	case retryMessageFinishedMsg:
		return m.handleRetryMessageFinished(msg).withPreviewCmd(nil)
	case stickerSentMsg:
		return m.handleStickerSent(msg).withPreviewCmd(nil)
	case messagesLoadedMsg:
		next, cmd := m.handleMessagesLoaded(msg)
		return next.withPreviewCmd(cmd)
	case olderMessagesLoadedMsg:
		next, cmd := m.handleOlderMessagesLoaded(msg)
		return next.withPreviewCmd(cmd)
	case historyRequestedMsg:
		return m.handleHistoryRequested(msg), nil
	case messageFilterAppliedMsg:
		return m.handleMessageFilterApplied(msg).withPreviewCmd(nil)
	case messageFilterClearedMsg:
		return m.handleMessageFilterCleared(msg).withPreviewCmd(nil)
	case mentionCandidatesLoadedMsg:
		return m.handleMentionCandidatesLoaded(msg), nil
	case refreshDebouncedMsg:
		next, cmd := m.handleRefreshDebounced()
		return next.withPreviewCmd(cmd)
	case syncOverlayDoneMsg:
		if msg.Generation == m.syncOverlay.Generation && m.syncOverlay.Completed {
			m.syncOverlay = syncOverlayState{}
		}
		return m, nil
	case presenceExpiredMsg:
		if presence, ok := m.presenceByChat[msg.ChatID]; ok && !presence.ExpiresAt.After(msg.At) {
			presence.Typing = false
			presence.ExpiresAt = time.Time{}
			if presenceEmpty(presence) {
				delete(m.presenceByChat, msg.ChatID)
			} else {
				m.presenceByChat[msg.ChatID] = presence
			}
		}
		return m, nil
	case ownPresenceIdleMsg:
		if msg.Generation == m.ownPresenceGeneration && msg.ChatID == m.ownPresenceChatID && m.ownPresenceComposing {
			m.sendOwnPresence(msg.ChatID, false)
		}
		return m, nil
	case tea.FocusMsg:
		m.reportAppFocusChanged(true)
		return m, nil
	case tea.BlurMsg:
		m.reportAppFocusChanged(false)
		return m, nil
	case tea.WindowSizeMsg:
		viewportChanged := msg.Width != m.width || msg.Height != m.height
		m.width = msg.Width
		m.height = msg.Height
		m.compactLayout = msg.Width < 110
		if (!m.infoPaneVisible || m.compactLayout) && m.focus == FocusPreview {
			m.focus = FocusMessages
		}
		m.keepActiveChatVisible()
		if viewportChanged && m.terminalOverlayBackendActive() {
			m.pauseOverlays(true, true)
		}
		return m.withPreviewCmd(nil)
	case clipboardCopiedMsg:
		if msg.Err != nil {
			m.status = fmt.Sprintf("yanked %d message(s); clipboard failed: %v", msg.Count, msg.Err)
		} else {
			m.status = fmt.Sprintf("yanked %d message(s) to clipboard", msg.Count)
		}
		return m, nil
	case ClipboardAttachmentPastedMsg:
		return m.handleClipboardAttachmentPasted(msg)
	case ClipboardImageCopiedMsg:
		return withPreviewResult(m.handleClipboardImageCopied(msg))
	case AttachmentPickedMsg:
		return m.handlePickedAttachment(msg)
	case StickerPickedMsg:
		return m.handlePickedSticker(msg)
	case mediaPreviewReadyMsg:
		return withPreviewResult(m.handleMediaPreviewReady(msg))
	case mediaDownloadedMsg:
		return withPreviewResult(m.handleMediaDownloaded(msg))
	case mediaSavedMsg:
		return withPreviewResult(m.handleMediaSaved(msg))
	case audioStartedMsg:
		return withPreviewResult(m.handleAudioStarted(msg))
	case audioFinishedMsg:
		return m.handleAudioFinished(msg)
	case MediaOpenFinishedMsg:
		m.clearMediaDownloadInFlight(msg.MessageID)
		if msg.MessageID != "" && strings.TrimSpace(msg.Media.LocalPath) != "" {
			if updated, _, _ := m.updateLoadedMedia(msg.MessageID, msg.Media); updated && m.saveMedia != nil {
				if err := m.saveMedia(msg.Media); err != nil {
					m.status = fmt.Sprintf("open metadata failed: %v", err)
					return m, nil
				}
			}
		}
		if msg.Err != nil {
			m.status = fmt.Sprintf("open failed: %s", shortError(msg.Err))
		} else {
			m.status = fmt.Sprintf("opened media: %s", msg.Path)
		}
		return m.withPreviewCmd(nil)
	case MessageDeletedForEveryoneMsg:
		m.handleMessageDeletedForEveryone(msg)
		return m.withPreviewCmd(nil)
	case MessageEditedMsg:
		m.handleMessageEdited(msg)
		return m.withPreviewCmd(nil)
	case ForwardMessagesFinishedMsg:
		m.handleForwardMessagesFinished(msg)
		return m.withPreviewCmd(nil)
	case mediaOverlayMsg:
		if msg.Err != nil {
			if !m.overlaySyncPending || msg.Signature != m.overlayPendingSignature {
				return m, nil
			}
			failedOverlay := m.overlay
			retry := m.overlayConsecutiveFailures == 0
			m.overlaySignature = ""
			m.overlaySyncPending = false
			m.overlayPendingSignature = ""
			m.overlay = nil
			m.overlayConsecutiveFailures++
			m.status = fmt.Sprintf("overlay failed: %s", shortError(msg.Err))
			closeCmd := closeOverlayManagerCmd(failedOverlay)
			if !retry {
				return m, closeCmd
			}
			return m.withPreviewCmd(closeCmd)
		}
		if m.overlaySyncPending && msg.Signature == m.overlayPendingSignature {
			m.overlaySignature = msg.Signature
			m.overlaySyncPending = false
			m.overlayPendingSignature = ""
			m.overlayConsecutiveFailures = 0
		}
		if m.mediaOverlayPaused || m.avatarOverlayPaused {
			return m.withPreviewCmd(nil)
		}
		return m, nil
	case sixelOverlayMsg:
		if msg.Err != nil {
			m.sixelSignature = ""
			m.sixelSyncPending = false
			m.sixelPendingSignature = ""
			m.status = fmt.Sprintf("sixel failed: %s", shortError(msg.Err))
			return m, nil
		}
		if m.sixelSyncPending && msg.Signature == m.sixelPendingSignature {
			m.sixelSignature = msg.Signature
			m.sixelSyncPending = false
			m.sixelPendingSignature = ""
		}
		if m.mediaOverlayPaused || m.avatarOverlayPaused {
			return m.withPreviewCmd(nil)
		}
		return m, nil
	case overlayResumeMsg:
		if msg.Generation != m.overlayPauseGeneration {
			return m, nil
		}
		if m.overlaySyncPending || m.sixelSyncPending {
			m.overlayResumeQueued = 0
			return m, nil
		}
		if m.overlay != nil {
			m.overlay.Invalidate()
		}
		if m.sixel != nil {
			m.sixel.Invalidate()
		}
		m.mediaOverlayPaused = false
		m.avatarOverlayPaused = false
		m.overlayResumeQueued = 0
		return m.withPreviewCmd(nil)
	case terminalSizePolledMsg:
		nextPoll := m.terminalSizePollCmd()
		if !msg.OK || msg.Width <= 0 || msg.Height <= 0 {
			return m, nextPoll
		}
		if msg.Width == m.width && msg.Height == m.height {
			return m, nextPoll
		}
		m.width = msg.Width
		m.height = msg.Height
		m.compactLayout = msg.Width < 110
		if (!m.infoPaneVisible || m.compactLayout) && m.focus == FocusPreview {
			m.focus = FocusMessages
		}
		m.keepActiveChatVisible()
		next, cmd := m.withPreviewCmd(nil)
		return next, batchCmds(cmd, nextPoll)
	case tea.KeyMsg:
		updated, cmd := m.handleKey(msg)
		return withPreviewResult(updated, cmd)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.keyMatches(msg, m.config.Keymap.GlobalQuit) {
		m.quitting = true
		return m, tea.Quit
	}
	if m.inlineFallbackPrompt {
		return m.handleInlineFallbackPrompt(msg)
	}
	if m.syncOverlay.Visible {
		return m, nil
	}
	if m.leaderPending {
		return m.handleLeaderKey(msg)
	}

	switch m.mode {
	case ModeInsert:
		return m.updateInsert(msg)
	case ModeCommand:
		return m.updateCommand(msg)
	case ModeSearch:
		return m.updateSearch(msg)
	case ModeConfirm:
		return m.updateConfirm(msg)
	case ModeForward:
		return m.updateForward(msg)
	case ModeVisual:
		return m.updateVisual(msg)
	default:
		return m.updateNormal(msg)
	}
}

func (m Model) handleSpecialKeyToken(token string) (tea.Model, tea.Cmd) {
	if token == "shift+enter" && m.mode == ModeInsert {
		keys := m.config.Keymap
		if m.keyTokenMatches(token, keys.InsertNewline) || m.keyTokenMatches(token, keys.InsertNewlineAlt) {
			m.clearMentionState()
			m.composer += "\n"
			m.sendOwnPresence(m.currentChat().ID, true)
			return m, ownPresenceIdleCmd(m.currentChat().ID, m.ownPresenceGeneration)
		}
	}
	if m.mode == ModeNormal && !m.helpVisible && !m.inlineFallbackPrompt && !m.syncOverlay.Visible && !m.leaderPending {
		action := m.normalActionForToken(token)
		if action != "" {
			return m.runNormalAction(action, m.consumeCount())
		}
	}
	return m, nil
}

func (m Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keys := m.config.Keymap
	if m.helpVisible {
		if m.keyMatches(msg, keys.HelpClose) || m.keyMatches(msg, keys.HelpCloseAlt) {
			m.helpVisible = false
			m.status = "help closed"
			return m, nil
		}
		return m, nil
	}

	if m.leaderPending {
		return m.handleLeaderKey(msg)
	}
	if m.keyMatches(msg, keys.NormalCancel) {
		m.pendingCount = 0
		if m.activeSearch != "" || strings.TrimSpace(m.lastSearch) != "" || len(m.searchMatches) > 0 || m.searchIndex != -1 {
			m.clearSearch()
			m.status = "search cleared"
		}
		return m, nil
	}
	if m.keyMatchesLeaderStart(msg) {
		m.leaderPending = true
		m.leaderSequence = ""
		m.pendingCount = 0
		m.status = fmt.Sprintf("leader: %s", leaderDisplay(m.config.LeaderKey))
		return m, nil
	}
	if m.captureCount(msg) {
		return m, nil
	}
	count := m.consumeCount()

	action := m.normalActionForKey(msg)
	if action == "" {
		return m, nil
	}
	return m.runNormalAction(action, count)
}

const (
	normalActionQuit              = "quit"
	normalActionHelp              = "help"
	normalActionInsert            = "insert"
	normalActionReply             = "reply"
	normalActionRetryFailedMedia  = "retry_failed_media"
	normalActionVisual            = "visual"
	normalActionCommand           = "command"
	normalActionSearch            = "search"
	normalActionFocusNext         = "focus_next"
	normalActionFocusPrevious     = "focus_previous"
	normalActionFocusLeft         = "focus_left"
	normalActionFocusRightReply   = "focus_right_or_reply"
	normalActionMoveDown          = "move_down"
	normalActionMoveUp            = "move_up"
	normalActionGoTop             = "go_top"
	normalActionGoBottom          = "go_bottom"
	normalActionOpen              = "open"
	normalActionOpenMedia         = "open_media"
	normalActionOpenMediaDetached = "open_media_detached"
	normalActionYankMessage       = "yank_message"
	normalActionEditMessage       = "edit_message"
	normalActionPickSticker       = "pick_sticker"
	normalActionSearchNext        = "search_next"
	normalActionSearchPrevious    = "search_previous"
	normalActionToggleUnread      = "toggle_unread"
	normalActionTogglePinned      = "toggle_pinned"
	normalActionCopyImage         = "copy_image"
	normalActionSaveMedia         = "save_media"
	normalActionUnloadPreviews    = "unload_previews"
	normalActionDeleteForEveryone = "delete_for_everyone"
)

func (m Model) normalActionForKey(msg tea.KeyMsg) string {
	for _, binding := range m.normalActionBindings() {
		if m.keyMatches(msg, binding.binding) {
			return binding.action
		}
	}
	return ""
}

func (m Model) normalActionForToken(token string) string {
	for _, binding := range m.normalActionBindings() {
		if m.keyTokenMatches(token, binding.binding) {
			return binding.action
		}
	}
	return ""
}

func (m Model) normalActionForLeaderSequence(sequence []string) (action string, exact bool, prefix bool) {
	for _, binding := range m.normalActionBindings() {
		matches, partial := leaderBindingMatch(binding.binding, sequence)
		if matches {
			return binding.action, true, false
		}
		if partial {
			prefix = true
		}
	}
	return "", false, prefix
}

type normalActionBinding struct {
	binding string
	action  string
}

func (m Model) normalActionBindings() []normalActionBinding {
	keys := m.config.Keymap
	return []normalActionBinding{
		{binding: keys.NormalQuit, action: normalActionQuit},
		{binding: keys.NormalHelp, action: normalActionHelp},
		{binding: keys.NormalInsert, action: normalActionInsert},
		{binding: keys.NormalReply, action: normalActionReply},
		{binding: keys.NormalRetryFailedMedia, action: normalActionRetryFailedMedia},
		{binding: keys.NormalVisual, action: normalActionVisual},
		{binding: keys.NormalCommand, action: normalActionCommand},
		{binding: keys.NormalSearch, action: normalActionSearch},
		{binding: keys.NormalFocusNext, action: normalActionFocusNext},
		{binding: keys.NormalFocusPrevious, action: normalActionFocusPrevious},
		{binding: keys.NormalFocusLeft, action: normalActionFocusLeft},
		{binding: keys.NormalFocusRightOrReply, action: normalActionFocusRightReply},
		{binding: keys.NormalMoveDown, action: normalActionMoveDown},
		{binding: keys.NormalMoveUp, action: normalActionMoveUp},
		{binding: keys.NormalGoTop, action: normalActionGoTop},
		{binding: keys.NormalGoBottom, action: normalActionGoBottom},
		{binding: keys.NormalOpen, action: normalActionOpen},
		{binding: keys.NormalOpenMedia, action: normalActionOpenMedia},
		{binding: keys.NormalOpenMediaDetached, action: normalActionOpenMediaDetached},
		{binding: keys.NormalYankMessage, action: normalActionYankMessage},
		{binding: keys.NormalEditMessage, action: normalActionEditMessage},
		{binding: keys.NormalPickSticker, action: normalActionPickSticker},
		{binding: keys.NormalSearchNext, action: normalActionSearchNext},
		{binding: keys.NormalSearchPrevious, action: normalActionSearchPrevious},
		{binding: keys.NormalToggleUnread, action: normalActionToggleUnread},
		{binding: keys.NormalTogglePinned, action: normalActionTogglePinned},
		{binding: keys.NormalCopyImage, action: normalActionCopyImage},
		{binding: keys.NormalSaveMedia, action: normalActionSaveMedia},
		{binding: keys.NormalUnloadPreviews, action: normalActionUnloadPreviews},
		{binding: keys.NormalDeleteForEverybody, action: normalActionDeleteForEveryone},
	}
}

func (m Model) runNormalAction(action string, count int) (tea.Model, tea.Cmd) {
	switch action {
	case normalActionQuit:
		m.quitting = true
		return m, tea.Quit
	case normalActionHelp:
		m.helpVisible = true
		m.status = "help"
	case normalActionInsert:
		if len(m.chats) == 0 {
			m.status = "no chat selected"
			return m, nil
		}
		return m.beginInsert(nil)
	case normalActionReply:
		return m.beginReplyToFocusedMessage()
	case normalActionRetryFailedMedia:
		return m, m.retryFocusedMediaMessage()
	case normalActionVisual:
		if len(m.currentMessages()) == 0 {
			m.status = "no messages to select"
			return m, nil
		}
		m.mode = ModeVisual
		m.focus = FocusMessages
		m.visualAnchor = m.messageCursor
	case normalActionCommand:
		m.mode = ModeCommand
		m.commandLine = ""
	case normalActionSearch:
		m.mode = ModeSearch
		m.searchLine = ""
	case normalActionFocusNext:
		return m, m.cycleFocus(1)
	case normalActionFocusPrevious:
		return m, m.cycleFocus(-1)
	case normalActionFocusLeft:
		return m, m.moveFocus(-1)
	case normalActionFocusRightReply:
		if m.focus == FocusMessages && (!m.infoPaneVisible || m.compactLayout) {
			return m.beginReplyToFocusedMessage()
		}
		return m, m.moveFocus(1)
	case normalActionMoveDown:
		return m, m.moveCursor(count)
	case normalActionMoveUp:
		return m, m.moveCursor(-count)
	case normalActionGoTop:
		if m.focus == FocusMessages {
			m.messageCursor = 0
			m.messageScrollTop = 0
			m.pauseOverlays(true, false)
		} else {
			previousChat := m.activeChat
			previousScrollTop := m.chatScrollTop
			m.activeChat = 0
			m.chatScrollTop = 0
			loadCmd := m.ensureCurrentMessagesLoaded(true, false)
			if m.activeChat != previousChat {
				m.pauseOverlays(true, m.chatScrollTop != previousScrollTop)
			}
			return m, loadCmd
		}
	case normalActionGoBottom:
		if m.focus == FocusMessages {
			if messageCount := len(m.currentMessages()); messageCount > 0 {
				target := messageCount - 1
				if count > 1 {
					target = count - 1
				}
				m.messageCursor = clamp(target, 0, messageCount-1)
				m.messageScrollTop = m.messageCursor
				if m.messageCursor == messageCount-1 {
					m.clearNewMessagesBelow(m.currentChat().ID)
				}
				m.pauseOverlays(true, false)
			}
		} else {
			if chatCount := len(m.chats); chatCount > 0 {
				previousChat := m.activeChat
				previousScrollTop := m.chatScrollTop
				target := chatCount - 1
				if count > 1 {
					target = count - 1
				}
				m.activeChat = clamp(target, 0, chatCount-1)
				m.keepActiveChatVisible()
				loadCmd := m.ensureCurrentMessagesLoaded(true, false)
				if m.activeChat != previousChat {
					m.pauseOverlays(true, m.chatScrollTop != previousScrollTop)
				}
				return m, loadCmd
			}
		}
	case normalActionOpen:
		if m.focus == FocusChats {
			if len(m.chats) == 0 {
				m.status = "no chat selected"
				return m, nil
			}
			m.focus = FocusMessages
			activateCmd := m.ensureCurrentMessagesLoaded(true, true)
			m.status = fmt.Sprintf("opened %s", m.currentChat().DisplayTitle())
			return m, activateCmd
		} else if m.focus == FocusMessages || m.focus == FocusPreview {
			return m.activateFocusedMediaPreview()
		}
	case normalActionOpenMedia:
		return m.openFocusedMedia()
	case normalActionOpenMediaDetached:
		return m.openFocusedMediaDetached()
	case normalActionYankMessage:
		return m.yankFocusedMessage()
	case normalActionEditMessage:
		return m.beginEditFocusedMessage()
	case normalActionPickSticker:
		return m.startStickerPicker()
	case normalActionSearchNext:
		return m, m.advanceSearch(1)
	case normalActionSearchPrevious:
		return m, m.advanceSearch(-1)
	case normalActionToggleUnread:
		if err := m.setUnreadOnly(!m.unreadOnly); err != nil {
			m.status = fmt.Sprintf("filter failed: %v", err)
			return m, nil
		}
	case normalActionTogglePinned:
		if err := m.setPinnedFirst(!m.pinnedFirst); err != nil {
			m.status = fmt.Sprintf("sort failed: %v", err)
			return m, nil
		}
	case normalActionCopyImage:
		return m.copyFocusedImage()
	case normalActionSaveMedia:
		return m.saveFocusedMedia()
	case normalActionUnloadPreviews:
		return m.clearMediaPreviews("media previews unloaded")
	case normalActionDeleteForEveryone:
		m.armDeleteFocusedMessageForEveryone()
	default:
		return m, nil
	}

	return m, nil
}

func (m Model) beginReplyToFocusedMessage() (tea.Model, tea.Cmd) {
	message, ok := m.focusedMessage()
	if !ok {
		m.status = "no message selected"
		return m, nil
	}
	return m.beginInsert(&message)
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
	m.attachments = nil
	m.replyTo = nil
	target := message
	m.editTarget = &target
	activateCmd := m.handleCurrentChatActivated()
	m.sendOwnPresence(m.currentChat().ID, true)
	m.status = "editing message"
	return m, batchCmds(activateCmd, ownPresenceIdleCmd(m.currentChat().ID, m.ownPresenceGeneration))
}

func (m Model) beginInsert(quote *store.Message) (tea.Model, tea.Cmd) {
	if len(m.chats) == 0 || m.currentChat().ID == "" {
		m.status = "no chat selected"
		return m, nil
	}
	m.mode = ModeInsert
	m.focus = FocusMessages
	m.composer = m.draftsByChat[m.currentChat().ID]
	m.composerMentions = slices.Clone(m.composerMentionsByChat[m.currentChat().ID])
	m.clearMentionState()
	if quote != nil {
		quoted := *quote
		m.replyTo = &quoted
		m.status = "replying"
	} else {
		m.replyTo = nil
	}
	m.editTarget = nil
	activateCmd := m.handleCurrentChatActivated()
	m.sendOwnPresence(m.currentChat().ID, true)
	return m, batchCmds(activateCmd, ownPresenceIdleCmd(m.currentChat().ID, m.ownPresenceGeneration))
}

func ownPresenceIdleCmd(chatID string, generation int) tea.Cmd {
	if strings.TrimSpace(chatID) == "" {
		return nil
	}
	return tea.Tick(ownPresenceIdle, func(time.Time) tea.Msg {
		return ownPresenceIdleMsg{ChatID: chatID, Generation: generation}
	})
}

func (m *Model) sendOwnPresence(chatID string, composing bool) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" || m.sendPresence == nil || m.connectionState != ConnectionOnline {
		return
	}
	if composing {
		m.ownPresenceGeneration++
		if m.ownPresenceComposing && m.ownPresenceChatID == chatID {
			return
		}
		m.ownPresenceChatID = chatID
		m.ownPresenceComposing = true
		_ = m.sendPresence(chatID, true)
		return
	}
	if !m.ownPresenceComposing || m.ownPresenceChatID != chatID {
		return
	}
	m.ownPresenceGeneration++
	m.ownPresenceComposing = false
	_ = m.sendPresence(chatID, false)
}

func (m *Model) handleCurrentChatActivated() tea.Cmd {
	m.subscribeCurrentChatPresence()
	return m.markCurrentChatRead(false)
}

func (m *Model) subscribeCurrentChatPresence() {
	if m.subscribePresence == nil || m.connectionState != ConnectionOnline {
		return
	}
	chat := m.currentChat()
	chatID := strings.TrimSpace(chat.ID)
	if chatID == "" || chat.Kind == "group" || strings.HasSuffix(chat.JID, "@g.us") {
		return
	}
	if m.presenceSubscribed == nil {
		m.presenceSubscribed = map[string]bool{}
	}
	if m.presenceSubscribed[chatID] {
		return
	}
	if err := m.subscribePresence(chatID); err != nil {
		return
	}
	m.presenceSubscribed[chatID] = true
}

func (m *Model) markCurrentChatRead(manual bool) tea.Cmd {
	if m.markRead == nil {
		if manual {
			m.status = "read receipts unavailable"
		}
		return nil
	}
	if m.connectionState != ConnectionOnline {
		if manual {
			m.status = "read receipts need WhatsApp online"
		}
		return nil
	}
	chat := m.currentChat()
	if chat.ID == "" {
		if manual {
			m.status = "no active chat"
		}
		return nil
	}
	if !manual && chat.Unread <= 0 {
		return nil
	}
	if m.readReceiptInflight == nil {
		m.readReceiptInflight = map[string]bool{}
	}
	if m.readReceiptInflight[chat.ID] {
		if manual {
			m.status = "read receipt already queued"
		}
		return nil
	}
	messages := readableMessages(m.currentMessages())
	if len(messages) == 0 {
		if manual {
			m.status = "no loaded unread messages to mark read"
		}
		return nil
	}
	m.readReceiptInflight[chat.ID] = true
	if manual {
		m.status = "mark read queued"
	}
	return m.markReadCmd(chat, messages, manual)
}

func readableMessages(messages []store.Message) []store.Message {
	out := make([]store.Message, 0, len(messages))
	for _, message := range messages {
		if message.IsOutgoing || strings.TrimSpace(message.RemoteID) == "" {
			continue
		}
		out = append(out, message)
	}
	return out
}

func (m Model) handleLeaderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.keyMatches(msg, m.config.Keymap.NormalCancel) {
		m.leaderPending = false
		m.leaderSequence = ""
		m.status = "leader cancelled"
		return m, nil
	}
	key := keyTokenFromMsg(msg)
	sequence := strings.Fields(m.leaderSequence)
	sequence = append(sequence, key)
	sequenceText := strings.Join(sequence, "")
	action, exact, prefix := m.normalActionForLeaderSequence(sequence)
	if exact {
		m.leaderPending = false
		m.leaderSequence = ""
		return m.runNormalAction(action, 1)
	}
	if prefix {
		m.leaderSequence = strings.Join(sequence, " ")
		m.status = fmt.Sprintf("leader: %s", sequenceText)
		return m, nil
	}
	m.leaderPending = false
	m.leaderSequence = ""
	m.status = fmt.Sprintf("unknown leader key: %s", sequenceText)
	return m, nil
}

func (m Model) updateInsert(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keys := m.config.Keymap
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
		m.composer += "\n"
		m.sendOwnPresence(m.currentChat().ID, true)
		return m, ownPresenceIdleCmd(m.currentChat().ID, m.ownPresenceGeneration)
	}

	switch {
	case m.keyMatches(msg, keys.InsertCancel):
		if m.editTarget != nil {
			chatID := m.currentChat().ID
			m.composer = ""
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
		m.replyTo = nil
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
		if m.requireOnlineForSend && m.connectionState != ConnectionOnline {
			m.status = "sending needs WhatsApp online"
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
	m.composer = trimLastCluster(m.composer)
	m.pruneComposerMentions()
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

func (m Model) updateCommand(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keys := m.config.Keymap
	switch {
	case m.keyMatches(msg, keys.CommandCancel):
		m.mode = ModeNormal
		m.commandLine = ""
	case m.keyMatches(msg, keys.CommandRun):
		cmd := strings.TrimSpace(m.commandLine)
		m.commandLine = ""
		m.mode = ModeNormal
		if cmd != "" {
			m.commandHistory = append(m.commandHistory, cmd)
		}
		return m.executeCommand(cmd)
	case m.keyMatches(msg, keys.CommandBackspace) || m.keyMatches(msg, keys.CommandBackspaceAlt):
		m.commandLine = trimLastCluster(m.commandLine)
	default:
		if text := keyText(msg); text != "" {
			m.commandLine += text
		}
	}

	return m, nil
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keys := m.config.Keymap
	switch {
	case m.keyMatches(msg, keys.SearchCancel):
		m.clearSearch()
		m.mode = ModeNormal
		m.status = "search cleared"
	case m.keyMatches(msg, keys.SearchRun):
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
		searchCmd := m.advanceSearch(1)
		m.mode = ModeNormal
		return m, searchCmd
	case m.keyMatches(msg, keys.SearchBackspace) || m.keyMatches(msg, keys.SearchBackspaceAlt):
		m.searchLine = trimLastCluster(m.searchLine)
	default:
		if text := keyText(msg); text != "" {
			m.searchLine += text
		}
	}

	return m, nil
}

func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keys := m.config.Keymap
	switch {
	case m.keyMatches(msg, keys.ConfirmCancel):
		m.mode = ModeNormal
		m.confirmLine = ""
		m.deleteForEveryoneConfirmID = ""
		m.status = "delete for everybody cancelled"
	case m.keyMatches(msg, keys.ConfirmRun):
		confirmation := strings.TrimSpace(m.confirmLine)
		m.confirmLine = ""
		if confirmation != "Y" {
			m.mode = ModeNormal
			m.deleteForEveryoneConfirmID = ""
			m.status = "delete for everybody cancelled"
			return m, nil
		}
		m.mode = ModeNormal
		return m.deleteConfirmedMessageForEveryone()
	case m.keyMatches(msg, keys.ConfirmBackspace) || m.keyMatches(msg, keys.ConfirmBackspaceAlt):
		m.confirmLine = trimLastCluster(m.confirmLine)
	default:
		if text := keyText(msg); text != "" {
			m.confirmLine += text
		}
	}

	return m, nil
}

func (m Model) updateForward(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keys := m.config.Keymap
	if m.forwardSearchActive {
		switch {
		case m.keyMatches(msg, keys.ForwardCancel):
			m.forwardSearchActive = false
			m.forwardQuery = ""
			m.rebuildForwardCandidates()
			m.status = "forward search cleared"
		case m.keyMatches(msg, keys.ForwardSend):
			m.forwardSearchActive = false
		case m.keyMatches(msg, keys.ForwardBackspace) || m.keyMatches(msg, keys.ForwardBackspaceAlt):
			m.forwardQuery = trimLastCluster(m.forwardQuery)
			m.rebuildForwardCandidates()
		default:
			if text := keyText(msg); text != "" {
				m.forwardQuery += text
				m.rebuildForwardCandidates()
			}
		}
		return m, nil
	}

	switch {
	case m.keyMatches(msg, keys.ForwardCancel):
		m.clearForwardPicker()
		m.mode = ModeNormal
		m.status = "forward cancelled"
	case m.keyMatches(msg, keys.ForwardSearch):
		m.forwardSearchActive = true
		m.status = "forward search"
	case m.keyMatches(msg, keys.ForwardSend):
		recipients := m.forwardSelectedRecipients()
		if len(recipients) == 0 {
			m.status = "no forward recipients selected"
			return m, nil
		}
		if len(m.forwardSourceMessages) == 0 {
			m.clearForwardPicker()
			m.mode = ModeNormal
			m.status = "no messages selected"
			return m, nil
		}
		request := ForwardMessagesRequest{
			Messages:   slices.Clone(m.forwardSourceMessages),
			Recipients: recipients,
		}
		count := len(request.Messages)
		recipientCount := len(request.Recipients)
		forward := m.forwardMessages
		m.clearForwardPicker()
		m.mode = ModeNormal
		if forward == nil {
			m.status = "forwarding unavailable"
			return m, nil
		}
		m.status = fmt.Sprintf("forwarding %d message(s) to %d chat(s)", count, recipientCount)
		return m, forward(request)
	case m.keyMatches(msg, keys.ForwardToggle):
		m.toggleForwardRecipient()
	case m.keyMatches(msg, keys.ForwardMoveDown):
		m.moveForwardCursor(1)
	case m.keyMatches(msg, keys.ForwardMoveUp):
		m.moveForwardCursor(-1)
	}

	return m, nil
}

func (m Model) handleInlineFallbackPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keys := m.config.Keymap
	switch {
	case m.keyMatches(msg, keys.ConfirmCancel), m.keyMatches(msg, keys.NormalCancel):
		m.inlineFallbackPrompt = false
		m.inlineFallbackDeclined = true
		m.status = "inline image fallback skipped; media will open externally"
	case m.keyMatches(msg, keys.ConfirmRun), msg.Type == tea.KeyEnter:
		m.inlineFallbackPrompt = false
		m.inlineFallbackAccepted = true
		m.inlineFallbackDeclined = false
		m.status = "using chafa fallback for inline images"
	default:
		return m, nil
	}
	return m, nil
}

func (m Model) updateVisual(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keys := m.config.Keymap
	switch {
	case m.keyMatches(msg, keys.VisualCancel):
		m.mode = ModeNormal
	case m.keyMatches(msg, keys.VisualMoveDown):
		return m, m.moveCursor(1)
	case m.keyMatches(msg, keys.VisualMoveUp):
		return m, m.moveCursor(-1)
	case m.keyMatches(msg, keys.VisualYank):
		return m.yankMessages(m.selectedMessages())
	case m.keyMatches(msg, keys.VisualForward):
		return m.startForwardPicker(m.selectedMessages())
	}

	return m, nil
}

func (m Model) yankFocusedMessage() (tea.Model, tea.Cmd) {
	if m.focus != FocusMessages && m.focus != FocusPreview {
		m.status = "no message selected"
		return m, nil
	}
	message, ok := m.focusedMessage()
	if !ok {
		m.status = "no message selected"
		return m, nil
	}
	return m.yankMessages([]store.Message{message})
}

func (m Model) yankMessages(messages []store.Message) (tea.Model, tea.Cmd) {
	var parts []string
	for _, message := range messages {
		parts = append(parts, message.Body)
	}
	m.yankRegister = strings.Join(parts, "\n")
	m.mode = ModeNormal
	if m.copyToClipboard == nil {
		m.status = fmt.Sprintf("yanked %d message(s) to register", len(messages))
		return m, nil
	}
	count := len(messages)
	text := m.yankRegister
	m.status = fmt.Sprintf("yanked %d message(s); copying clipboard", count)
	return m, func() tea.Msg {
		return clipboardCopiedMsg{Count: count, Err: m.copyToClipboard(text)}
	}
}

func (m *Model) cycleFocus(delta int) tea.Cmd {
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
	var cmd tea.Cmd
	if m.focus == FocusMessages {
		cmd = m.ensureCurrentMessagesLoaded(false, true)
	}
	m.status = fmt.Sprintf("focus: %s", m.focus)
	return cmd
}

func (m *Model) moveFocus(delta int) tea.Cmd {
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
			return m.ensureCurrentMessagesLoaded(false, true)
		case FocusMessages:
			if m.infoPaneVisible && !m.compactLayout {
				m.focus = FocusPreview
			}
		}
	}
	return nil
}

func (m *Model) moveCursor(delta int) tea.Cmd {
	switch m.focus {
	case FocusChats:
		if len(m.chats) == 0 {
			return nil
		}
		previousChat := m.activeChat
		previousScrollTop := m.chatScrollTop
		m.activeChat = clamp(m.activeChat+delta, 0, len(m.chats)-1)
		m.keepActiveChatVisible()
		loadCmd := m.ensureCurrentMessagesLoaded(true, false)
		if m.activeChat != previousChat {
			m.pauseOverlays(true, m.chatScrollTop != previousScrollTop)
		}
		return loadCmd
	case FocusMessages:
		if len(m.currentMessages()) == 0 {
			return nil
		}
		if delta < 0 && m.messageCursor == 0 {
			return m.loadOlderOrRequestHistory()
		}
		previous := m.messageCursor
		previousScrollTop := m.messageScrollTop
		m.messageCursor = clamp(m.messageCursor+delta, 0, len(m.currentMessages())-1)
		m.keepMessageCursorNearViewport()
		if m.messageCursor != previous && m.messageScrollTop != previousScrollTop {
			m.pauseOverlays(true, false)
		}
	}
	return nil
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
		loadCmd := m.ensureCurrentMessagesLoaded(false, true)
		m.status = "focus: messages"
		return m, loadCmd
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
		m.previewGeneration++
		m.clearOverlayPause()
		if m.previewReport.Selected == media.BackendUeberzugPP && m.overlay == nil {
			m.overlay = media.NewOverlayManager(m.previewReport.UeberzugPPOutput)
		}
		m.ensureSixelManager()
		m.status = fmt.Sprintf("preview backend: %s", m.previewReport.Selected)
		return m, batchCmds(m.clearOverlayCmd(), m.clearSixelCmd())
	case cmd == "preview-cache clear" || cmd == "clear-preview-cache":
		return m.clearMediaPreviews("preview cache cleared")
	case cmd == "media-hide" || cmd == "media hide" || cmd == "media-previews hide" || cmd == "media previews hide":
		return m.clearMediaPreviews("media previews unloaded")
	case cmd == "media-preview" || cmd == "preview-media" || cmd == "media preview":
		return m.activateFocusedMediaPreview()
	case cmd == "media-open" || cmd == "media open":
		return m.openFocusedMedia()
	case cmd == "media-save" || cmd == "media save":
		return m.saveFocusedMedia()
	case cmd == "copy-image" || cmd == "copy image":
		return m.copyFocusedImage()
	case cmd == "paste-attachment" || cmd == "paste attachment" || cmd == "paste-image" || cmd == "paste image":
		return m.startClipboardAttachmentPaste()
	case cmd == "retry-message" || cmd == "retry message" || cmd == "retry":
		return m, m.retryFocusedMediaMessage()
	case cmd == "history-fetch" || cmd == "history fetch":
		return m, m.loadOlderOrRequestHistory()
	case cmd == "mark-read" || cmd == "mark read":
		return m, m.markCurrentChatRead(true)
	case cmd == "quote-jump" || cmd == "quote jump":
		return m, m.jumpToQuotedMessage()
	case strings.HasPrefix(cmd, "react "):
		emoji := strings.TrimSpace(strings.TrimPrefix(cmd, "react "))
		if strings.EqualFold(emoji, "clear") {
			emoji = ""
		}
		return m, m.reactToFocusedMessage(emoji)
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
		return m.clearMessageFilter()
	case strings.HasPrefix(cmd, "filter messages "):
		query := strings.TrimSpace(strings.TrimPrefix(cmd, "filter messages "))
		if query == "" || query == "clear" {
			return m.clearMessageFilter()
		}
		return m.applyMessageFilter(query)
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
	case cmd == "sticker" || cmd == "pick-sticker" || cmd == "sticker pick":
		return m.startStickerPicker()
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
	case cmd == "delete-message-everybody" || cmd == "delete message everybody" || cmd == "delete-message-everyone" || cmd == "delete message everyone":
		m.armDeleteFocusedMessageForEveryone()
	case cmd == "delete-message-everybody confirm" || cmd == "delete message everybody confirm" || cmd == "delete-message-everyone confirm" || cmd == "delete message everyone confirm":
		return m.deleteConfirmedMessageForEveryone()
	case cmd == "delete-message-everybody!" || cmd == "delete message everybody!" || cmd == "delete-message-everyone!" || cmd == "delete message everyone!":
		m.armDeleteFocusedMessageForEveryone()
		if m.deleteForEveryoneConfirmID == "" {
			break
		}
		return m.deleteConfirmedMessageForEveryone()
	case cmd == "edit-message" || cmd == "edit message":
		return m.beginEditFocusedMessage()
	default:
		m.status = fmt.Sprintf("unknown command: %s", cmd)
	}

	return m, nil
}

func (m *Model) rebuildSearchMatches() {
	query := strings.TrimSpace(m.lastSearch)
	m.searchMatches = nil
	m.searchIndex = -1
	if query == "" {
		return
	}

	switch m.lastSearchFocus {
	case FocusChats:
		for i, chat := range m.chats {
			if textmatch.Contains(chat.DisplayTitle(), query) {
				m.searchMatches = append(m.searchMatches, i)
			}
		}
	case FocusMessages, FocusPreview:
		for i, message := range m.currentMessages() {
			if textmatch.Contains(message.Body, query) {
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

func (m *Model) advanceSearch(delta int) tea.Cmd {
	if strings.TrimSpace(m.lastSearch) == "" {
		m.status = "no active search"
		return nil
	}
	if m.lastSearchFocus != m.focus && !(m.lastSearchFocus == FocusPreview && m.focus == FocusMessages) {
		m.status = "search belongs to another pane"
		return nil
	}
	if len(m.searchMatches) == 0 {
		m.rebuildSearchMatches()
	}
	if len(m.searchMatches) == 0 {
		m.status = fmt.Sprintf("no matches for %q", m.lastSearch)
		return nil
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
		return m.ensureCurrentMessagesLoaded(true, false)
	} else {
		m.messageCursor = target
		m.messageScrollTop = target
	}
	return nil
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
	if m.connectionState != ConnectionOnline {
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
	if m.connectionState == ConnectionOnline && m.requestHistory != nil {
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

func (m *Model) reactToFocusedMessage(emoji string) tea.Cmd {
	message, ok := m.focusedMessage()
	if !ok {
		m.status = "no message selected"
		return nil
	}
	if strings.TrimSpace(message.RemoteID) == "" {
		m.status = "focused message has no WhatsApp id"
		return nil
	}
	if m.connectionState != ConnectionOnline {
		m.status = "reactions need WhatsApp online"
		return nil
	}
	if m.sendReaction == nil {
		m.status = "reactions unavailable"
		return nil
	}
	if strings.TrimSpace(emoji) == "" {
		m.status = "reaction clear queued"
	} else {
		m.status = "reaction queued"
	}
	return m.sendReactionCmd(message, emoji)
}

func (m *Model) retryFocusedMediaMessage() tea.Cmd {
	message, ok := m.focusedMessage()
	if !ok {
		m.status = "no message selected"
		return nil
	}
	if err := m.validateRetryMessage(message); err != nil {
		m.status = err.Error()
		return nil
	}
	if m.retryMessage == nil {
		m.status = "retry is unavailable"
		return nil
	}
	m.status = "retry queued"
	return m.retryMessageCmd(message)
}

func (m Model) messageLimitForChat(chatID string) int {
	if chatID == "" || m.messageLimitsByChat == nil {
		return messageLoadLimit
	}
	if limit := m.messageLimitsByChat[chatID]; limit > 0 {
		return limit
	}
	return messageLoadLimit
}

func (m *Model) addMessageLimit(chatID string, delta int) {
	if chatID == "" || delta <= 0 {
		return
	}
	if m.messageLimitsByChat == nil {
		m.messageLimitsByChat = map[string]int{}
	}
	m.messageLimitsByChat[chatID] = m.messageLimitForChat(chatID) + delta
}

func (m *Model) applySnapshot(snapshot store.Snapshot, preferredChatID, messageFilter string) error {
	if preferredChatID == "" {
		preferredChatID = snapshot.ActiveChatID
	}
	if preferredChatID == "" {
		preferredChatID = m.currentChat().ID
	}

	oldChatID := m.currentChat().ID
	oldCursor := m.messageCursor
	oldScrollTop := m.messageScrollTop
	oldMessages := m.currentMessages()
	oldMessageCount := len(oldMessages)
	oldTailID := ""
	if oldMessageCount > 0 {
		oldTailID = oldMessages[oldMessageCount-1].ID
	}
	oldWasAtTail := oldMessageCount > 0 && oldCursor == oldMessageCount-1 && (m.focus == FocusMessages || m.focus == FocusPreview)
	messageViewNarrowed := m.messageViewNarrowed(messageFilter)
	oldFocusedID := ""
	if oldCursor >= 0 && oldCursor < oldMessageCount {
		oldFocusedID = oldMessages[oldCursor].ID
	}

	m.allChats = slices.Clone(snapshot.Chats)
	m.draftsByChat = cloneDrafts(snapshot.DraftsByChat)
	if m.messagesByChat == nil {
		m.messagesByChat = map[string][]store.Message{}
	}
	for chatID, messages := range snapshot.MessagesByChat {
		m.messagesByChat[chatID] = slices.Clone(messages)
		delete(m.unfilteredByChat, chatID)
	}

	if m.activeSearch != "" && m.lastSearchFocus == FocusChats && m.searchChats != nil {
		if err := m.runStoreSearch(); err != nil {
			return err
		}
	} else {
		m.searchChatSource = nil
		if err := m.applyChatView(m.allChats, preferredChatID); err != nil {
			return err
		}
	}

	if oldChatID != "" && m.currentChat().ID == oldChatID {
		messageCount := len(m.currentMessages())
		if oldFocusedID != "" {
			if index := indexOfMessage(m.currentMessages(), oldFocusedID); index >= 0 {
				delta := index - oldCursor
				m.messageCursor = index
				m.messageScrollTop = clamp(oldScrollTop+delta, 0, max(0, messageCount-1))
			} else {
				m.messageCursor = clamp(oldCursor, 0, max(0, messageCount-1))
				m.messageScrollTop = clamp(oldScrollTop, 0, max(0, messageCount-1))
			}
		} else {
			m.messageCursor = clamp(oldCursor, 0, max(0, messageCount-1))
			m.messageScrollTop = clamp(oldScrollTop, 0, max(0, messageCount-1))
		}
		if !messageViewNarrowed && m.activeChatTailAdvanced(oldTailID) {
			if oldWasAtTail {
				m.showCurrentChatLatest()
			} else {
				m.recordNewMessages(oldChatID, oldTailID)
			}
		}
	}

	if strings.TrimSpace(messageFilter) != "" {
		query := strings.TrimSpace(messageFilter)
		chatID := m.currentChat().ID
		source := m.filterSource(chatID)
		m.unfilteredByChat[chatID] = slices.Clone(source)
		var filtered []store.Message
		for _, message := range source {
			if textmatch.Contains(message.Body, query) {
				filtered = append(filtered, message)
			}
		}
		m.messagesByChat[chatID] = filtered
		m.messageFilter = query
		m.messageCursor = 0
		m.messageScrollTop = 0
		m.status = fmt.Sprintf("message filter: %s", query)
	}
	if strings.TrimSpace(m.lastSearch) != "" {
		m.rebuildSearchMatches()
	}
	if m.mode == ModeInsert && m.editTarget == nil && m.composer == "" {
		if draft := m.draftsByChat[m.currentChat().ID]; strings.TrimSpace(draft) != "" {
			m.composer = draft
		}
	}
	return nil
}

func (m Model) messageViewNarrowed(messageFilter string) bool {
	if strings.TrimSpace(messageFilter) != "" || strings.TrimSpace(m.messageFilter) != "" {
		return true
	}
	return strings.TrimSpace(m.activeSearch) != "" && (m.lastSearchFocus == FocusMessages || m.lastSearchFocus == FocusPreview)
}

func (m Model) activeChatTailAdvanced(oldTailID string) bool {
	if strings.TrimSpace(oldTailID) == "" {
		return false
	}
	messages := m.currentMessages()
	if len(messages) == 0 {
		return false
	}
	newTailID := strings.TrimSpace(messages[len(messages)-1].ID)
	if newTailID == "" || newTailID == oldTailID {
		return false
	}
	return indexOfMessage(messages, oldTailID) >= 0
}

func (m *Model) recordNewMessages(chatID, oldTailID string) {
	if strings.TrimSpace(chatID) == "" || strings.TrimSpace(oldTailID) == "" {
		return
	}
	messages := m.currentMessages()
	if chatID != m.currentChat().ID || len(messages) == 0 {
		return
	}
	oldTailIndex := indexOfMessage(messages, oldTailID)
	if oldTailIndex < 0 || oldTailIndex >= len(messages)-1 {
		return
	}
	if m.newMessageStateByChat == nil {
		m.newMessageStateByChat = map[string]newMessageState{}
	}
	state := m.newMessageStateByChat[chatID]
	firstIndex := -1
	if strings.TrimSpace(state.FirstMessageID) != "" {
		firstIndex = indexOfMessage(messages, state.FirstMessageID)
	}
	if firstIndex < 0 {
		firstIndex = oldTailIndex + 1
		state.FirstMessageID = messages[firstIndex].ID
	}
	state.NewCount = len(messages) - firstIndex
	m.newMessageStateByChat[chatID] = state
}

func (m *Model) clearNewMessagesBelow(chatID string) {
	if strings.TrimSpace(chatID) == "" || m.newMessageStateByChat == nil {
		return
	}
	delete(m.newMessageStateByChat, chatID)
}

func (m Model) newMessagesState(chatID string) (newMessageState, int, bool) {
	if strings.TrimSpace(chatID) == "" || m.newMessageStateByChat == nil {
		return newMessageState{}, -1, false
	}
	state, ok := m.newMessageStateByChat[chatID]
	if !ok || strings.TrimSpace(state.FirstMessageID) == "" {
		return newMessageState{}, -1, false
	}
	if chatID != m.currentChat().ID || m.messageViewNarrowed("") {
		return newMessageState{}, -1, false
	}
	messages := m.currentMessages()
	if len(messages) == 0 || m.messageCursor >= len(messages)-1 {
		return newMessageState{}, -1, false
	}
	firstIndex := indexOfMessage(messages, state.FirstMessageID)
	if firstIndex < 0 || firstIndex >= len(messages) {
		return newMessageState{}, -1, false
	}
	state.NewCount = len(messages) - firstIndex
	if state.NewCount <= 0 {
		return newMessageState{}, -1, false
	}
	return state, firstIndex, true
}

func (m Model) hasNewMessagesBelow(chatID string) bool {
	_, _, ok := m.newMessagesState(chatID)
	return ok
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

func (m Model) applyMessageFilter(query string) (Model, tea.Cmd) {
	query = strings.TrimSpace(query)
	if query == "" {
		return m.clearMessageFilter()
	}

	chatID := m.currentChat().ID
	if chatID == "" {
		return m, nil
	}
	source := m.filterSource(chatID)
	m.unfilteredByChat[chatID] = slices.Clone(source)

	var filtered []store.Message
	if m.searchMessages != nil {
		m.filterGeneration++
		m.messageFilter = query
		m.messageCursor = 0
		m.messageScrollTop = 0
		m.status = fmt.Sprintf("filtering messages: %s", query)
		return m, m.searchMessagesFilterCmd(m.filterGeneration, chatID, query)
	} else {
		for _, message := range source {
			if textmatch.Contains(message.Body, query) {
				filtered = append(filtered, message)
			}
		}
	}

	m.messagesByChat[chatID] = filtered
	m.messageFilter = query
	m.messageCursor = 0
	m.messageScrollTop = 0
	m.status = fmt.Sprintf("message filter: %s", query)
	return m, nil
}

func (m Model) clearMessageFilter() (Model, tea.Cmd) {
	chatID := m.currentChat().ID
	if chatID == "" {
		m.messageFilter = ""
		return m, nil
	}
	m.filterGeneration++
	if base, ok := m.unfilteredByChat[chatID]; ok {
		m.messagesByChat[chatID] = slices.Clone(base)
		delete(m.unfilteredByChat, chatID)
	} else if m.loadMessages != nil {
		m.messageFilter = ""
		m.status = "clearing message filter"
		return m, m.reloadMessagesForFilterClearCmd(m.filterGeneration, chatID, m.messageLimitForChat(chatID))
	}
	m.messageFilter = ""
	m.messageCursor = clamp(m.messageCursor, 0, max(0, len(m.currentMessages())-1))
	m.messageScrollTop = clamp(m.messageScrollTop, 0, max(0, len(m.currentMessages())-1))
	m.status = "message filter cleared"
	return m, nil
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
		m.reportActiveChatChanged()
		return nil
	}

	m.activeChat = indexOfChat(m.chats, preferredChatID)
	if m.activeChat == -1 {
		m.activeChat = 0
	}
	m.keepActiveChatVisible()
	m.reportActiveChatChanged()
	return nil
}

func (m *Model) reportActiveChatChanged() {
	if m.activeChatChanged == nil {
		return
	}
	chatID := strings.TrimSpace(m.currentChat().ID)
	if chatID == m.lastReportedActiveChat {
		return
	}
	m.lastReportedActiveChat = chatID
	m.activeChatChanged(chatID)
}

func (m *Model) reportAppFocusChanged(focused bool) {
	if m.appFocusKnown && m.appFocused == focused {
		return
	}
	m.appFocusKnown = true
	m.appFocused = focused
	if m.appFocusChanged != nil {
		m.appFocusChanged(focused)
	}
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

func (m *Model) keepMessageCursorNearViewport() {
	messageCount := len(m.currentMessages())
	if messageCount == 0 {
		m.messageScrollTop = 0
		m.clearNewMessagesBelow(m.currentChat().ID)
		return
	}
	m.messageCursor = clamp(m.messageCursor, 0, messageCount-1)
	m.messageScrollTop = clamp(m.messageScrollTop, 0, messageCount-1)
	if m.messageCursor == messageCount-1 {
		m.messageScrollTop = m.messageCursor
		m.clearNewMessagesBelow(m.currentChat().ID)
		return
	}
	if scrollTop, ok := m.messageScrollTopWithCursorVisible(); ok {
		m.messageScrollTop = scrollTop
		if m.messageCursor+1 < m.messageScrollTop &&
			m.terminalMessageMediaSyncActive() &&
			!m.messageCursorBlockStartsAtTopVisible() {
			m.messageScrollTop = m.messageCursor
		}
		return
	}
	if m.messageCursor < m.messageScrollTop {
		m.messageScrollTop = m.messageCursor
		return
	}
	if m.messageCursor > m.messageScrollTop+2 {
		m.messageScrollTop = m.messageCursor - 2
	}
}

func (m Model) messageScrollTopWithCursorVisible() (int, bool) {
	width := m.messagePaneContentWidth()
	height := m.messageViewportHeight()
	if width <= 0 || height <= 0 {
		return 0, false
	}
	messages := m.currentMessages()
	if len(messages) == 0 {
		return 0, true
	}
	blocks := m.visibleMessageBlocks(messages, width, height, nil)
	if len(blocks) == 0 {
		return 0, true
	}
	localCursor := messageBlockIndexForCursor(blocks, clamp(m.messageCursor, 0, len(messages)-1))
	localScrollTop := messageBlockIndexForScrollTop(blocks, clamp(m.messageScrollTop, 0, len(messages)-1))
	localScrollTop = dividerViewportScrollTop(blocks, localScrollTop, localCursor, height)
	spans := messageViewportSpans(blocks, localScrollTop, localCursor, height)
	if messageBlockSpansContain(spans, localCursor) {
		return blocks[localScrollTop].messageIndex, true
	}
	target := adjustedMessageScrollTop(blocks, localScrollTop, localCursor, height)
	target = dividerViewportScrollTop(blocks, target, localCursor, height)
	return clamp(blocks[target].messageIndex, 0, len(messages)-1), true
}

func (m Model) messageCursorBlockStartsAtTopVisible() bool {
	width := m.messagePaneContentWidth()
	height := m.messageViewportHeight()
	if width <= 0 || height <= 0 {
		return false
	}
	messages := m.currentMessages()
	if len(messages) == 0 {
		return true
	}
	blocks := m.visibleMessageBlocks(messages, width, height, nil)
	if len(blocks) == 0 {
		return true
	}
	localCursor := messageBlockIndexForCursor(blocks, clamp(m.messageCursor, 0, len(messages)-1))
	localScrollTop := messageBlockIndexForScrollTop(blocks, clamp(m.messageScrollTop, 0, len(messages)-1))
	localScrollTop = dividerViewportScrollTop(blocks, localScrollTop, localCursor, height)
	spans := messageViewportSpans(blocks, localScrollTop, localCursor, height)
	return messageBlockSpanStartsAtTop(spans, localCursor)
}

func (m *Model) showCurrentChatLatest() {
	messageCount := len(m.currentMessages())
	chatID := m.currentChat().ID
	if messageCount == 0 {
		m.messageCursor = 0
		m.messageScrollTop = 0
		m.clearNewMessagesBelow(chatID)
		return
	}
	m.messageCursor = messageCount - 1
	m.messageScrollTop = m.messageCursor
	m.clearNewMessagesBelow(chatID)
}

func (m Model) withPreviewCmd(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if m.quitting {
		return m, batchCmds(cmd, m.clearOverlayCmd(), m.clearSixelCmd())
	}
	next := m
	next.reportVisibleChatsChanged()
	next, stickerCmd := next.ensureVisibleStickerMedia()
	next.maybePromptInlineFallback()
	next, previewCmd := next.queueRequestedPreviewCmd()
	overlayCmd := next.syncOverlayCmd()
	sixelCmd := next.syncSixelCmd()
	resumeCmd := next.queueOverlayResumeCmd()
	return next, batchCmds(cmd, stickerCmd, previewCmd, overlayCmd, sixelCmd, resumeCmd)
}

func (m *Model) pauseOverlays(mediaPreview, chatAvatars bool) {
	if !mediaPreview && !chatAvatars {
		return
	}
	changed := false
	if mediaPreview {
		changed = changed || !m.mediaOverlayPaused
		m.mediaOverlayPaused = true
	}
	if chatAvatars {
		changed = changed || !m.avatarOverlayPaused
		m.avatarOverlayPaused = true
	}
	m.overlayPauseGeneration++
	m.overlayResumeQueued = 0
	if !changed {
		return
	}
	if m.overlay != nil {
		m.overlay.Invalidate()
	}
	if m.sixel != nil {
		m.sixel.Invalidate()
	}
}

func (m *Model) queueOverlayResumeCmd() tea.Cmd {
	if !m.mediaOverlayPaused && !m.avatarOverlayPaused {
		return nil
	}
	if m.overlaySyncPending || m.sixelSyncPending {
		return nil
	}
	if m.overlayResumeQueued == m.overlayPauseGeneration {
		return nil
	}
	m.overlayResumeQueued = m.overlayPauseGeneration
	return overlayResumeCmd(m.overlayPauseGeneration)
}

func (m *Model) clearOverlayPause() {
	if !m.mediaOverlayPaused && !m.avatarOverlayPaused && m.overlayResumeQueued == 0 {
		return
	}
	m.mediaOverlayPaused = false
	m.avatarOverlayPaused = false
	m.overlayPauseGeneration++
	m.overlayResumeQueued = 0
	if m.overlay != nil {
		m.overlay.Invalidate()
	}
	if m.sixel != nil {
		m.sixel.Invalidate()
	}
}

func closeOverlayManagerCmd(manager *media.OverlayManager) tea.Cmd {
	if manager == nil {
		return nil
	}
	return func() tea.Msg {
		_ = manager.Close()
		return nil
	}
}

func (m *Model) ensureSixelManager() {
	if m.previewReport.Selected != media.BackendSixel || m.sixel != nil || m.sixelWriter == nil {
		return
	}
	m.sixel = media.NewSixelManagerForWriter(m.sixelWriter)
}

func (m Model) terminalOverlayBackendActive() bool {
	return m.previewReport.Selected == media.BackendUeberzugPP || m.previewReport.Selected == media.BackendSixel
}

func (m Model) terminalMediaSyncActive() bool {
	return m.mediaOverlayPaused ||
		m.avatarOverlayPaused ||
		m.overlaySyncPending ||
		m.sixelSyncPending ||
		strings.TrimSpace(m.overlaySignature) != "" ||
		strings.TrimSpace(m.sixelSignature) != ""
}

func (m Model) terminalMessageMediaSyncActive() bool {
	return m.terminalMediaSyncActive() &&
		(len(m.visibleMediaPlacements()) > 0 || len(m.visibleSixelMediaPlacements()) > 0)
}

func (m *Model) invalidateStaleOverlaySync(nextSignature string) {
	if !m.overlaySyncPending || nextSignature == m.overlayPendingSignature || m.overlay == nil {
		return
	}
	m.overlay.Invalidate()
}

func (m *Model) invalidateStaleSixelSync(nextSignature string) {
	if !m.sixelSyncPending || nextSignature == m.sixelPendingSignature || m.sixel == nil {
		return
	}
	m.sixel.Invalidate()
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

var inlineFallbackAllowed = platformAllowsInlineFallback

func (m Model) inlineFallbackAvailable() bool {
	return inlineFallbackAllowed() &&
		m.previewReport.Selected == media.BackendExternal &&
		m.previewReport.Reasons[media.BackendChafa] == "available"
}

func (m Model) inlineFallbackNeedsPrompt() bool {
	return m.inlineFallbackAvailable() &&
		!m.inlineFallbackAccepted &&
		!m.inlineFallbackDeclined &&
		!m.inlineFallbackPrompt
}

func (m Model) inlinePreviewBackend() (media.Backend, bool) {
	switch m.previewReport.Selected {
	case media.BackendNone:
		return "", false
	case media.BackendExternal:
		if m.inlineFallbackAccepted && m.inlineFallbackAvailable() {
			return media.BackendChafa, true
		}
		return "", false
	default:
		return m.previewReport.Selected, true
	}
}

func (m Model) avatarPreviewBackend() (media.Backend, bool) {
	if backend, ok := media.AvatarPreviewBackend(m.previewReport); ok {
		return backend, true
	}
	if m.inlineFallbackAccepted && m.inlineFallbackAvailable() {
		return media.BackendChafa, true
	}
	return "", false
}

func (m *Model) maybePromptInlineFallback() {
	if !m.inlineFallbackNeedsPrompt() || m.helpVisible || m.syncOverlay.Visible {
		return
	}
	if m.hasVisibleRequestedInlineMedia() || m.hasVisibleAvatarFallbackCandidate() {
		m.showInlineFallbackPrompt()
	}
}

func (m Model) hasVisibleRequestedInlineMedia() bool {
	if len(m.previewRequested) == 0 {
		return false
	}
	messages := m.currentMessages()
	if len(messages) == 0 {
		return false
	}
	start, end := 0, len(messages)
	if geometry, ok := m.messagePaneGeometry(); ok {
		start, end = m.visibleMessageRange(len(messages), max(1, geometry.height-2))
	}
	for _, message := range messages[start:end] {
		for _, item := range message.Media {
			if !m.previewRequested[mediaActivationKey(message, item)] {
				continue
			}
			request, ok := m.previewRequestForMediaWithBackend(message, item, 0, 0, media.BackendChafa)
			if !ok {
				continue
			}
			if strings.TrimSpace(request.LocalPath) != "" || strings.TrimSpace(request.ThumbnailPath) != "" {
				return true
			}
		}
	}
	return false
}

func (m Model) hasVisibleAvatarFallbackCandidate() bool {
	for _, chatID := range m.visibleChatIDs() {
		if _, ok := m.chatAvatarPreviewRequestWithBackend(m.chatByID(chatID), media.BackendChafa); ok {
			return true
		}
	}
	return false
}

func (m *Model) showInlineFallbackPrompt() {
	m.inlineFallbackPrompt = true
	m.status = "choose an inline image fallback"
}

func (m Model) queueRequestedPreviewCmd() (Model, tea.Cmd) {
	requests := append(m.requestedPreviewRequests(), m.requestedAvatarPreviewRequests()...)
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
	generation := m.previewGeneration
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
				Key:        media.PreviewKey(req),
				Generation: generation,
				Request:    req,
				Preview:    previewer.Render(context.Background(), req),
			}
		})
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) ensureVisibleStickerMedia() (Model, tea.Cmd) {
	if len(m.currentMessages()) == 0 {
		return *m, nil
	}
	if m.previewRequested == nil {
		m.previewRequested = map[string]bool{}
	}

	start, end := 0, len(m.currentMessages())
	if geometry, ok := m.messagePaneGeometry(); ok {
		start, end = m.visibleMessageRange(len(m.currentMessages()), max(1, geometry.height-2))
	}
	var cmds []tea.Cmd
	for _, message := range m.currentMessages()[start:end] {
		for _, item := range message.Media {
			if !strings.EqualFold(strings.TrimSpace(item.Kind), "sticker") {
				continue
			}
			key := mediaActivationKey(message, item)
			m.previewRequested[key] = true
			if strings.TrimSpace(item.LocalPath) != "" || m.downloadMedia == nil || strings.EqualFold(strings.TrimSpace(item.DownloadState), "failed") {
				continue
			}
			if !m.startMediaDownload(message, item, "downloading sticker") {
				continue
			}
			messageCopy := message
			itemCopy := item
			cmds = append(cmds, func() tea.Msg {
				downloaded, err := m.downloadMedia(messageCopy, itemCopy)
				return mediaDownloadedMsg{
					MessageID: messageCopy.ID,
					Media:     downloaded,
					Err:       err,
				}
			})
		}
	}
	return *m, batchCmds(cmds...)
}

func (m *Model) syncOverlayCmd() tea.Cmd {
	if m.previewReport.Selected != media.BackendUeberzugPP {
		return m.clearOverlayCmd()
	}
	if m.helpVisible || m.syncOverlay.Visible {
		return m.clearOverlayCmd()
	}
	placements := m.syncableOverlayPlacements()
	signature := overlayPlacementsSignature(placements)
	if signature == m.overlaySignature || (m.overlaySyncPending && signature == m.overlayPendingSignature) {
		return nil
	}
	if signature == "" {
		return m.syncEmptyOverlayCmd()
	}
	if m.overlay == nil {
		m.overlay = media.NewOverlayManager(m.previewReport.UeberzugPPOutput)
	}
	m.invalidateStaleOverlaySync(signature)
	m.overlaySyncPending = true
	m.overlayPendingSignature = signature
	manager := m.overlay
	epoch := manager.Epoch()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), terminalMediaSyncTimeout)
		defer cancel()
		return mediaOverlayMsg{
			Signature: signature,
			Err:       manager.SyncEpoch(ctx, epoch, placements),
		}
	}
}

func (m *Model) syncEmptyOverlayCmd() tea.Cmd {
	signature := ""
	if signature == m.overlaySignature || (m.overlaySyncPending && signature == m.overlayPendingSignature) {
		return nil
	}
	if m.overlay == nil {
		m.overlaySignature = ""
		m.overlaySyncPending = false
		m.overlayPendingSignature = ""
		return nil
	}
	m.invalidateStaleOverlaySync(signature)
	m.overlaySyncPending = true
	m.overlayPendingSignature = signature
	manager := m.overlay
	epoch := manager.Epoch()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), terminalMediaSyncTimeout)
		defer cancel()
		return mediaOverlayMsg{
			Signature: signature,
			Err:       manager.SyncEpoch(ctx, epoch, nil),
		}
	}
}

func (m Model) syncableOverlayPlacements() []media.Placement {
	var placements []media.Placement
	if !m.mediaOverlayPaused {
		placements = append(placements, m.visibleMediaPlacements()...)
	}
	if !m.avatarOverlayPaused {
		placements = append(placements, m.visibleChatAvatarPlacements()...)
	}
	return placements
}

func (m *Model) clearOverlayCmd() tea.Cmd {
	signature := ""
	if m.overlay == nil {
		m.overlaySignature = ""
		m.overlaySyncPending = false
		m.overlayPendingSignature = ""
		return nil
	}
	if signature == m.overlaySignature || (m.overlaySyncPending && signature == m.overlayPendingSignature) {
		return nil
	}
	m.invalidateStaleOverlaySync(signature)
	m.overlaySyncPending = true
	m.overlayPendingSignature = signature
	manager := m.overlay
	epoch := manager.Epoch()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), terminalMediaSyncTimeout)
		defer cancel()
		return mediaOverlayMsg{
			Signature: signature,
			Err:       manager.SyncEpoch(ctx, epoch, nil),
		}
	}
}

func (m *Model) syncSixelCmd() tea.Cmd {
	if m.previewReport.Selected != media.BackendSixel {
		return m.clearSixelCmd()
	}
	if m.helpVisible || m.syncOverlay.Visible {
		return m.clearSixelCmd()
	}
	placements := m.syncableSixelPlacements()
	signature := media.SixelPlacementsSignature(placements)
	if signature == m.sixelSignature || (m.sixelSyncPending && signature == m.sixelPendingSignature) {
		return nil
	}
	if signature == "" {
		return m.clearSixelCmd()
	}
	m.ensureSixelManager()
	if m.sixel == nil {
		m.sixelSignature = ""
		m.sixelSyncPending = false
		m.sixelPendingSignature = ""
		return nil
	}
	m.invalidateStaleSixelSync(signature)
	m.sixelSyncPending = true
	m.sixelPendingSignature = signature
	manager := m.sixel
	epoch := manager.Epoch()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), terminalMediaSyncTimeout)
		defer cancel()
		timer := time.NewTimer(sixelPaintDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return sixelOverlayMsg{
				Signature: signature,
				Err:       ctx.Err(),
			}
		}
		return sixelOverlayMsg{
			Signature: signature,
			Err:       manager.SyncEpoch(ctx, epoch, placements),
		}
	}
}

func (m Model) syncableSixelPlacements() []media.SixelPlacement {
	var placements []media.SixelPlacement
	if !m.mediaOverlayPaused {
		placements = append(placements, m.visibleSixelMediaPlacements()...)
	}
	if !m.avatarOverlayPaused {
		placements = append(placements, m.visibleSixelChatAvatarPlacements()...)
	}
	return placements
}

func (m *Model) clearSixelCmd() tea.Cmd {
	signature := ""
	if m.sixel == nil {
		m.sixelSignature = ""
		m.sixelSyncPending = false
		m.sixelPendingSignature = ""
		return nil
	}
	if signature == m.sixelSignature || (m.sixelSyncPending && signature == m.sixelPendingSignature) {
		return nil
	}
	m.invalidateStaleSixelSync(signature)
	m.sixelSyncPending = true
	m.sixelPendingSignature = signature
	manager := m.sixel
	epoch := manager.Epoch()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), terminalMediaSyncTimeout)
		defer cancel()
		return sixelOverlayMsg{
			Signature: signature,
			Err:       manager.SyncEpoch(ctx, epoch, nil),
		}
	}
}

func overlayPlacementsSignature(placements []media.Placement) string {
	if len(placements) == 0 {
		return ""
	}
	parts := make([]string, 0, len(placements))
	for _, placement := range placements {
		parts = append(parts, fmt.Sprintf(
			"%q %d %d %d %d %q %q",
			placement.Identifier,
			placement.X,
			placement.Y,
			placement.MaxWidth,
			placement.MaxHeight,
			placement.Path,
			placement.Scaler,
		))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

func (m Model) requestedPreviewRequests() []media.PreviewRequest {
	if m.width <= 0 || m.height <= 0 || m.previewReport.Selected == media.BackendNone {
		return nil
	}
	if _, ok := m.inlinePreviewBackend(); !ok {
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
	start, end := 0, len(messages)
	if geometry, ok := m.messagePaneGeometry(); ok {
		start, end = m.visibleMessageRange(len(messages), max(1, geometry.height-2))
	}
	for _, message := range messages[start:end] {
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
			if highQualityPreviewRequiresLocalFile(request.Backend, media.MediaKind(request.MIMEType, request.FileName)) && strings.TrimSpace(request.LocalPath) == "" {
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

func (m Model) requestedAvatarPreviewRequests() []media.PreviewRequest {
	if m.width <= 0 || m.height <= 0 {
		return nil
	}
	backend, ok := m.avatarPreviewBackend()
	if !ok {
		return nil
	}
	chatIDs := m.visibleChatIDs()
	if len(chatIDs) == 0 {
		return nil
	}

	requests := make([]media.PreviewRequest, 0, len(chatIDs))
	for _, chatID := range chatIDs {
		chat := m.chatByID(chatID)
		request, ok := m.chatAvatarPreviewRequestWithBackend(chat, backend)
		if !ok {
			continue
		}
		requests = append(requests, request)
	}
	return requests
}

func (m Model) previewRequestForMedia(message store.Message, item store.MediaMetadata, width, height int) (media.PreviewRequest, bool) {
	backend := m.previewReport.Selected
	if inlineBackend, ok := m.inlinePreviewBackend(); ok {
		backend = inlineBackend
	}
	return m.previewRequestForMediaWithBackend(message, item, width, height, backend)
}

func (m Model) previewRequestForMediaWithBackend(message store.Message, item store.MediaMetadata, width, height int, backend media.Backend) (media.PreviewRequest, bool) {
	item, _ = normalizeManagedMediaMetadata(m.paths, item)
	kind := media.MediaKind(item.MIMEType, item.FileName)
	requestMIMEType := item.MIMEType
	requestFileName := item.FileName
	requestLocalPath := item.LocalPath
	requestThumbnailPath := item.ThumbnailPath
	if strings.EqualFold(strings.TrimSpace(item.Kind), "sticker") {
		kind = media.KindImage
		if media.MediaKind(requestMIMEType, requestFileName) == media.KindUnsupported {
			requestMIMEType = "image/png"
			requestFileName = "sticker.png"
			if strings.TrimSpace(requestThumbnailPath) != "" {
				requestLocalPath = ""
			}
		}
	}
	if kind != media.KindImage && kind != media.KindVideo {
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
		MIMEType:      requestMIMEType,
		FileName:      requestFileName,
		LocalPath:     requestLocalPath,
		ThumbnailPath: requestThumbnailPath,
		CacheDir:      m.paths.PreviewCacheDir,
		Backend:       backend,
		Width:         width,
		Height:        height,
	}
	if request.MessageID == "" {
		request.MessageID = message.ID
	}
	return request, true
}

func (m Model) chatAvatarPreviewRequest(chat store.Chat) (media.PreviewRequest, bool) {
	backend, ok := m.avatarPreviewBackend()
	if !ok {
		return media.PreviewRequest{}, false
	}
	return m.chatAvatarPreviewRequestWithBackend(chat, backend)
}

func (m Model) chatAvatarPreviewRequestWithBackend(chat store.Chat, backend media.Backend) (media.PreviewRequest, bool) {
	avatarPath := strings.TrimSpace(chat.AvatarPath)
	avatarThumbPath := strings.TrimSpace(chat.AvatarThumbPath)
	if avatarPath == "" && avatarThumbPath == "" {
		return media.PreviewRequest{}, false
	}
	return media.PreviewRequest{
		MessageID:     chatAvatarPreviewMessagePrefix + chat.ID,
		MIMEType:      "image/png",
		FileName:      "avatar.png",
		LocalPath:     avatarPath,
		ThumbnailPath: avatarThumbPath,
		CacheDir:      m.paths.PreviewCacheDir,
		Backend:       backend,
		Width:         chatAvatarPreviewWidth,
		Height:        chatAvatarPreviewHeight,
		Compact:       true,
	}, true
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
	message, item, err := m.repairManagedMediaCache(message, item)
	if err != nil {
		m.status = fmt.Sprintf("repair media metadata failed: %v", err)
		return m, nil
	}

	kind := media.MediaKind(item.MIMEType, item.FileName)
	if kind == media.KindAudio {
		return m.toggleFocusedAudio(message, item)
	}
	if strings.TrimSpace(item.LocalPath) == "" && strings.TrimSpace(item.ThumbnailPath) == "" {
		if m.downloadMedia != nil {
			if !m.startMediaDownload(message, item, "downloading media") {
				return m, nil
			}
			return m, func() tea.Msg {
				downloaded, err := m.downloadMedia(message, item)
				return mediaDownloadedMsg{
					MessageID: message.ID,
					Media:     downloaded,
					Err:       err,
				}
			}
		}
		m.status = mediaDownloadUnavailableStatus(item)
		return m, nil
	}
	if kind == media.KindUnsupported {
		if strings.TrimSpace(item.LocalPath) != "" {
			return m.openFocusedMedia()
		}
		m.status = fmt.Sprintf("no inline preview for %s", m.mediaDisplayName(item))
		return m, nil
	}
	inlineBackend, hasInlineBackend := m.inlinePreviewBackend()
	if !hasInlineBackend {
		if m.inlineFallbackNeedsPrompt() {
			if m.previewRequested == nil {
				m.previewRequested = map[string]bool{}
			}
			m.previewRequested[mediaActivationKey(message, item)] = true
			m.showInlineFallbackPrompt()
			return m, nil
		}
		if strings.TrimSpace(item.LocalPath) != "" {
			return m.openFocusedMedia()
		}
		m.status = fmt.Sprintf("preview backend %s cannot render inline", m.previewReport.Selected)
		return m, nil
	}
	if highQualityPreviewRequiresLocalFile(inlineBackend, kind) && strings.TrimSpace(item.LocalPath) == "" {
		if m.downloadMedia != nil {
			if !m.startMediaDownload(message, item, "downloading media") {
				return m, nil
			}
			return m, func() tea.Msg {
				downloaded, err := m.downloadMedia(message, item)
				return mediaDownloadedMsg{
					MessageID: message.ID,
					Media:     downloaded,
					Err:       err,
				}
			}
		}
		m.status = mediaDownloadUnavailableStatus(item)
		return m, nil
	}

	request, ok := m.previewRequestForMedia(message, item, 0, 0)
	if !ok {
		m.status = fmt.Sprintf("no inline preview for %s", m.mediaDisplayName(item))
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
		m.status = fmt.Sprintf("rendering preview: %s", m.mediaDisplayName(item))
		return m, nil
	}
	if m.previewRequested == nil {
		m.previewRequested = map[string]bool{}
	}
	m.previewRequested[mediaActivationKey(message, item)] = true
	m.status = fmt.Sprintf("rendering preview: %s", m.mediaDisplayName(item))
	return m, nil
}

func (m Model) openFocusedMedia() (tea.Model, tea.Cmd) {
	return m.openFocusedMediaWith(m.openMedia, "media opener unavailable", "opening media")
}

func (m Model) openFocusedMediaDetached() (tea.Model, tea.Cmd) {
	return m.openFocusedMediaWith(m.openMediaDetached, "detached media opener unavailable", "opening media externally")
}

func (m Model) openFocusedMediaWith(open func(store.MediaMetadata) tea.Cmd, unavailableStatus, statusPrefix string) (tea.Model, tea.Cmd) {
	message, item, ok := m.focusedMedia()
	if !ok {
		m.status = "no media on focused message"
		return m, nil
	}
	message, item, err := m.repairManagedMediaCache(message, item)
	if err != nil {
		m.status = fmt.Sprintf("repair media metadata failed: %v", err)
		return m, nil
	}
	if strings.TrimSpace(item.LocalPath) == "" {
		if m.downloadMedia != nil {
			if !m.startMediaDownload(message, item, "downloading media") {
				return m, nil
			}
			return m, func() tea.Msg {
				downloaded, err := m.downloadMedia(message, item)
				if err == nil && strings.TrimSpace(downloaded.LocalPath) != "" && open != nil {
					if openCmd := open(downloaded); openCmd != nil {
						openMsg := openCmd()
						if finished, ok := openMsg.(MediaOpenFinishedMsg); ok {
							finished.MessageID = message.ID
							finished.Media = downloaded
							return finished
						}
						if openMsg != nil {
							return openMsg
						}
					}
				}
				return mediaDownloadedMsg{MessageID: message.ID, Media: downloaded, Err: err}
			}
		}
		m.status = mediaDownloadUnavailableStatus(item)
		return m, nil
	}
	if open == nil {
		m.status = unavailableStatus
		return m, nil
	}
	m.status = fmt.Sprintf("%s: %s", statusPrefix, m.mediaDisplayName(item))
	m.pauseOverlays(true, false)
	return m, open(item)
}

func (m Model) saveFocusedMedia() (tea.Model, tea.Cmd) {
	message, item, ok := m.focusedMedia()
	if !ok {
		m.status = "no media on focused message"
		return m, nil
	}
	message, item, err := m.repairManagedMediaCache(message, item)
	if err != nil {
		m.status = fmt.Sprintf("repair media metadata failed: %v", err)
		return m, nil
	}
	if strings.TrimSpace(item.LocalPath) == "" {
		if m.downloadMedia != nil {
			if !m.startMediaDownload(message, item, "downloading media") {
				return m, nil
			}
			return m, func() tea.Msg {
				downloaded, err := m.downloadMedia(message, item)
				if err != nil {
					return mediaSavedMsg{MessageID: message.ID, Media: downloaded, Err: err}
				}
				path, err := saveMediaToDownloads(downloaded, m.config.DownloadsDir)
				return mediaSavedMsg{MessageID: message.ID, Media: downloaded, Path: path, Err: err}
			}
		}
		m.status = mediaDownloadUnavailableStatus(item)
		return m, nil
	}
	m.status = fmt.Sprintf("saving media: %s", m.mediaDisplayName(item))
	return m, func() tea.Msg {
		path, err := saveMediaToDownloads(item, m.config.DownloadsDir)
		return mediaSavedMsg{MessageID: message.ID, Media: item, Path: path, Err: err}
	}
}

func (m Model) copyFocusedImage() (tea.Model, tea.Cmd) {
	message, item, ok := m.focusedMedia()
	if !ok {
		m.status = "no media on focused message"
		return m, nil
	}
	if media.MediaKind(item.MIMEType, item.FileName) != media.KindImage {
		m.status = "focused media is not an image"
		return m, nil
	}
	message, item, err := m.repairManagedMediaCache(message, item)
	if err != nil {
		m.status = fmt.Sprintf("repair media metadata failed: %v", err)
		return m, nil
	}
	if m.copyImageToClipboard == nil {
		m.status = "image clipboard copy unavailable"
		return m, nil
	}
	if strings.TrimSpace(item.LocalPath) == "" {
		if m.downloadMedia != nil {
			if !m.startMediaDownload(message, item, "downloading image") {
				return m, nil
			}
			return m, func() tea.Msg {
				downloaded, err := m.downloadMedia(message, item)
				if err != nil {
					return ClipboardImageCopiedMsg{MessageID: message.ID, Media: downloaded, Err: err}
				}
				return clipboardImageCopiedFromCmd(m.copyImageToClipboard(downloaded), message.ID, downloaded)
			}
		}
		m.status = mediaDownloadUnavailableStatus(item)
		return m, nil
	}
	m.status = fmt.Sprintf("copying image: %s", m.mediaDisplayName(item))
	return m, func() tea.Msg {
		return clipboardImageCopiedFromCmd(m.copyImageToClipboard(item), message.ID, item)
	}
}

func clipboardImageCopiedFromCmd(cmd tea.Cmd, messageID string, item store.MediaMetadata) tea.Msg {
	if cmd == nil {
		return ClipboardImageCopiedMsg{MessageID: messageID, Media: item}
	}
	msg := cmd()
	if copied, ok := msg.(ClipboardImageCopiedMsg); ok {
		copied.MessageID = messageID
		if strings.TrimSpace(copied.Media.MessageID) == "" {
			copied.Media = item
		}
		return copied
	}
	return ClipboardImageCopiedMsg{MessageID: messageID, Media: item}
}

func (m Model) toggleFocusedAudio(message store.Message, item store.MediaMetadata) (tea.Model, tea.Cmd) {
	message, item, err := m.repairManagedMediaCache(message, item)
	if err != nil {
		m.status = fmt.Sprintf("repair media metadata failed: %v", err)
		return m, nil
	}
	if m.audioMediaKey != "" && m.audioMediaKey == mediaActivationKey(message, item) {
		return m.stopAudio("audio stopped")
	}
	if strings.TrimSpace(item.LocalPath) == "" && m.downloadMedia == nil {
		m.status = mediaDownloadUnavailableStatus(item)
		return m, nil
	}
	if m.startAudio == nil {
		m.status = "audio player unavailable"
		return m, nil
	}
	if strings.TrimSpace(item.LocalPath) == "" && !m.startMediaDownload(message, item, "downloading audio") {
		return m, nil
	}

	m.stopActiveAudio()
	m.audioSession++
	session := m.audioSession
	m.audioMessageID = message.ID
	m.audioMediaKey = mediaActivationKey(message, item)
	m.audioDisplayName = m.mediaDisplayName(item)
	if strings.TrimSpace(item.LocalPath) == "" {
		m.status = fmt.Sprintf("downloading audio: %s", m.audioDisplayName)
	} else {
		m.status = fmt.Sprintf("starting audio: %s", m.audioDisplayName)
	}
	return m, m.startAudioCmd(session, message, item)
}

func (m Model) startAudioCmd(session int, message store.Message, item store.MediaMetadata) tea.Cmd {
	return func() tea.Msg {
		audioItem := item
		downloaded := false
		if strings.TrimSpace(audioItem.LocalPath) == "" {
			downloaded = true
			next, err := m.downloadMedia(message, audioItem)
			audioItem = next
			if audioItem.MessageID == "" {
				audioItem.MessageID = message.ID
			}
			if audioItem.DownloadState == "" && strings.TrimSpace(audioItem.LocalPath) != "" {
				audioItem.DownloadState = "downloaded"
			}
			if err != nil {
				return audioStartedMsg{Session: session, MessageID: message.ID, Media: audioItem, Err: err}
			}
		}
		if strings.TrimSpace(audioItem.LocalPath) == "" {
			return audioStartedMsg{
				Session:    session,
				MessageID:  message.ID,
				Media:      audioItem,
				Downloaded: downloaded,
				Err:        fmt.Errorf("audio is not downloaded"),
			}
		}

		process, err := m.startAudio(audioItem)
		return audioStartedMsg{
			Session:    session,
			MessageID:  message.ID,
			Media:      audioItem,
			Process:    process,
			Downloaded: downloaded,
			Err:        err,
		}
	}
}

func (m Model) handleAudioStarted(msg audioStartedMsg) (tea.Model, tea.Cmd) {
	m.clearMediaDownloadInFlight(msg.MessageID)
	if msg.Session != m.audioSession {
		if msg.Process != nil {
			_ = msg.Process.Stop()
		}
		return m, nil
	}

	if msg.Downloaded {
		updated, err := m.applyDownloadedAudio(msg.MessageID, msg.Media)
		if err != nil {
			if msg.Process != nil {
				_ = msg.Process.Stop()
			}
			m.clearAudioState()
			m.status = fmt.Sprintf("audio failed: %s", shortError(err))
			return m, nil
		}
		msg.Media = updated
	}

	if msg.Err != nil {
		if msg.Process != nil {
			_ = msg.Process.Stop()
		}
		m.clearAudioState()
		m.status = fmt.Sprintf("audio failed: %s", shortError(msg.Err))
		return m, nil
	}
	if msg.Process == nil {
		m.clearAudioState()
		m.status = "audio failed: player did not start"
		return m, nil
	}

	m.audioProcess = msg.Process
	m.audioMessageID = msg.MessageID
	m.audioMediaKey = mediaActivationKey(store.Message{ID: msg.MessageID}, msg.Media)
	m.audioDisplayName = m.mediaDisplayName(msg.Media)
	m.status = fmt.Sprintf("playing audio: %s", m.audioDisplayName)
	return m, m.waitAudioCmd(msg.Session, msg.MessageID, m.audioMediaKey, msg.Process)
}

func (m Model) handleAudioFinished(msg audioFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.Session != m.audioSession || msg.MediaKey != m.audioMediaKey {
		return m, nil
	}
	displayName := m.audioDisplayName
	m.clearAudioState()
	if msg.Err != nil {
		m.status = fmt.Sprintf("audio stopped: %s", shortError(msg.Err))
	} else {
		m.status = fmt.Sprintf("audio finished: %s", displayName)
	}
	return m, nil
}

func (m Model) waitAudioCmd(session int, messageID, mediaKey string, process AudioProcess) tea.Cmd {
	return func() tea.Msg {
		return audioFinishedMsg{
			Session:   session,
			MessageID: messageID,
			MediaKey:  mediaKey,
			Err:       process.Wait(),
		}
	}
}

func (m Model) stopAudio(status string) (tea.Model, tea.Cmd) {
	m.stopActiveAudio()
	m.audioSession++
	m.clearAudioState()
	if strings.TrimSpace(status) == "" {
		status = "audio stopped"
	}
	m.status = status
	return m, nil
}

func (m *Model) stopActiveAudio() {
	if m.audioProcess != nil {
		_ = m.audioProcess.Stop()
		m.audioProcess = nil
	}
}

func (m *Model) clearAudioState() {
	m.audioProcess = nil
	m.audioMessageID = ""
	m.audioMediaKey = ""
	m.audioDisplayName = ""
}

func (m *Model) applyDownloadedAudio(messageID string, item store.MediaMetadata) (store.MediaMetadata, error) {
	if strings.TrimSpace(messageID) == "" {
		return store.MediaMetadata{}, fmt.Errorf("missing message id")
	}
	if item.MessageID == "" {
		item.MessageID = messageID
	}
	if item.DownloadState == "" && strings.TrimSpace(item.LocalPath) != "" {
		item.DownloadState = "downloaded"
	}
	if strings.TrimSpace(item.LocalPath) == "" {
		return store.MediaMetadata{}, fmt.Errorf("downloaded audio has no local file")
	}

	updated, _, message := m.updateLoadedMedia(messageID, item)
	if !updated {
		return store.MediaMetadata{}, fmt.Errorf("message is not loaded")
	}
	item = message.Media[0]
	if m.saveMedia != nil {
		if err := m.saveMedia(item); err != nil {
			return store.MediaMetadata{}, fmt.Errorf("save media metadata: %w", err)
		}
	}
	return item, nil
}

func (m Model) clearMediaPreviews(status string) (tea.Model, tea.Cmd) {
	m.previewCache = map[string]media.Preview{}
	m.previewInflight = map[string]bool{}
	m.previewRequested = map[string]bool{}
	m.previewGeneration++
	m.clearOverlayPause()
	if m.overlay != nil {
		m.overlay.Invalidate()
	}
	if m.sixel != nil {
		m.sixel.Invalidate()
	}
	if strings.TrimSpace(status) == "" {
		status = "media previews unloaded"
	}
	m.status = status
	return m, batchCmds(m.clearOverlayCmd(), m.clearSixelCmd())
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
	height := min(previewMaxHeight(m.config), 18)
	if geometry, ok := m.messagePaneGeometry(); ok {
		available := max(4, geometry.height-6)
		viewportLimit := max(4, geometry.height*45/100)
		height = min(height, min(available, viewportLimit))
	}
	if m.compactLayout && m.layoutWidth() < 72 {
		height = min(height, 10)
	}
	return width, max(4, height)
}

func (m Model) messagePaneContentWidth() int {
	width := m.layoutWidth()
	if width <= 0 {
		return 0
	}

	if m.compactLayout {
		style := m.panelStyle(FocusMessages)
		return panelContentWidth(style, width)
	}

	chatWidth := max(24, width/4)
	previewWidth := max(26, width/4)
	messageWidth := width - chatWidth
	if m.infoPaneVisible {
		messageWidth -= previewWidth
	}
	style := m.panelStyle(FocusMessages)
	return panelContentWidth(style, messageWidth)
}

func (m Model) messagePaneContentHeight() int {
	if m.height <= 0 {
		return 0
	}
	inputHeight := m.inputHeight()
	bodyHeight := m.height - 1 - inputHeight
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	return panelContentHeight(m.panelStyle(FocusMessages), bodyHeight)
}

func (m Model) messageViewportHeight() int {
	height := m.messagePaneContentHeight()
	if height <= 0 {
		return 0
	}
	width := m.messagePaneContentWidth()
	headerHeight := countLines(m.renderMessageHeader(m.currentChat().Title, max(1, width)))
	bodyHeight := max(1, height-headerHeight)
	footer := m.renderMessageFooter(max(1, width-2))
	footerHeight := min(countLines(footer), bodyHeight)
	return max(1, bodyHeight-footerHeight)
}

func (m Model) handleMediaPreviewReady(msg mediaPreviewReadyMsg) (tea.Model, tea.Cmd) {
	if msg.Generation != m.previewGeneration {
		return m, nil
	}
	if m.previewCache == nil {
		m.previewCache = map[string]media.Preview{}
	}
	if m.previewInflight != nil {
		delete(m.previewInflight, msg.Key)
	}
	m.previewCache[msg.Key] = msg.Preview
	if msg.Preview.Err != nil {
		if isChatAvatarPreviewRequest(msg.Request) {
			return m, nil
		}
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
		if !isChatAvatarPreviewRequest(msg.Request) {
			m.status = fmt.Sprintf("preview ready: %s (%s %s %dx%d)", previewRequestName(msg.Request), msg.Preview.RenderedBackend, msg.Preview.SourceKind, msg.Preview.Width, msg.Preview.Height)
		}
	}
	return m, nil
}

func isChatAvatarPreviewRequest(request media.PreviewRequest) bool {
	return strings.HasPrefix(request.MessageID, chatAvatarPreviewMessagePrefix)
}

func (m Model) handleMediaDownloaded(msg mediaDownloadedMsg) (tea.Model, tea.Cmd) {
	m.clearMediaDownloadInFlight(msg.MessageID)
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
	m.status = fmt.Sprintf("downloaded media; rendering preview: %s", m.mediaDisplayName(msg.Media))
	return m, nil
}

func (m Model) handleMediaSaved(msg mediaSavedMsg) (tea.Model, tea.Cmd) {
	m.clearMediaDownloadInFlight(msg.MessageID)
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
		return 18
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

func (m Model) handlePickedSticker(msg StickerPickedMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeNormal
	m.focus = FocusMessages
	if msg.Cancelled {
		m.status = "sticker picker cancelled"
		return m, nil
	}
	if msg.Err != nil {
		m.status = fmt.Sprintf("sticker failed: %v", msg.Err)
		return m, nil
	}
	if strings.TrimSpace(msg.Sticker.ID) == "" {
		m.status = "sticker picker returned no sticker"
		return m, nil
	}
	if m.sendSticker == nil {
		m.status = "sticker send is unavailable"
		return m, nil
	}
	if len(m.chats) == 0 || m.currentChat().ID == "" {
		m.status = "no chat selected"
		return m, nil
	}

	chatID := m.currentChat().ID
	m.status = "sticker queued"
	return m, m.sendStickerCmd(chatID, msg.Sticker)
}

func (m Model) handleClipboardAttachmentPasted(msg ClipboardAttachmentPastedMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeInsert
	m.focus = FocusMessages
	if msg.Err != nil {
		m.status = fmt.Sprintf("paste attachment failed: %v", msg.Err)
		return m, nil
	}
	m.stageAttachment(msg.Attachment)
	return m, nil
}

func (m Model) handleClipboardImageCopied(msg ClipboardImageCopiedMsg) (tea.Model, tea.Cmd) {
	m.clearMediaDownloadInFlight(msg.MessageID)
	if msg.Err != nil {
		m.status = fmt.Sprintf("copy image failed: %v", msg.Err)
		return m, nil
	}
	if msg.MessageID != "" && strings.TrimSpace(msg.Media.LocalPath) != "" {
		if updated, _, _ := m.updateLoadedMedia(msg.MessageID, msg.Media); updated && m.saveMedia != nil {
			if err := m.saveMedia(msg.Media); err != nil {
				m.status = fmt.Sprintf("copy metadata failed: %v", err)
				return m, nil
			}
		}
	}
	m.status = fmt.Sprintf("copied image: %s", m.mediaDisplayName(msg.Media))
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

func (m Model) startStickerPicker() (tea.Model, tea.Cmd) {
	if len(m.chats) == 0 || m.currentChat().ID == "" {
		m.status = "no chat selected"
		return m, nil
	}
	m.mode = ModeNormal
	m.focus = FocusMessages
	if m.pickSticker == nil {
		m.status = "sticker picker unavailable"
		return m, nil
	}
	if m.sendSticker == nil {
		m.status = "sticker send is unavailable"
		return m, nil
	}
	if m.requireOnlineForSend && m.connectionState != ConnectionOnline {
		m.status = "sticker send needs WhatsApp online"
		return m, nil
	}

	m.status = "opening sticker picker"
	return m, m.pickSticker()
}

func (m Model) startClipboardAttachmentPaste() (tea.Model, tea.Cmd) {
	if len(m.chats) == 0 || m.currentChat().ID == "" {
		m.status = "no chat selected"
		return m, nil
	}
	m.mode = ModeInsert
	m.focus = FocusMessages
	if m.pasteAttachmentFromClipboard == nil {
		m.status = "clipboard attachment paste unavailable"
		return m, nil
	}
	m.status = "pasting attachment from clipboard"
	return m, m.pasteAttachmentFromClipboard()
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

func (m Model) validateAttachmentsForSend(body string) error {
	if len(m.attachments) == 0 {
		return nil
	}
	if len(m.attachments) > 1 {
		return fmt.Errorf("only one attachment per message is supported")
	}
	attachment := m.attachments[0]
	localPath := strings.TrimSpace(attachment.LocalPath)
	if localPath == "" {
		return fmt.Errorf("attachment local path is required")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			name := strings.TrimSpace(attachment.FileName)
			if name == "" {
				name = localPath
			}
			return fmt.Errorf("attachment file is missing: %s", name)
		}
		return fmt.Errorf("stat attachment: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("attachment path is a directory")
	}
	if media.MediaKind(attachment.MIMEType, attachment.FileName) == media.KindAudio && body != "" {
		return fmt.Errorf("audio attachments do not support captions")
	}
	return nil
}

func (m Model) validateRetryMessage(message store.Message) error {
	if !message.IsOutgoing {
		return fmt.Errorf("retry needs an outgoing message")
	}
	if strings.TrimSpace(message.Status) != "failed" {
		return fmt.Errorf("retry needs a failed message")
	}
	if len(message.Media) == 0 {
		return fmt.Errorf("retry needs a media attachment")
	}
	if len(message.Media) > 1 {
		return fmt.Errorf("only one attachment per message is supported")
	}
	if m.requireOnlineForSend && m.connectionState != ConnectionOnline {
		return fmt.Errorf("retry needs WhatsApp online")
	}
	item := message.Media[0]
	if strings.TrimSpace(item.LocalPath) == "" {
		return fmt.Errorf("retry needs a local attachment file")
	}
	info, err := os.Stat(item.LocalPath)
	if err != nil {
		if os.IsNotExist(err) {
			name := strings.TrimSpace(item.FileName)
			if name == "" {
				name = item.LocalPath
			}
			return fmt.Errorf("attachment file is missing: %s", name)
		}
		return fmt.Errorf("stat attachment: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("attachment path is a directory")
	}
	return nil
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
	m.deleteConfirmID = ""
	m.deleteForEveryoneConfirmID = ""
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

	m.removeLoadedMessage(message.ID)
	m.deleteConfirmID = ""
	m.rebuildSearchMatches()
	m.status = "deleted message locally"
	return nil
}

func (m *Model) armDeleteFocusedMessageForEveryone() {
	m.deleteForEveryoneConfirmID = ""
	m.deleteConfirmID = ""
	m.confirmLine = ""
	message, ok := m.focusedMessage()
	if !ok {
		m.status = "no message selected"
		return
	}
	if err := m.validateDeleteForEveryone(message); err != nil {
		m.status = fmt.Sprintf("delete for everybody unavailable: %v", err)
		return
	}
	m.deleteForEveryoneConfirmID = message.ID
	m.mode = ModeConfirm
	m.status = "type Y and press enter to delete for everybody"
}

func (m Model) deleteConfirmedMessageForEveryone() (Model, tea.Cmd) {
	m.mode = ModeNormal
	message, ok := m.focusedMessage()
	if !ok {
		m.deleteForEveryoneConfirmID = ""
		m.confirmLine = ""
		m.status = "delete for everybody failed: no message selected"
		return m, nil
	}
	if m.deleteForEveryoneConfirmID != message.ID {
		m.deleteForEveryoneConfirmID = ""
		m.confirmLine = ""
		m.status = "delete for everybody failed: confirmation expired"
		return m, nil
	}
	if err := m.validateDeleteForEveryone(message); err != nil {
		m.deleteForEveryoneConfirmID = ""
		m.confirmLine = ""
		m.status = fmt.Sprintf("delete for everybody failed: %v", err)
		return m, nil
	}
	cmd := m.deleteMessageForEveryone(message)
	if cmd == nil {
		m.deleteForEveryoneConfirmID = ""
		m.confirmLine = ""
		m.status = "delete for everybody failed: unavailable"
		return m, nil
	}
	m.deleteForEveryoneConfirmID = ""
	m.confirmLine = ""
	m.status = "delete for everybody queued"
	return m, cmd
}

func (m Model) validateDeleteForEveryone(message store.Message) error {
	if m.deleteMessageForEveryone == nil {
		return fmt.Errorf("live delete is unavailable")
	}
	if m.connectionState != ConnectionOnline {
		return fmt.Errorf("WhatsApp must be online")
	}
	if !message.IsOutgoing {
		return fmt.Errorf("only your outgoing messages can be deleted for everybody")
	}
	if strings.TrimSpace(message.RemoteID) == "" {
		return fmt.Errorf("message has no WhatsApp id")
	}
	return nil
}

func (m Model) submitEditedMessage() (tea.Model, tea.Cmd) {
	if m.editTarget == nil {
		m.status = "edit failed: no message selected"
		m.mode = ModeNormal
		return m, nil
	}
	body := strings.TrimSpace(m.composer)
	target := *m.editTarget
	if err := m.validateEditMessage(target, body); err != nil {
		m.status = fmt.Sprintf("edit failed: %v", err)
		return m, nil
	}
	cmd := m.editMessage(target, body)
	if cmd == nil {
		m.status = "edit failed: unavailable"
		return m, nil
	}
	chatID := m.currentChat().ID
	m.composer = ""
	m.attachments = nil
	m.replyTo = nil
	m.editTarget = nil
	m.sendOwnPresence(chatID, false)
	m.mode = ModeNormal
	m.focus = FocusMessages
	m.status = "edit queued"
	return m, cmd
}

func (m Model) validateEditMessage(message store.Message, body string) error {
	if err := m.validateEditTarget(message); err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("edit body is required")
	}
	if strings.TrimSpace(body) == strings.TrimSpace(message.Body) {
		return fmt.Errorf("message is unchanged")
	}
	return nil
}

func (m Model) validateEditTarget(message store.Message) error {
	if m.editMessage == nil {
		return fmt.Errorf("live edit is unavailable")
	}
	if m.connectionState != ConnectionOnline {
		return fmt.Errorf("WhatsApp must be online")
	}
	if !message.IsOutgoing {
		return fmt.Errorf("only your outgoing text messages can be edited")
	}
	if strings.TrimSpace(message.RemoteID) == "" {
		return fmt.Errorf("message has no WhatsApp id")
	}
	if strings.TrimSpace(message.Body) == "" {
		return fmt.Errorf("message has no editable text")
	}
	if len(message.Media) > 0 {
		return fmt.Errorf("media captions are not editable yet")
	}
	return nil
}

func (m *Model) handleMessageDeletedForEveryone(msg MessageDeletedForEveryoneMsg) {
	if msg.Err != nil {
		m.status = fmt.Sprintf("delete for everybody failed: %s", shortError(msg.Err))
		return
	}
	messageID := strings.TrimSpace(msg.MessageID)
	if messageID == "" {
		m.status = "delete for everybody failed: missing message id"
		return
	}
	if !m.removeLoadedMessage(messageID) {
		m.status = "deleted message for everybody"
		return
	}
	m.rebuildSearchMatches()
	m.status = "deleted message for everybody"
}

func (m *Model) handleMessageEdited(msg MessageEditedMsg) {
	if msg.Err != nil {
		m.status = fmt.Sprintf("edit failed: %s", shortError(msg.Err))
		return
	}
	messageID := strings.TrimSpace(msg.MessageID)
	body := strings.TrimSpace(msg.Body)
	if messageID == "" {
		m.status = "edit failed: missing message id"
		return
	}
	if body == "" {
		m.status = "edit failed: empty body"
		return
	}
	editedAt := msg.EditedAt
	if editedAt.IsZero() {
		editedAt = time.Now()
	}
	if !m.updateLoadedMessageBody(messageID, body, editedAt) {
		m.status = "edited message"
		return
	}
	m.rebuildSearchMatches()
	m.status = "edited message"
}

func (m *Model) handleForwardMessagesFinished(msg ForwardMessagesFinishedMsg) {
	if msg.Err != nil {
		m.status = fmt.Sprintf("forward failed: %s", shortError(msg.Err))
		return
	}
	parts := []string{fmt.Sprintf("forwarded %d message(s)", msg.Sent)}
	if msg.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d unavailable", msg.Skipped))
	}
	if msg.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", msg.Failed))
	}
	m.status = strings.Join(parts, "; ")
}

func (m *Model) updateLoadedMessageBody(messageID, body string, editedAt time.Time) bool {
	if strings.TrimSpace(messageID) == "" {
		return false
	}
	updated := false
	for chatID, messages := range m.messagesByChat {
		for i := range messages {
			if messages[i].ID != messageID {
				continue
			}
			messages[i].Body = body
			messages[i].EditedAt = editedAt
			m.messagesByChat[chatID] = messages
			updated = true
			break
		}
	}
	for chatID, messages := range m.unfilteredByChat {
		for i := range messages {
			if messages[i].ID != messageID {
				continue
			}
			messages[i].Body = body
			messages[i].EditedAt = editedAt
			m.unfilteredByChat[chatID] = messages
			updated = true
			break
		}
	}
	return updated
}

func (m *Model) removeLoadedMessage(messageID string) bool {
	if strings.TrimSpace(messageID) == "" {
		return false
	}
	removed := false
	for chatID, messages := range m.messagesByChat {
		updated := removeMessageByID(messages, messageID)
		if len(updated) != len(messages) {
			removed = true
			m.messagesByChat[chatID] = updated
		}
	}
	for chatID, messages := range m.unfilteredByChat {
		updated := removeMessageByID(messages, messageID)
		if len(updated) != len(messages) {
			removed = true
			m.unfilteredByChat[chatID] = updated
		}
	}
	m.messageCursor = clamp(m.messageCursor, 0, max(0, len(m.currentMessages())-1))
	m.messageScrollTop = clamp(m.messageScrollTop, 0, max(0, len(m.currentMessages())-1))
	return removed
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
		if len(messages[index].Media) == 0 {
			if mediaItem.DownloadState == "" {
				mediaItem.DownloadState = "downloaded"
			}
			messages[index].Media = []store.MediaMetadata{mediaItem}
		} else {
			mediaItem = mergeMediaMetadata(messages[index].Media[0], mediaItem)
			if mediaItem.DownloadState == "" {
				mediaItem.DownloadState = "downloaded"
			}
			messages[index].Media[0] = mediaItem
		}
		return messages, true, messages[index]
	}
	return messages, false, store.Message{}
}

func mergeMediaMetadata(existing, next store.MediaMetadata) store.MediaMetadata {
	incomingLocalPath := strings.TrimSpace(next.LocalPath) != ""
	if strings.TrimSpace(next.MessageID) == "" {
		next.MessageID = existing.MessageID
	}
	if strings.TrimSpace(next.Kind) == "" {
		next.Kind = existing.Kind
	}
	if strings.TrimSpace(next.MIMEType) == "" {
		next.MIMEType = existing.MIMEType
	}
	if strings.TrimSpace(next.FileName) == "" {
		next.FileName = existing.FileName
	}
	if next.SizeBytes <= 0 {
		next.SizeBytes = existing.SizeBytes
	}
	if strings.TrimSpace(next.LocalPath) == "" {
		next.LocalPath = existing.LocalPath
	}
	if strings.TrimSpace(next.ThumbnailPath) == "" {
		next.ThumbnailPath = existing.ThumbnailPath
	}
	if strings.TrimSpace(existing.LocalPath) != "" && !incomingLocalPath && strings.TrimSpace(next.DownloadState) == "remote" {
		next.DownloadState = existing.DownloadState
	}
	if !incomingLocalPath && strings.TrimSpace(next.DownloadState) == "" {
		next.DownloadState = existing.DownloadState
	}
	if !next.IsAnimated {
		next.IsAnimated = existing.IsAnimated
	}
	if !next.IsLottie {
		next.IsLottie = existing.IsLottie
	}
	if strings.TrimSpace(next.AccessibilityLabel) == "" {
		next.AccessibilityLabel = existing.AccessibilityLabel
	}
	if next.UpdatedAt.IsZero() {
		next.UpdatedAt = existing.UpdatedAt
	}
	return next
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

func mediaDownloadKey(message store.Message, item store.MediaMetadata) string {
	if messageID := strings.TrimSpace(item.MessageID); messageID != "" {
		return messageID
	}
	return strings.TrimSpace(message.ID)
}

func (m *Model) startMediaDownload(message store.Message, item store.MediaMetadata, label string) bool {
	key := mediaDownloadKey(message, item)
	if key == "" {
		m.status = "media download needs a message id"
		return false
	}
	if m.mediaDownloadInflight != nil && m.mediaDownloadInflight[key] {
		m.status = fmt.Sprintf("%s: %s", label, m.mediaDisplayName(item))
		return false
	}
	if m.mediaDownloadInflight == nil {
		m.mediaDownloadInflight = map[string]bool{}
	}
	m.mediaDownloadInflight[key] = true
	m.status = fmt.Sprintf("%s: %s", label, m.mediaDisplayName(item))
	return true
}

func (m *Model) clearMediaDownloadInFlight(messageID string) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" || m.mediaDownloadInflight == nil {
		return
	}
	delete(m.mediaDownloadInflight, messageID)
}

func leaderDisplay(leader string) string {
	leader = strings.TrimSpace(leader)
	if leader == "" || strings.EqualFold(leader, "space") {
		return "space"
	}
	return leader
}

func (m Model) mediaDisplayName(item store.MediaMetadata) string {
	if strings.EqualFold(strings.TrimSpace(item.Kind), "sticker") {
		if label := strings.TrimSpace(m.sanitizeDisplayLine(item.AccessibilityLabel)); label != "" {
			return label
		}
		return "sticker"
	}
	name := strings.TrimSpace(m.sanitizeDisplayLine(item.FileName))
	if name == "" {
		name = strings.TrimSpace(m.sanitizeDisplayLine(item.LocalPath))
	}
	if name == "" {
		return "media"
	}
	return name
}

func mediaDisplayName(item store.MediaMetadata) string {
	if strings.EqualFold(strings.TrimSpace(item.Kind), "sticker") {
		if label := strings.TrimSpace(sanitizeDisplayLine(item.AccessibilityLabel)); label != "" {
			return label
		}
		return "sticker"
	}
	name := strings.TrimSpace(sanitizeDisplayLine(item.FileName))
	if name == "" {
		name = strings.TrimSpace(sanitizeDisplayLine(item.LocalPath))
	}
	if name == "" {
		return "media"
	}
	return name
}

func highQualityPreviewRequiresLocalFile(backend media.Backend, kind media.Kind) bool {
	if kind != media.KindImage && kind != media.KindVideo {
		return false
	}
	return backend == media.BackendUeberzugPP || backend == media.BackendSixel
}

func mediaDownloadUnavailableStatus(item store.MediaMetadata) string {
	if strings.TrimSpace(item.LocalPath) == "" && strings.TrimSpace(item.ThumbnailPath) != "" {
		return fmt.Sprintf("%s only has a thumbnail; full media download is not implemented", mediaDisplayName(item))
	}
	return fmt.Sprintf("%s is not downloaded yet; WhatsApp media download is not implemented", mediaDisplayName(item))
}

func (m *Model) reportVisibleChatsChanged() {
	if m.visibleChatsChanged == nil {
		return
	}
	chatIDs := m.visibleChatIDs()
	signature := strings.Join(chatIDs, "\n")
	if signature == m.lastReportedVisibleChats {
		return
	}
	m.lastReportedVisibleChats = signature
	m.visibleChatsChanged(slices.Clone(chatIDs))
}

func (m Model) visibleChatIDs() []string {
	if len(m.chats) == 0 {
		return nil
	}
	visible := visibleChatCellCount(m.chatPaneContentHeight())
	if visible <= 0 {
		visible = 1
	}
	start := adjustedChatScrollTop(m.chatScrollTop, m.activeChat, len(m.chats), visible)
	end := min(len(m.chats), start+visible)
	chatIDs := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		if chatID := strings.TrimSpace(m.chats[i].ID); chatID != "" {
			chatIDs = append(chatIDs, chatID)
		}
	}
	return chatIDs
}

func (m Model) chatByID(chatID string) store.Chat {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return store.Chat{}
	}
	for _, chat := range m.chats {
		if chat.ID == chatID {
			return chat
		}
	}
	for _, chat := range m.allChats {
		if chat.ID == chatID {
			return chat
		}
	}
	return store.Chat{}
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
		return strings.ToLower(left.DisplayTitle()) < strings.ToLower(right.DisplayTitle())
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

func indexOfMessage(messages []store.Message, messageID string) int {
	if messageID == "" {
		return -1
	}
	for i, message := range messages {
		if message.ID == messageID {
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

func (m Model) startForwardPicker(messages []store.Message) (tea.Model, tea.Cmd) {
	if len(messages) == 0 {
		m.status = "no messages selected"
		return m, nil
	}
	if m.forwardMessages == nil {
		m.status = "forwarding unavailable"
		return m, nil
	}
	m.mode = ModeForward
	m.forwardSourceMessages = slices.Clone(messages)
	m.forwardQuery = ""
	m.forwardSearchActive = false
	m.forwardSelected = map[string]bool{}
	m.forwardSelectedOrder = nil
	m.forwardCursor = 0
	m.rebuildForwardCandidates()
	m.status = fmt.Sprintf("forward %d message(s)", len(messages))
	return m, nil
}

func (m *Model) clearForwardPicker() {
	m.forwardQuery = ""
	m.forwardSearchActive = false
	m.forwardSourceMessages = nil
	m.forwardCandidates = nil
	m.forwardCursor = 0
	m.forwardSelected = map[string]bool{}
	m.forwardSelectedOrder = nil
}

func (m *Model) rebuildForwardCandidates() {
	query := strings.TrimSpace(m.forwardQuery)
	candidates := make([]store.Chat, 0, len(m.allChats))
	for _, chat := range m.allChats {
		if strings.TrimSpace(chat.ID) == "" {
			continue
		}
		if query == "" {
			candidates = append(candidates, chat)
			continue
		}
		if textmatch.Contains(strings.Join([]string{chat.DisplayTitle(), chat.JID, chat.ID}, " "), query) {
			candidates = append(candidates, chat)
		}
	}
	m.forwardCandidates = candidates
	if len(m.forwardCandidates) == 0 {
		m.forwardCursor = 0
		return
	}
	m.forwardCursor = clamp(m.forwardCursor, 0, len(m.forwardCandidates)-1)
}

func (m *Model) moveForwardCursor(delta int) {
	if len(m.forwardCandidates) == 0 {
		m.forwardCursor = 0
		return
	}
	m.forwardCursor = clamp(m.forwardCursor+delta, 0, len(m.forwardCandidates)-1)
}

func (m *Model) toggleForwardRecipient() {
	if len(m.forwardCandidates) == 0 {
		m.status = "no matching chats"
		return
	}
	chat := m.forwardCandidates[clamp(m.forwardCursor, 0, len(m.forwardCandidates)-1)]
	if strings.TrimSpace(chat.ID) == "" {
		return
	}
	if m.forwardSelected == nil {
		m.forwardSelected = map[string]bool{}
	}
	if m.forwardSelected[chat.ID] {
		delete(m.forwardSelected, chat.ID)
		m.forwardSelectedOrder = removeString(m.forwardSelectedOrder, chat.ID)
		return
	}
	m.forwardSelected[chat.ID] = true
	m.forwardSelectedOrder = append(m.forwardSelectedOrder, chat.ID)
}

func (m Model) forwardSelectedRecipients() []store.Chat {
	if len(m.forwardSelected) == 0 {
		return nil
	}
	byID := make(map[string]store.Chat, len(m.allChats))
	for _, chat := range m.allChats {
		if strings.TrimSpace(chat.ID) != "" {
			byID[chat.ID] = chat
		}
	}
	recipients := make([]store.Chat, 0, len(m.forwardSelected))
	for _, id := range m.forwardSelectedOrder {
		if !m.forwardSelected[id] {
			continue
		}
		if chat, ok := byID[id]; ok {
			recipients = append(recipients, chat)
		}
	}
	for id := range m.forwardSelected {
		if slices.Contains(m.forwardSelectedOrder, id) {
			continue
		}
		if chat, ok := byID[id]; ok {
			recipients = append(recipients, chat)
		}
	}
	return recipients
}

func removeString(values []string, target string) []string {
	out := values[:0]
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
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

func (m *Model) persistCurrentDraft() tea.Cmd {
	chatID := m.currentChat().ID
	if chatID == "" {
		return nil
	}
	m.saveComposerMentionState(chatID, m.composer)
	m.localSetDraft(chatID, m.composer)
	return m.saveDraftCmd(chatID, m.composer)
}

func (m *Model) setDraft(chatID, body string) error {
	if m.saveDraft != nil {
		if err := m.saveDraft(chatID, body); err != nil {
			return err
		}
	}
	m.localSetDraft(chatID, body)
	return nil
}

func (m *Model) localSetDraft(chatID, body string) {
	if chatID == m.currentChat().ID && body == m.composer {
		m.saveComposerMentionState(chatID, body)
	}

	if strings.TrimSpace(body) == "" {
		delete(m.draftsByChat, chatID)
		delete(m.composerMentionsByChat, chatID)
		m.updateChatDraftFlag(chatID, false)
		return
	}

	m.draftsByChat[chatID] = body
	m.updateChatDraftFlag(chatID, true)
}

func (m *Model) saveComposerMentionState(chatID, body string) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" || m.composerMentionsByChat == nil {
		return
	}
	if strings.TrimSpace(body) == "" {
		delete(m.composerMentionsByChat, chatID)
		return
	}
	mentions := m.validComposerMentions()
	if len(mentions) == 0 {
		delete(m.composerMentionsByChat, chatID)
		return
	}
	m.composerMentionsByChat[chatID] = slices.Clone(mentions)
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

func trimLastCluster(value string) string {
	if value == "" {
		return value
	}
	lastStart := 0
	seen := false
	graphemes := uniseg.NewGraphemes(value)
	for graphemes.Next() {
		start, _ := graphemes.Positions()
		lastStart = start
		seen = true
	}
	if !seen {
		return ""
	}
	return value[:lastStart]
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}
