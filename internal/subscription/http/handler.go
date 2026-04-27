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
	ListByEmail(ctx context.Context, email string) ([]domain.SubscriptionView, error)
}

type Handler struct {
	svc   SubscriptionService
	pages pages.Renderer
}

func NewHandler(svc SubscriptionService) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status := errorStatus(err)
	if status == http.StatusInternalServerError {
		zerolog.Ctx(r.Context()).Error().Err(err).Msg("internal server error")
	}
	msg := err.Error()
	if status == http.StatusInternalServerError {
		msg = "internal server error"
	}
	writeJSON(w, status, ErrorResponse{Error: msg})
}

func errorStatus(err error) int {
	switch {
	case errors.Is(err, domain.ErrInvalidRepoFormat):
		return http.StatusBadRequest
	case errors.As(err, new(*BadRequestError)):
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

// Subscribe godoc
//
//	@Summary		Subscribe to a repository
//	@Description	Subscribe to GitHub repository release notifications. A confirmation email will be sent.
//	@Tags			subscriptions
//	@Accept			json
//	@Produce		json
//	@Param			body	body		SubscribeRequest	true	"Subscription request"
//	@Success		202		{object}	StatusResponse
//	@Failure		400		{object}	ErrorResponse
//	@Failure		409		{object}	ErrorResponse	"already subscribed"
//	@Failure		500		{object}	ErrorResponse
//	@Router			/api/subscribe [post]
func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	var req SubscribeRequest
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

	writeJSON(w, http.StatusAccepted, StatusResponse{Status: "pending_confirmation"})
}

// Confirm godoc
//
//	@Summary		Confirm subscription
//	@Description	Confirms a subscription using the one-time token from the confirmation email (valid 24h).
//	@Tags			subscriptions
//	@Produce		html
//	@Param			token	path	string	true	"Confirmation token"
//	@Success		200
//	@Failure		404	"token not found"
//	@Failure		410	"token expired"
//	@Router			/api/confirm/{token} [get]
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

// Unsubscribe godoc
//
//	@Summary		Unsubscribe
//	@Description	Unsubscribes and permanently removes all subscription data (GDPR hard delete).
//	@Tags			subscriptions
//	@Produce		html
//	@Param			token	path	string	true	"Unsubscribe token"
//	@Success		200
//	@Failure		404	"token not found"
//	@Router			/api/unsubscribe/{token} [get]
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

// Landing godoc
//
//	@Summary		Landing page
//	@Description	Subscription form (HTML).
//	@Tags			pages
//	@Produce		html
//	@Success		200
//	@Router			/ [get]
func (h *Handler) Landing(w http.ResponseWriter, r *http.Request) {
	h.pages.Landing(w)
}

// Subscribed godoc
//
//	@Summary		Subscribed success page
//	@Description	Shown after a successful subscription form submission (HTML).
//	@Tags			pages
//	@Produce		html
//	@Success		200
//	@Router			/subscribed [get]
func (h *Handler) Subscribed(w http.ResponseWriter, r *http.Request) {
	h.pages.Subscribed(w)
}

// ListByEmail godoc
//
//	@Summary		List active subscriptions
//	@Description	Returns all confirmed subscriptions for a given email address.
//	@Tags			subscriptions
//	@Produce		json
//	@Param			email	query		string				true	"Email address"	example:"user@example.com"
//	@Success		200		{object}	SubscriptionsResponse
//	@Failure		400		{object}	ErrorResponse
//	@Failure		500		{object}	ErrorResponse
//	@Router			/api/subscriptions [get]
func (h *Handler) ListByEmail(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	if _, err := mail.ParseAddress(email); err != nil || email == "" {
		h.writeError(w, r, errBadRequest("invalid email"))
		return
	}

	subs, err := h.svc.ListByEmail(r.Context(), email)
	if err != nil {
		zerolog.Ctx(r.Context()).Error().Err(err).Msg("list subscriptions")
		h.writeError(w, r, err)
		return
	}

	items := make([]SubscriptionItem, len(subs))
	for i, s := range subs {
		items[i] = SubscriptionItem{
			Repository:  s.RepoOwner + "/" + s.RepoName,
			ConfirmedAt: s.ConfirmedAt,
			CreatedAt:   s.CreatedAt,
		}
	}
	writeJSON(w, http.StatusOK, SubscriptionsResponse{Subscriptions: items})
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
