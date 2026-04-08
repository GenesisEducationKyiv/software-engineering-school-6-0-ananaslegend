package repository

import (
	"context"
	"fmt"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

func (r *Repository) UpsertRepo(ctx context.Context, p domain.UpsertRepoParams) (int64, error) {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO repositories (owner, name)
		VALUES ($1, $2)
		ON CONFLICT (owner, name) DO NOTHING
	`, p.Owner, p.Name)
	if err != nil {
		return 0, fmt.Errorf("upsert repo insert: %w", err)
	}

	var id int64
	err = r.pool.QueryRow(ctx, `
		SELECT id FROM repositories WHERE owner = $1 AND name = $2
	`, p.Owner, p.Name).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert repo select: %w", err)
	}
	return id, nil
}
