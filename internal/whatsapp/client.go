package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
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

var stickerAppStatePatchNames = appstate.AllPatchNames

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

type ChatAvatarResult struct {
	ChatJID   string
	AvatarID  string
	URL       string
	Changed   bool
	Cleared   bool
	UpdatedAt time.Time
}

type Adapter interface {
	Connect(ctx context.Context) error
	Login(ctx context.Context, handleQR QRHandler) error
	Logout(ctx context.Context) error
	GenerateMessageID() string
	CanonicalChatJID(ctx context.Context, chatJID string) (string, error)
	SendText(ctx context.Context, request TextSendRequest) (SendResult, error)
	SendMedia(ctx context.Context, request MediaSendRequest) (SendResult, error)
	SendSticker(ctx context.Context, request StickerSendRequest) (SendResult, error)
	ForwardMessage(ctx context.Context, request ForwardMessageRequest) (SendResult, error)
	MarkRead(ctx context.Context, targets []ReadReceiptTarget) error
	SendReaction(ctx context.Context, request ReactionSendRequest) (SendResult, error)
	DeleteMessageForEveryone(ctx context.Context, request DeleteForEveryoneRequest) (SendResult, error)
	EditMessage(ctx context.Context, request EditMessageRequest) (SendResult, error)
	SendChatPresence(ctx context.Context, chatJID string, composing bool) error
	SubscribePresence(ctx context.Context, chatJID string) error
	SubscribeEvents(ctx context.Context) (<-chan Event, error)
	RequestHistoryBefore(ctx context.Context, anchor HistoryAnchor, limit int) error
	SyncRecentStickers(ctx context.Context) ([]Event, error)
	DownloadMedia(ctx context.Context, descriptor MediaDownloadDescriptor, targetPath string) error
	GetChatAvatar(ctx context.Context, chatJID, existingID string) (ChatAvatarResult, error)
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
		Path:     sessionURIPath(sessionPath),
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

type ForwardMessageRequest struct {
	ChatJID  string
	Payload  []byte
	RemoteID string
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

type DeleteForEveryoneRequest struct {
	ChatJID        string
	TargetRemoteID string
}

type EditMessageRequest struct {
	ChatJID        string
	TargetRemoteID string
	Body           string
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

func (c *Client) ForwardMessage(ctx context.Context, request ForwardMessageRequest) (SendResult, error) {
	if c == nil || c.client == nil {
		return SendResult{}, ErrClientNotOpen
	}
	normalizedChatJID, err := NormalizeSendChatJID(request.ChatJID)
	if err != nil {
		return SendResult{}, err
	}
	to, err := types.ParseJID(normalizedChatJID)
	if err != nil {
		return SendResult{}, fmt.Errorf("parse forward chat jid: %w", err)
	}
	message, err := ForwardedMessageFromPayload(request.Payload)
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
	resp, err := c.client.SendMessage(ctx, to, message, whatsmeow.SendRequestExtra{ID: types.MessageID(remoteID)})
	if err != nil {
		return SendResult{}, fmt.Errorf("send whatsapp forward: %w", err)
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

func ForwardedMessageFromPayload(payload []byte) (*waE2E.Message, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("forward payload is required")
	}
	var message waE2E.Message
	if err := proto.Unmarshal(payload, &message); err != nil {
		return nil, fmt.Errorf("decode forward payload: %w", err)
	}
	if messageForwardContext(&message) == nil {
		return nil, fmt.Errorf("message type cannot be forwarded exactly")
	}
	markMessageForwarded(&message)
	return &message, nil
}

func markMessageForwarded(message *waE2E.Message) {
	contextInfo := messageForwardContext(message)
	if contextInfo == nil {
		return
	}
	score := contextInfo.GetForwardingScore() + 1
	contextInfo.ForwardingScore = proto.Uint32(score)
	contextInfo.IsForwarded = proto.Bool(true)
}

func messageForwardContext(message *waE2E.Message) *waE2E.ContextInfo {
	if message == nil {
		return nil
	}
	if body := message.GetConversation(); body != "" {
		message.ExtendedTextMessage = &waE2E.ExtendedTextMessage{
			Text:        proto.String(body),
			ContextInfo: &waE2E.ContextInfo{},
		}
		message.Conversation = nil
		return message.ExtendedTextMessage.ContextInfo
	}
	if text := message.GetExtendedTextMessage(); text != nil {
		if text.ContextInfo == nil {
			text.ContextInfo = &waE2E.ContextInfo{}
		}
		return text.ContextInfo
	}
	if image := message.GetImageMessage(); image != nil {
		if image.ContextInfo == nil {
			image.ContextInfo = &waE2E.ContextInfo{}
		}
		return image.ContextInfo
	}
	if video := message.GetVideoMessage(); video != nil {
		if video.ContextInfo == nil {
			video.ContextInfo = &waE2E.ContextInfo{}
		}
		return video.ContextInfo
	}
	if audio := message.GetAudioMessage(); audio != nil {
		if audio.ContextInfo == nil {
			audio.ContextInfo = &waE2E.ContextInfo{}
		}
		return audio.ContextInfo
	}
	if document := message.GetDocumentMessage(); document != nil {
		if document.ContextInfo == nil {
			document.ContextInfo = &waE2E.ContextInfo{}
		}
		return document.ContextInfo
	}
	if sticker := message.GetStickerMessage(); sticker != nil {
		if sticker.ContextInfo == nil {
			sticker.ContextInfo = &waE2E.ContextInfo{}
		}
		return sticker.ContextInfo
	}
	return nil
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

func (c *Client) DeleteMessageForEveryone(ctx context.Context, request DeleteForEveryoneRequest) (SendResult, error) {
	if c == nil || c.client == nil {
		return SendResult{}, ErrClientNotOpen
	}
	chatJID, err := NormalizeSendChatJID(request.ChatJID)
	if err != nil {
		return SendResult{}, err
	}
	targetRemoteID := strings.TrimSpace(request.TargetRemoteID)
	if targetRemoteID == "" {
		return SendResult{}, fmt.Errorf("delete target message id is required")
	}
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return SendResult{}, fmt.Errorf("parse delete chat jid: %w", err)
	}
	resp, err := c.client.SendMessage(ctx, chat, c.client.BuildRevoke(chat, types.EmptyJID, types.MessageID(targetRemoteID)))
	if err != nil {
		return SendResult{}, fmt.Errorf("send whatsapp delete for everybody: %w", err)
	}
	timestamp := resp.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	return SendResult{
		MessageID: LocalMessageID(chatJID, targetRemoteID),
		RemoteID:  targetRemoteID,
		Status:    "deleted",
		Timestamp: timestamp,
	}, nil
}

func (c *Client) EditMessage(ctx context.Context, request EditMessageRequest) (SendResult, error) {
	if c == nil || c.client == nil {
		return SendResult{}, ErrClientNotOpen
	}
	chatJID, err := NormalizeSendChatJID(request.ChatJID)
	if err != nil {
		return SendResult{}, err
	}
	targetRemoteID := strings.TrimSpace(request.TargetRemoteID)
	if targetRemoteID == "" {
		return SendResult{}, fmt.Errorf("edit target message id is required")
	}
	body := strings.TrimSpace(request.Body)
	if body == "" {
		return SendResult{}, fmt.Errorf("edit body is required")
	}
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return SendResult{}, fmt.Errorf("parse edit chat jid: %w", err)
	}
	resp, err := c.client.SendMessage(ctx, chat, c.client.BuildEdit(chat, types.MessageID(targetRemoteID), &waE2E.Message{
		Conversation: proto.String(body),
	}))
	if err != nil {
		return SendResult{}, fmt.Errorf("send whatsapp edit: %w", err)
	}
	timestamp := resp.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	return SendResult{
		MessageID: LocalMessageID(chatJID, targetRemoteID),
		RemoteID:  targetRemoteID,
		Status:    "edited",
		Timestamp: timestamp,
	}, nil
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

func (c *Client) SyncRecentStickers(ctx context.Context) ([]Event, error) {
	if c == nil || c.client == nil {
		return nil, ErrClientNotOpen
	}

	previousEmitFullSync := c.client.EmitAppStateEventsOnFullSync
	c.client.EmitAppStateEventsOnFullSync = true
	defer func() {
		c.client.EmitAppStateEventsOnFullSync = previousEmitFullSync
	}()

	var out []Event
	var syncErr error
	for _, name := range stickerAppStatePatchNames {
		rawEvents, err := c.client.DangerousInternals().FetchAppState(ctx, name, true, false)
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			syncErr = errors.Join(syncErr, fmt.Errorf("fetch whatsapp sticker app state %s: %w", name, err))
			continue
		}
		for _, raw := range rawEvents {
			for _, event := range c.normalizeWhatsmeowEvent(ctx, raw) {
				switch event.Kind {
				case EventRecentSticker, EventRecentStickerRemove:
					out = append(out, event)
				}
			}
		}
	}
	return out, syncErr
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

	if mediaDownloadNeedsBufferedWrite(descriptor) {
		var data []byte
		data, err = c.downloadMediaBytes(ctx, descriptor, mediaType)
		if err == nil {
			var written int
			written, err = file.Write(data)
			if err == nil && written != len(data) {
				err = io.ErrShortWrite
			}
		}
	} else {
		err = c.downloadMediaToFile(ctx, descriptor, mediaType, file)
	}
	if err != nil {
		return fmt.Errorf("download whatsapp media: %w", err)
	}
	ok = true
	return nil
}

func (c *Client) downloadMediaToFile(ctx context.Context, descriptor MediaDownloadDescriptor, mediaType whatsmeow.MediaType, file *os.File) error {
	var attempts []error
	for _, source := range mediaDownloadSources(descriptor) {
		if len(attempts) > 0 {
			if err := resetMediaDownloadTarget(file); err != nil {
				return errors.Join(append(attempts, err)...)
			}
		}
		sourceDescriptor := mediaDownloadDescriptorForSource(descriptor, source)
		var err error
		switch source {
		case mediaDownloadSourceURL:
			err = c.client.DownloadToFile(ctx, downloadableMedia{
				descriptor: sourceDescriptor,
				mediaType:  mediaType,
			}, file)
		case mediaDownloadSourceDirectPath:
			err = c.client.DownloadMediaWithPathToFile(
				ctx,
				sourceDescriptor.DirectPath,
				sourceDescriptor.FileEncSHA256,
				sourceDescriptor.FileSHA256,
				sourceDescriptor.MediaKey,
				mediaDownloadValidationLength(sourceDescriptor),
				mediaType,
				"",
				file,
			)
		}
		if err == nil {
			return nil
		}
		attempts = append(attempts, fmt.Errorf("%s: %w", source, err))
	}
	if len(attempts) == 0 {
		return fmt.Errorf("media download source is required")
	}
	return errors.Join(attempts...)
}

func (c *Client) downloadMediaBytes(ctx context.Context, descriptor MediaDownloadDescriptor, mediaType whatsmeow.MediaType) ([]byte, error) {
	var attempts []error
	for _, source := range mediaDownloadSources(descriptor) {
		sourceDescriptor := mediaDownloadDescriptorForSource(descriptor, source)
		var (
			data []byte
			err  error
		)
		switch source {
		case mediaDownloadSourceURL:
			data, err = c.client.Download(ctx, downloadableMedia{
				descriptor: sourceDescriptor,
				mediaType:  mediaType,
			})
		case mediaDownloadSourceDirectPath:
			data, err = c.client.DownloadMediaWithPath(
				ctx,
				sourceDescriptor.DirectPath,
				sourceDescriptor.FileEncSHA256,
				sourceDescriptor.FileSHA256,
				sourceDescriptor.MediaKey,
				mediaDownloadValidationLength(sourceDescriptor),
				mediaType,
				"",
			)
		}
		if err == nil {
			return data, nil
		}
		attempts = append(attempts, fmt.Errorf("%s: %w", source, err))
	}
	if len(attempts) == 0 {
		return nil, fmt.Errorf("media download source is required")
	}
	return nil, errors.Join(attempts...)
}

func mediaDownloadNeedsBufferedWrite(descriptor MediaDownloadDescriptor) bool {
	return len(descriptor.FileSHA256) == 0
}

type mediaDownloadSource string

const (
	mediaDownloadSourceURL        mediaDownloadSource = "url"
	mediaDownloadSourceDirectPath mediaDownloadSource = "direct path"
)

func mediaDownloadSources(descriptor MediaDownloadDescriptor) []mediaDownloadSource {
	out := make([]mediaDownloadSource, 0, 2)
	if mediaDownloadURLUsable(descriptor.URL) {
		out = append(out, mediaDownloadSourceURL)
	}
	if strings.TrimSpace(descriptor.DirectPath) != "" {
		out = append(out, mediaDownloadSourceDirectPath)
	}
	return out
}

func mediaDownloadURLUsable(raw string) bool {
	raw = strings.TrimSpace(raw)
	return raw != "" && !strings.HasPrefix(raw, "https://web.whatsapp.net")
}

func mediaDownloadDescriptorForSource(descriptor MediaDownloadDescriptor, source mediaDownloadSource) MediaDownloadDescriptor {
	descriptor.URL = strings.TrimSpace(descriptor.URL)
	descriptor.DirectPath = strings.TrimSpace(descriptor.DirectPath)
	switch source {
	case mediaDownloadSourceURL:
		descriptor.DirectPath = ""
	case mediaDownloadSourceDirectPath:
		descriptor.URL = ""
	}
	return descriptor
}

func resetMediaDownloadTarget(file *os.File) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek media download target: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncate media download target: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind media download target: %w", err)
	}
	return nil
}

func mediaDownloadValidationLength(descriptor MediaDownloadDescriptor) int {
	if descriptor.FileLength <= 0 || len(descriptor.FileSHA256) == 0 {
		return -1
	}
	maxInt := int64(int(^uint(0) >> 1))
	if descriptor.FileLength > maxInt {
		return -1
	}
	return int(descriptor.FileLength)
}

func (c *Client) GetChatAvatar(ctx context.Context, chatJID, existingID string) (ChatAvatarResult, error) {
	if c == nil || c.client == nil {
		return ChatAvatarResult{}, ErrClientNotOpen
	}
	chatJID = strings.TrimSpace(chatJID)
	if chatJID == "" {
		return ChatAvatarResult{}, fmt.Errorf("chat jid is required")
	}
	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return ChatAvatarResult{}, fmt.Errorf("parse chat jid: %w", err)
	}
	if !supportedChat(jid) {
		return ChatAvatarResult{}, fmt.Errorf("unsupported avatar chat jid %s", chatJID)
	}

	info, err := c.client.GetProfilePictureInfo(ctx, jid, chatAvatarProfilePictureParams(existingID))
	switch {
	case err == nil:
	case errors.Is(err, whatsmeow.ErrProfilePictureNotSet), errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized):
		return ChatAvatarResult{
			ChatJID:   jid.String(),
			Cleared:   true,
			UpdatedAt: time.Now(),
		}, nil
	default:
		return ChatAvatarResult{}, fmt.Errorf("get profile picture info for %s: %w", chatJID, err)
	}
	if info == nil {
		return ChatAvatarResult{
			ChatJID:   jid.String(),
			AvatarID:  strings.TrimSpace(existingID),
			UpdatedAt: time.Now(),
		}, nil
	}
	return ChatAvatarResult{
		ChatJID:   jid.String(),
		AvatarID:  strings.TrimSpace(info.ID),
		URL:       strings.TrimSpace(info.URL),
		Changed:   true,
		UpdatedAt: time.Now(),
	}, nil
}

