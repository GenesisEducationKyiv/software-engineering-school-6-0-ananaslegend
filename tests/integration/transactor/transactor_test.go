//go:build integration

package transactor_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/pkg/transactor"
	"github.com/ananaslegend/reposeetory/tests/internal"
)

func TestWithinTransaction_CommitsOnSuccess(t *testing.T) {
	ctx := context.Background()
	pg := internal.NewPostgres(ctx, t)
	tx := transactor.New(pg.Pool)
	tbl := newScratchTable(ctx, t, pg.Pool)

	err := tx.WithinTransaction(ctx, func(ctx context.Context) error {
		conn := transactor.ConnFromContext(ctx, pg.Pool)
		_, err := conn.Exec(ctx, "INSERT INTO "+tbl+" (id) VALUES (1)")
		return err
	})
	require.NoError(t, err)

	var n int
	require.NoError(t, pg.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+tbl).Scan(&n))
	assert.Equal(t, 1, n, "row must be visible after commit")
}

func TestWithinTransaction_RollsBackOnError(t *testing.T) {
	ctx := context.Background()
	pg := internal.NewPostgres(ctx, t)
	tx := transactor.New(pg.Pool)
	tbl := newScratchTable(ctx, t, pg.Pool)

	sentinel := errors.New("fn failed")
	err := tx.WithinTransaction(ctx, func(ctx context.Context) error {
		conn := transactor.ConnFromContext(ctx, pg.Pool)
		_, _ = conn.Exec(ctx, "INSERT INTO "+tbl+" (id) VALUES (1)")
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	var n int
	require.NoError(t, pg.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+tbl).Scan(&n))
	assert.Zero(t, n, "row must NOT be visible after rollback")
}

func TestWithinTransaction_NestedStartsSeparateTransaction(t *testing.T) {
	// Documents the current contract: each WithinTransaction call begins
	// a fresh tx on the underlying pool; the inner call does NOT join the
	// outer one (see pkg/transactor/transactor.go — pool.BeginTx is called
	// every time). If anyone ever switches to savepoint semantics, this
	// test will fail loudly — that's the point.
	ctx := context.Background()
	pg := internal.NewPostgres(ctx, t)
	tx := transactor.New(pg.Pool)
	tbl := newScratchTable(ctx, t, pg.Pool)

	outerErr := tx.WithinTransaction(ctx, func(ctx context.Context) error {
		outerConn := transactor.ConnFromContext(ctx, pg.Pool)
		_, err := outerConn.Exec(ctx, "INSERT INTO "+tbl+" (id) VALUES (1)")
		require.NoError(t, err)

		innerErr := tx.WithinTransaction(ctx, func(ctx context.Context) error {
			innerConn := transactor.ConnFromContext(ctx, pg.Pool)
			_, err := innerConn.Exec(ctx, "INSERT INTO "+tbl+" (id) VALUES (2)")
			return err
		})
		require.NoError(t, innerErr)
		return errors.New("rollback outer")
	})
	require.Error(t, outerErr)

	var ids []int
	rows, err := pg.Pool.Query(ctx, "SELECT id FROM "+tbl+" ORDER BY id")
	require.NoError(t, err)
	for rows.Next() {
		var id int
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	rows.Close()
	assert.Equal(t, []int{2}, ids,
		"inner tx commits independently; outer rolls back its own row")
}

func TestConnFromContext_NoTxInContext_ReturnsPool(t *testing.T) {
	ctx := context.Background()
	pg := internal.NewPostgres(ctx, t)

	got := transactor.ConnFromContext(ctx, pg.Pool)
	var one int
	require.NoError(t, got.QueryRow(ctx, "SELECT 1").Scan(&one))
	assert.Equal(t, 1, one)
}

func TestConnFromContext_TxInContext_ReturnsTxNotPool(t *testing.T) {
	ctx := context.Background()
	pg := internal.NewPostgres(ctx, t)
	tx := transactor.New(pg.Pool)

	err := tx.WithinTransaction(ctx, func(ctx context.Context) error {
		conn := transactor.ConnFromContext(ctx, pg.Pool)
		_, ok := conn.(pgx.Tx)
		assert.True(t, ok,
			"ConnFromContext inside WithinTransaction must return pgx.Tx, got %T", conn)
		return nil
	})
	require.NoError(t, err)
}

// tname sanitises t.Name() into a valid SQL identifier suffix so each
// test method gets its own scratch table without colliding.
func tname(t *testing.T) string {
	out := make([]rune, 0, len(t.Name()))
	for _, r := range t.Name() {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// newScratchTable creates a single-column table and registers a DROP via
// t.Cleanup. Uses the pool directly (no Tx).
func newScratchTable(ctx context.Context, t *testing.T, pool transactor.Conn) string {
	name := "tx_scratch_" + tname(t)
	_, err := pool.Exec(ctx, "CREATE TABLE "+name+" (id INT PRIMARY KEY)")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+name) })
	return name
}
