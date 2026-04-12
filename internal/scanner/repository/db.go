package repository

import (
	"context"
	"fmt"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/internal/transactor"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository implements scanner.Repository using pgx.
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

// GetRepositoriesWithLock selects up to limit repos FOR UPDATE SKIP LOCKED.
func (r *Repository) GetRepositoriesWithLock(ctx context.Context, limit int) ([]domain.GitHubRepo, error) {
	rows, err := r.conn(ctx).Query(ctx, `
		SELECT id, owner, name, last_seen_tag
		FROM repositories
		ORDER BY last_checked_at NULLS FIRST
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("scanner: get repos with lock: %w", err)
	}
	defer rows.Close()

	var repos []domain.GitHubRepo
	for rows.Next() {
		var repo domain.GitHubRepo
		if err := rows.Scan(&repo.ID, &repo.Owner, &repo.Name, &repo.LastSeenTag); err != nil {
			return nil, fmt.Errorf("scanner: scan repo row: %w", err)
		}
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

// InsertNotifications inserts release_notifications rows for all confirmed subscribers of repoID.
func (r *Repository) InsertNotifications(ctx context.Context, repoID int64, tag string) error {
	_, err := r.conn(ctx).Exec(ctx, `
		INSERT INTO release_notifications (subscription_id, repository_id, release_tag)
		SELECT id, $1, $2
		FROM subscriptions
		WHERE repository_id = $1 AND confirmed_at IS NOT NULL
	`, repoID, tag)
	if err != nil {
		return fmt.Errorf("scanner: insert notifications repo %d: %w", repoID, err)
	}
	return nil
}

// UpsertLastSeen updates last_seen_tag and last_checked_at for repoID.
func (r *Repository) UpsertLastSeen(ctx context.Context, repoID int64, newTag string) error {
	_, err := r.conn(ctx).Exec(ctx, `
		UPDATE repositories
		SET last_seen_tag = $1, last_checked_at = NOW()
		WHERE id = $2
	`, newTag, repoID)
	if err != nil {
		return fmt.Errorf("scanner: upsert last seen repo %d: %w", repoID, err)
	}
	return nil
}
