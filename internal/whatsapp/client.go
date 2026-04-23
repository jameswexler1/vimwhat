package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

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

type MediaDownloadDescriptor struct {
	MessageID     string
	Kind          string
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
	FileLength    int64
	UpdatedAt     time.Time
}

type Adapter interface {
	Connect(ctx context.Context) error
	Login(ctx context.Context, handleQR QRHandler) error
	Logout(ctx context.Context) error
	GenerateMessageID() string
	SendText(ctx context.Context, request TextSendRequest) (SendResult, error)
	SendMedia(ctx context.Context, request MediaSendRequest) (SendResult, error)
	MarkRead(ctx context.Context, targets []ReadReceiptTarget) error
	SendReaction(ctx context.Context, request ReactionSendRequest) (SendResult, error)
	SendChatPresence(ctx context.Context, chatJID string, composing bool) error
	SubscribePresence(ctx context.Context, chatJID string) error
	SubscribeEvents(ctx context.Context) (<-chan Event, error)
	RequestHistoryBefore(ctx context.Context, anchor HistoryAnchor, limit int) error
	DownloadMedia(ctx context.Context, descriptor MediaDownloadDescriptor, targetPath string) error
	RefreshChatMetadata(ctx context.Context) ([]Event, error)
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

func (c *Client) GenerateMessageID() string {
	if c == nil || c.client == nil {
		return ""
	}
	return string(c.client.GenerateMessageID())
}

type TextSendRequest struct {
	ChatJID           string
	Body              string
	RemoteID          string
	QuotedRemoteID    string
	QuotedSenderJID   string
	QuotedMessageBody string
}

type ReadReceiptTarget struct {
	ChatJID   string
	RemoteID  string
	SenderJID string
	Timestamp time.Time
}

type ReactionSendRequest struct {
	ChatJID         string
	TargetRemoteID  string
	TargetSenderJID string
	Emoji           string
	RemoteID        string
}

func (c *Client) SendText(ctx context.Context, request TextSendRequest) (SendResult, error) {
	if c == nil || c.client == nil {
		return SendResult{}, ErrClientNotOpen
	}
	body := strings.TrimSpace(request.Body)
	if body == "" {
		return SendResult{}, fmt.Errorf("text body is required")
	}
	normalizedChatJID, err := NormalizeSendChatJID(request.ChatJID)
	if err != nil {
		return SendResult{}, err
	}
	to, err := types.ParseJID(normalizedChatJID)
	if err != nil {
		return SendResult{}, fmt.Errorf("parse send chat jid: %w", err)
	}
	remoteID := strings.TrimSpace(request.RemoteID)
	if remoteID == "" {
		remoteID = c.GenerateMessageID()
	}
	if remoteID == "" {
		return SendResult{}, fmt.Errorf("generate message id failed")
	}

	resp, err := c.client.SendMessage(ctx, to, c.textMessage(body, request), whatsmeow.SendRequestExtra{ID: types.MessageID(remoteID)})
	if err != nil {
		return SendResult{}, fmt.Errorf("send whatsapp text: %w", err)
	}
	if resp.ID != "" {
		remoteID = string(resp.ID)
	}
	timestamp := resp.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	return SendResult{
		MessageID: LocalMessageID(normalizedChatJID, remoteID),
		RemoteID:  remoteID,
		Status:    "sent",
		Timestamp: timestamp,
	}, nil
}

func (c *Client) textMessage(body string, request TextSendRequest) *waE2E.Message {
	contextInfo := c.quoteContextInfo(request.QuotedRemoteID, request.QuotedSenderJID, request.QuotedMessageBody)
	if contextInfo == nil {
		return &waE2E.Message{Conversation: proto.String(body)}
	}
	return &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        proto.String(body),
			ContextInfo: contextInfo,
		},
	}
}

func (c *Client) quoteContextInfo(quotedRemoteID, quotedSenderJID, quotedMessageBody string) *waE2E.ContextInfo {
	quotedRemoteID = strings.TrimSpace(quotedRemoteID)
	if quotedRemoteID == "" {
		return nil
	}
	contextInfo := &waE2E.ContextInfo{
		StanzaID: proto.String(quotedRemoteID),
	}
	if participant := c.quoteParticipant(quotedSenderJID); participant != "" {
		contextInfo.Participant = proto.String(participant)
	}
	if quotedBody := strings.TrimSpace(quotedMessageBody); quotedBody != "" {
		contextInfo.QuotedMessage = &waE2E.Message{Conversation: proto.String(quotedBody)}
	}
	return contextInfo
}

func (c *Client) quoteParticipant(senderJID string) string {
	senderJID = strings.TrimSpace(senderJID)
	if senderJID == "" {
		return ""
	}
	if senderJID == "me" {
		if c != nil && c.client != nil && c.client.Store != nil && c.client.Store.ID != nil {
			return c.client.Store.ID.ToNonAD().String()
		}
		return ""
	}
	jid, err := types.ParseJID(senderJID)
	if err != nil {
		return senderJID
	}
	return jid.ToNonAD().String()
}

