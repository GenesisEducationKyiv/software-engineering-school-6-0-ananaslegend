package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"

	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
)

func NewRouter(log zerolog.Logger, subHandler *subhttp.Handler) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(RequestLogger(log))

	subHandler.Register(r)

	return r
}
