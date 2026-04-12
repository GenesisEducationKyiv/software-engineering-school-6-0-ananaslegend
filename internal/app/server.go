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
)

func newHTTPServer(cfg config.Config, pool *pgxpool.Pool, log zerolog.Logger, reg *prometheus.Registry) *http.Server {
	svc := service.New(service.Config{
		Repo:            repository.New(pool),
		GitHub:          githubclient.NewStubClient(),
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