func chatAvatarProfilePictureParams(existingID string) *whatsmeow.GetProfilePictureParams {
	return &whatsmeow.GetProfilePictureParams{
		Preview:    false,
		ExistingID: strings.TrimSpace(existingID),
	}
}

func mediaTypeForDownloadKind(kind string) (whatsmeow.MediaType, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "image":
		return whatsmeow.MediaImage, nil
	case "sticker":
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
	EventChatUpsert          EventKind = "chat_upsert"
	EventMessageUpsert       EventKind = "message_upsert"
	EventMessageEdit         EventKind = "message_edit"
	EventMessageDelete       EventKind = "message_delete"
	EventReceiptUpdate       EventKind = "receipt_update"
	EventReactionUpdate      EventKind = "reaction_update"
	EventPresenceUpdate      EventKind = "presence_update"
	EventMediaMetadata       EventKind = "media_metadata"
	EventRecentSticker       EventKind = "recent_sticker"
	EventRecentStickerRemove EventKind = "recent_sticker_remove"
	EventConnectionState     EventKind = "connection_state"
	EventHistoryStatus       EventKind = "history_status"
	EventOfflineSync         EventKind = "offline_sync"
	EventContactUpsert       EventKind = "contact_upsert"
	EventChatAvatarUpdate    EventKind = "chat_avatar_update"
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
	Kind          EventKind
	Chat          ChatEvent
	Message       MessageEvent
	Edit          MessageEditEvent
	Delete        MessageDeleteEvent
	Receipt       ReceiptEvent
	Reaction      ReactionEvent
	Presence      PresenceEvent
	Media         MediaEvent
	Sticker       RecentStickerEvent
	StickerRemove RecentStickerRemoveEvent
	Connection    ConnectionEvent
	History       HistoryEvent
	Offline       OfflineSyncEvent
	Contact       ContactEvent
	Avatar        AvatarEvent
}

