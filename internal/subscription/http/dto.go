package http

import "time"

// SubscribeRequest is the body for POST /api/subscribe.
type SubscribeRequest struct {
	Email      string `json:"email"      example:"user@example.com"`
	Repository string `json:"repository" example:"ananaslegend/reposeetory"`
}

// StatusResponse is returned on successful subscription.
type StatusResponse struct {
	Status string `json:"status" example:"pending_confirmation"`
}

// ErrorResponse is returned on all API errors.
type ErrorResponse struct {
	Error string `json:"error" example:"invalid email"`
}

// SubscriptionItem represents a single active subscription.
type SubscriptionItem struct {
	Repository  string     `json:"repository"   example:"ananaslegend/reposeetory"`
	ConfirmedAt *time.Time `json:"confirmed_at" example:"2024-01-01T00:00:00Z"`
	CreatedAt   time.Time  `json:"created_at"   example:"2024-01-01T00:00:00Z"`
}

// SubscriptionsResponse is returned by GET /api/subscriptions.
type SubscriptionsResponse struct {
	Subscriptions []SubscriptionItem `json:"subscriptions"`
}