func (c *Client) MarkRead(ctx context.Context, targets []ReadReceiptTarget) error {
	if c == nil || c.client == nil {
		return ErrClientNotOpen
	}
	groups := make(map[string]markReadGroup)
	for _, target := range targets {
		if strings.TrimSpace(target.RemoteID) == "" {
			continue
		}
		chatJID, err := NormalizeSendChatJID(target.ChatJID)
		if err != nil {
			return err
		}
		chat, err := types.ParseJID(chatJID)
		if err != nil {
			return fmt.Errorf("parse read chat jid: %w", err)
		}
		sender, err := readReceiptSender(chat, target.SenderJID)
		if err != nil {
			return err
		}
		key := chat.String() + "\x00" + sender.String()
		group := groups[key]
		group.chat = chat
		group.sender = sender
		group.ids = append(group.ids, types.MessageID(target.RemoteID))
		if target.Timestamp.After(group.timestamp) {
			group.timestamp = target.Timestamp
		}
		groups[key] = group
	}
	if len(groups) == 0 {
		return fmt.Errorf("no readable whatsapp message ids")
	}
	for _, group := range groups {
		timestamp := group.timestamp
		if timestamp.IsZero() {
			timestamp = time.Now()
		}
		if err := c.client.MarkRead(ctx, group.ids, timestamp, group.chat, group.sender); err != nil {
			return fmt.Errorf("mark whatsapp read: %w", err)
		}
	}
	return nil
}

type markReadGroup struct {
	chat      types.JID
	sender    types.JID
	ids       []types.MessageID
	timestamp time.Time
}

func readReceiptSender(chat types.JID, senderJID string) (types.JID, error) {
	if chat.Server == types.DefaultUserServer || chat.Server == types.HiddenUserServer {
		return types.EmptyJID, nil
	}
	senderJID = strings.TrimSpace(senderJID)
	if senderJID == "" || senderJID == "me" {
		return types.EmptyJID, nil
	}
	sender, err := types.ParseJID(senderJID)
	if err != nil {
		return types.EmptyJID, fmt.Errorf("parse read sender jid: %w", err)
	}
	return sender, nil
}

func (c *Client) SendReaction(ctx context.Context, request ReactionSendRequest) (SendResult, error) {
	if c == nil || c.client == nil {
		return SendResult{}, ErrClientNotOpen
	}
	chatJID, err := NormalizeSendChatJID(request.ChatJID)
	if err != nil {
		return SendResult{}, err
	}
	if strings.TrimSpace(request.TargetRemoteID) == "" {
		return SendResult{}, fmt.Errorf("reaction target message id is required")
	}
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return SendResult{}, fmt.Errorf("parse reaction chat jid: %w", err)
	}
	sender, err := reactionTargetSender(request.TargetSenderJID)
	if err != nil {
		return SendResult{}, err
	}
	remoteID := strings.TrimSpace(request.RemoteID)
	if remoteID == "" {
		remoteID = c.GenerateMessageID()
	}
	if remoteID == "" {
		return SendResult{}, fmt.Errorf("generate message id failed")
	}
	resp, err := c.client.SendMessage(
		ctx,
		chat,
		c.client.BuildReaction(chat, sender, types.MessageID(request.TargetRemoteID), request.Emoji),
		whatsmeow.SendRequestExtra{ID: types.MessageID(remoteID)},
	)
	if err != nil {
		return SendResult{}, fmt.Errorf("send whatsapp reaction: %w", err)
	}
	if resp.ID != "" {
		remoteID = string(resp.ID)
	}
	timestamp := resp.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	return SendResult{
		MessageID: LocalMessageID(chatJID, remoteID),
		RemoteID:  remoteID,
		Status:    "sent",
		Timestamp: timestamp,
	}, nil
}

func reactionTargetSender(senderJID string) (types.JID, error) {
	senderJID = strings.TrimSpace(senderJID)
	if senderJID == "" || senderJID == "me" {
		return types.EmptyJID, nil
	}
	sender, err := types.ParseJID(senderJID)
	if err != nil {
		return types.EmptyJID, fmt.Errorf("parse reaction sender jid: %w", err)
	}
	return sender, nil
}

func (c *Client) SendChatPresence(ctx context.Context, chatJID string, composing bool) error {
	if c == nil || c.client == nil {
		return ErrClientNotOpen
	}
	normalizedChatJID, err := NormalizeSendChatJID(chatJID)
	if err != nil {
		return err
	}
	to, err := types.ParseJID(normalizedChatJID)
	if err != nil {
		return fmt.Errorf("parse presence chat jid: %w", err)
	}
	state := types.ChatPresencePaused
	if composing {
		state = types.ChatPresenceComposing
	}
	if err := c.client.SendChatPresence(ctx, to, state, types.ChatPresenceMediaText); err != nil {
		return fmt.Errorf("send whatsapp chat presence: %w", err)
	}
	return nil
}

