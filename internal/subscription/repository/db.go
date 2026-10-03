package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ananaslegend/reposeetory/pkg/transactor"
)

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// conn returns the active transaction from ctx, or the pool if no transaction is present.
func (r *Repository) conn(ctx context.Context) transactor.Conn {
	return transactor.ConnFromContext(ctx, r.pool)
}
