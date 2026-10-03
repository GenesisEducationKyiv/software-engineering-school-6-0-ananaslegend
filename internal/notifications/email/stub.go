package email

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/notifications/contract"
)

type StubMailer struct{}

func NewStubMailer() *StubMailer {
	return &StubMailer{}
}

func (s *StubMailer) SendConfirmation(ctx context.Context, p contract.SendConfirmationRequest) error {
	zerolog.Ctx(ctx).Info().
		Str("to", p.To).
		Str("confirm_url", p.ConfirmURL).
		Msg("mailer stub: would send confirmation email")
	return nil
}

func (s *StubMailer) SendRelease(ctx context.Context, p contract.SendReleaseRequest) error {
	zerolog.Ctx(ctx).Info().
		Str("to", p.To).
		Str("repo", p.RepoFullName).
		Str("tag", p.ReleaseTag).
		Str("release_url", p.ReleaseURL).
		Msg("mailer stub: would send release notification email")
	return nil
}
