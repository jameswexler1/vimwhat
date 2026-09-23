package ui

import (
	"errors"
	"reflect"
	"slices"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"vimwhat/internal/store"
)

type draftTicket struct {
	ChatID   string
	Revision uint64
	Draft    store.ComposerDraft
}
type draftDebouncedMsg struct{ Ticket draftTicket }

// Commands may execute in any order. The coordinator gives each requested save
// a revision and serializes callbacks, including the final shutdown flush.
type draftCoordinator struct {
	mu     sync.Mutex
	next   uint64
	latest map[string]draftTicket
}

func (d *draftCoordinator) reserve(chatID string, draft store.ComposerDraft) draftTicket {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.next++
	ticket := draftTicket{chatID, d.next, cloneComposerDraft(draft)}
	d.latest[chatID] = ticket
	return ticket
}

func (d *draftCoordinator) save(ticket draftTicket, save func(string, store.ComposerDraft) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.latest[ticket.ChatID].Revision != ticket.Revision {
		return nil
	}
	return save(ticket.ChatID, cloneComposerDraft(ticket.Draft))
}

func (d *draftCoordinator) flush(save func(string, store.ComposerDraft) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var errs []error
	for _, ticket := range d.latest {
		errs = append(errs, save(ticket.ChatID, cloneComposerDraft(ticket.Draft)))
	}
	return errors.Join(errs...)
}

func cloneComposerDraft(d store.ComposerDraft) store.ComposerDraft {
	d.Media = slices.Clone(d.Media)
	d.Mentions = slices.Clone(d.Mentions)
	if d.Reply != nil {
		reply := *d.Reply
		d.Reply = &reply
	}
	return d
}

func (m Model) composerDraft() store.ComposerDraft {
	var media []store.MediaMetadata
	if len(m.attachments) > 0 {
		media = m.mediaForLocalMessage("", m.attachments)
		for i := range media {
			media[i].UpdatedAt = time.Time{}
		}
	}
	return cloneComposerDraft(store.ComposerDraft{Body: m.composer, Media: media, Reply: m.replyTo, Mentions: m.composerMentions})
}

func (m *Model) overlayPendingDrafts() {
	m.draftCoordinator.mu.Lock()
	defer m.draftCoordinator.mu.Unlock()
	for id, ticket := range m.draftCoordinator.latest {
		m.composerDrafts[id] = cloneComposerDraft(ticket.Draft)
		m.draftsByChat[id] = ticket.Draft.Body
		m.updateChatDraftFlag(id, !ticket.Draft.Empty())
	}
}

func (m *Model) restoreComposerDraft(chatID string) {
	draft := m.composerDrafts[chatID]
	draft.Body = m.draftsByChat[chatID]
	m.composer = draft.Body
	m.composerMentions = slices.Clone(draft.Mentions)
	m.replyTo = draft.Reply
	m.attachments = nil
	for _, item := range draft.Media {
		m.attachments = append(m.attachments, Attachment{LocalPath: item.LocalPath, FileName: item.FileName, MIMEType: item.MIMEType, SizeBytes: item.SizeBytes, ThumbnailPath: item.ThumbnailPath, DownloadState: item.DownloadState})
	}
}

func (m Model) draftSaver() func(string, store.ComposerDraft) error {
	full, text := m.saveComposerDraft, m.saveDraft
	return func(chatID string, draft store.ComposerDraft) error {
		if full != nil {
			return full(chatID, draft)
		}
		if text != nil {
			return text(chatID, draft.Body)
		}
		return nil
	}
}

func (m Model) saveDraftTicketCmd(ticket draftTicket) tea.Cmd {
	coordinator, save := m.draftCoordinator, m.draftSaver()
	return func() tea.Msg {
		return draftSavedMsg{ChatID: ticket.ChatID, Body: ticket.Draft.Body, Err: coordinator.save(ticket, save)}
	}
}

func (m Model) saveDraftCmd(chatID, body string) tea.Cmd {
	if chatID == "" || m.draftCoordinator == nil {
		return nil
	}
	draft := m.composerDrafts[chatID]
	if chatID == m.currentChat().ID && m.editTarget == nil {
		draft = m.composerDraft()
	}
	draft.Body = body
	m.composerDrafts[chatID] = cloneComposerDraft(draft)
	return m.saveDraftTicketCmd(m.draftCoordinator.reserve(chatID, draft))
}

func (m *Model) captureComposerDraft() tea.Cmd {
	if m.editTarget != nil || m.currentChat().ID == "" {
		return nil
	}
	id := m.currentChat().ID
	draft := m.composerDraft()
	if reflect.DeepEqual(m.composerDrafts[id], draft) {
		return nil
	}
	m.composerDrafts[id] = cloneComposerDraft(draft)
	m.localSetDraft(id, draft.Body)
	m.updateChatDraftFlag(id, !draft.Empty())
	ticket := m.draftCoordinator.reserve(id, draft)
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return draftDebouncedMsg{ticket} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case quoteWindowLoadedMsg:
		next, cmd := m.handleQuoteWindow(msg)
		next.boundHistoryCache()
		return next.withPreviewCmd(cmd)
	case newerWindowLoadedMsg:
		next := m.handleNewerWindow(msg)
		next.boundHistoryCache()
		return next.withPreviewCmd(nil)
	}
	if debounced, ok := msg.(draftDebouncedMsg); ok {
		return m, m.saveDraftTicketCmd(debounced.Ticket)
	}
	oldChat := m.currentChat().ID
	wasEditing := m.editTarget != nil
	updated, cmd := m.update(msg)
	next := updated.(Model)
	next.boundHistoryCache()
	if oldChat != next.currentChat().ID {
		// Switching panes/chats must never carry attachments or reply context.
		next.restoreComposerDraft(next.currentChat().ID)
		return next, cmd
	}
	if !wasEditing && next.editTarget == nil {
		cmd = batchCmds(cmd, next.captureComposerDraft())
	}
	return next, cmd
}
