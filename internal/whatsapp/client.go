package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	_ "modernc.org/sqlite"
)

var ErrNotImplemented = errors.New("whatsapp integration not implemented yet")
var ErrClientNotOpen = errors.New("whatsapp client is not open")

var errSessionRejected = errors.New("whatsapp session was rejected")

const authTimeout = 45 * time.Second

type QRHandler = func(code string)

type HistoryAnchor struct {
	ChatJID   string
	MessageID string
	IsFromMe  bool
	Timestamp time.Time
}

type Adapter interface {
	Connect(ctx context.Context) error
	Login(ctx context.Context, handleQR QRHandler) error
	Logout(ctx context.Context) error
	SendText(ctx context.Context, chatJID, body string) (SendResult, error)
	SubscribeEvents(ctx context.Context) (<-chan Event, error)
	RequestHistoryBefore(ctx context.Context, anchor HistoryAnchor, limit int) error
}

type Client struct {
	client    *whatsmeow.Client
	container *sqlstore.Container
}

type SessionState string

const (
	SessionMissing  SessionState = "missing"
	SessionUnpaired SessionState = "unpaired"
	SessionPaired   SessionState = "paired"
)

type SessionStatus struct {
	State   SessionState
	Devices int
	JID     string
}

func (s SessionStatus) HasCredentials() bool {
	return s.State == SessionPaired
}

func (s SessionStatus) String() string {
	switch s.State {
	case SessionMissing:
		return "not configured"
	case SessionUnpaired:
		return "not paired"
	case SessionPaired:
		if s.JID != "" {
			return fmt.Sprintf("paired locally (%s)", s.JID)
		}
		return "paired locally"
	default:
		return "unknown"
	}
}

func OpenSession(ctx context.Context, sessionPath string) (*Client, error) {
	container, err := openContainer(ctx, sessionPath, true)
	if err != nil {
		return nil, err
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = container.Close()
		return nil, fmt.Errorf("open whatsapp device store: %w", err)
	}

	return &Client{
		client:    whatsmeow.NewClient(deviceStore, nil),
		container: container,
	}, nil
}

func CheckSessionStatus(ctx context.Context, sessionPath string) (SessionStatus, error) {
	if sessionPath == "" {
		return SessionStatus{}, fmt.Errorf("session path is required")
	}
	info, err := os.Stat(sessionPath)
	if errors.Is(err, os.ErrNotExist) {
		return SessionStatus{State: SessionMissing}, nil
	}
	if err != nil {
		return SessionStatus{}, fmt.Errorf("check session file: %w", err)
	}
	if info.IsDir() {
		return SessionStatus{}, fmt.Errorf("session path is a directory")
	}

	container, err := openContainer(ctx, sessionPath, false)
	if err != nil {
		return SessionStatus{}, err
	}
	defer container.Close()

	devices, err := container.GetAllDevices(ctx)
	if err != nil {
		return SessionStatus{}, fmt.Errorf("query whatsapp sessions: %w", err)
	}

	status := SessionStatus{
		State:   SessionUnpaired,
		Devices: len(devices),
	}
	for _, device := range devices {
		if device.ID != nil {
			status.State = SessionPaired
			status.JID = device.ID.String()
			break
		}
	}

	return status, nil
}

func SessionURI(sessionPath string) string {
	query := url.Values{}
	query.Add("_pragma", "foreign_keys=on")
	query.Add("_pragma", "busy_timeout=5000")
	query.Add("_pragma", "journal_mode=WAL")

	return (&url.URL{
		Scheme:   "file",
		Path:     filepath.ToSlash(filepath.Clean(sessionPath)),
		RawQuery: query.Encode(),
	}).String()
}

