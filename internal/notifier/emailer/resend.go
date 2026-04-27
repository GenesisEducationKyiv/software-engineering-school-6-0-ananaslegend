package emailer

import (
	"bytes"
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
	var htmlBuf, txtBuf bytes.Buffer
	if err := confirmationHTMLTmpl.Execute(&htmlBuf, p); err != nil {
		return fmt.Errorf("render confirmation html: %w", err)
	}
	if err := confirmationTXTTmpl.Execute(&txtBuf, p); err != nil {
		return fmt.Errorf("render confirmation txt: %w", err)
	}

	_, err := m.client.Emails.Send(&resend.SendEmailRequest{
		From:    m.from,
		To:      []string{p.To},
		Subject: fmt.Sprintf("Confirm your subscription to %s", p.RepoFullName),
		Html:    htmlBuf.String(),
		Text:    txtBuf.String(),
	})
	if err != nil {
		return fmt.Errorf("send confirmation email: %w", err)
	}
	return nil
}

func (m *ResendMailer) SendRelease(ctx context.Context, p domain.SendReleaseParams) error {
	var htmlBuf, txtBuf bytes.Buffer
	if err := releaseHTMLTmpl.Execute(&htmlBuf, p); err != nil {
		return fmt.Errorf("render release html: %w", err)
	}
	if err := releaseTXTTmpl.Execute(&txtBuf, p); err != nil {
		return fmt.Errorf("render release txt: %w", err)
	}

	_, err := m.client.Emails.Send(&resend.SendEmailRequest{
		From:    m.from,
		To:      []string{p.To},
		Subject: fmt.Sprintf("New release %s for %s", p.ReleaseTag, p.RepoFullName),
		Html:    htmlBuf.String(),
		Text:    txtBuf.String(),
	})
	if err != nil {
		return fmt.Errorf("send release email: %w", err)
	}
	return nil
}
