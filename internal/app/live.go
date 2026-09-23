package app

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"vimwhat/internal/ui"
	"vimwhat/internal/whatsapp"
)

func runLiveWhatsApp(
	ctx context.Context,
	env Environment,
	updates chan<- ui.LiveUpdate,
	historyRequests <-chan string,
	textSendRequests <-chan textSendRequest,
	mediaSendRequests <-chan mediaSendRequest,
	readReceiptRequests <-chan readReceiptRequest,
	reactionRequests <-chan reactionRequest,
	deleteEveryoneRequests <-chan deleteEveryoneRequest,
	editMessageRequests <-chan editMessageRequest,
	forwardRequests <-chan forwardMessagesRequest,
	presenceRequests <-chan presenceRequest,
	presenceSubscribeRequests <-chan presenceSubscribeRequest,
	mediaDownloadRequests <-chan mediaDownloadRequest,
	stickerSyncRequests <-chan stickerSyncRequest,
	activeChatUpdates <-chan string,
	appFocusUpdates <-chan bool,
	visibleChatUpdates <-chan []string,
) {
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{ConnectionState: ui.ConnectionConnecting})

	session, err := openWhatsAppSession(ctx, env)
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			ConnectionState: ui.ConnectionOffline,
			Status:          fmt.Sprintf("whatsapp open failed: %s", shortStatusError(err)),
			Sync:            &ui.SyncProgressUpdate{},
		})
		return
	}
	defer session.Close()
	liveCtx, cancelLive := context.WithCancel(ctx)
	defer cancelLive()

	live, ok := session.(WhatsAppLiveSession)
	if !ok {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			ConnectionState: ui.ConnectionOffline,
			Status:          "whatsapp live session unavailable",
			Sync:            &ui.SyncProgressUpdate{},
		})
		return
	}

	events, err := live.SubscribeEvents(liveCtx)
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			ConnectionState: ui.ConnectionOffline,
			Status:          fmt.Sprintf("whatsapp subscribe failed: %s", shortStatusError(err)),
			Sync:            &ui.SyncProgressUpdate{},
		})
		return
	}

	if err := retryConnection(liveCtx, live.Connect, func(err error, delay time.Duration) {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{ConnectionState: ui.ConnectionReconnecting, Status: fmt.Sprintf("connection failed; retry in %s: %s", delay, shortStatusError(err))})
	}, waitReconnect); err != nil {
		if ctx.Err() != nil {
			return
		}
		state := ui.ConnectionOffline
		if isLoggedOutConnectionError(err) {
			state = ui.ConnectionLoggedOut
		}
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			ConnectionState: state,
			Status:          fmt.Sprintf("whatsapp connect failed: %s", shortStatusError(err)),
			Sync:            &ui.SyncProgressUpdate{},
		})
		return
	}
	markLivePresenceAvailable(ctx, live, updates)
	protocolReady := false
	connectionCatchUpStartedAt := time.Now()
	connectionReplayGuard := false
	readyValue := func(value bool) *bool { return &value }
	waitingForCatchUp := func() *ui.SyncProgressUpdate {
		return &ui.SyncProgressUpdate{
			Active:   true,
			Title:    "Checking for WhatsApp updates",
			Subtitle: "Waiting for WhatsApp to report reconnect history.",
		}
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		ConnectionState: ui.ConnectionOnline,
		ProtocolReady:   readyValue(false),
		Sync:            waitingForCatchUp(),
	})

	var protocolWG sync.WaitGroup
	defer protocolWG.Wait()

	notificationJobs, stopNotifications := startNotificationWorker(ctx, env, updates)
	defer stopNotifications()

	downloadJobs := make(chan mediaDownloadRequest, mediaDownloadQueueSize)
	var downloadWG sync.WaitGroup
	for i := 0; i < mediaDownloadWorkers; i++ {
		downloadWG.Add(1)
		go func() {
			defer downloadWG.Done()
			mediaDownloadWorker(ctx, env.Store, live, env.Paths, downloadJobs)
		}()
	}
	defer func() {
		close(downloadJobs)
		downloadWG.Wait()
	}()

	avatarJobs := make(chan avatarRefreshRequest, avatarRefreshQueueSize)
	avatarResults := make(chan avatarRefreshResult, avatarRefreshQueueSize)
	var avatarWG sync.WaitGroup
	avatarWG.Add(1)
	go func() {
		defer avatarWG.Done()
		avatarRefreshWorker(ctx, env.Store, live, env.Paths, avatarJobs, avatarResults)
	}()
	defer func() {
		close(avatarJobs)
		avatarWG.Wait()
	}()

	stickerCacheJobs := make(chan recentStickerCacheRequest, recentStickerCacheQueueSize)
	stickerCacheResults := make(chan recentStickerCacheResult, recentStickerCacheQueueSize)
	var stickerCacheWG sync.WaitGroup
	for i := 0; i < recentStickerCacheWorkers; i++ {
		stickerCacheWG.Add(1)
		go func() {
			defer stickerCacheWG.Done()
			recentStickerCacheWorker(ctx, env.Store, live, env.Paths, stickerCacheJobs, stickerCacheResults)
		}()
	}
	defer func() {
		close(stickerCacheJobs)
		stickerCacheWG.Wait()
	}()

	ingestor := whatsapp.Ingestor{Store: env.Store}
	historyInflight := map[string]time.Time{}
	avatarInflight := map[string]bool{}
	metadataResults := refreshChatMetadata(ctx, live)
	viewState := notificationContext{}
	online := true
	pendingPreferredChatID := ""
	startupSyncPending := false
	var startupSyncTimer *time.Timer
	var startupSyncTimerC <-chan time.Time
	stopStartupSyncTimer := func() {
		if startupSyncTimer != nil {
			if !startupSyncTimer.Stop() {
				select {
				case <-startupSyncTimer.C:
				default:
				}
			}
		}
		startupSyncTimerC = nil
	}
	startStartupSyncTimer := func() {
		startupSyncPending = true
		duration := liveStartupSyncSettle
		if duration <= 0 {
			duration = 1 * time.Millisecond
		}
		if startupSyncTimer == nil {
			startupSyncTimer = time.NewTimer(duration)
		} else {
			if !startupSyncTimer.Stop() {
				select {
				case <-startupSyncTimer.C:
				default:
				}
			}
			startupSyncTimer.Reset(duration)
		}
		startupSyncTimerC = startupSyncTimer.C
	}
	resolveStartupSyncOverlay := func() {
		if !startupSyncPending {
			return
		}
		startupSyncPending = false
		stopStartupSyncTimer()
	}
	clearStartupSyncOverlay := func() {
		if !startupSyncPending {
			return
		}
		resolveStartupSyncOverlay()
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Sync: &ui.SyncProgressUpdate{}})
	}
	startStartupSyncTimer()
	offlineSync := offlineSyncState{}
	startupAppStateUpdates := startStartupAppStateSync(ctx, env.Store, live, env.Paths, &protocolWG, online)
	notifications := notificationGate{Pending: startupAppStateUpdates != nil}
	var pendingCatchUpSummary map[string]int
	var offlineSyncIdleTimer *time.Timer
	var offlineSyncIdleTimerC <-chan time.Time
	var offlineSyncMaxTimer *time.Timer
	var offlineSyncMaxTimerC <-chan time.Time
	var lateReplayTimer *time.Timer
	var lateReplayTimerC <-chan time.Time
	var lateReplayMaxTimer *time.Timer
	var lateReplayMaxTimerC <-chan time.Time
	lateReplayDirty := false
	resetOfflineSyncIdleTimer := func(duration time.Duration) {
		if duration <= 0 {
			duration = offlineSyncInactivity
		}
		if offlineSyncIdleTimer == nil {
			offlineSyncIdleTimer = time.NewTimer(duration)
		} else {
			if !offlineSyncIdleTimer.Stop() {
				select {
				case <-offlineSyncIdleTimer.C:
				default:
				}
			}
			offlineSyncIdleTimer.Reset(duration)
		}
		offlineSyncIdleTimerC = offlineSyncIdleTimer.C
	}
	stopOfflineSyncIdleTimer := func() {
		if offlineSyncIdleTimer != nil {
			if !offlineSyncIdleTimer.Stop() {
				select {
				case <-offlineSyncIdleTimer.C:
				default:
				}
			}
		}
		offlineSyncIdleTimerC = nil
	}
	startOfflineSyncMaxTimer := func(duration time.Duration) {
		if duration <= 0 {
			duration = time.Millisecond
		}
		if offlineSyncMaxTimer == nil {
			offlineSyncMaxTimer = time.NewTimer(duration)
		} else {
			if !offlineSyncMaxTimer.Stop() {
				select {
				case <-offlineSyncMaxTimer.C:
				default:
				}
			}
			offlineSyncMaxTimer.Reset(duration)
		}
		offlineSyncMaxTimerC = offlineSyncMaxTimer.C
	}
	stopOfflineSyncMaxTimer := func() {
		if offlineSyncMaxTimer != nil {
			if !offlineSyncMaxTimer.Stop() {
				select {
				case <-offlineSyncMaxTimer.C:
				default:
				}
			}
		}
		offlineSyncMaxTimerC = nil
	}
	clearOfflineSyncTimers := func() {
		if offlineSyncIdleTimer != nil {
			if !offlineSyncIdleTimer.Stop() {
				select {
				case <-offlineSyncIdleTimer.C:
				default:
				}
			}
		}
		stopOfflineSyncMaxTimer()
		offlineSyncIdleTimerC = nil
	}
	resetLateReplayTimer := func() {
		if lateReplayTimer == nil {
			lateReplayTimer = time.NewTimer(lateReplaySettle)
		} else {
			if !lateReplayTimer.Stop() {
				select {
				case <-lateReplayTimer.C:
				default:
				}
			}
			lateReplayTimer.Reset(lateReplaySettle)
		}
		lateReplayTimerC = lateReplayTimer.C
		if lateReplayMaxTimerC == nil {
			if lateReplayMaxTimer == nil {
				lateReplayMaxTimer = time.NewTimer(lateReplayMaxDuration)
			} else {
				lateReplayMaxTimer.Reset(lateReplayMaxDuration)
			}
			lateReplayMaxTimerC = lateReplayMaxTimer.C
		}
	}
	stopLateReplayTimer := func() {
		if lateReplayTimer != nil {
			if !lateReplayTimer.Stop() {
				select {
				case <-lateReplayTimer.C:
				default:
				}
			}
		}
		lateReplayTimerC = nil
		if lateReplayMaxTimer != nil {
			if !lateReplayMaxTimer.Stop() {
				select {
				case <-lateReplayMaxTimer.C:
				default:
				}
			}
		}
		lateReplayMaxTimerC = nil
	}
	flushLateReplay := func() {
		stopLateReplayTimer()
		if lateReplayDirty {
			lateReplayDirty = false
			sendLiveUpdate(ctx, updates, ui.LiveUpdate{
				Refresh: true,
				Status:  "recovered messages applied",
			})
		}
	}
	finishOfflineSync := func(status string, event whatsapp.OfflineSyncEvent) {
		if !offlineSync.active {
			clearOfflineSyncTimers()
			return
		}
		explicitCatchUp := !offlineSync.implicit
		summary := maps.Clone(offlineSync.summaryChats)
		syncUpdate, dirty := offlineSync.finish(event)
		syncUpdate.Finalizing = dirty
		clearOfflineSyncTimers()
		protocolReady = true
		if explicitCatchUp {
			connectionReplayGuard = true
		}
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh:         dirty,
			Status:          status,
			PreferredChatID: pendingPreferredChatID,
			Sync:            &syncUpdate,
			ProtocolReady:   readyValue(true),
		})
		if len(summary) > 0 {
			if startupAppStateUpdates != nil {
				if pendingCatchUpSummary == nil {
					pendingCatchUpSummary = map[string]int{}
				}
				for chatID, count := range summary {
					pendingCatchUpSummary[chatID] += count
				}
			} else {
				queueCatchUpSummary(context.Background(), env.Store, notificationJobs, viewState, summary)
			}
		}
		pendingPreferredChatID = ""
	}
	defer clearOfflineSyncTimers()
	defer stopLateReplayTimer()
	defer stopStartupSyncTimer()
	for {
		select {
		case result := <-stickerCacheResults:
			if result.Err != nil {
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{
					Status: fmt.Sprintf("sticker cache failed: %s", shortStatusError(result.Err)),
				})
				continue
			}
			if strings.TrimSpace(result.Sticker.LocalPath) == "" {
				continue
			}
			_, err := ingestor.Apply(ctx, whatsapp.Event{Kind: whatsapp.EventRecentSticker, Sticker: result.Sticker})
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{
					Status: fmt.Sprintf("sticker cache ingest failed: %s", shortStatusError(err)),
				})
				continue
			}
			if offlineSync.active {
				offlineSync.dirty = true
				continue
			}
			sendLiveUpdate(ctx, updates, ui.LiveUpdate{Refresh: true})
		case event, ok := <-events:
			if !ok {
				clearStartupSyncOverlay()
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{ConnectionState: ui.ConnectionOffline})
				return
			}
			viewState = drainPendingLiveViewState(ctx, avatarJobs, avatarInflight, activeChatUpdates, appFocusUpdates, visibleChatUpdates, viewState)
			if event.Kind == whatsapp.EventConnectionState {
				wasOnline := online
				online = event.Connection.State == whatsapp.ConnectionOnline
				if online {
					if !wasOnline {
						connectionCatchUpStartedAt = time.Now()
					}
					connectionReplayGuard = false
					protocolReady = false
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{
						ProtocolReady: readyValue(false),
						Sync:          waitingForCatchUp(),
					})
					startStartupSyncTimer()
					markLivePresenceAvailable(ctx, live, updates)
				} else {
					protocolReady = false
					if offlineSync.active {
						dirty := offlineSync.dirty
						offlineSync = offlineSyncState{}
						clearOfflineSyncTimers()
						sendLiveUpdate(ctx, updates, ui.LiveUpdate{
							Refresh:       dirty,
							ProtocolReady: readyValue(false),
							Sync:          &ui.SyncProgressUpdate{},
						})
					}
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{ProtocolReady: readyValue(false)})
					clearStartupSyncOverlay()
				}
				sendLiveUpdate(ctx, updates, liveUpdateForConnectionEvent(event.Connection))
				continue
			}
			if event.Kind == whatsapp.EventOfflineSync {
				now := time.Now()
				if event.Offline.Active {
					resolveStartupSyncOverlay()
					protocolReady = false
					connectionReplayGuard = false
					syncUpdate := offlineSync.start(event.Offline, now)
					resetOfflineSyncIdleTimer(offlineSync.idleDuration())
					stopOfflineSyncMaxTimer()
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{
						Status:        "syncing WhatsApp updates",
						Sync:          &syncUpdate,
						ProtocolReady: readyValue(false),
					})
					continue
				}
				if event.Offline.Progress {
					if !offlineSync.active {
						continue
					}
					if syncUpdate, ok := offlineSync.markProgress(max(1, event.Offline.Processed), now); ok {
						sendLiveUpdate(ctx, updates, ui.LiveUpdate{Sync: &syncUpdate})
					}
					if !offlineSync.serverComplete {
						resetOfflineSyncIdleTimer(offlineSync.idleDuration())
					}
					continue
				}
				if event.Offline.Completed {
					if !offlineSync.active {
						continue
					}
					syncUpdate := offlineSync.markServerComplete(event.Offline)
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{
						Status: "recovering WhatsApp messages",
						Sync:   &syncUpdate,
					})
					if offlineSync.canSettle() {
						stopOfflineSyncMaxTimer()
						resetOfflineSyncIdleTimer(offlineSyncSettle)
					} else {
						stopOfflineSyncIdleTimer()
						startOfflineSyncMaxTimer(offlineSyncRecoveryTimeout)
					}
					continue
				}
			}
			if event.Kind == whatsapp.EventMessageRecovery {
				if offlineSync.active {
					offlineSync.updateRecovery(event.Recovery)
					offlineSync.dirty = true
					syncUpdate := offlineSync.liveUpdate(true, false)
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{Sync: &syncUpdate})
					if offlineSync.serverComplete {
						if offlineSync.canSettle() {
							stopOfflineSyncMaxTimer()
							resetOfflineSyncIdleTimer(offlineSyncSettle)
						} else {
							stopOfflineSyncIdleTimer()
							startOfflineSyncMaxTimer(offlineSyncRecoveryTimeout)
						}
					} else {
						resetOfflineSyncIdleTimer(offlineSync.idleDuration())
					}
				}
				continue
			}
			event, connectionReplayGuard = classifyConnectionReplay(event, connectionCatchUpStartedAt, connectionReplayGuard)
			manualHistoryImport := isManualHistoryImportEvent(event, historyInflight)
			if isImplicitDatabaseImportEvent(event, manualHistoryImport) && !offlineSync.active {
				resolveStartupSyncOverlay()
				syncUpdate := offlineSync.startImplicit(time.Now())
				resetOfflineSyncIdleTimer(offlineSync.idleDuration())
				startOfflineSyncMaxTimer(databaseImportMaxDuration)
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{
					Status: "updating local database",
					Sync:   &syncUpdate,
				})
			}
			if event.Kind == whatsapp.EventChatUpsert {
				mergedAliases, err := mergeEventChatAliases(ctx, env.Store, event.Chat)
				if err != nil {
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{
						Status: fmt.Sprintf("chat merge failed: %s", shortStatusError(err)),
					})
					continue
				}
				if viewState.activeChatID != "" && slices.Contains(mergedAliases, viewState.activeChatID) {
					viewState.activeChatID = event.Chat.ID
					pendingPreferredChatID = event.Chat.ID
				}
			}
			if event.Kind == whatsapp.EventPresenceUpdate {
				sendLiveUpdate(ctx, updates, liveUpdateForPresenceEvent(event.Presence))
				continue
			}
			if event.Kind == whatsapp.EventChatAvatarUpdate {
				if handleAvatarEvent(ctx, env.Store, env.Paths, avatarJobs, avatarInflight, updates, event.Avatar) {
					if offlineSync.active {
						offlineSync.dirty = true
					} else {
						sendLiveUpdate(ctx, updates, ui.LiveUpdate{
							Refresh: true,
							Status:  "chat avatar removed",
						})
					}
				}
				continue
			}
			if event.Kind == whatsapp.EventMediaMetadata {
				prepared, err := prepareIncomingMediaEvent(env.Paths, event.Media)
				if err != nil {
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{
						Status: fmt.Sprintf("media cache failed: %s", shortStatusError(err)),
					})
				} else {
					event.Media = prepared
				}
			}
			if event.Kind == whatsapp.EventRecentSticker {
				prepared, needsDownload, err := prepareRecentStickerEventMetadata(ctx, env.Store, env.Paths, event.Sticker)
				if err != nil {
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{
						Status: fmt.Sprintf("sticker cache failed: %s", shortStatusError(err)),
					})
				} else {
					event.Sticker = prepared
					if needsDownload {
						select {
						case stickerCacheJobs <- recentStickerCacheRequest{Sticker: prepared}:
						default:
							sendLiveUpdate(ctx, updates, ui.LiveUpdate{
								Status: "sticker cache queue is full",
							})
						}
					}
				}
			}
			result, err := ingestor.Apply(ctx, event)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{
					Status: fmt.Sprintf("whatsapp ingest failed: %s", shortStatusError(err)),
				})
				continue
			}
			offlineSync.addSummary(result)
			if event.Kind == whatsapp.EventHistoryStatus {
				delete(historyInflight, event.History.ChatID)
				if offlineSync.active {
					if offlineSync.implicit {
						if syncUpdate, ok := offlineSync.markProcessed(time.Now()); ok {
							sendLiveUpdate(ctx, updates, ui.LiveUpdate{Sync: &syncUpdate})
						}
					} else {
						offlineSync.dirty = true
					}
					resetOfflineSyncIdleTimer(offlineSync.idleDuration())
					continue
				}
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{
					Refresh:         true,
					Status:          historyStatusLine(event.History),
					HistoryChatID:   event.History.ChatID,
					HistoryMessages: event.History.Messages,
					PreferredChatID: pendingPreferredChatID,
				})
				pendingPreferredChatID = ""
				continue
			}
			if offlineSync.active {
				var (
					syncUpdate ui.SyncProgressUpdate
					shouldSend bool
				)
				offlineSync.degraded = false
				if offlineSync.implicit {
					syncUpdate, shouldSend = offlineSync.markProcessed(time.Now())
				} else {
					offlineSync.dirty = true
				}
				if offlineSync.serverComplete {
					if offlineSync.canSettle() {
						stopOfflineSyncMaxTimer()
						resetOfflineSyncIdleTimer(offlineSyncSettle)
					} else {
						stopOfflineSyncIdleTimer()
						startOfflineSyncMaxTimer(offlineSyncRecoveryTimeout)
					}
				} else {
					resetOfflineSyncIdleTimer(offlineSync.idleDuration())
				}
				if shouldSend {
					sendLiveUpdate(ctx, updates, ui.LiveUpdate{Sync: &syncUpdate})
				}
				continue
			}
			if isHistoricalImportEvent(event) {
				continue
			}
			if event.Replayed || event.Message.Recovered {
				lateReplayDirty = true
				resetLateReplayTimer()
				continue
			}
			notifications.QueueOrSend(context.Background(), env.Store, notificationJobs, updates, avatarJobs, avatarInflight, viewState, result)
			sendLiveUpdate(ctx, updates, ui.LiveUpdate{
				Refresh:         true,
				PreferredChatID: pendingPreferredChatID,
			})
			pendingPreferredChatID = ""
		case chatID, ok := <-historyRequests:
			if !ok {
				return
			}
			handleHistoryRequest(ctx, env.Store, live, updates, historyInflight, online && protocolReady, chatID)
		case request, ok := <-textSendRequests:
			if !ok {
				return
			}
			handleTextSendRequest(ctx, env.Store, live, updates, &protocolWG, online && protocolReady, request)
		case request, ok := <-mediaSendRequests:
			if !ok {
				return
			}
			handleMediaSendRequest(ctx, env.Store, live, updates, &protocolWG, online && protocolReady, request)
		case request, ok := <-readReceiptRequests:
			if !ok {
				return
			}
			handleReadReceiptRequest(ctx, env.Store, live, updates, &protocolWG, online && protocolReady, request)
		case request, ok := <-reactionRequests:
			if !ok {
				return
			}
			handleReactionRequest(ctx, env.Store, live, updates, &protocolWG, online && protocolReady, request)
		case request, ok := <-deleteEveryoneRequests:
			if !ok {
				return
			}
			handleDeleteEveryoneRequest(ctx, env.Store, live, updates, &protocolWG, online && protocolReady, request)
		case request, ok := <-editMessageRequests:
			if !ok {
				return
			}
			handleEditMessageRequest(ctx, env.Store, live, updates, &protocolWG, online && protocolReady, request)
		case request, ok := <-forwardRequests:
			if !ok {
				return
			}
			handleForwardMessagesRequest(ctx, env.Store, live, updates, &protocolWG, online && protocolReady, request)
		case request, ok := <-presenceRequests:
			if !ok {
				return
			}
			handlePresenceRequest(ctx, live, online && protocolReady, request)
		case request, ok := <-presenceSubscribeRequests:
			if !ok {
				return
			}
			handlePresenceSubscribeRequest(ctx, live, online && protocolReady, request)
		case result, ok := <-metadataResults:
			if ok {
				ingested := 0
				for _, event := range result.Events {
					if _, err := ingestor.Apply(ctx, event); err != nil {
						sendLiveUpdate(ctx, updates, ui.LiveUpdate{
							Status: fmt.Sprintf("metadata ingest failed: %s", shortStatusError(err)),
						})
						continue
					}
					ingested++
				}
				update := ui.LiveUpdate{Refresh: ingested > 0}
				if offlineSync.active && update.Refresh {
					offlineSync.dirty = true
					update.Refresh = false
				}
				if result.Err != nil {
					update.Status = fmt.Sprintf("metadata refresh failed: %s", shortStatusError(result.Err))
				}
				if update.Refresh || update.Status != "" {
					sendLiveUpdate(ctx, updates, update)
				}
			}
			metadataResults = nil
		case startupUpdate, ok := <-startupAppStateUpdates:
			if !ok {
				startupAppStateUpdates = nil
				if notifications.Pending {
					notifications.Flush(context.Background(), env.Store, notificationJobs, updates, avatarJobs, avatarInflight)
				}
				if len(pendingCatchUpSummary) > 0 {
					queueCatchUpSummary(context.Background(), env.Store, notificationJobs, viewState, pendingCatchUpSummary)
					pendingCatchUpSummary = nil
				}
				continue
			}
			if startupUpdate.Done {
				notifications.Flush(context.Background(), env.Store, notificationJobs, updates, avatarJobs, avatarInflight)
				if len(pendingCatchUpSummary) > 0 {
					queueCatchUpSummary(context.Background(), env.Store, notificationJobs, viewState, pendingCatchUpSummary)
					pendingCatchUpSummary = nil
				}
			}
			update := startupUpdate.Update
			if offlineSync.active && update.Refresh {
				offlineSync.dirty = true
				update.Refresh = false
			}
			if update.Refresh || strings.TrimSpace(update.Status) != "" {
				sendLiveUpdate(ctx, updates, update)
			}
		case request, ok := <-mediaDownloadRequests:
			if !ok {
				return
			}
			enqueueMediaDownload(ctx, downloadJobs, online && protocolReady, request)
		case request, ok := <-stickerSyncRequests:
			if !ok {
				return
			}
			handleStickerSyncRequest(ctx, env.Store, live, env.Paths, updates, &protocolWG, online && protocolReady, request)
		case chatID, ok := <-activeChatUpdates:
			if !ok {
				activeChatUpdates = nil
				continue
			}
			viewState.activeChatID = chatID
			enqueueAvatarRefresh(ctx, avatarJobs, avatarInflight, chatID)
		case focused, ok := <-appFocusUpdates:
			if !ok {
				appFocusUpdates = nil
				continue
			}
			viewState.appFocusKnown = true
			viewState.appFocused = focused
		case chatIDs, ok := <-visibleChatUpdates:
			if !ok {
				visibleChatUpdates = nil
				continue
			}
			if viewState.activeChatID != "" {
				enqueueAvatarRefresh(ctx, avatarJobs, avatarInflight, viewState.activeChatID)
			}
			for _, chatID := range chatIDs {
				enqueueAvatarRefresh(ctx, avatarJobs, avatarInflight, chatID)
			}
		case result, ok := <-avatarResults:
			if !ok {
				avatarResults = nil
				continue
			}
			delete(avatarInflight, result.ChatID)
			if result.Err != nil {
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{
					Status: fmt.Sprintf("avatar refresh failed: %s", shortStatusError(result.Err)),
				})
				continue
			}
			refresh := result.Refresh
			if offlineSync.active && refresh {
				offlineSync.dirty = true
				refresh = false
			}
			if refresh || strings.TrimSpace(result.Status) != "" {
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{
					Refresh: refresh,
					Status:  result.Status,
				})
			}
		case <-offlineSyncIdleTimerC:
			if offlineSync.implicit {
				finishOfflineSync("database update complete", whatsapp.OfflineSyncEvent{
					Completed: true,
					Total:     offlineSync.total,
					Processed: offlineSync.processed,
				})
				continue
			}
			if !offlineSync.serverComplete {
				offlineSync.degraded = true
				offlineSyncIdleTimerC = nil
				syncUpdate := offlineSync.liveUpdate(true, false)
				syncUpdate.Subtitle = "No recent progress; still waiting for WhatsApp to finish the reconnect stream."
				sendLiveUpdate(ctx, updates, ui.LiveUpdate{
					Status: "WhatsApp sync is taking longer than expected; still waiting",
					Sync:   &syncUpdate,
				})
				continue
			}
			finishOfflineSync("sync complete", whatsapp.OfflineSyncEvent{
				Completed: true,
				Total:     offlineSync.total,
				Processed: offlineSync.processed,
			})
		case <-offlineSyncMaxTimerC:
			if offlineSync.implicit {
				offlineSync.degraded = true
				finishOfflineSync("database update timed out; refreshed latest data", whatsapp.OfflineSyncEvent{
					Completed: true,
					Total:     offlineSync.total,
					Processed: offlineSync.processed,
				})
				continue
			}
			if !offlineSync.serverComplete {
				offlineSyncMaxTimerC = nil
				continue
			}
			offlineSync.degraded = true
			finishOfflineSync("sync complete; some message recoveries are still pending", whatsapp.OfflineSyncEvent{
				Completed: true,
				Total:     offlineSync.total,
				Processed: offlineSync.processed,
			})
		case <-lateReplayTimerC:
			flushLateReplay()
		case <-lateReplayMaxTimerC:
			flushLateReplay()
		case <-startupSyncTimerC:
			if offlineSync.active {
				resolveStartupSyncOverlay()
				continue
			}
			resolveStartupSyncOverlay()
			protocolReady = true
			sendLiveUpdate(ctx, updates, ui.LiveUpdate{
				ProtocolReady: readyValue(true),
				Sync:          &ui.SyncProgressUpdate{},
			})
		case <-ctx.Done():
			return
		}
	}
}
