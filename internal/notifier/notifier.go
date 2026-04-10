package notifier

//go:generate mockgen -source=notifier.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"fmt"
	"time"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/rs/zerolog"
)

// PendingNotification is one outbox row joined with subscription + repository data.
type PendingNotification struct {
	ID         int64
	Email      string
	RepoOwner  string
	RepoName   string
	ReleaseTag string
}

// Repository is the storage contract for the notifier.
// ProcessNext begins a transaction, selects one pending release_notifications row
// FOR UPDATE SKIP LOCKED, calls fn with it. If fn returns nil: marks sent_at and
// commits. If fn returns an error: rolls back. Returns (false, nil) when the
// queue is empty.
type Repository interface {
	ProcessNext(ctx context.Context, fn func(ctx context.Context, n PendingNotification) error) (bool, error)
}

// MailSender sends release notification emails.
type MailSender interface {
	SendRelease(ctx context.Context, p domain.SendReleaseParams) error
}

// Config holds Notifier dependencies.
type Config struct {
	Repo     Repository
	Mailer   MailSender
	Interval time.Duration
}

// Notifier periodically drains the release_notifications outbox by sending emails.
type Notifier struct {
	repo     Repository
	mailer   MailSender
	interval time.Duration
}

// New creates a Notifier from cfg.
func New(cfg Config) *Notifier {
	return &Notifier{repo: cfg.Repo, mailer: cfg.Mailer, interval: cfg.Interval}
}

// Run blocks until ctx is cancelled, flushing the outbox on each interval.
func (n *Notifier) Run(ctx context.Context) {
	ticker := time.NewTicker(n.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.Flush(ctx)
		}
	}
}

// Flush drains all currently pending notifications. Exported for testing.
func (n *Notifier) Flush(ctx context.Context) {
	for {
		processed, err := n.repo.ProcessNext(ctx, func(ctx context.Context, pending PendingNotification) error {
			return n.mailer.SendRelease(ctx, domain.SendReleaseParams{
				To:           pending.Email,
				RepoFullName: pending.RepoOwner + "/" + pending.RepoName,
				ReleaseTag:   pending.ReleaseTag,
				ReleaseURL: fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s",
					pending.RepoOwner, pending.RepoName, pending.ReleaseTag),
			})
		})
		if err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Msg("notifier: process next failed")
			return
		}
		if !processed {
			return
		}
	}
}
