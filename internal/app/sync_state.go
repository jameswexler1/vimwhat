package app

import (
	"strings"
	"time"
	"vimwhat/internal/ui"
	"vimwhat/internal/whatsapp"
)

type offlineSyncState struct {
	active          bool
	implicit        bool
	dirty           bool
	serverComplete  bool
	degraded        bool
	total           int
	processed       int
	appDataChanges  int
	messages        int
	notifications   int
	receipts        int
	lastProgress    time.Time
	pendingRecovery map[string]struct{}
	summaryChats    map[string]int
}

func (s *offlineSyncState) start(event whatsapp.OfflineSyncEvent, now time.Time) ui.SyncProgressUpdate {
	dirty := false
	processed := 0
	if s.active && s.implicit {
		dirty = s.dirty
		processed = s.processed
	}
	total := max(0, event.Total)
	if processed > total {
		total = processed
	}
	*s = offlineSyncState{
		active:          true,
		dirty:           dirty,
		total:           total,
		processed:       processed,
		appDataChanges:  max(0, event.AppDataChanges),
		messages:        max(0, event.Messages),
		notifications:   max(0, event.Notifications),
		receipts:        max(0, event.Receipts),
		lastProgress:    now,
		pendingRecovery: map[string]struct{}{},
		summaryChats:    map[string]int{},
	}
	return s.liveUpdate(true, false)
}

func (s *offlineSyncState) startImplicit(now time.Time) ui.SyncProgressUpdate {
	*s = offlineSyncState{
		active:          true,
		implicit:        true,
		lastProgress:    now,
		pendingRecovery: map[string]struct{}{},
		summaryChats:    map[string]int{},
	}
	return s.liveUpdate(true, false)
}

func (s offlineSyncState) idleDuration() time.Duration {
	if s.implicit {
		return databaseImportInactivity
	}
	return offlineSyncInactivity
}

func (s *offlineSyncState) markProcessed(now time.Time) (ui.SyncProgressUpdate, bool) {
	if !s.active {
		return ui.SyncProgressUpdate{}, false
	}
	s.dirty = true
	s.degraded = false
	s.processed++
	if s.total > 0 && s.processed > s.total {
		s.processed = s.total
	}
	if !s.lastProgress.IsZero() && now.Sub(s.lastProgress) < offlineSyncProgressEvery && (s.total == 0 || s.processed < s.total) {
		return ui.SyncProgressUpdate{}, false
	}
	s.lastProgress = now
	return s.liveUpdate(true, false), true
}

func (s *offlineSyncState) markProgress(count int, now time.Time) (ui.SyncProgressUpdate, bool) {
	if !s.active || count <= 0 {
		return ui.SyncProgressUpdate{}, false
	}
	s.degraded = false
	s.processed += count
	if s.total > 0 && s.processed > s.total {
		s.processed = s.total
	}
	if !s.lastProgress.IsZero() && now.Sub(s.lastProgress) < offlineSyncProgressEvery && (s.total == 0 || s.processed < s.total) {
		return ui.SyncProgressUpdate{}, false
	}
	s.lastProgress = now
	return s.liveUpdate(true, false), true
}

func recoveryKey(event whatsapp.MessageRecoveryEvent) string {
	return strings.TrimSpace(event.ChatID) + "\x00" + strings.TrimSpace(event.MessageID)
}

func (s *offlineSyncState) updateRecovery(event whatsapp.MessageRecoveryEvent) {
	if !s.active {
		return
	}
	if s.pendingRecovery == nil {
		s.pendingRecovery = map[string]struct{}{}
	}
	key := recoveryKey(event)
	if key == "\x00" {
		return
	}
	if event.Pending {
		s.pendingRecovery[key] = struct{}{}
	} else {
		delete(s.pendingRecovery, key)
	}
}

func (s *offlineSyncState) addSummary(result whatsapp.ApplyResult) {
	if !s.active || !result.MessageInserted || result.Message.IsOutgoing || result.Message.Historical {
		return
	}
	chatID := strings.TrimSpace(result.Message.ChatID)
	if chatID == "" {
		return
	}
	if s.summaryChats == nil {
		s.summaryChats = map[string]int{}
	}
	s.summaryChats[chatID]++
}

func (s offlineSyncState) canSettle() bool {
	return s.active && s.serverComplete && len(s.pendingRecovery) == 0
}

func (s *offlineSyncState) markServerComplete(event whatsapp.OfflineSyncEvent) ui.SyncProgressUpdate {
	s.serverComplete = true
	if event.Total > s.total {
		s.total = event.Total
	}
	if event.Processed > s.processed {
		s.processed = event.Processed
	}
	if s.total > 0 && s.processed > s.total {
		s.processed = s.total
	}
	return s.liveUpdate(true, false)
}

func (s *offlineSyncState) finish(event whatsapp.OfflineSyncEvent) (ui.SyncProgressUpdate, bool) {
	if event.Total > s.total {
		s.total = event.Total
	}
	if event.Processed > s.processed {
		s.processed = event.Processed
	}
	if s.total > 0 {
		s.processed = s.total
	}
	dirty := s.dirty
	update := s.liveUpdate(false, true)
	update.Finalizing = true
	update.Degraded = s.degraded
	*s = offlineSyncState{}
	return update, dirty
}

func (s offlineSyncState) liveUpdate(active, completed bool) ui.SyncProgressUpdate {
	update := ui.SyncProgressUpdate{
		Active:          active,
		Completed:       completed,
		Degraded:        s.degraded,
		Total:           s.total,
		Processed:       s.processed,
		AppDataChanges:  s.appDataChanges,
		Messages:        s.messages,
		Notifications:   s.notifications,
		Receipts:        s.receipts,
		PendingRecovery: len(s.pendingRecovery),
	}
	if s.implicit {
		if completed {
			update.Title = "Database update complete"
			update.Subtitle = "Latest local WhatsApp data is ready."
		} else {
			update.Title = "Updating local database"
			update.Subtitle = "Historical chats and messages are being applied locally."
		}
	}
	return update
}
