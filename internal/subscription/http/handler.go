package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/mail"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/go-chi/chi/v5"
)

type SubscriptionService interface {
	Subscribe(ctx context.Context, p domain.SubscribeParams) error
	Confirm(ctx context.Context, token string) error
	Unsubscribe(ctx context.Context, token string) error
}

type Handler struct {
	svc        SubscriptionService
	writeError func(w http.ResponseWriter, r *http.Request, err error)
}

func NewHandler(svc SubscriptionService, writeError func(w http.ResponseWriter, r *http.Request, err error)) *Handler {
	return &Handler{svc: svc, writeError: writeError}
}

func (h *Handler) Register(r chi.Router) {
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
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "confirmed"})
}

func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := h.svc.Unsubscribe(r.Context(), token); err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "unsubscribed"})
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
