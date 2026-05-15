//go:build integration || e2e

package internal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/confirmer"
	confirmerrepo "github.com/ananaslegend/reposeetory/internal/confirmer/repository"
	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	"github.com/ananaslegend/reposeetory/internal/httpapi"
	"github.com/ananaslegend/reposeetory/internal/notifier"
	"github.com/ananaslegend/reposeetory/internal/notifier/emailer"
	notifierrepo "github.com/ananaslegend/reposeetory/internal/notifier/repository"
	"github.com/ananaslegend/reposeetory/internal/scanner"
	scannerrepo "github.com/ananaslegend/reposeetory/internal/scanner/repository"
	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
	subrepo "github.com/ananaslegend/reposeetory/internal/subscription/repository"
	"github.com/ananaslegend/reposeetory/internal/subscription/service"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
	"github.com/stretchr/testify/require"
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

// E2EAppConfig configures a full topology App used by tests/e2e/.
type E2EAppConfig struct {
	Pool            *pgxpool.Pool
	Mailpit         *Mailpit
	GitHubRESTURL   string
	GitHubGraphURL  string
	AppBaseURL      string        // optional; defaults to httptest server URL
	ConfirmTokenTTL time.Duration // default 24h
	ScannerTick     time.Duration // default 50ms
	DrainerTick     time.Duration // default 50ms (confirmer + notifier)
}

// E2EApp is the full wired stack: HTTP server + scanner/notifier/confirmer
// goroutines + SMTP mailer pointed at Mailpit. Workers are stopped via
// t.Cleanup.
type E2EApp struct {
	*App
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewE2EApp builds the HTTP stack via NewApp, wires the SMTP mailer
// against cfg.Mailpit, and starts scanner+notifier+confirmer goroutines.
//
// BaseURL strategy (option d): an httptest.Server is spun up with a nil
// handler first so the OS assigns its port. The subscription service is then
// built with that URL, and the finished router is installed via
// server.Config.Handler before any request can arrive.
func NewE2EApp(t testing.TB, cfg E2EAppConfig) *E2EApp {
	t.Helper()

	if cfg.ConfirmTokenTTL == 0 {
		cfg.ConfirmTokenTTL = 24 * time.Hour
	}
	if cfg.ScannerTick == 0 {
		cfg.ScannerTick = 50 * time.Millisecond
	}
	if cfg.DrainerTick == 0 {
		cfg.DrainerTick = 50 * time.Millisecond
	}

	// Step 1: spin up the httptest.Server with a nil handler so we know its URL.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	baseURL := cfg.AppBaseURL
	if baseURL == "" {
		baseURL = server.URL
	}

	// Step 2: build all components with the now-known baseURL.
	registry := prometheus.NewRegistry()

	gh := githubclient.New(githubclient.Config{
		Token:      "test-token",
		RESTURL:    cfg.GitHubRESTURL,
		GraphQLURL: cfg.GitHubGraphURL,
	})

	subRepo := subrepo.New(cfg.Pool)

	svc := service.New(service.Config{
		Repo:            subRepo,
		GitHub:          gh,
		AppBaseURL:      baseURL,
		ConfirmTokenTTL: cfg.ConfirmTokenTTL,
		Registry:        registry,
	})

	handler := subhttp.NewHandler(svc)

	router := httpapi.NewRouter(httpapi.RouterConfig{
		Log:        zerolog.Nop(),
		SubHandler: handler,
		Registry:   registry,
	})

	// Step 3: swap in the real handler — the server is already listening.
	server.Config.Handler = router

	// Step 4: build SMTP mailer pointed at Mailpit.
	mailer, err := emailer.NewSMTPMailer(emailer.SMTPMailerConfig{
		Host:      cfg.Mailpit.SMTPHost,
		Port:      cfg.Mailpit.SMTPPort,
		From:      "noreply@test.local",
		TLSPolicy: "none",
	})
	require.NoError(t, err, "create smtp mailer for e2e")

	// Step 5: build transactor and worker repos.
	txr := transactor.New(cfg.Pool)

	scan := scanner.New(scanner.Config{
		Tx:       txr,
		Repo:     scannerrepo.New(cfg.Pool),
		GitHub:   gh,
		Interval: cfg.ScannerTick,
		Registry: registry,
	})

	notify := notifier.New(notifier.Config{
		Tx:       txr,
		Repo:     notifierrepo.New(cfg.Pool),
		Mailer:   mailer,
		Interval: cfg.DrainerTick,
		BaseURL:  baseURL,
		Registry: registry,
	})

	confirm := confirmer.New(confirmer.Config{
		Tx:       txr,
		Repo:     confirmerrepo.New(cfg.Pool),
		Mailer:   mailer,
		Interval: cfg.DrainerTick,
		BaseURL:  baseURL,
		Registry: registry,
	})

	// Step 6: start goroutines; stop them when the test ends.
	ctx, cancel := context.WithCancel(context.Background())

	app := &E2EApp{
		App: &App{
			Pool:     cfg.Pool,
			Registry: registry,
			Server:   server,
			Client:   server.Client(),
		},
		cancel: cancel,
	}

	app.wg.Add(3)
	go func() { defer app.wg.Done(); scan.Run(ctx) }()
	go func() { defer app.wg.Done(); notify.Run(ctx) }()
	go func() { defer app.wg.Done(); confirm.Run(ctx) }()

	t.Cleanup(func() {
		cancel()
		app.wg.Wait()
	})

	return app
}
