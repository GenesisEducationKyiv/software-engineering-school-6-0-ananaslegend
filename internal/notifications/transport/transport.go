package transport

//go:generate mockgen -source=transport.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/notifications/contract"
)

type Sender interface {
	SendConfirmation(ctx context.Context, p contract.SendConfirmationRequest) error
	SendRelease(ctx context.Context, p contract.SendReleaseRequest) error
}

type Config struct {
	Sender   Sender
	Registry *prometheus.Registry
}

type Handler struct {
	sender Sender
	sent   *prometheus.CounterVec
}

// NewHandler builds a Handler. If Registry is nil the metric is created
// but not registered (test-friendly, matching the project convention).
func NewHandler(cfg Config) *Handler {
	sent := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "notifications_sent_total",
		Help: "Total notifications attempted by the notifications service.",
	}, []string{"type", "result"})
	if cfg.Registry != nil {
		cfg.Registry.MustRegister(sent)
	}
	return &Handler{sender: cfg.Sender, sent: sent}
}

// SendRelease handles POST /v1/notifications/release.
func (h *Handler) SendRelease(w http.ResponseWriter, r *http.Request) {
	var req contract.SendReleaseRequest
	if err := decode(r, &req); err != nil || req.To == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := h.sender.SendRelease(r.Context(), req); err != nil {
		h.sent.WithLabelValues("release", "error").Inc()
		zerolog.Ctx(r.Context()).Error().Err(err).Msg("notifications: send release failed")
		http.Error(w, "delivery failed", http.StatusBadGateway)
		return
	}
	h.sent.WithLabelValues("release", "ok").Inc()
	w.WriteHeader(http.StatusNoContent)
}

// SendConfirmation handles POST /v1/notifications/confirmation.
func (h *Handler) SendConfirmation(w http.ResponseWriter, r *http.Request) {
	var req contract.SendConfirmationRequest
	if err := decode(r, &req); err != nil || req.To == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := h.sender.SendConfirmation(r.Context(), req); err != nil {
		h.sent.WithLabelValues("confirmation", "error").Inc()
		zerolog.Ctx(r.Context()).Error().Err(err).Msg("notifications: send confirmation failed")
		http.Error(w, "delivery failed", http.StatusBadGateway)
		return
	}
	h.sent.WithLabelValues("confirmation", "ok").Inc()
	w.WriteHeader(http.StatusNoContent)
}

func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err //nolint:wrapcheck // boundary decode; caller maps to 400
	}
	return nil
}