type ApplyResult struct {
	MessageInserted bool
	Message         MessageEvent
}

type ConnectionEvent struct {
	State  ConnectionState
	Detail string
}

type ChatEvent struct {
	ID            string
	JID           string
	AliasIDs      []string
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
	ChatID      string
	DisplayName string
	NotifyName  string
	Phone       string
	UpdatedAt   time.Time
	TitleSource string
}

type MessageEvent struct {
	ID                  string
	RemoteID            string
	ChatID              string
	ChatJID             string
	Sender              string
	SenderJID           string
	Body                string
	NotificationPreview string
	Timestamp           time.Time
	IsOutgoing          bool
	Status              string
	QuotedMessageID     string
	QuotedRemoteID      string
	ForwardPayload      []byte
	Historical          bool
}

type MessageDeleteEvent struct {
	MessageID     string
	RemoteID      string
	ChatID        string
	ChatJID       string
	DeletedReason string
	Timestamp     time.Time
	Historical    bool
}

type MessageEditEvent struct {
	MessageID  string
	RemoteID   string
	ChatID     string
	ChatJID    string
	Body       string
	EditedAt   time.Time
	Historical bool
}

type HistoryEvent struct {
	ChatID         string
	SyncType       string
	Messages       int
	Exhausted      bool
	TerminalReason string
}

