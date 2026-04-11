package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const uniqueViolationCode = "23505"

func (r *Repository) CreateSubscription(ctx context.Context, p domain.CreateSubscriptionParams) (*domain.Subscription, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("create subscription: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var sub domain.Subscription
	err = tx.QueryRow(ctx, `
		INSERT INTO subscriptions
			(email, repository_id, confirm_token, confirm_token_expires_at, unsubscribe_token)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, email, repository_id, confirmed_at, confirm_token, confirm_token_expires_at, unsubscribe_token, created_at
	`, p.Email, p.RepositoryID, p.ConfirmToken, p.ConfirmTokenExpiresAt, p.UnsubscribeToken).Scan(
		&sub.ID, &sub.Email, &sub.RepositoryID,
		&sub.ConfirmedAt, &sub.ConfirmToken, &sub.ConfirmTokenExpiresAt,
		&sub.UnsubscribeToken, &sub.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			return nil, domain.ErrAlreadyExists
		}
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO confirmation_notifications (subscription_id) VALUES ($1)
	`, sub.ID); err != nil {
		return nil, fmt.Errorf("create subscription: queue confirmation: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("create subscription: commit: %w", err)
	}

	return &sub, nil
}

func (r *Repository) GetByConfirmToken(ctx context.Context, token string) (*domain.Subscription, error) {
	var sub domain.Subscription
	err := r.pool.QueryRow(ctx, `
		SELECT id, email, repository_id, confirmed_at, confirm_token, confirm_token_expires_at, unsubscribe_token, created_at
		FROM subscriptions
		WHERE confirm_token = $1
	`, token).Scan(
		&sub.ID, &sub.Email, &sub.RepositoryID,
		&sub.ConfirmedAt, &sub.ConfirmToken, &sub.ConfirmTokenExpiresAt,
		&sub.UnsubscribeToken, &sub.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrTokenNotFound
		}
		return nil, fmt.Errorf("get by confirm token: %w", err)
	}
	return &sub, nil
}

func (r *Repository) MarkConfirmed(ctx context.Context, p domain.MarkConfirmedParams) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE subscriptions
		SET confirmed_at = $1, confirm_token = NULL, confirm_token_expires_at = NULL
		WHERE id = $2
	`, p.Now, p.ID)
	if err != nil {
		return fmt.Errorf("mark confirmed: %w", err)
	}
	return nil
}

func (r *Repository) DeleteByUnsubscribeToken(ctx context.Context, token string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM subscriptions WHERE unsubscribe_token = $1
	`, token)
	if err != nil {
		return false, fmt.Errorf("delete subscription: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
