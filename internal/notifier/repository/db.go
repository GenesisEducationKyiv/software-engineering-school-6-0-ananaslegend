package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ananaslegend/reposeetory/internal/notifier"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type txKey struct{}

// Repository implements notifier.Repository using pgx.
type Repository struct {
	pool *pgxpool.Pool
}

// New creates a Repository.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// ProcessNext selects one pending notification FOR UPDATE SKIP LOCKED, calls fn,
// and commits (or rolls back on fn error).
func (r *Repository) ProcessNext(ctx context.Context, fn func(context.Context, notifier.PendingNotification) error) (bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("notifier: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	txCtx := context.WithValue(ctx, txKey{}, tx)

	var n notifier.PendingNotification
	err = tx.QueryRow(txCtx, `
		SELECT rn.id, s.email, r.owner, r.name, rn.release_tag
		FROM release_notifications rn
		JOIN subscriptions s ON rn.subscription_id = s.id
		JOIN repositories  r ON rn.repository_id  = r.id
		WHERE rn.sent_at IS NULL
		ORDER BY rn.created_at
		LIMIT 1
		FOR UPDATE OF rn SKIP LOCKED
	`).Scan(&n.ID, &n.Email, &n.RepoOwner, &n.RepoName, &n.ReleaseTag)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Commit(ctx)
			return false, nil
		}
		return false, fmt.Errorf("notifier: lock next pending: %w", err)
	}

	if err := fn(txCtx, n); err != nil {
		return false, fmt.Errorf("notifier: process notification %d: %w", n.ID, err)
	}

	if _, err := tx.Exec(txCtx, `
		UPDATE release_notifications SET sent_at = $1 WHERE id = $2
	`, time.Now(), n.ID); err != nil {
		return false, fmt.Errorf("notifier: mark sent %d: %w", n.ID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("notifier: commit: %w", err)
	}
	return true, nil
}
