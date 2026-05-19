package transactor

//go:generate mockgen -source=transactor.go -destination=mocks/mock_transactor.go -package=mocks

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type dbKey struct{}

// Conn is the common interface satisfied by both *pgxpool.Pool and pgx.Tx.
type Conn interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// Transactor manages database transactions.
type Transactor interface {
	WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// PgxTransactor implements Transactor using pgxpool.
type PgxTransactor struct {
	pool *pgxpool.Pool
}

// New creates a PgxTransactor.
func New(pool *pgxpool.Pool) *PgxTransactor {
	return &PgxTransactor{pool: pool}
}

// WithinTransaction begins a transaction, runs fn with a tx-enriched context, and commits.
// On fn error the transaction is rolled back.
func (t *PgxTransactor) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	tx, err := t.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := fn(context.WithValue(ctx, dbKey{}, Conn(tx))); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ConnFromContext returns the Conn stored in ctx by WithinTransaction,
// or pool if no transaction is active.
func ConnFromContext(ctx context.Context, pool Conn) Conn {
	if db, ok := ctx.Value(dbKey{}).(Conn); ok {
		return db
	}
	return pool
}
