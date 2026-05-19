package confirmer

//go:generate mockgen -source=confirmer.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/pkg/transactor"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
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
type Repository interface {
	GetConfirmationsWithLock(ctx context.Context, limit int) ([]PendingConfirmation, error)
	MarkSent(ctx context.Context, id int64) error
}

// MailSender sends confirmation emails.
type MailSender interface {
	SendConfirmation(ctx context.Context, p domain.SendConfirmationParams) error
}

// Config holds Confirmer dependencies.
type Config struct {
	Tx       transactor.Transactor
	Repo     Repository
	Mailer   MailSender
	Interval time.Duration
	BaseURL  string
	Registry *prometheus.Registry
}

// Confirmer periodically drains the confirmation_notifications outbox by sending emails.
type Confirmer struct {
	tx       transactor.Transactor
	repo     Repository
	mailer   MailSender
	interval time.Duration
	baseURL  string
	m        confirmerMetrics
}

const confirmLimit = 1

// New creates a Confirmer from cfg.
func New(cfg Config) *Confirmer {
	return &Confirmer{
		tx:       cfg.Tx,
		repo:     cfg.Repo,
		mailer:   cfg.Mailer,
		interval: cfg.Interval,
		baseURL:  cfg.BaseURL,
		m:        newConfirmerMetrics(cfg.Registry),
	}
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
		var processed bool
		err := c.tx.WithinTransaction(ctx, func(ctx context.Context) error {
			items, err := c.repo.GetConfirmationsWithLock(ctx, confirmLimit)
			if err != nil {
				return fmt.Errorf("confirmer.Confirmer.Flush: Repository.GetConfirmationsWithLock: %w", err)
			}
			if len(items) == 0 {
				return nil
			}
			p := items[0]
			err = c.mailer.SendConfirmation(ctx, domain.SendConfirmationParams{
				To:           p.Email,
				ConfirmURL:   c.baseURL + "/api/confirm/" + p.ConfirmToken,
				RepoFullName: p.RepoOwner + "/" + p.RepoName,
			})
			if err != nil {
				c.m.emailsSent.WithLabelValues("error").Inc()
				return fmt.Errorf("confirmer.Confirmer.Flush: MailSender.SendConfirmation: %w", err)
			}
			c.m.emailsSent.WithLabelValues("ok").Inc()
			if err = c.repo.MarkSent(ctx, p.ID); err != nil {
				return fmt.Errorf("confirmer.Confirmer.Flush: Repository.MarkSent: %w", err)
			}
			processed = true
			return nil
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
