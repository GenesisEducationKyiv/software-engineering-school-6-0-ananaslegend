package mailer

import (
	"context"
	"embed"
	"fmt"
	htmltpl "html/template"
	texttpl "text/template"

	mail "github.com/wneessen/go-mail"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

//go:embed templates/*.html templates/*.txt
var templateFS embed.FS

var (
	confirmationHTMLTmpl = htmltpl.Must(htmltpl.ParseFS(templateFS, "templates/confirmation.html"))
	confirmationTXTTmpl  = texttpl.Must(texttpl.ParseFS(templateFS, "templates/confirmation.txt"))
	releaseHTMLTmpl      = htmltpl.Must(htmltpl.ParseFS(templateFS, "templates/release.html"))
	releaseTXTTmpl       = texttpl.Must(texttpl.ParseFS(templateFS, "templates/release.txt"))
)

type SMTPMailerConfig struct {
	Host      string
	Port      int
	User      string
	Password  string
	From      string
	TLSPolicy string // "starttls" | "ssl" | "none"
}

type SMTPMailer struct {
	client *mail.Client
	from   string
}

func NewSMTPMailer(cfg SMTPMailerConfig) (*SMTPMailer, error) {
	from := cfg.From
	if from == "" {
		from = cfg.User
	}

	tlsPolicy := parseTLSPolicy(cfg.TLSPolicy)

	opts := []mail.Option{
		mail.WithPort(cfg.Port),
		mail.WithTLSPolicy(tlsPolicy),
	}
	if cfg.User != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthPlain),
			mail.WithUsername(cfg.User),
			mail.WithPassword(cfg.Password),
		)
	}

	client, err := mail.NewClient(cfg.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("create smtp client: %w", err)
	}

	return &SMTPMailer{client: client, from: from}, nil
}

func (m *SMTPMailer) SendConfirmation(ctx context.Context, p domain.SendConfirmationParams) error {
	msg := mail.NewMsg()
	if err := msg.From(m.from); err != nil {
		return fmt.Errorf("set from: %w", err)
	}
	if err := msg.To(p.To); err != nil {
		return fmt.Errorf("set to: %w", err)
	}
	msg.Subject(fmt.Sprintf("Confirm your subscription to %s", p.RepoFullName))

	if err := msg.SetBodyHTMLTemplate(confirmationHTMLTmpl, p); err != nil {
		return fmt.Errorf("render confirmation html: %w", err)
	}
	if err := msg.AddAlternativeTextTemplate(confirmationTXTTmpl, p); err != nil {
		return fmt.Errorf("render confirmation txt: %w", err)
	}

	if err := m.client.DialAndSend(msg); err != nil {
		return fmt.Errorf("send confirmation email: %w", err)
	}
	return nil
}

func (m *SMTPMailer) SendRelease(ctx context.Context, p domain.SendReleaseParams) error {
	msg := mail.NewMsg()
	if err := msg.From(m.from); err != nil {
		return fmt.Errorf("set from: %w", err)
	}
	if err := msg.To(p.To); err != nil {
		return fmt.Errorf("set to: %w", err)
	}
	msg.Subject(fmt.Sprintf("New release %s for %s", p.ReleaseTag, p.RepoFullName))

	if err := msg.SetBodyHTMLTemplate(releaseHTMLTmpl, p); err != nil {
		return fmt.Errorf("render release html: %w", err)
	}
	if err := msg.AddAlternativeTextTemplate(releaseTXTTmpl, p); err != nil {
		return fmt.Errorf("render release txt: %w", err)
	}

	if err := m.client.DialAndSend(msg); err != nil {
		return fmt.Errorf("send release email: %w", err)
	}
	return nil
}

func parseTLSPolicy(policy string) mail.TLSPolicy {
	switch policy {
	case "ssl":
		return mail.TLSMandatory
	case "none":
		return mail.NoTLS
	default: // "starttls"
		return mail.TLSOpportunistic
	}
}
