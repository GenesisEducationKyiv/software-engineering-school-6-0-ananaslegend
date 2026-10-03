package repository

import (
	"context"
	"fmt"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

func (r *Repository) SaveRepo(ctx context.Context, p domain.UpsertRepoParams) (int64, error) {
	var id int64
	// ON CONFLICT DO UPDATE з no-op SET потрібен, щоб RETURNING спрацював і у разі вже існуючого репо.
	err := r.conn(ctx).QueryRow(ctx, `
		INSERT INTO repositories (owner, name)
		VALUES ($1, $2)
		ON CONFLICT (owner, name) DO UPDATE SET owner = EXCLUDED.owner
		RETURNING id
	`, p.Owner, p.Name).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("subscription.Repository.SaveRepo: %w", err)
	}
	return id, nil
}
