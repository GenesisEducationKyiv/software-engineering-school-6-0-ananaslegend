//go:generate swag init -g cmd/api/main.go -o docs --parseDependency

// @title			Reposeetory API
// @version			1.0
// @description		GitHub Release Notification API. Subscribe to repositories and get email notifications on new releases.
// @contact.name	ananaslegend
// @contact.url		https://github.com/ananaslegend/reposeetory
// @license.name	MIT
// @host			localhost:8080
// @BasePath		/
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/config"
	"github.com/ananaslegend/reposeetory/internal/confirmer"
	confirmerrepo "github.com/ananaslegend/reposeetory/internal/confirmer/repository"
	"github.com/ananaslegend/reposeetory/internal/httpapi"
	"github.com/ananaslegend/reposeetory/internal/logger"
	"github.com/ananaslegend/reposeetory/internal/notifier"
	"github.com/ananaslegend/reposeetory/internal/notifier/emailer"
	notifierrepo "github.com/ananaslegend/reposeetory/internal/notifier/repository"
	"github.com/ananaslegend/reposeetory/internal/scanner"
	scannerrepo "github.com/ananaslegend/reposeetory/internal/scanner/repository"
	"github.com/ananaslegend/reposeetory/internal/storage/postgres"
	redisstorage "github.com/ananaslegend/reposeetory/internal/storage/redis"
	"github.com/ananaslegend/reposeetory/internal/subscription/repository"
	"github.com/ananaslegend/reposeetory/internal/subscription/service"
	dbmigrations "github.com/ananaslegend/reposeetory/migrations"

	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
)

type fullMailer interface {
	notifier.MailSender
	confirmer.MailSender
}

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

	log.Debug().Str("scanner_interval", cfg.ScannerInterval.String()).Str("notifier_interval", cfg.NotifierInterval.String()).Str("confirm_interval", cfg.ConfirmerInterval.String()).Send()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := runMigrations(cfg.DatabaseURL, log); err != nil {
		log.Fatal().Err(err).Msg("run migrations")
	}

	pool, err := postgres.NewPool(ctx, postgres.Config{
		URL:      cfg.DatabaseURL,
		MaxConns: cfg.DBMaxConns,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("connect to database")
	}
	defer pool.Close()

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
		postgres.NewPoolCollector(pool),
	)

	var mailSender fullMailer
	switch {
	case cfg.ResendAPIKey != "":
		mailSender = emailer.NewResendMailer(cfg.ResendAPIKey, cfg.ResendFrom)
		log.Info().Msg("mailer: resend")
	case cfg.SMTPHost != "":
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
		log.Info().Msg("mailer: smtp")
	default:
		mailSender = emailer.NewStubMailer()
		log.Info().Msg("mailer: stub")
	}

	githubClient := githubclient.NewClient(cfg.GitHubToken)

	var releaseProvider scanner.ReleaseProvider = githubClient
	if cfg.RedisURL != "" {
		rdb, err := redisstorage.NewClient(redisstorage.Config{URL: cfg.RedisURL})
		if err != nil {
			log.Warn().Err(err).Msg("redis unavailable, github caching disabled")
		} else {
			defer rdb.Close()
			releaseProvider = githubclient.NewCachingClient(githubclient.CachingConfig{
				Provider: githubClient,
				RDB:      rdb,
				TTL:      10 * time.Minute,
				Registry: reg,
			})
			log.Info().Msg("github release cache: redis")
		}
	} else {
		log.Info().Msg("github release cache: disabled")
	}

	scan := scanner.New(scanner.Config{
		Repo:     scannerrepo.New(pool),
		GitHub:   releaseProvider,
		Interval: cfg.ScannerInterval,
		Registry: reg,
	})

	notify := notifier.New(notifier.Config{
		Repo:     notifierrepo.New(pool),
		Mailer:   mailSender,
		Interval: cfg.NotifierInterval,
		BaseURL:  cfg.AppBaseURL,
		Registry: reg,
	})

	go scan.Run(ctx)
	go notify.Run(ctx)

	confirm := confirmer.New(confirmer.Config{
		Repo:     confirmerrepo.New(pool),
		Mailer:   mailSender,
		Interval: cfg.ConfirmerInterval,
		BaseURL:  cfg.AppBaseURL,
		Registry: reg,
	})
	go confirm.Run(ctx)

	svc := service.New(service.Config{
		Repo:            repository.New(pool),
		GitHub:          githubclient.NewStubClient(),
		AppBaseURL:      cfg.AppBaseURL,
		ConfirmTokenTTL: cfg.ConfirmTokenTTL,
		Registry:        reg,
	})
	subHandler := subhttp.NewHandler(svc, httpapi.WriteError)
	router := httpapi.NewRouter(httpapi.RouterConfig{Log: log, SubHandler: subHandler, Registry: reg})

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

func runMigrations(databaseURL string, log zerolog.Logger) error {
	src, err := iofs.New(dbmigrations.FS, ".")
	if err != nil {
		return err
	}
	pgx5URL := "pgx5://" + strings.TrimPrefix(strings.TrimPrefix(databaseURL, "postgres://"), "postgresql://")
	m, err := migrate.NewWithSourceInstance("iofs", src, pgx5URL)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	log.Info().Msg("migrations applied")
	return nil
}
