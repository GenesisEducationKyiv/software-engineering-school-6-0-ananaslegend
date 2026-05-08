package notifier

//go:generate mockgen -source=notifier.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/pkg/transactor"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

// PendingNotification is one outbox row joined with subscription + repository data.
type PendingNotification struct {
	ID               int64
	Email            string
	RepoOwner        string
	RepoName         string
	ReleaseTag       string
	UnsubscribeToken string
}

// Repository is the storage contract for the notifier.
type Repository interface {
	GetNotificationsWithLock(ctx context.Context, limit int) ([]PendingNotification, error)
	MarkSent(ctx context.Context, id int64) error
}

// MailSender sends release notification emails.
type MailSender interface {
	SendRelease(ctx context.Context, p domain.SendReleaseParams) error
}

// Config holds Notifier dependencies.
type Config struct {
	Tx       transactor.Transactor
	Repo     Repository
	Mailer   MailSender
	Interval time.Duration
	BaseURL  string
	Registry *prometheus.Registry
}

// Notifier periodically drains the release_notifications outbox by sending emails.
type Notifier struct {
	tx       transactor.Transactor
	repo     Repository
	mailer   MailSender
	interval time.Duration
	baseURL  string
	m        notifierMetrics
}

const notifyLimit = 1

// New creates a Notifier from cfg.
func New(cfg Config) *Notifier {
	return &Notifier{
		tx:       cfg.Tx,
		repo:     cfg.Repo,
		mailer:   cfg.Mailer,
		interval: cfg.Interval,
		baseURL:  cfg.BaseURL,
		m:        newNotifierMetrics(cfg.Registry),
	}
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
	start := time.Now()
	defer func() { n.m.flushDuration.Observe(time.Since(start).Seconds()) }()

	for {
		var processed bool
		err := n.tx.WithinTransaction(ctx, func(ctx context.Context) error {
			items, err := n.repo.GetNotificationsWithLock(ctx, notifyLimit)
			if err != nil {
				return fmt.Errorf("notifier.Notifier.Flush: Repository.GetNotificationsWithLock: %w", err)
			}
			if len(items) == 0 {
				return nil
			}
			p := items[0]
			sendErr := n.mailer.SendRelease(ctx, domain.SendReleaseParams{
				To:           p.Email,
				RepoFullName: p.RepoOwner + "/" + p.RepoName,
				ReleaseTag:   p.ReleaseTag,
				ReleaseURL: fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s",
					p.RepoOwner, p.RepoName, p.ReleaseTag),
				UnsubscribeURL: fmt.Sprintf("%s/api/unsubscribe/%s", n.baseURL, p.UnsubscribeToken),
			})
			if sendErr != nil {
				n.m.emailsSent.WithLabelValues("error").Inc()
				return fmt.Errorf("notifier.Notifier.Flush: MailSender.SendRelease: %w", sendErr)
			}
			n.m.emailsSent.WithLabelValues("ok").Inc()
			if err = n.repo.MarkSent(ctx, p.ID); err != nil {
				return fmt.Errorf("notifier.Notifier.Flush: Repository.MarkSent: %w", err)
			}
			processed = true
			return nil
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
