package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"vimwhat/internal/store"
	"vimwhat/internal/ui"
	"vimwhat/internal/whatsapp"
)

func TestStartupReadinessRequiresEveryCoreStream(t *testing.T) {
	s := startupSyncState{needInitialHistory: true}
	assertBlocked := func() {
		t.Helper()
		if s.ready() || !s.progress(true).Active {
			t.Fatal("startup became ready too early")
		}
	}
	assertBlocked()
	s.appStateDone = true
	assertBlocked()
	s.metadataDone = true
	assertBlocked()
	s.history(whatsapp.HistoryProgressEvent{SyncType: "INITIAL_BOOTSTRAP", Pending: true})
	assertBlocked()
	s.history(whatsapp.HistoryProgressEvent{SyncType: "INITIAL_BOOTSTRAP", ProgressKnown: true, Progress: 50})
	assertBlocked()
	s.history(whatsapp.HistoryProgressEvent{SyncType: "INITIAL_BOOTSTRAP", Chunk: 1, ProgressKnown: true, Progress: 100})
	if !s.ready() || s.progress(true).Active {
		t.Fatal("finished imports did not become ready")
	}
	if !s.progress(false).Active {
		t.Fatal("catch-up must still gate startup")
	}
	// Cross-goroutine delivery can put a downloaded batch before its notice.
	s.history(whatsapp.HistoryProgressEvent{SyncType: "INITIAL_BOOTSTRAP", Chunk: 1, Pending: true})
	if !s.ready() {
		t.Fatal("late duplicate notice reopened a completed batch")
	}
	s.history(whatsapp.HistoryProgressEvent{SyncType: "INITIAL_BOOTSTRAP", ProgressKnown: true, Progress: 50})
	if !s.ready() {
		t.Fatal("older progress regressed completed transfer")
	}
	s.err = errors.New("metadata failed")
	if s.ready() || !s.progress(true).Failed {
		t.Fatal("failure reported as ready")
	}
}

func TestOptionalHistoryDoesNotBlockStartup(t *testing.T) {
	s := startupSyncState{appStateDone: true, metadataDone: true}
	for _, kind := range []string{"FULL", "ON_DEMAND", "INITIAL_STATUS_V3", "NON_BLOCKING_DATA"} {
		s.history(whatsapp.HistoryProgressEvent{SyncType: kind, Pending: true})
	}
	if !s.ready() {
		t.Fatal("optional history blocked messaging")
	}
}

func startSyncTestLive(t *testing.T, session WhatsAppLiveSession) <-chan ui.LiveUpdate {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan ui.LiveUpdate, 128)
	done := make(chan struct{})
	env := Environment{Store: db, OpenWhatsAppSession: func(context.Context, string) (WhatsAppSession, error) { return session, nil }}
	go func() {
		defer close(done)
		runLiveWhatsApp(ctx, env, updates, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("live loop failed to shut down")
		}
		_ = db.Close()
	})
	return updates
}

func TestStartupDoesNotFinalizeBeforeAppStateAndHistory(t *testing.T) {
	previous := liveStartupSyncSettle
	liveStartupSyncSettle = time.Millisecond
	defer func() { liveStartupSyncSettle = previous }()
	block := make(chan struct{})
	base := &fakeLiveWhatsAppSession{events: make(chan whatsapp.Event, 8), syncAppStateBlock: block}
	updates := startSyncTestLive(t, base)
	waitForLiveUpdate(t, updates, func(u ui.LiveUpdate) bool { return u.ProtocolReady != nil && *u.ProtocolReady })
	// The connection check finished, but account readiness must not have.
	base.events <- whatsapp.Event{Kind: whatsapp.EventHistoryProgress, HistoryProgress: whatsapp.HistoryProgressEvent{SyncType: "INITIAL_BOOTSTRAP"}}
	close(block)
	final := waitForLiveUpdate(t, updates, func(u ui.LiveUpdate) bool { return u.Startup != nil && !u.Startup.Active })
	if !final.Refresh || final.Sync == nil || !final.Sync.Finalizing {
		t.Fatalf("missing atomic snapshot barrier: %+v", final)
	}
}

