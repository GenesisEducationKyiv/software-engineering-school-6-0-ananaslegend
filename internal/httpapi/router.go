package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	httpswagger "github.com/swaggo/http-swagger/v2"

	_ "github.com/ananaslegend/reposeetory/docs"
	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
)

func NewRouter(log zerolog.Logger, subHandler *subhttp.Handler) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(RequestLogger(log))

	r.Get("/", subHandler.Landing)
	r.Get("/subscribed", subHandler.Subscribed)

	r.Post("/api/subscribe", subHandler.Subscribe)
	r.Get("/api/confirm/{token}", subHandler.Confirm)
	r.Get("/api/unsubscribe/{token}", subHandler.Unsubscribe)

	r.Get("/swagger/*", httpswagger.Handler())

	return r
}
