package ui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"time"
)

func (m Model) handleSyncProgress(update SyncProgressUpdate) (Model, tea.Cmd) {
	m.syncOverlay.Generation++
	m.syncOverlay.Visible = update.Active || update.Finalizing || update.Completed
	m.syncOverlay.Active = update.Active
	m.syncOverlay.Completed = update.Completed
	m.syncOverlay.Finalizing = update.Finalizing
	m.syncOverlay.Degraded = update.Degraded
	m.syncOverlay.Title = strings.TrimSpace(update.Title)
	m.syncOverlay.Subtitle = strings.TrimSpace(update.Subtitle)
	m.syncOverlay.Total = max(0, update.Total)
	m.syncOverlay.Processed = max(0, update.Processed)
	m.syncOverlay.AppDataChanges = max(0, update.AppDataChanges)
	m.syncOverlay.Messages = max(0, update.Messages)
	m.syncOverlay.Notifications = max(0, update.Notifications)
	m.syncOverlay.Receipts = max(0, update.Receipts)
	m.syncOverlay.PendingRecovery = max(0, update.PendingRecovery)
	if m.syncOverlay.Total > 0 && m.syncOverlay.Processed > m.syncOverlay.Total {
		m.syncOverlay.Processed = m.syncOverlay.Total
	}
	if update.Active {
		if !m.backgroundSync {
			m.leaderPending = false
			m.leaderSequence = ""
		}
		if syncProgressShouldReplaceStatus(m.status) {
			m.status = syncProgressStatus(update, "syncing WhatsApp updates")
		}
		return m, nil
	}
	if update.Finalizing {
		m.syncOverlay.Title = "Finalizing WhatsApp updates"
		m.syncOverlay.Subtitle = "Applying the latest local snapshot before opening chats."
		m.status = syncProgressStatus(update, "finalizing WhatsApp updates")
		return m, nil
	}
	if update.Completed {
		m.status = syncProgressStatus(update, "sync complete")
		return m, syncOverlayDoneCmd(m.syncOverlay.Generation)
	}
	m.syncOverlay = syncOverlayState{}
	return m, nil
}

func (m *Model) completeSyncOverlay(title, subtitle string) tea.Cmd {
	m.syncOverlay.Generation++
	m.syncOverlay.Visible = true
	m.syncOverlay.Active = false
	m.syncOverlay.Completed = true
	m.syncOverlay.Finalizing = false
	m.syncOverlay.Title = strings.TrimSpace(title)
	m.syncOverlay.Subtitle = strings.TrimSpace(subtitle)
	return syncOverlayDoneCmd(m.syncOverlay.Generation)
}

func (m Model) handleSyncFinalizeReloadFailure(err error) (Model, tea.Cmd) {
	if m.syncFinalizeRetries < 2 && m.reloadSnapshot != nil {
		m.syncFinalizeRetries++
		m.reloadInFlight = true
		m.status = fmt.Sprintf("final refresh failed; retrying (%d/2)", m.syncFinalizeRetries)
		m.syncOverlay.Degraded = true
		m.syncOverlay.Subtitle = fmt.Sprintf("Final snapshot refresh failed; retrying (%d/2).", m.syncFinalizeRetries)
		return m, m.reloadSnapshotCmd()
	}

	m.syncFinalizePending = false
	m.syncFinalizeNeedsReload = false
	m.syncFinalizeRetries = 0
	m.protocolReady = false
	m.status = fmt.Sprintf("sync applied; final refresh failed: %v", err)
	m.startupProgress = StartupProgressUpdate{Active: true, Failed: true,
		Stage: "Final chat refresh failed", Detail: "Updates were stored but could not be displayed. Restart vimwhat to retry."}
	doneCmd := m.completeSyncOverlay(
		"Sync completed with a refresh error",
		"WhatsApp updates were stored, but the final chat view could not be refreshed.",
	)
	return m, batchCmds(doneCmd, m.nextQueuedRefreshCmd())
}

func syncProgressStatus(update SyncProgressUpdate, fallback string) string {
	if title := strings.TrimSpace(update.Title); title != "" {
		fallback = title
	}
	if update.PendingRecovery > 0 {
		return fmt.Sprintf("%s; recovering %d message(s)", fallback, update.PendingRecovery)
	}
	if update.Active && update.Total > 0 {
		return fmt.Sprintf("%s %d/%d", fallback, min(update.Processed, update.Total), update.Total)
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
