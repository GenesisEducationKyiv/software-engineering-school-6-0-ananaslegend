package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ananaslegend/reposeetory/internal/notifier"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
)

// Repository implements notifier.Repository using pgx.
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

// GetNotificationsWithLock selects up to limit pending release_notifications FOR UPDATE SKIP LOCKED.
func (r *Repository) GetNotificationsWithLock(ctx context.Context, limit int) ([]notifier.PendingNotification, error) {
	rows, err := r.conn(ctx).Query(ctx, `
		SELECT rn.id, s.email, r.owner, r.name, rn.release_tag, s.unsubscribe_token
		FROM release_notifications rn
		JOIN subscriptions s ON rn.subscription_id = s.id
		JOIN repositories  r ON rn.repository_id  = r.id
		WHERE rn.sent_at IS NULL
		ORDER BY rn.created_at
		LIMIT $1
		FOR UPDATE OF rn SKIP LOCKED
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("notifier: get notifications with lock: %w", err)
	}
	defer rows.Close()

	var items []notifier.PendingNotification
	for rows.Next() {
		var n notifier.PendingNotification
		if err := rows.Scan(&n.ID, &n.Email, &n.RepoOwner, &n.RepoName, &n.ReleaseTag, &n.UnsubscribeToken); err != nil {
			return nil, fmt.Errorf("notifier: scan notification row: %w", err)
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notifier.Repository.GetNotificationsWithLock: rows.Err: %w", err)
	}
	return items, nil
}

// MarkSent sets sent_at = NOW() for the notification with the given id.
func (r *Repository) MarkSent(ctx context.Context, id int64) error {
	_, err := r.conn(ctx).Exec(ctx, `
		UPDATE release_notifications SET sent_at = $1 WHERE id = $2
	`, time.Now(), id)
	if err != nil {
		return fmt.Errorf("notifier: mark sent %d: %w", id, err)
	}
	return nil
}
