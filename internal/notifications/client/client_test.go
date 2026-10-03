package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/notifications/client"
	"github.com/ananaslegend/reposeetory/internal/notifications/contract"
)

func TestSendRelease_2xx_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/notifications/release", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := client.New(srv.URL, srv.Client())
	require.NoError(t, c.SendRelease(context.Background(), contract.SendReleaseRequest{To: "u@example.com"}))
}

func TestSendRelease_4xx_Permanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := client.New(srv.URL, srv.Client())
	err := c.SendRelease(context.Background(), contract.SendReleaseRequest{To: "u@example.com"})
	require.Error(t, err)
	require.True(t, errors.Is(err, contract.ErrPermanent))
}

func TestSendRelease_5xx_Transient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := client.New(srv.URL, srv.Client())
	err := c.SendRelease(context.Background(), contract.SendReleaseRequest{To: "u@example.com"})
	require.Error(t, err)
	require.False(t, errors.Is(err, contract.ErrPermanent))
}

func TestSendConfirmation_2xx_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/notifications/confirmation", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := client.New(srv.URL, srv.Client())
	require.NoError(t, c.SendConfirmation(context.Background(), contract.SendConfirmationRequest{To: "u@example.com"}))
}
