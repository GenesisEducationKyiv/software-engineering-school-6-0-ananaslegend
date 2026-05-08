package service

//go:generate mockgen -source=service.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// normalizeRepo extracts "owner/name" from a URL or a plain "owner/name" string.
// Strips trailing ".git" and takes the last two slash-separated segments.
func normalizeRepo(s string) string {
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return s
}

// Repository is the storage contract expected by this service.
type Repository interface {
	UpsertRepo(ctx context.Context, p domain.UpsertRepoParams) (int64, error)
	CreateSubscription(ctx context.Context, p domain.CreateSubscriptionParams) (*domain.Subscription, error)
	GetByConfirmToken(ctx context.Context, token string) (*domain.Subscription, error)
	MarkConfirmed(ctx context.Context, p domain.MarkConfirmedParams) error
	DeleteByUnsubscribeToken(ctx context.Context, token string) (bool, error)
	ListByEmail(ctx context.Context, email string) ([]domain.SubscriptionView, error)
}

// RemoteRepositoryProvider checks whether a GitHub repository exists.
type RemoteRepositoryProvider interface {
	RepoExists(ctx context.Context, p domain.RepoExistsParams) (bool, error)
}

// Config holds all dependencies and settings for Service.
type Config struct {
	Repo            Repository
	GitHub          RemoteRepositoryProvider
	AppBaseURL      string
	ConfirmTokenTTL time.Duration
	Registry        *prometheus.Registry
}

type Service struct {
	repo            Repository
	github          RemoteRepositoryProvider
	appBaseURL      string
	confirmTokenTTL time.Duration
	m               serviceMetrics
}

func New(cfg Config) *Service {
	return &Service{
		repo:            cfg.Repo,
		github:          cfg.GitHub,
		appBaseURL:      cfg.AppBaseURL,
		confirmTokenTTL: cfg.ConfirmTokenTTL,
		m:               newServiceMetrics(cfg.Registry),
	}
}

func (s *Service) Subscribe(ctx context.Context, p domain.SubscribeParams) error {
	p.Repository = normalizeRepo(p.Repository)
	if !repoNameRe.MatchString(p.Repository) {
		return domain.ErrInvalidRepoFormat
	}
	parts := strings.SplitN(p.Repository, "/", 2)
	owner, name := parts[0], parts[1]

	exists, err := s.github.RepoExists(ctx, domain.RepoExistsParams{Owner: owner, Name: name})
	if err != nil {
		return fmt.Errorf("check repo existence: %w", err)
	}
	if !exists {
		return domain.ErrRepoNotFound
	}

	repoID, err := s.repo.UpsertRepo(ctx, domain.UpsertRepoParams{Owner: owner, Name: name})
	if err != nil {
		return fmt.Errorf("upsert repo: %w", err)
	}

	confirmToken, err := domain.GenerateToken()
	if err != nil {
		return fmt.Errorf("generate confirm token: %w", err)
	}
	unsubscribeToken, err := domain.GenerateToken()
	if err != nil {
		return fmt.Errorf("generate unsubscribe token: %w", err)
	}

	_, err = s.repo.CreateSubscription(ctx, domain.CreateSubscriptionParams{
		Email:                 p.Email,
		RepositoryID:          repoID,
		ConfirmToken:          confirmToken,
		ConfirmTokenExpiresAt: time.Now().Add(s.confirmTokenTTL),
		UnsubscribeToken:      unsubscribeToken,
	})
	if err != nil {
		return fmt.Errorf("subscription.Service.Subscribe: Repository.CreateSubscription: %w", err)
	}

	zerolog.Ctx(ctx).Info().
		Str("email", p.Email).
		Str("repo", p.Repository).
		Int64("repo_id", repoID).
		Msg("subscription created")
	s.m.subscriptionsCreated.Inc()
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
