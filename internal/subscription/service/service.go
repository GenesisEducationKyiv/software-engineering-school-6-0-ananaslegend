package service

//go:generate mockgen -source=service.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
)

// Repository is the storage contract expected by this service.
type Repository interface {
	SaveRepo(ctx context.Context, p domain.UpsertRepoParams) (int64, error)
	CreateSubscription(ctx context.Context, p domain.CreateSubscriptionParams) (*domain.Subscription, error)
	MarkConfirmed(ctx context.Context, p domain.MarkConfirmedParams) error
	GetByConfirmToken(ctx context.Context, token string) (*domain.Subscription, error)
	DeleteByUnsubscribeToken(ctx context.Context, token string) (bool, error)
	ListByEmail(ctx context.Context, email string) ([]domain.SubscriptionView, error)
}

type Confirmator interface {
	CreateConfirmation(ctx context.Context, subscriptionID int64) error
}

// RemoteRepositoryProvider checks whether a GitHub repository exists.
type RemoteRepositoryProvider interface {
	RepoExists(ctx context.Context, p domain.RepoExistsParams) (bool, error)
}

// Config holds all dependencies and settings for Service.
type Config struct {
	Tx              transactor.Transactor
	Repo            Repository
	Confirms        Confirmator
	GitHub          RemoteRepositoryProvider
	AppBaseURL      string
	ConfirmTokenTTL time.Duration
	Registry        *prometheus.Registry
}

type Service struct {
	tx                 transactor.Transactor
	repo               Repository
	confirms           Confirmator
	remoteRepoProvider RemoteRepositoryProvider
	appBaseURL         string
	confirmTokenTTL    time.Duration
	m                  serviceMetrics
}

func New(cfg Config) *Service {
	return &Service{
		tx:                 cfg.Tx,
		repo:               cfg.Repo,
		confirms:           cfg.Confirms,
		remoteRepoProvider: cfg.GitHub,
		appBaseURL:         cfg.AppBaseURL,
		confirmTokenTTL:    cfg.ConfirmTokenTTL,
		m:                  newServiceMetrics(cfg.Registry),
	}
}

func (s *Service) Subscribe(ctx context.Context, p domain.SubscribeParams) error {
	ctx = zerolog.Ctx(ctx).With().
		Str("email", p.Email).
		Str("repo", p.Repository).
		Logger().
		WithContext(ctx)

	ref, err := domain.ParseRepoRef(p.Repository)
	if err != nil {
		return fmt.Errorf("subscription.Service.Subscribe: domain.ParseRepoRef: %w", err)
	}

	if err = s.remoteRepositoryExists(ctx, ref); err != nil {
		return fmt.Errorf("subscription.Service.Subscribe: remoteRepositoryExists: %w", err)
	}

	tokens, err := domain.NewConfirmTokens(time.Now(), s.confirmTokenTTL)
	if err != nil {
		return fmt.Errorf("subscription.Service.Subscribe: domain.NewConfirmTokens: %w", err)
	}

	if err = s.createSubscription(ctx, ref, tokens, p.Email); err != nil {
		zerolog.Ctx(ctx).Error().Err(err).Msg("failed to subscribe to repo")

		return err
	}

	zerolog.Ctx(ctx).Info().Msg("subscription created")
	s.m.subscriptionsCreated.Inc()

	return nil
}

func (s *Service) createSubscription(ctx context.Context, ref domain.RepoRef, tokens domain.ConfirmTokens, email string) error {
	return s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		repoID, err := s.repo.SaveRepo(ctx, ref)
		if err != nil {
			return fmt.Errorf("subscription.Service.createSubscription: Repository.SaveRepo: %w", err)
		}

		sub, err := s.repo.CreateSubscription(ctx, domain.CreateSubscriptionParams{
			Email:                 email,
			RepositoryID:          repoID,
			ConfirmToken:          tokens.Confirm,
			ConfirmTokenExpiresAt: tokens.ConfirmExpiresAt,
			UnsubscribeToken:      tokens.Unsubscribe,
		})
		if err != nil {
			return fmt.Errorf("subscription.Service.createSubscription: Repository.CreateSubscription: %w", err)
		}

		if err := s.confirms.CreateConfirmation(ctx, sub.ID); err != nil {
			return fmt.Errorf("subscription.Service.createSubscription: Confirmator.CreateConfirmation: %w", err)
		}

		return nil
	})
}

func (s *Service) remoteRepositoryExists(ctx context.Context, repo domain.RepoExistsParams) error {
	exists, err := s.remoteRepoProvider.RepoExists(ctx, repo)
	if err != nil {
		return fmt.Errorf("subscription.Service.Subscribe: GitHub.RepoExists: %w", err)
	}
	if !exists {
		return domain.ErrRepoNotFound
	}

	return nil
}

func (s *Service) Confirm(ctx context.Context, token string) error {
	sub, err := s.repo.GetByConfirmToken(ctx, token)
	if err != nil {
		return fmt.Errorf("subscription.Service.Confirm: Repository.GetByConfirmToken: %w", err)
	}

	now := time.Now()
	if sub.ConfirmTokenExpiresAt != nil && now.After(*sub.ConfirmTokenExpiresAt) {
		return domain.ErrTokenExpired
	}

	if err := s.repo.MarkConfirmed(ctx, domain.MarkConfirmedParams{ID: sub.ID, Now: now}); err != nil {
		return fmt.Errorf("mark confirmed: %w", err)
	}

	zerolog.Ctx(ctx).Info().Int64("subscription_id", sub.ID).Msg("subscription confirmed")
	s.m.subscriptionsConfirmed.Inc()
	return nil
}

func (s *Service) Unsubscribe(ctx context.Context, token string) error {
	deleted, err := s.repo.DeleteByUnsubscribeToken(ctx, token)
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	if !deleted {
		return domain.ErrTokenNotFound
	}

	zerolog.Ctx(ctx).Info().Msg("unsubscribed")
	s.m.subscriptionsDeleted.Inc()
	return nil
}

func (s *Service) ListByEmail(ctx context.Context, email string) ([]domain.SubscriptionView, error) {
	subs, err := s.repo.ListByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("list by email: %w", err)
	}
	return subs, nil
}
