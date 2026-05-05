package app

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"vimwhat/internal/config"
	"vimwhat/internal/notify"
	"vimwhat/internal/store"
	"vimwhat/internal/ui"
	"vimwhat/internal/whatsapp"
)

const notificationQueueSize = 64
const notificationQueueWait = 50 * time.Millisecond

type NotificationOpener func(config.Config) notify.Notifier

func defaultOpenNotifier(cfg config.Config) notify.Notifier {
	return notify.New(cfg)
}

func openNotifier(env Environment) notify.Notifier {
	opener := env.OpenNotifier
	if opener == nil {
		opener = defaultOpenNotifier
	}
	return opener(env.Config)
}

func startNotificationWorker(ctx context.Context, env Environment, updates chan<- ui.LiveUpdate) (chan<- notify.Notification, func()) {
	notifier := openNotifier(env)
	if notifier == nil {
		return nil, func() {}
	}
	if notifier.Report().Selected == notify.BackendNone {
		return nil, func() {}
	}

	jobs := make(chan notify.Notification, notificationQueueSize)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var lastError string
		for {
			select {
			case job, ok := <-jobs:
				if !ok {
					return
				}
				sendCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				err := notifier.Notify(sendCtx, job)
				cancel()
				if err == nil || ctx.Err() != nil {
					lastError = ""
					continue
				}
				status := "notification failed: " + shortStatusError(err)
				if status == lastError {
					continue
				}
				lastError = status
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: status})
			case <-ctx.Done():
				return
			}
		}
	}()

	return jobs, func() {
		close(jobs)
		wg.Wait()
	}
}

func queueNotification(ctx context.Context, jobs chan<- notify.Notification, job notify.Notification) bool {
	if jobs == nil {
		return false
	}
	select {
	case jobs <- job:
		return true
	default:
	}
	timer := time.NewTimer(notificationQueueWait)
	defer timer.Stop()
	select {
	case jobs <- job:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func (g *notificationGate) QueueOrSend(
	ctx context.Context,
	db *store.Store,
	jobs chan<- notify.Notification,
	updates chan<- ui.LiveUpdate,
	avatarJobs chan<- avatarRefreshRequest,
	avatarInflight map[string]bool,
	view notificationContext,
	result whatsapp.ApplyResult,
) {
	if g != nil && g.Pending {
		if !notificationMaySend(view, result) {
			return
		}
		candidate := pendingNotificationCandidate{
			View:   view,
			Result: result,
		}
		if len(g.Candidates) >= notificationQueueSize {
			copy(g.Candidates, g.Candidates[1:])
			g.Candidates[len(g.Candidates)-1] = candidate
			sendLiveUpdateNonblocking(updates, ui.LiveUpdate{Status: "notification backlog full; dropped oldest pending notification"})
			return
		}
		g.Candidates = append(g.Candidates, candidate)
		return
	}
	queueNotificationForResult(ctx, db, jobs, updates, avatarJobs, avatarInflight, view, result)
}

func (g *notificationGate) Flush(
	ctx context.Context,
	db *store.Store,
	jobs chan<- notify.Notification,
	updates chan<- ui.LiveUpdate,
	avatarJobs chan<- avatarRefreshRequest,
	avatarInflight map[string]bool,
) {
	if g == nil || !g.Pending {
		return
	}
	candidates := g.Candidates
	g.Pending = false
	g.Candidates = nil
	for _, candidate := range candidates {
		queueNotificationForResult(ctx, db, jobs, updates, avatarJobs, avatarInflight, candidate.View, candidate.Result)
	}
}

func queueNotificationForResult(
	ctx context.Context,
	db *store.Store,
	jobs chan<- notify.Notification,
	updates chan<- ui.LiveUpdate,
	avatarJobs chan<- avatarRefreshRequest,
	avatarInflight map[string]bool,
	view notificationContext,
	result whatsapp.ApplyResult,
) {
	note, ok := buildNotification(ctx, db, view, result)
	if !ok {
		return
	}
	if strings.TrimSpace(note.IconPath) == "" {
		enqueueAvatarRefresh(ctx, avatarJobs, avatarInflight, result.Message.ChatID)
	}
	if !queueNotification(ctx, jobs, note) {
		sendLiveUpdateNonblocking(updates, ui.LiveUpdate{Status: "notification queue is full; skipped desktop notification"})
	}
}

func sendLiveUpdateNonblocking(updates chan<- ui.LiveUpdate, update ui.LiveUpdate) {
	if updates == nil {
		return
	}
	select {
	case updates <- update:
	default:
	}
}

func notificationMaySend(view notificationContext, result whatsapp.ApplyResult) bool {
	if !result.MessageInserted {
		return false
	}
	message := result.Message
	if message.IsOutgoing || message.Historical || strings.TrimSpace(message.ChatID) == "" {
		return false
	}
	return !suppressActiveChatNotification(message.ChatID, view)
}

type notificationContext struct {
	activeChatID  string
	appFocused    bool
	appFocusKnown bool
}

func buildNotification(ctx context.Context, db *store.Store, view notificationContext, result whatsapp.ApplyResult) (notify.Notification, bool) {
	if db == nil || !result.MessageInserted {
		return notify.Notification{}, false
	}
	message := result.Message
	if message.IsOutgoing || message.Historical || strings.TrimSpace(message.ChatID) == "" {
		return notify.Notification{}, false
	}
	if suppressActiveChatNotification(message.ChatID, view) {
		return notify.Notification{}, false
	}
	chat, ok, err := db.ChatByID(ctx, message.ChatID)
	if err != nil || !ok || chat.Muted {
		return notify.Notification{}, false
	}
	return notify.FormatChatMessage(notify.MessagePayload{
		ChatTitle: chat.DisplayTitle(),
		ChatKind:  chat.Kind,
		Sender:    message.Sender,
		Preview:   message.NotificationPreview,
		IconPath:  notificationIconPath(chat),
	}), true
}

func notificationIconPath(chat store.Chat) string {
	for _, candidate := range []string{chat.AvatarThumbPath, chat.AvatarPath} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func suppressActiveChatNotification(chatID string, view notificationContext) bool {
	chatID = strings.TrimSpace(chatID)
	activeChatID := strings.TrimSpace(view.activeChatID)
	if chatID == "" || activeChatID == "" || chatID != activeChatID {
		return false
	}
	return view.appFocusKnown && view.appFocused
}
