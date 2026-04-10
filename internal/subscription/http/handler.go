package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/internal/subscription/http/pages"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
)

type SubscriptionService interface {
	Subscribe(ctx context.Context, p domain.SubscribeParams) error
	Confirm(ctx context.Context, token string) error
	Unsubscribe(ctx context.Context, token string) error
}

type Handler struct {
	svc        SubscriptionService
	writeError func(w http.ResponseWriter, r *http.Request, err error)
	pages      pages.Renderer
}

func NewHandler(svc SubscriptionService, writeError func(w http.ResponseWriter, r *http.Request, err error)) *Handler {
	return &Handler{svc: svc, writeError: writeError}
}

func (h *Handler) Register(r chi.Router) {
	r.Get("/", h.Landing)
	r.Get("/subscribed", h.Subscribed)
	r.Post("/api/subscribe", h.Subscribe)
	r.Get("/api/confirm/{token}", h.Confirm)
	r.Get("/api/unsubscribe/{token}", h.Unsubscribe)
}

func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	var req subscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, errBadRequest("invalid JSON body"))
		return
	}

	if _, err := mail.ParseAddress(req.Email); err != nil || req.Email == "" {
		h.writeError(w, r, errBadRequest("invalid email"))
		return
	}

	if err := h.svc.Subscribe(r.Context(), domain.SubscribeParams{
		Email:      req.Email,
		Repository: req.Repository,
	}); err != nil {
		h.writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusAccepted, statusResponse{Status: "pending_confirmation"})
}

func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := h.svc.Confirm(r.Context(), token); err != nil {
		switch {
		case errors.Is(err, domain.ErrTokenNotFound):
			h.pages.Unavailable(w, http.StatusNotFound)
		case errors.Is(err, domain.ErrTokenExpired):
			h.pages.Unavailable(w, http.StatusGone)
		default:
			zerolog.Ctx(r.Context()).Error().Err(err).Msg("confirm subscription")
			h.pages.Oops(w, middleware.GetReqID(r.Context()))
		}
		return
	}
	h.pages.Confirmed(w)
}

func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := h.svc.Unsubscribe(r.Context(), token); err != nil {
		switch {
		case errors.Is(err, domain.ErrTokenNotFound):
			h.pages.Unavailable(w, http.StatusNotFound)
		default:
			zerolog.Ctx(r.Context()).Error().Err(err).Msg("unsubscribe")
			h.pages.Oops(w, middleware.GetReqID(r.Context()))
		}
		return
	}
	h.pages.Unsubscribed(w)
}

func (h *Handler) Landing(w http.ResponseWriter, r *http.Request) {
	h.pages.Landing(w)
}

func (h *Handler) Subscribed(w http.ResponseWriter, r *http.Request) {
	h.pages.Subscribed(w)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// BadRequestError is a sentinel for handler-level validation failures.
type BadRequestError struct{ msg string }

func (e *BadRequestError) Error() string { return e.msg }

func errBadRequest(msg string) error { return &BadRequestError{msg: msg} }
