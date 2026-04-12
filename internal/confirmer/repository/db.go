package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/ananaslegend/reposeetory/internal/confirmer"
	"github.com/ananaslegend/reposeetory/internal/transactor"
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

// conn returns the active transaction from ctx, or the pool if no transaction is present.
func (r *Repository) conn(ctx context.Context) transactor.Conn {
	return transactor.ConnFromContext(ctx, r.pool)
}

// GetConfirmationsWithLock selects up to limit pending confirmation_notifications FOR UPDATE SKIP LOCKED.
func (r *Repository) GetConfirmationsWithLock(ctx context.Context, limit int) ([]confirmer.PendingConfirmation, error) {
	rows, err := r.conn(ctx).Query(ctx, `
		SELECT cn.id, s.email, s.confirm_token, r.owner, r.name
		FROM confirmation_notifications cn
		JOIN subscriptions  s ON cn.subscription_id = s.id
		JOIN repositories   r ON s.repository_id   = r.id
		WHERE cn.sent_at IS NULL
		  AND s.confirm_token IS NOT NULL
		ORDER BY cn.created_at
		LIMIT $1
		FOR UPDATE OF cn SKIP LOCKED
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("confirmer: get confirmations with lock: %w", err)
	}
	defer rows.Close()

	var items []confirmer.PendingConfirmation
	for rows.Next() {
		var c confirmer.PendingConfirmation
		if err := rows.Scan(&c.ID, &c.Email, &c.ConfirmToken, &c.RepoOwner, &c.RepoName); err != nil {
			return nil, fmt.Errorf("confirmer: scan confirmation row: %w", err)
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

// MarkSent sets sent_at = NOW() for the confirmation_notification with the given id.
func (r *Repository) MarkSent(ctx context.Context, id int64) error {
	_, err := r.conn(ctx).Exec(ctx, `
		UPDATE confirmation_notifications SET sent_at = $1 WHERE id = $2
	`, time.Now(), id)
	if err != nil {
		return fmt.Errorf("confirmer: mark sent %d: %w", id, err)
	}
	return nil
}
