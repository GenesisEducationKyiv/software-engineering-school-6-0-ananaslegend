package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	httpswagger "github.com/swaggo/http-swagger/v2"

	_ "github.com/ananaslegend/reposeetory/docs"
	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
)

// RouterConfig holds all dependencies for the HTTP router.
type RouterConfig struct {
	Log        zerolog.Logger
	SubHandler *subhttp.Handler
	Registry   *prometheus.Registry
}

func NewRouter(cfg RouterConfig) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(RequestLogger(cfg.Log))
	r.Use(PrometheusMiddleware(cfg.Registry))

	r.Get("/", cfg.SubHandler.Landing)
	r.Get("/subscribed", cfg.SubHandler.Subscribed)

	r.Post("/api/subscribe", cfg.SubHandler.Subscribe)
	r.Get("/api/subscriptions", cfg.SubHandler.ListByEmail)
	r.Get("/api/confirm/{token}", cfg.SubHandler.Confirm)
	r.Get("/api/unsubscribe/{token}", cfg.SubHandler.Unsubscribe)

	r.Get("/swagger/*", httpswagger.Handler())

	if cfg.Registry != nil {
		r.Handle("/metrics", promhttp.HandlerFor(cfg.Registry, promhttp.HandlerOpts{}))
	}

	return r
}