type OfflineSyncEvent struct {
	Active         bool
	Completed      bool
	Total          int
	Processed      int
	AppDataChanges int
	Messages       int
	Notifications  int
	Receipts       int
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

type AvatarEvent struct {
	ChatID    string
	ChatJID   string
	AvatarID  string
	Remove    bool
	UpdatedAt time.Time
}

type MediaEvent struct {
	MessageID          string
	Kind               string
	MIMEType           string
	FileName           string
	SizeBytes          int64
	LocalPath          string
	ThumbnailPath      string
	ThumbnailData      []byte
	DownloadState      string
	IsAnimated         bool
	IsLottie           bool
	AccessibilityLabel string
	UpdatedAt          time.Time
	Download           MediaDownloadDescriptor
	Historical         bool
}

type RecentStickerEvent struct {
	ID            string
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
	FileLength    int64
	MIMEType      string
	FileName      string
	LocalPath     string
	Width         int
	Height        int
	Weight        float64
	LastUsedAt    time.Time
	IsFavorite    bool
	IsAnimated    bool
	IsLottie      bool
	IsAvatar      bool
	ImageHash     string
	UpdatedAt     time.Time
	Historical    bool
}

type RecentStickerRemoveEvent struct {
	LastUsedAt time.Time
	UpdatedAt  time.Time
}
