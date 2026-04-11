package confirmer

//go:generate mockgen -source=confirmer.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"time"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/rs/zerolog"
)

// PendingConfirmation is one outbox row joined with subscription + repository data.
type PendingConfirmation struct {
	ID           int64
	Email        string
	ConfirmToken string
	RepoOwner    string
	RepoName     string
}

// Repository is the storage contract for the confirmer.
// ProcessNext begins a transaction, selects one pending confirmation_notifications row
// FOR UPDATE SKIP LOCKED, calls fn with it. If fn returns nil: marks sent_at and
// commits. If fn returns an error: rolls back. Returns (false, nil) when the queue is empty.
type Repository interface {
	ProcessNext(ctx context.Context, fn func(ctx context.Context, c PendingConfirmation) error) (bool, error)
}

// MailSender sends confirmation emails.
type MailSender interface {
	SendConfirmation(ctx context.Context, p domain.SendConfirmationParams) error
}

// Config holds Confirmer dependencies.
type Config struct {
	Repo     Repository
	Mailer   MailSender
	Interval time.Duration
	BaseURL  string
}

// Confirmer periodically drains the confirmation_notifications outbox by sending emails.
type Confirmer struct {
	repo     Repository
	mailer   MailSender
	interval time.Duration
	baseURL  string
}

// New creates a Confirmer from cfg.
func New(cfg Config) *Confirmer {
	return &Confirmer{repo: cfg.Repo, mailer: cfg.Mailer, interval: cfg.Interval, baseURL: cfg.BaseURL}
}

// Run blocks until ctx is cancelled, flushing the outbox on each interval.
func (c *Confirmer) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Flush(ctx)
		}
	}
}

// Flush drains all currently pending confirmations. Exported for testing.
func (c *Confirmer) Flush(ctx context.Context) {
	for {
		processed, err := c.repo.ProcessNext(ctx, func(ctx context.Context, pending PendingConfirmation) error {
			return c.mailer.SendConfirmation(ctx, domain.SendConfirmationParams{
				To:           pending.Email,
				ConfirmURL:   c.baseURL + "/api/confirm/" + pending.ConfirmToken,
				RepoFullName: pending.RepoOwner + "/" + pending.RepoName,
			})
		})
		if err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Msg("confirmer: process next failed")
			return
		}
		if !processed {
			return
		}
	}
}
