package emailer

import (
	"context"
	"fmt"

	mail "github.com/wneessen/go-mail"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
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

	opts := []mail.Option{
		mail.WithPort(cfg.Port),
	}
	switch cfg.TLSPolicy {
	case "ssl":
		opts = append(opts, mail.WithSSL())
	case "none":
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	default: // "starttls"
		opts = append(opts, mail.WithTLSPolicy(mail.TLSOpportunistic))
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
	email, err := renderConfirmation(p)
	if err != nil {
		return fmt.Errorf("emailer.SMTPMailer.SendConfirmation: %w", err)
	}
	if err := m.send(ctx, p.To, email); err != nil {
		return fmt.Errorf("emailer.SMTPMailer.SendConfirmation: %w", err)
	}
	return nil
}

func (m *SMTPMailer) SendRelease(ctx context.Context, p domain.SendReleaseParams) error {
	email, err := renderRelease(p)
	if err != nil {
		return fmt.Errorf("emailer.SMTPMailer.SendRelease: %w", err)
	}
	if err := m.send(ctx, p.To, email); err != nil {
		return fmt.Errorf("emailer.SMTPMailer.SendRelease: %w", err)
	}
	return nil
}

func (m *SMTPMailer) send(ctx context.Context, to string, email renderedEmail) error {
	msg := mail.NewMsg()
	if err := msg.From(m.from); err != nil {
		return fmt.Errorf("set from: %w", err)
	}
	if err := msg.To(to); err != nil {
		return fmt.Errorf("set to: %w", err)
	}
	msg.Subject(email.Subject)
	msg.SetBodyString(mail.TypeTextHTML, email.HTML)
	msg.AddAlternativeString(mail.TypeTextPlain, email.Text)

	if err := m.client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("dial and send: %w", err)
	}
	return nil
}
