package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
)

// RequestLogger injects a request-scoped zerolog logger into the context
// and logs the completed request with status, duration, and size.
func RequestLogger(base zerolog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := middleware.GetReqID(r.Context())
			log := base.With().
				Str("request_id", reqID).
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Logger()

			r = r.WithContext(log.WithContext(r.Context()))

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)

			log.Info().
				Int("status", ww.Status()).
				Int("bytes", ww.BytesWritten()).
				Dur("duration", time.Since(start)).
				Msg("request")
		})
	}
}
