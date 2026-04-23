package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mdp/qrterminal/v3"

	"vimwhat/internal/whatsapp"
)

type WhatsAppSession interface {
	IsLoggedIn() bool
	Login(context.Context, func(string)) error
	Logout(context.Context) error
	Close() error
}

type WhatsAppLiveSession interface {
	WhatsAppSession
	Connect(context.Context) error
	GenerateMessageID() string
	SendText(context.Context, whatsapp.TextSendRequest) (whatsapp.SendResult, error)
	SendMedia(context.Context, whatsapp.MediaSendRequest) (whatsapp.SendResult, error)
	MarkRead(context.Context, []whatsapp.ReadReceiptTarget) error
	SendReaction(context.Context, whatsapp.ReactionSendRequest) (whatsapp.SendResult, error)
	SendChatPresence(context.Context, string, bool) error
	SubscribePresence(context.Context, string) error
	SubscribeEvents(context.Context) (<-chan whatsapp.Event, error)
	RequestHistoryBefore(context.Context, whatsapp.HistoryAnchor, int) error
	DownloadMedia(context.Context, whatsapp.MediaDownloadDescriptor, string) error
}

type WhatsAppMetadataSession interface {
	RefreshChatMetadata(context.Context) ([]whatsapp.Event, error)
}

type WhatsAppSessionOpener func(context.Context, string) (WhatsAppSession, error)

type WhatsAppSessionStatus struct {
	Label  string
	Paired bool
}

type WhatsAppSessionStatusChecker func(context.Context, string) (WhatsAppSessionStatus, error)

type QRRenderer func(io.Writer, string) error

func defaultOpenWhatsAppSession(ctx context.Context, sessionPath string) (WhatsAppSession, error) {
	return whatsapp.OpenSession(ctx, sessionPath)
}

func defaultCheckWhatsAppSession(ctx context.Context, sessionPath string) (WhatsAppSessionStatus, error) {
	status, err := whatsapp.CheckSessionStatus(ctx, sessionPath)
	if err != nil {
		return WhatsAppSessionStatus{}, err
	}
	return WhatsAppSessionStatus{
		Label:  status.String(),
		Paired: status.HasCredentials(),
	}, nil
}

func runLogin(env Environment, stdout, stderr io.Writer) int {
	session, err := openWhatsAppSession(context.Background(), env)
	if err != nil {
		fmt.Fprintf(stderr, "vimwhat: open whatsapp session: %v\n", err)
		return 1
	}
	defer session.Close()

	if session.IsLoggedIn() {
		fmt.Fprintln(stdout, "vimwhat: already logged in")
		return 0
	}

	renderQR := env.RenderQR
	if renderQR == nil {
		renderQR = renderTerminalQR
	}

	var renderErr error
	showedQRPrompt := false
	err = session.Login(context.Background(), func(code string) {
		if renderErr != nil {
			return
		}
		if !showedQRPrompt {
			fmt.Fprintln(stdout, "vimwhat: scan this QR with WhatsApp Linked devices")
			showedQRPrompt = true
		}
		fmt.Fprintln(stdout)
		renderErr = renderQR(stdout, code)
	})
	if err != nil {
		fmt.Fprintf(stderr, "vimwhat: login: %v\n", err)
		return 1
	}
	if renderErr != nil {
		fmt.Fprintf(stderr, "vimwhat: render login QR: %v\n", renderErr)
		return 1
	}

	fmt.Fprintln(stdout, "vimwhat: login complete")
	return 0
}

func runLogout(env Environment, stdout, stderr io.Writer) int {
	status, err := checkWhatsAppSession(context.Background(), env)
	if err != nil {
		fmt.Fprintf(stderr, "vimwhat: check whatsapp session: %v\n", err)
		return 1
	}
	if !status.Paired {
		fmt.Fprintln(stdout, "vimwhat: not logged in")
		return 0
	}

	session, err := openWhatsAppSession(context.Background(), env)
	if err != nil {
		fmt.Fprintf(stderr, "vimwhat: open whatsapp session: %v\n", err)
		return 1
	}
	defer session.Close()

	if err := session.Logout(context.Background()); err != nil {
		fmt.Fprintf(stderr, "vimwhat: logout: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, "vimwhat: logged out")
	return 0
}

func sessionStatusLine(env Environment, ctx context.Context) string {
	status, err := checkWhatsAppSession(ctx, env)
	if err != nil {
		return fmt.Sprintf("check failed: %v", err)
	}
	if strings.TrimSpace(status.Label) == "" {
		return "unknown"
	}
	return status.Label
}

func openWhatsAppSession(ctx context.Context, env Environment) (WhatsAppSession, error) {
	opener := env.OpenWhatsAppSession
	if opener == nil {
		opener = defaultOpenWhatsAppSession
	}
	return opener(ctx, env.Paths.SessionFile)
}

func checkWhatsAppSession(ctx context.Context, env Environment) (WhatsAppSessionStatus, error) {
	checker := env.CheckWhatsAppSession
	if checker == nil {
		checker = defaultCheckWhatsAppSession
	}
	return checker(ctx, env.Paths.SessionFile)
}

func renderTerminalQR(w io.Writer, code string) error {
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("qr code is empty")
	}
	qrterminal.GenerateWithConfig(code, qrterminal.Config{
		Level:      qrterminal.M,
		Writer:     w,
		HalfBlocks: true,
		QuietZone:  2,
	})
	return nil
}
