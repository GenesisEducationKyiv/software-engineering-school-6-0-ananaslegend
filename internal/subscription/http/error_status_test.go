package http

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

func TestErrorStatus_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"invalid repo format", domain.ErrInvalidRepoFormat, http.StatusBadRequest},
		{"bad-request error type", errBadRequest("bad email"), http.StatusBadRequest},
		{"repo not found", domain.ErrRepoNotFound, http.StatusNotFound},
		{"token not found", domain.ErrTokenNotFound, http.StatusNotFound},
		{"already exists", domain.ErrAlreadyExists, http.StatusConflict},
		{"token expired", domain.ErrTokenExpired, http.StatusGone},
		{"unrelated error", errors.New("kaboom"), http.StatusInternalServerError},
		{"wrapped sentinel still matches", wrap(domain.ErrTokenExpired), http.StatusGone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, errorStatus(tc.err))
		})
	}
}

// wrap simulates a layer adding context via fmt.Errorf("...: %w", err) so
// the test verifies errors.Is still finds the sentinel.
func wrap(err error) error {
	return &wrappedErr{cause: err}
}

type wrappedErr struct{ cause error }

func (w *wrappedErr) Error() string { return "wrapped: " + w.cause.Error() }
func (w *wrappedErr) Unwrap() error { return w.cause }
