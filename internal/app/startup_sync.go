package app

import (
	"context"
	"fmt"

	"vimwhat/internal/ui"
	"vimwhat/internal/whatsapp"
)

// Owns one connection generation of background imports. A reconnect cancels
// old workers before replacing their result channels.
type startupWork struct {
	context.Context
	cancel context.CancelFunc
}

func (w *startupWork) reset(parent context.Context) {
	w.stop()
	w.Context, w.cancel = context.WithCancel(parent)
}

func (w *startupWork) stop() {
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
}

// These are independent streams: none of their individual completion markers
// means the account is ready.
type startupSyncState struct {
	appStateDone       bool
	metadataDone       bool
	needInitialHistory bool
	sawInitialHistory  bool
	pending            map[string]bool
	completed          map[string]bool
	partialTypes       map[string]bool
	progressByType     map[string]int
	err                error
	finalized          bool
}

func (s *startupSyncState) history(event whatsapp.HistoryProgressEvent) {
	switch event.SyncType {
	case "INITIAL_BOOTSTRAP", "RECENT", "PUSH_NAME":
	default:
		return
	}
	if s.pending == nil {
		s.pending = map[string]bool{}
		s.completed = map[string]bool{}
		s.partialTypes = map[string]bool{}
		s.progressByType = map[string]int{}
	}
	key := fmt.Sprintf("%s/%d", event.SyncType, event.Chunk)
	if event.Pending {
		if !s.completed[key] {
			s.pending[key] = true
			s.finalized = false
		}
	} else {
		delete(s.pending, key)
		s.completed[key] = true
		if event.ProgressKnown {
			s.progressByType[event.SyncType] = max(s.progressByType[event.SyncType], event.Progress)
			if s.progressByType[event.SyncType] < 100 {
				s.partialTypes[event.SyncType] = true
				s.finalized = false
			} else {
				delete(s.partialTypes, event.SyncType)
			}
		}
		if event.SyncType == "INITIAL_BOOTSTRAP" || event.SyncType == "RECENT" {
			s.sawInitialHistory = true
		}
	}
}

func (s startupSyncState) ready() bool {
	return s.err == nil && s.appStateDone && s.metadataDone &&
		(!s.needInitialHistory || s.sawInitialHistory) && len(s.pending) == 0 && len(s.partialTypes) == 0
}

func (s startupSyncState) progress(catchUpReady bool) ui.StartupProgressUpdate {
	progress := ui.StartupProgressUpdate{Active: true}
	switch {
	case s.err != nil:
		progress.Failed = true
		progress.Stage = "Sync incomplete"
		progress.Detail = s.err.Error() + ". Restart vimwhat to retry."
	case !s.appStateDone:
		progress.Stage = "Loading contacts and chat settings"
		progress.Detail = "Synchronizing WhatsApp app-state; messaging is not ready."
	case !s.metadataDone:
		progress.Stage = "Resolving contact and group names"
		progress.Detail = "Reconciling saved names with the conversation list."
	case len(s.pending) > 0:
		progress.Stage = "Loading recent conversations"
		progress.Detail = fmt.Sprintf("Waiting for %d announced history batch(es) to download and import.", len(s.pending))
	case len(s.partialTypes) > 0:
		progress.Stage = "Loading recent conversations"
		progress.Detail = "WhatsApp reports more history chunks are still pending."
	case s.needInitialHistory && !s.sawInitialHistory:
		progress.Stage = "Waiting for initial conversations"
		progress.Detail = "The phone has not delivered an initial history batch yet."
	case !catchUpReady:
		progress.Stage = "Catching up messages"
		progress.Detail = "Waiting for the reconnect stream and message recoveries."
	default:
		progress.Active = false
	}
	return progress
}
