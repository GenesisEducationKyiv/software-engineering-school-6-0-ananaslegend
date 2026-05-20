package app

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/config"
	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	"github.com/ananaslegend/reposeetory/internal/httpapi"
	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
	"github.com/ananaslegend/reposeetory/internal/subscription/repository"
	"github.com/ananaslegend/reposeetory/internal/subscription/service"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
)

func newHTTPServer(cfg config.Config, pool *pgxpool.Pool, txr transactor.Transactor, log zerolog.Logger, reg *prometheus.Registry) *http.Server {
	repo := repository.New(pool)
	svc := service.New(service.Config{
		Tx:              txr,
		Repo:            repo,
		Confirms:        repo,
		GitHub:          githubclient.NewClient(cfg.GitHubToken),
		AppBaseURL:      cfg.AppBaseURL,
		ConfirmTokenTTL: cfg.ConfirmTokenTTL,
		Registry:        reg,
	})

	subHandler := subhttp.NewHandler(svc)
	router := httpapi.NewRouter(httpapi.RouterConfig{
		Log:        log,
		SubHandler: subHandler,
		Registry:   reg,
	})

	return &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      router,
		ReadTimeout:  cfg.HTTPReadTimeout,
		WriteTimeout: cfg.HTTPWriteTimeout,
	}
}
