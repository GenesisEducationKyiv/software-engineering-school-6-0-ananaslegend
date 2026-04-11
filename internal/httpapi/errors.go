package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
	"github.com/rs/zerolog"
)

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	status := statusFor(err)
	if status == http.StatusInternalServerError {
		zerolog.Ctx(r.Context()).Error().Err(err).Msg("internal server error")
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(subhttp.ErrorResponse{Error: userMessage(err, status)})
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, domain.ErrInvalidRepoFormat):
		return http.StatusBadRequest
	case isBadRequest(err):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrRepoNotFound), errors.Is(err, domain.ErrTokenNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrAlreadyExists):
		return http.StatusConflict
	case errors.Is(err, domain.ErrTokenExpired):
		return http.StatusGone
	default:
		return http.StatusInternalServerError
	}
}

func isBadRequest(err error) bool {
	var e *subhttp.BadRequestError
	return errors.As(err, &e)
}

func userMessage(err error, status int) string {
	if status == http.StatusInternalServerError {
		return "internal server error"
	}
	return err.Error()
}
