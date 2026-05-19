package emailer

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

type StubMailer struct{}

func NewStubMailer() *StubMailer {
	return &StubMailer{}
}

func (s *StubMailer) SendConfirmation(ctx context.Context, p domain.SendConfirmationParams) error {
	zerolog.Ctx(ctx).Info().
		Str("to", p.To).
		Str("confirm_url", p.ConfirmURL).
		Msg("mailer stub: would send confirmation email")
	return nil
}

func (s *StubMailer) SendRelease(ctx context.Context, p domain.SendReleaseParams) error {
	zerolog.Ctx(ctx).Info().
		Str("to", p.To).
		Str("repo", p.RepoFullName).
		Str("tag", p.ReleaseTag).
		Str("release_url", p.ReleaseURL).
		Msg("mailer stub: would send release notification email")
	return nil
}
