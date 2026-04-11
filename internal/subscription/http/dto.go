package http

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
