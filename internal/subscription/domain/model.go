package domain

import "time"

// --- Entities ---

type GitHubRepo struct {
	ID            int64
	Owner         string
	Name          string
	LastSeenTag   *string
	LastCheckedAt *time.Time
	CreatedAt     time.Time
}

type Subscription struct {
	ID                    int64
	Email                 string
	RepositoryID          int64
	ConfirmedAt           *time.Time
	ConfirmToken          *string
	ConfirmTokenExpiresAt *time.Time
	UnsubscribeToken      string
	CreatedAt             time.Time
}

type SubscriptionView struct {
	ID          int64
	RepoOwner   string
	RepoName    string
	ConfirmedAt *time.Time
	CreatedAt   time.Time
}

// --- Params (one per cross-package call with > 2 args) ---

type SubscribeParams struct {
	Email      string
	Repository string
}

type RepoExistsParams struct {
	Owner string
	Name  string
}

type UpsertRepoParams struct {
	Owner string
	Name  string
}

type CreateSubscriptionParams struct {
	Email                 string
	RepositoryID          int64
	ConfirmToken          string
	ConfirmTokenExpiresAt time.Time
	UnsubscribeToken      string
}

type MarkConfirmedParams struct {
	ID  int64
	Now time.Time
}

type SendConfirmationParams struct {
	To           string
	ConfirmURL   string
	RepoFullName string
}

type SendReleaseParams struct {
	To             string
	RepoFullName   string
	ReleaseTag     string
	ReleaseURL     string
	UnsubscribeURL string
}