func openContainer(ctx context.Context, sessionPath string, createParent bool) (*sqlstore.Container, error) {
	if sessionPath == "" {
		return nil, fmt.Errorf("session path is required")
	}
	if createParent {
		if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
			return nil, fmt.Errorf("create session directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", SessionURI(sessionPath))
	if err != nil {
		return nil, fmt.Errorf("open whatsapp session database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	container := sqlstore.NewWithDB(db, "sqlite", nil)
	if err := container.Upgrade(ctx); err != nil {
		_ = container.Close()
		return nil, fmt.Errorf("initialize whatsapp session database: %w", err)
	}

	return container, nil
}

func (c *Client) Connect(ctx context.Context) error {
	if c == nil || c.client == nil {
		return ErrClientNotOpen
	}
	if !c.hasStoredCredentials() {
		return fmt.Errorf("whatsapp session is not paired; run vimwhat login")
	}
	return c.connectAndWait(ctx, authTimeout)
}

func (c *Client) IsLoggedIn() bool {
	return c != nil && c.client != nil && c.client.IsLoggedIn()
}

func (c *Client) Login(ctx context.Context, handleQR QRHandler) error {
	if c == nil || c.client == nil {
		return ErrClientNotOpen
	}
	if c.IsLoggedIn() {
		return nil
	}
	if c.hasStoredCredentials() {
		if err := c.connectAndWait(ctx, authTimeout); err == nil {
			return nil
		} else if !errors.Is(err, errSessionRejected) {
			return err
		}

		if err := c.resetSession(ctx); err != nil {
			return err
		}
	}

	return c.loginWithQR(ctx, handleQR)
}

func (c *Client) loginWithQR(ctx context.Context, handleQR QRHandler) error {
	if c.client.IsConnected() {
		c.client.Disconnect()
	}
	authWaiter := c.newAuthWaiter()
	defer authWaiter.Close()
	qrChan, err := c.client.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("prepare login QR channel: %w", err)
	}
	if err := c.connectRaw(ctx); err != nil {
		return fmt.Errorf("connect for login: %w", err)
	}

	for {
		select {
		case item, ok := <-qrChan:
			if !ok {
				return fmt.Errorf("login QR channel closed before pairing completed")
			}
			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				if handleQR != nil {
					handleQR(item.Code)
				}
			case whatsmeow.QRChannelSuccess.Event:
				return authWaiter.Wait(ctx, authTimeout)
			case whatsmeow.QRChannelTimeout.Event:
				return fmt.Errorf("login QR code timed out")
			case whatsmeow.QRChannelClientOutdated.Event:
				return fmt.Errorf("whatsapp client is outdated")
			case whatsmeow.QRChannelScannedWithoutMultidevice.Event:
				return fmt.Errorf("qr was scanned without WhatsApp multi-device enabled")
			case whatsmeow.QRChannelErrUnexpectedEvent.Event:
				return fmt.Errorf("unexpected WhatsApp login event")
			case whatsmeow.QRChannelEventError:
				if item.Error != nil {
					return fmt.Errorf("whatsapp pairing failed: %w", item.Error)
				}
				return fmt.Errorf("whatsapp pairing failed")
			default:
				return fmt.Errorf("unexpected WhatsApp login event %q", item.Event)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) Logout(ctx context.Context) error {
	if c == nil || c.client == nil {
		return ErrClientNotOpen
	}
	if !c.hasStoredCredentials() {
		return nil
	}
	if !c.IsLoggedIn() {
		if err := c.connectAndWait(ctx, authTimeout); err != nil {
			if errors.Is(err, errSessionRejected) {
				if resetErr := c.resetSession(ctx); resetErr != nil {
					return resetErr
				}
				return nil
			}
			return fmt.Errorf("connect for logout: %w", err)
		}
	}
	if err := c.client.Logout(ctx); err != nil {
		if errors.Is(err, whatsmeow.ErrNotLoggedIn) {
			return nil
		}
		return err
	}
	return nil
}

func (c *Client) resetSession(ctx context.Context) error {
	if c == nil || c.client == nil {
		return ErrClientNotOpen
	}
	if c.hasStoredCredentials() {
		if err := c.client.Store.Delete(ctx); err != nil && !errors.Is(err, sqlstore.ErrDeviceIDMustBeSet) {
			return fmt.Errorf("delete rejected whatsapp session: %w", err)
		}
	}
	if err := c.reloadDevice(ctx); err != nil {
		return fmt.Errorf("reload whatsapp session: %w", err)
	}
	return nil
}

func (c *Client) reloadDevice(ctx context.Context) error {
	if c.container == nil {
		return fmt.Errorf("whatsapp session container is not open")
	}
	if c.client != nil {
		c.client.Disconnect()
	}

	deviceStore, err := c.container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("open whatsapp device store: %w", err)
	}
	c.client = whatsmeow.NewClient(deviceStore, nil)
	return nil
}

func (c *Client) hasStoredCredentials() bool {
	return c != nil && c.client != nil && c.client.Store != nil && c.client.Store.ID != nil
}

func (c *Client) connectRaw(ctx context.Context) error {
	if err := c.client.ConnectContext(ctx); err != nil && !errors.Is(err, whatsmeow.ErrAlreadyConnected) {
		return err
	}
	return nil
}

func (c *Client) connectAndWait(ctx context.Context, timeout time.Duration) error {
	authWaiter := c.newAuthWaiter()
	defer authWaiter.Close()
	if err := c.connectRaw(ctx); err != nil {
		return err
	}
	return authWaiter.Wait(ctx, timeout)
}

type authWaiter struct {
	client    *whatsmeow.Client
	result    chan error
	handlerID uint32
}

func (c *Client) newAuthWaiter() authWaiter {
	waiter := authWaiter{
		client: c.client,
		result: make(chan error, 1),
	}
	sendResult := func(err error) {
		select {
		case waiter.result <- err:
		default:
		}
	}
	waiter.handlerID = c.client.AddEventHandler(func(evt any) {
		switch event := evt.(type) {
		case *events.Connected:
			sendResult(nil)
		case *events.LoggedOut:
			sendResult(fmt.Errorf("%w: %s", errSessionRejected, event.Reason.String()))
		case *events.ConnectFailure:
			err := fmt.Errorf("whatsapp connect failure: %s", event.Reason.String())
			if event.Reason.IsLoggedOut() {
				err = fmt.Errorf("%w: %s", errSessionRejected, event.Reason.String())
			}
			sendResult(err)
		case *events.ClientOutdated:
			sendResult(fmt.Errorf("whatsapp client is outdated"))
		case *events.TemporaryBan:
			sendResult(fmt.Errorf("whatsapp temporary ban: %s", event.String()))
		case *events.StreamReplaced:
			sendResult(fmt.Errorf("whatsapp stream was replaced by another connection"))
		case *events.ManualLoginReconnect:
			sendResult(fmt.Errorf("whatsapp login requires a manual reconnect"))
		}
	})
	return waiter
}

func (w authWaiter) Wait(ctx context.Context, timeout time.Duration) error {
	if w.client.IsLoggedIn() {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-w.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		if w.client.IsLoggedIn() {
			return nil
		}
		return fmt.Errorf("timed out waiting for WhatsApp authentication")
	}
}

func (w authWaiter) Close() {
	if w.client != nil && w.handlerID != 0 {
		w.client.RemoveEventHandler(w.handlerID)
	}
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	if c.client != nil {
		c.client.Disconnect()
	}
	if c.container != nil {
		return c.container.Close()
	}
	return nil
}

func (Client) SendText(context.Context, string, string) (SendResult, error) {
	return SendResult{}, ErrNotImplemented
}

func (c *Client) SubscribeEvents(ctx context.Context) (<-chan Event, error) {
	if c == nil || c.client == nil {
		return nil, ErrClientNotOpen
	}

	raw := make(chan any, 256)
	out := make(chan Event, 256)
	handlerID := c.client.AddEventHandler(func(evt any) {
		select {
		case raw <- evt:
		case <-ctx.Done():
		}
	})

	go func() {
		defer close(out)
		defer c.client.RemoveEventHandler(handlerID)

		for {
			select {
			case evt := <-raw:
				for _, normalized := range c.normalizeWhatsmeowEvent(evt) {
					select {
					case out <- normalized:
					case <-ctx.Done():
						return
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

func (c *Client) RequestHistoryBefore(ctx context.Context, anchor HistoryAnchor, limit int) error {
	if c == nil || c.client == nil {
		return ErrClientNotOpen
	}
	info, err := historyAnchorMessageInfo(anchor)
	if err != nil {
		return err
	}
	if limit <= 0 {
		limit = 50
	}
	_, err = c.client.SendPeerMessage(ctx, c.client.BuildHistorySyncRequest(&info, limit))
	if err != nil {
		return fmt.Errorf("request whatsapp history before %s: %w", anchor.MessageID, err)
	}
	return nil
}

func historyAnchorMessageInfo(anchor HistoryAnchor) (types.MessageInfo, error) {
	if anchor.ChatJID == "" {
		return types.MessageInfo{}, fmt.Errorf("history anchor chat jid is required")
	}
	if anchor.MessageID == "" {
		return types.MessageInfo{}, fmt.Errorf("history anchor message id is required")
	}
	if anchor.Timestamp.IsZero() {
		return types.MessageInfo{}, fmt.Errorf("history anchor timestamp is required")
	}
	chat, err := types.ParseJID(anchor.ChatJID)
	if err != nil {
		return types.MessageInfo{}, fmt.Errorf("parse history anchor chat jid: %w", err)
	}
	if !supportedChat(chat) {
		return types.MessageInfo{}, fmt.Errorf("unsupported history chat jid %s", anchor.ChatJID)
	}
	return types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     chat,
			IsFromMe: anchor.IsFromMe,
			IsGroup:  chat.Server == types.GroupServer,
		},
		ID:        anchor.MessageID,
		Timestamp: anchor.Timestamp,
	}, nil
}

type SendResult struct {
	MessageID string
	RemoteID  string
	Status    string
}

type EventKind string

const (
	EventChatUpsert      EventKind = "chat_upsert"
	EventMessageUpsert   EventKind = "message_upsert"
	EventReceiptUpdate   EventKind = "receipt_update"
	EventMediaMetadata   EventKind = "media_metadata"
	EventConnectionState EventKind = "connection_state"
	EventHistoryStatus   EventKind = "history_status"
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

type Event struct {
	Kind       EventKind
	Chat       ChatEvent
	Message    MessageEvent
	Receipt    ReceiptEvent
	Media      MediaEvent
	Connection ConnectionEvent
	History    HistoryEvent
}

type ConnectionEvent struct {
	State  ConnectionState
	Detail string
}

type ChatEvent struct {
	ID            string
	JID           string
	Title         string
	Kind          string
	Unread        int
	UnreadKnown   bool
	Pinned        bool
	Muted         bool
	LastMessageAt time.Time
	Historical    bool
}

type MessageEvent struct {
	ID              string
	RemoteID        string
	ChatID          string
	ChatJID         string
	Sender          string
	SenderJID       string
	Body            string
	Timestamp       time.Time
	IsOutgoing      bool
	Status          string
	QuotedMessageID string
	QuotedRemoteID  string
	Historical      bool
}

type HistoryEvent struct {
	ChatID         string
	SyncType       string
	Messages       int
	Exhausted      bool
	TerminalReason string
}

type ReceiptEvent struct {
	MessageID string
	ChatID    string
	Status    string
}

type MediaEvent struct {
	MessageID     string
	MIMEType      string
	FileName      string
	SizeBytes     int64
	LocalPath     string
	ThumbnailPath string
	DownloadState string
	UpdatedAt     time.Time
	Historical    bool
}
