package repository

import "github.com/jackc/pgx/v5/pgxpool"

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}
