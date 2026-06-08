package app

import (
	"fmt"

	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/config"
	"github.com/ananaslegend/reposeetory/internal/notifier/emailer"
	"github.com/ananaslegend/reposeetory/internal/observability/emailermetrics"
	"github.com/ananaslegend/reposeetory/internal/observability/redmetrics"
)

func newEmailer(cfg config.Config, log zerolog.Logger, red *redmetrics.RED) (emailer.Emailer, error) {
	switch {
	case cfg.ResendAPIKey != "":
		log.Info().Msg("mailer: resend")
		return emailermetrics.Wrap(emailer.NewResendMailer(cfg.ResendAPIKey, cfg.ResendFrom), "resend", red), nil

	case cfg.SMTPHost != "":
		mailer, err := emailer.NewSMTPMailer(emailer.SMTPMailerConfig{
			Host:      cfg.SMTPHost,
			Port:      cfg.SMTPPort,
			User:      cfg.SMTPUser,
			Password:  cfg.SMTPPass,
			From:      cfg.SMTPFrom,
			TLSPolicy: cfg.SMTPTLSPolicy,
		})
		if err != nil {
			return nil, fmt.Errorf("app.newEmailer: emailer.NewSMTPMailer: %w", err)
		}
		log.Info().Msg("mailer: smtp")
		return emailermetrics.Wrap(mailer, "smtp", red), nil

	default:
		log.Info().Msg("mailer: stub")
		return emailermetrics.Wrap(emailer.NewStubMailer(), "stub", red), nil
	}
}
