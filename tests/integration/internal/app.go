//go:build integration

package internal

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	"github.com/ananaslegend/reposeetory/internal/httpapi"
	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
	subrepo "github.com/ananaslegend/reposeetory/internal/subscription/repository"
	"github.com/ananaslegend/reposeetory/internal/subscription/service"
)

// AppConfig holds the knobs callers normally control. Sensible defaults are
// applied when fields are zero.
type AppConfig struct {
	Pool            *pgxpool.Pool
	GitHubBaseURL   string
	GitHubToken     string
	AppBaseURL      string
	ConfirmTokenTTL time.Duration
}

// App is the wired test instance: an httptest.Server in front of the real
// chi router, sharing a single Prometheus Registry across handler, service,
// and github client.
type App struct {
	Pool     *pgxpool.Pool
	Registry *prometheus.Registry
	Server   *httptest.Server
	Client   *http.Client
}

// NewApp builds the subscription HTTP stack and wraps it in an httptest.Server.
// The server is closed automatically when the calling test finishes.
//
// Cron workers (scanner, notifier, confirmer) are intentionally NOT started —
// callers verify outbox-table state directly.
func NewApp(t testing.TB, cfg AppConfig) *App {
	t.Helper()

	if cfg.AppBaseURL == "" {
		cfg.AppBaseURL = "http://test.local"
	}
	if cfg.ConfirmTokenTTL == 0 {
		cfg.ConfirmTokenTTL = 24 * time.Hour
	}
	if cfg.GitHubToken == "" {
		cfg.GitHubToken = "test-token"
	}

	registry := prometheus.NewRegistry()

	gh := githubclient.New(githubclient.Config{
		Token:   cfg.GitHubToken,
		RESTURL: cfg.GitHubBaseURL,
	})

	repo := subrepo.New(cfg.Pool)

	svc := service.New(service.Config{
		Repo:            repo,
		GitHub:          gh,
		AppBaseURL:      cfg.AppBaseURL,
		ConfirmTokenTTL: cfg.ConfirmTokenTTL,
		Registry:        registry,
	})

	handler := subhttp.NewHandler(svc)

	router := httpapi.NewRouter(httpapi.RouterConfig{
		Log:        zerolog.Nop(),
		SubHandler: handler,
		Registry:   registry,
	})

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return &App{
		Pool:     cfg.Pool,
		Registry: registry,
		Server:   server,
		Client:   server.Client(),
	}
}
