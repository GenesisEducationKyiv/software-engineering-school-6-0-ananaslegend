package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/ananaslegend/reposeetory/internal/config"
	"github.com/ananaslegend/reposeetory/internal/httpapi"
	"github.com/ananaslegend/reposeetory/internal/logger"
	"github.com/ananaslegend/reposeetory/internal/notifier"
	"github.com/ananaslegend/reposeetory/internal/notifier/emailer"
	notifierrepo "github.com/ananaslegend/reposeetory/internal/notifier/repository"
	"github.com/ananaslegend/reposeetory/internal/scanner"
	scannerrepo "github.com/ananaslegend/reposeetory/internal/scanner/repository"
	"github.com/ananaslegend/reposeetory/internal/storage/postgres"
	"github.com/ananaslegend/reposeetory/internal/subscription/repository"
	"github.com/ananaslegend/reposeetory/internal/subscription/service"
	"github.com/rs/zerolog"

	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		l := zerolog.New(os.Stderr)
		l.Fatal().Err(err).Msg("load config")
	}

	log := logger.New(logger.Config{
		Level:  cfg.LogLevel,
		Pretty: cfg.LogPretty,
	})

	log.Info().Str("scanner_interval", cfg.ScannerInterval.String()).Str("notifier_interval", cfg.NotifierInterval.String()).Send()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, postgres.Config{
		URL:      cfg.DatabaseURL,
		MaxConns: cfg.DBMaxConns,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("connect to database")
	}
	defer pool.Close()

	var mailSender service.MailSender
	if cfg.SMTPHost != "" {
		smtpMailer, err := emailer.NewSMTPMailer(emailer.SMTPMailerConfig{
			Host:      cfg.SMTPHost,
			Port:      cfg.SMTPPort,
			User:      cfg.SMTPUser,
			Password:  cfg.SMTPPass,
			From:      cfg.SMTPFrom,
			TLSPolicy: cfg.SMTPTLSPolicy,
		})
		if err != nil {
			log.Fatal().Err(err).Msg("create smtp mailer")
		}
		mailSender = smtpMailer
	} else {
		mailSender = emailer.NewStubMailer()
	}

	githubClient := githubclient.NewClient(cfg.GitHubToken)

	scan := scanner.New(scanner.Config{
		Repo:     scannerrepo.New(pool),
		GitHub:   githubClient,
		Interval: cfg.ScannerInterval,
	})

	notify := notifier.New(notifier.Config{
		Repo:     notifierrepo.New(pool),
		Mailer:   mailSender,
		Interval: cfg.NotifierInterval,
	})

	go scan.Run(ctx)
	go notify.Run(ctx)

	svc := service.New(service.Config{
		Repo:            repository.New(pool),
		GitHub:          githubclient.NewStubClient(),
		Mailer:          mailSender,
		AppBaseURL:      cfg.AppBaseURL,
		ConfirmTokenTTL: cfg.ConfirmTokenTTL,
	})
	subHandler := subhttp.NewHandler(svc, httpapi.WriteError)
	router := httpapi.NewRouter(log, subHandler)

	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      router,
		ReadTimeout:  cfg.HTTPReadTimeout,
		WriteTimeout: cfg.HTTPWriteTimeout,
	}

	go func() {
		log.Info().Str("addr", cfg.HTTPAddr).Msg("server listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("http server error")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTPShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("graceful shutdown failed")
	}
	log.Info().Msg("shutdown complete")
}
