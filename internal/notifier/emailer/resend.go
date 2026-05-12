package emailer

import (
	"context"
	"fmt"

	"github.com/resend/resend-go/v2"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

type ResendMailer struct {
	client *resend.Client
	from   string
}

func NewResendMailer(apiKey, from string) *ResendMailer {
	return &ResendMailer{
		client: resend.NewClient(apiKey),
		from:   from,
	}
}

func (m *ResendMailer) SendConfirmation(ctx context.Context, p domain.SendConfirmationParams) error {
	email, err := renderConfirmation(p)
	if err != nil {
		return fmt.Errorf("emailer.ResendMailer.SendConfirmation: %w", err)
	}
	if err := m.send(ctx, p.To, email); err != nil {
		return fmt.Errorf("emailer.ResendMailer.SendConfirmation: %w", err)
	}
	return nil
}

func (m *ResendMailer) SendRelease(ctx context.Context, p domain.SendReleaseParams) error {
	email, err := renderRelease(p)
	if err != nil {
		return fmt.Errorf("emailer.ResendMailer.SendRelease: %w", err)
	}
	if err := m.send(ctx, p.To, email); err != nil {
		return fmt.Errorf("emailer.ResendMailer.SendRelease: %w", err)
	}
	return nil
}

func (m *ResendMailer) send(ctx context.Context, to string, email renderedEmail) error {
	_, err := m.client.Emails.SendWithContext(ctx, &resend.SendEmailRequest{
		From:    m.from,
		To:      []string{to},
		Subject: email.Subject,
		Html:    email.HTML,
		Text:    email.Text,
	})
	if err != nil {
		return fmt.Errorf("resend send: %w", err)
	}
	return nil
}
