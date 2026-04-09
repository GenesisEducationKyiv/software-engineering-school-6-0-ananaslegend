package service

//go:generate mockgen -source=service.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/rs/zerolog"
)

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// Repository is the storage contract expected by this service.
type Repository interface {
	UpsertRepo(ctx context.Context, p domain.UpsertRepoParams) (int64, error)
	CreateSubscription(ctx context.Context, p domain.CreateSubscriptionParams) (*domain.Subscription, error)
	GetByConfirmToken(ctx context.Context, token string) (*domain.Subscription, error)
	MarkConfirmed(ctx context.Context, p domain.MarkConfirmedParams) error
	DeleteByUnsubscribeToken(ctx context.Context, token string) (bool, error)
}

// RemoteRepositoryProvider checks whether a GitHub repository exists.
type RemoteRepositoryProvider interface {
	RepoExists(ctx context.Context, p domain.RepoExistsParams) (bool, error)
}

// MailSender sends transactional emails.
type MailSender interface {
	SendConfirmation(ctx context.Context, p domain.SendConfirmationParams) error
	SendRelease(ctx context.Context, p domain.SendReleaseParams) error
}

// Config holds all dependencies and settings for Service.
type Config struct {
	Repo            Repository
	GitHub          RemoteRepositoryProvider
	Mailer          MailSender
	AppBaseURL      string
	ConfirmTokenTTL time.Duration
}

type Service struct {
	repo            Repository
	github          RemoteRepositoryProvider
	mailer          MailSender
	appBaseURL      string
	confirmTokenTTL time.Duration
}

func New(cfg Config) *Service {
	return &Service{
		repo:            cfg.Repo,
		github:          cfg.GitHub,
		mailer:          cfg.Mailer,
		appBaseURL:      cfg.AppBaseURL,
		confirmTokenTTL: cfg.ConfirmTokenTTL,
	}
}

func (s *Service) Subscribe(ctx context.Context, p domain.SubscribeParams) error {
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
		return err // ErrAlreadyExists propagated as-is
	}

	if err := s.mailer.SendConfirmation(ctx, domain.SendConfirmationParams{
		To:             p.Email,
		ConfirmURL:     s.appBaseURL + "/api/confirm/" + confirmToken,
		RepoFullName:   p.Repository,
		UnsubscribeURL: s.appBaseURL + "/api/unsubscribe/" + unsubscribeToken,
	}); err != nil {
		zerolog.Ctx(ctx).Error().Err(err).Str("email", p.Email).Msg("failed to send confirmation email")
		return fmt.Errorf("send confirmation: %w", err)
	}

	zerolog.Ctx(ctx).Info().
		Str("email", p.Email).
		Str("repo", p.Repository).
		Int64("repo_id", repoID).
		Msg("subscription created")
	return nil
}

func (s *Service) Confirm(ctx context.Context, token string) error {
	sub, err := s.repo.GetByConfirmToken(ctx, token)
	if err != nil {
		return err
	}

	now := time.Now()
	if sub.ConfirmTokenExpiresAt != nil && now.After(*sub.ConfirmTokenExpiresAt) {
		return domain.ErrTokenExpired
	}

	if err := s.repo.MarkConfirmed(ctx, domain.MarkConfirmedParams{ID: sub.ID, Now: now}); err != nil {
		return fmt.Errorf("mark confirmed: %w", err)
	}

	zerolog.Ctx(ctx).Info().Int64("subscription_id", sub.ID).Msg("subscription confirmed")
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
	return nil
}
