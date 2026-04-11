package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ananaslegend/reposeetory/internal/confirmer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository implements confirmer.Repository using pgx.
type Repository struct {
	pool *pgxpool.Pool
}

// New creates a Repository.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// ProcessNext selects one pending confirmation FOR UPDATE SKIP LOCKED, calls fn,
// and commits (or rolls back on fn error).
func (r *Repository) ProcessNext(ctx context.Context, fn func(context.Context, confirmer.PendingConfirmation) error) (bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("confirmer: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var c confirmer.PendingConfirmation
	err = tx.QueryRow(ctx, `
		SELECT cn.id, s.email, s.confirm_token, r.owner, r.name
		FROM confirmation_notifications cn
		JOIN subscriptions  s ON cn.subscription_id = s.id
		JOIN repositories   r ON s.repository_id   = r.id
		WHERE cn.sent_at IS NULL
		ORDER BY cn.created_at
		LIMIT 1
		FOR UPDATE OF cn SKIP LOCKED
	`).Scan(&c.ID, &c.Email, &c.ConfirmToken, &c.RepoOwner, &c.RepoName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("confirmer: lock next pending: %w", err)
	}

	if err := fn(ctx, c); err != nil {
		return false, fmt.Errorf("confirmer: process confirmation %d: %w", c.ID, err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE confirmation_notifications SET sent_at = $1 WHERE id = $2
	`, time.Now(), c.ID); err != nil {
		return false, fmt.Errorf("confirmer: mark sent %d: %w", c.ID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("confirmer: commit: %w", err)
	}
	return true, nil
}
