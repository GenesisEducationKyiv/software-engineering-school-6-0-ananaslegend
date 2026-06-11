package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/httpapi"
	"github.com/ananaslegend/reposeetory/internal/notifications/email"
	"github.com/ananaslegend/reposeetory/internal/notifications/transport"
)

type svcConfig struct {
	HTTPAddr        string        `envconfig:"NOTIFICATIONS_HTTP_ADDR" default:":8081"`
	ShutdownTimeout time.Duration `envconfig:"HTTP_SHUTDOWN_TIMEOUT" default:"15s"`

	LogLevel  string `envconfig:"LOG_LEVEL" default:"info"`
	LogPretty bool   `envconfig:"LOG_PRETTY" default:"true"`

	SMTPHost      string `envconfig:"SMTP_HOST"`
	SMTPPort      int    `envconfig:"SMTP_PORT" default:"587"`
	SMTPUser      string `envconfig:"SMTP_USER"`
	SMTPPass      string `envconfig:"SMTP_PASS"`
	SMTPFrom      string `envconfig:"SMTP_FROM"`
	SMTPTLSPolicy string `envconfig:"SMTP_TLS_POLICY" default:"starttls"`

	ResendAPIKey string `envconfig:"RESEND_API_KEY"`
	ResendFrom   string `envconfig:"RESEND_FROM"`
}

func main() {
	_ = godotenv.Load()

	var cfg svcConfig
	if err := envconfig.Process("", &cfg); err != nil {
		l := zerolog.New(os.Stderr)
		l.Fatal().Err(err).Msg("load config")
	}

	log := newLogger(cfg)

	em, err := newSender(cfg, log)
	if err != nil {
		log.Fatal().Err(err).Msg("create sender")
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	h := transport.NewHandler(transport.Config{Sender: em, Registry: reg})

	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      newRouter(h, reg),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info().Str("addr", cfg.HTTPAddr).Msg("notifications service listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("http server error")
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("graceful shutdown failed")
	}
	log.Info().Msg("shutdown complete")
}

func newRouter(h *transport.Handler, reg *prometheus.Registry) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(httpapi.RequestLogger(zerolog.Nop()))
	r.Use(httpapi.PrometheusMiddleware(reg))

	r.Post("/v1/notifications/release", h.SendRelease)
	r.Post("/v1/notifications/confirmation", h.SendConfirmation)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	return r
}

func newLogger(cfg svcConfig) zerolog.Logger {
	var l zerolog.Logger
	if cfg.LogPretty {
		l = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	} else {
		l = zerolog.New(os.Stderr).With().Timestamp().Logger()
	}
	lvl, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		lvl = zerolog.InfoLevel
	}
	return l.Level(lvl)
}

func newSender(cfg svcConfig, log zerolog.Logger) (email.Sender, error) {
	switch {
	case cfg.ResendAPIKey != "":
		log.Info().Msg("sender: resend")
		return email.NewResendMailer(cfg.ResendAPIKey, cfg.ResendFrom), nil
	case cfg.SMTPHost != "":
		m, err := email.NewSMTPMailer(email.SMTPMailerConfig{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser,
			Password: cfg.SMTPPass, From: cfg.SMTPFrom, TLSPolicy: cfg.SMTPTLSPolicy,
		})
		if err != nil {
			return nil, fmt.Errorf("main.newSender: email.NewSMTPMailer: %w", err)
		}
		log.Info().Msg("sender: smtp")
		return m, nil
	default:
		log.Info().Msg("sender: stub")
		return email.NewStubMailer(), nil
	}
}
