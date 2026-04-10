package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/ananaslegend/reposeetory/internal/scanner"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type txKey struct{}

// Repository implements scanner.Repository using pgx.
type Repository struct {
	pool *pgxpool.Pool
}

// New creates a Repository.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// RunInTx begins a transaction, locks repos, calls fn, processes results, commits.
func (r *Repository) RunInTx(ctx context.Context, limit int, fn func(context.Context, []domain.GitHubRepo) ([]scanner.ScanResult, error)) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("scanner: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	txCtx := context.WithValue(ctx, txKey{}, tx)

	repos, err := lockRepos(txCtx, limit)
	if err != nil {
		return err
	}

	results, err := fn(txCtx, repos)
	if err != nil {
		return err
	}

	now := time.Now()
	for _, res := range results {
		if !res.IsFirstScan {
			if err := insertNotifications(txCtx, res.RepoID, res.NewTag); err != nil {
				return err
			}
		}
		if err := upsertLastSeen(txCtx, res.RepoID, res.NewTag, now); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("scanner: commit: %w", err)
	}
	return nil
}

func lockRepos(ctx context.Context, limit int) ([]domain.GitHubRepo, error) {
	tx := ctx.Value(txKey{}).(pgx.Tx)
	rows, err := tx.Query(ctx, `
		SELECT id, owner, name, last_seen_tag
		FROM repositories
		ORDER BY last_checked_at NULLS FIRST
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("scanner: lock repos: %w", err)
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

func insertNotifications(ctx context.Context, repoID int64, tag string) error {
	tx := ctx.Value(txKey{}).(pgx.Tx)
	_, err := tx.Exec(ctx, `
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

func upsertLastSeen(ctx context.Context, repoID int64, newTag string, now time.Time) error {
	tx := ctx.Value(txKey{}).(pgx.Tx)
	_, err := tx.Exec(ctx, `
		UPDATE repositories
		SET last_seen_tag = $1, last_checked_at = $2
		WHERE id = $3
	`, newTag, now, repoID)
	if err != nil {
		return fmt.Errorf("scanner: upsert last seen repo %d: %w", repoID, err)
	}
	return nil
}
