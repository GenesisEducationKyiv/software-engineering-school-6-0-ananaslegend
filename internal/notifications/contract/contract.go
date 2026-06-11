package contract

import "errors"

type SendReleaseRequest struct {
	To             string `json:"to"`
	RepoFullName   string `json:"repo_full_name"`
	ReleaseTag     string `json:"release_tag"`
	ReleaseURL     string `json:"release_url"`
	UnsubscribeURL string `json:"unsubscribe_url"`
}

type SendConfirmationRequest struct {
	To           string `json:"to"`
	ConfirmURL   string `json:"confirm_url"`
	RepoFullName string `json:"repo_full_name"`
}

// ErrPermanent classifies a notification failure as non-retryable (HTTP 4xx):
// the request itself is malformed, so re-sending will never succeed.
// Drainers drop the outbox row instead of retrying. Transient failures
// (5xx / network / timeout) are returned without this sentinel.
var ErrPermanent = errors.New("notifications: permanent failure")
