package email

import (
	"context"
	"fmt"

	"github.com/resend/resend-go/v2"

	"github.com/ananaslegend/reposeetory/internal/notifications/contract"
)

type ResendMailer struct {
	client *resend.Client
	from   string
}

// NewResendMailer wires the real Resend HTTP client against the public API.
// Production callers stay unchanged.
func NewResendMailer(apiKey, from string) *ResendMailer {
	return NewResendMailerWithClient(resend.NewClient(apiKey), from)
}

// NewResendMailerWithClient is the seam unit tests use to inject a Resend
// client whose BaseURL points at a httptest.Server. Production code does not
// call this directly — use NewResendMailer.
func NewResendMailerWithClient(client *resend.Client, from string) *ResendMailer {
	return &ResendMailer{
		client: client,
		from:   from,
	}
}

func (m *ResendMailer) SendConfirmation(ctx context.Context, p contract.SendConfirmationRequest) error {
	email, err := renderConfirmation(p)
	if err != nil {
		return fmt.Errorf("email.ResendMailer.SendConfirmation: %w", err)
	}
	if err := m.send(ctx, p.To, email); err != nil {
		return fmt.Errorf("email.ResendMailer.SendConfirmation: %w", err)
	}
	return nil
}

func (m *ResendMailer) SendRelease(ctx context.Context, p contract.SendReleaseRequest) error {
	email, err := renderRelease(p)
	if err != nil {
		return fmt.Errorf("email.ResendMailer.SendRelease: %w", err)
	}
	if err := m.send(ctx, p.To, email); err != nil {
		return fmt.Errorf("email.ResendMailer.SendRelease: %w", err)
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