func (c *Client) SubscribePresence(ctx context.Context, chatJID string) error {
	if c == nil || c.client == nil {
		return ErrClientNotOpen
	}
	normalizedChatJID, err := NormalizeSendChatJID(chatJID)
	if err != nil {
		return err
	}
	to, err := types.ParseJID(normalizedChatJID)
	if err != nil {
		return fmt.Errorf("parse presence subscription jid: %w", err)
	}
	if to.Server == types.GroupServer {
		return nil
	}
	if err := c.client.SubscribePresence(ctx, to); err != nil {
		return fmt.Errorf("subscribe whatsapp presence: %w", err)
	}
	return nil
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
				for _, normalized := range c.normalizeWhatsmeowEvent(ctx, evt) {
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

func (c *Client) DownloadMedia(ctx context.Context, descriptor MediaDownloadDescriptor, targetPath string) error {
	if c == nil || c.client == nil {
		return ErrClientNotOpen
	}
	if strings.TrimSpace(targetPath) == "" {
		return fmt.Errorf("media download target path is required")
	}
	mediaType, err := mediaTypeForDownloadKind(descriptor.Kind)
	if err != nil {
		return err
	}
	if strings.TrimSpace(descriptor.DirectPath) == "" && strings.TrimSpace(descriptor.URL) == "" {
		return fmt.Errorf("media download source is required")
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create media download directory: %w", err)
	}
	file, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("create media download target: %w", err)
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(targetPath)
		}
	}()

	if strings.TrimSpace(descriptor.DirectPath) != "" {
		fileLength := -1
		if descriptor.FileLength > 0 {
			fileLength = int(descriptor.FileLength)
		}
		err = c.client.DownloadMediaWithPathToFile(
			ctx,
			descriptor.DirectPath,
			descriptor.FileEncSHA256,
			descriptor.FileSHA256,
			descriptor.MediaKey,
			fileLength,
			mediaType,
			"",
			file,
		)
	} else {
		err = c.client.DownloadToFile(ctx, downloadableMedia{
			descriptor: descriptor,
			mediaType:  mediaType,
		}, file)
	}
	if err != nil {
		return fmt.Errorf("download whatsapp media: %w", err)
	}
	ok = true
	return nil
}

func mediaTypeForDownloadKind(kind string) (whatsmeow.MediaType, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "image":
		return whatsmeow.MediaImage, nil
	case "video":
		return whatsmeow.MediaVideo, nil
	case "audio":
		return whatsmeow.MediaAudio, nil
	case "document":
		return whatsmeow.MediaDocument, nil
	default:
		return "", fmt.Errorf("unsupported media download kind %q", kind)
	}
}

type downloadableMedia struct {
	descriptor MediaDownloadDescriptor
	mediaType  whatsmeow.MediaType
}

func (m downloadableMedia) GetDirectPath() string {
	return m.descriptor.DirectPath
}

func (m downloadableMedia) GetURL() string {
	return m.descriptor.URL
}

func (m downloadableMedia) GetMediaKey() []byte {
	return m.descriptor.MediaKey
}

func (m downloadableMedia) GetFileSHA256() []byte {
	return m.descriptor.FileSHA256
}

func (m downloadableMedia) GetFileEncSHA256() []byte {
	return m.descriptor.FileEncSHA256
}

func (m downloadableMedia) GetMediaType() whatsmeow.MediaType {
	return m.mediaType
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
	Timestamp time.Time
	Notice    string
}

type EventKind string

const (
	EventChatUpsert      EventKind = "chat_upsert"
	EventMessageUpsert   EventKind = "message_upsert"
	EventReceiptUpdate   EventKind = "receipt_update"
	EventReactionUpdate  EventKind = "reaction_update"
	EventPresenceUpdate  EventKind = "presence_update"
	EventMediaMetadata   EventKind = "media_metadata"
	EventConnectionState EventKind = "connection_state"
	EventHistoryStatus   EventKind = "history_status"
	EventContactUpsert   EventKind = "contact_upsert"
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
	Reaction   ReactionEvent
	Presence   PresenceEvent
	Media      MediaEvent
	Connection ConnectionEvent
	History    HistoryEvent
	Contact    ContactEvent
}

type ConnectionEvent struct {
	State  ConnectionState
	Detail string
}

type ChatEvent struct {
	ID            string
	JID           string
	Title         string
	TitleSource   string
	Kind          string
	Unread        int
	UnreadKnown   bool
	Pinned        bool
	Muted         bool
	LastMessageAt time.Time
	Historical    bool
}

type ContactEvent struct {
	JID         string
	DisplayName string
	NotifyName  string
	Phone       string
	UpdatedAt   time.Time
	TitleSource string
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

type ReactionEvent struct {
	MessageID  string
	ChatID     string
	SenderJID  string
	Emoji      string
	Timestamp  time.Time
	IsOutgoing bool
}

type PresenceEvent struct {
	ChatID    string
	SenderJID string
	Sender    string
	Typing    bool
	UpdatedAt time.Time
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
	Download      MediaDownloadDescriptor
	Historical    bool
}