func TestMissingInitialHistoryIsAnExplicitFailure(t *testing.T) {
	previousTimeout, previousSettle := startupSyncTimeout, liveStartupSyncSettle
	startupSyncTimeout, liveStartupSyncSettle = 80*time.Millisecond, time.Millisecond
	defer func() { startupSyncTimeout, liveStartupSyncSettle = previousTimeout, previousSettle }()
	base := &fakeLiveWhatsAppSession{events: make(chan whatsapp.Event, 8)}
	updates := startSyncTestLive(t, base)
	failed := waitForLiveUpdate(t, updates, func(u ui.LiveUpdate) bool { return u.Startup != nil && u.Startup.Failed })
	if !failed.Startup.Active || failed.Startup.Stage != "Sync incomplete" {
		t.Fatalf("failure = %+v", failed)
	}
}

func TestMetadataFailureDoesNotBecomeReady(t *testing.T) {
	base := &fakeLiveWhatsAppSession{events: make(chan whatsapp.Event, 8)}
	base.events <- whatsapp.Event{Kind: whatsapp.EventHistoryProgress, HistoryProgress: whatsapp.HistoryProgressEvent{SyncType: "INITIAL_BOOTSTRAP"}}
	session := &fakeMetadataLiveWhatsAppSession{fakeLiveWhatsAppSession: base, metadataErr: errors.New("name lookup failed")}
	updates := startSyncTestLive(t, session)
	waitForLiveUpdate(t, updates, func(u ui.LiveUpdate) bool { return u.Startup != nil && u.Startup.Failed })
}

type observedMetadataSession struct {
	*fakeLiveWhatsAppSession
	started chan struct{}
	release <-chan struct{}
}

func (s *observedMetadataSession) RefreshChatMetadata(ctx context.Context) ([]whatsapp.Event, error) {
	select {
	case s.started <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return nil, nil
}

func TestStartupReconcilesMetadataAfterAppStateAndWaitsForIt(t *testing.T) {
	previous := liveStartupSyncSettle
	liveStartupSyncSettle = time.Millisecond
	defer func() { liveStartupSyncSettle = previous }()
	appRelease, metadataRelease := make(chan struct{}), make(chan struct{})
	base := &fakeLiveWhatsAppSession{events: make(chan whatsapp.Event, 8), syncAppStateBlock: appRelease}
	session := &observedMetadataSession{fakeLiveWhatsAppSession: base, started: make(chan struct{}, 1), release: metadataRelease}
	updates := startSyncTestLive(t, session)
	waitForLiveUpdate(t, updates, func(u ui.LiveUpdate) bool { return u.ProtocolReady != nil && *u.ProtocolReady })
	select {
	case <-session.started:
		t.Fatal("metadata raced ahead of app-state")
	default:
	}
	base.events <- whatsapp.Event{Kind: whatsapp.EventHistoryProgress, HistoryProgress: whatsapp.HistoryProgressEvent{SyncType: "INITIAL_BOOTSTRAP"}}
	close(appRelease)
	waitForSignal(t, session.started)
	waitForLiveUpdate(t, updates, func(u ui.LiveUpdate) bool {
		if u.Startup != nil && !u.Startup.Active {
			t.Fatal("startup finalized before metadata")
		}
		return u.Startup != nil && u.Startup.Stage == "Resolving contact and group names"
	})
	close(metadataRelease)
	final := waitForLiveUpdate(t, updates, func(u ui.LiveUpdate) bool { return u.Startup != nil && !u.Startup.Active })
	if !final.Refresh || final.Sync == nil || !final.Sync.Finalizing {
		t.Fatalf("missing final barrier: %+v", final)
	}
}

func TestStartupWorkCancelsPreviousGeneration(t *testing.T) {
	work := startupWork{}
	work.reset(context.Background())
	old := work.Context
	work.reset(context.Background())
	if old.Err() == nil || work.Err() != nil {
		t.Fatal("reconnect did not replace import context")
	}
	work.stop()
	if work.Err() == nil {
		t.Fatal("shutdown did not cancel current import")
	}
}
