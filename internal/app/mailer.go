package app

import (
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/config"
	"github.com/ananaslegend/reposeetory/internal/notifier/emailer"
)

func newEmailer(cfg config.Config, log zerolog.Logger) (emailer.Emailer, error) {
	switch {
	case cfg.ResendAPIKey != "":
		log.Info().Msg("mailer: resend")
		return emailer.NewResendMailer(cfg.ResendAPIKey, cfg.ResendFrom), nil

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
			return nil, err
		}
		log.Info().Msg("mailer: smtp")
		return mailer, nil

	default:
		log.Info().Msg("mailer: stub")
		return emailer.NewStubMailer(), nil
	}
}
