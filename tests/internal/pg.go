//go:build integration || e2e

package internal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ananaslegend/reposeetory/migrations"
)

// Postgres wraps a testcontainer-backed Postgres instance plus a connection pool.
// Lifetime is bound to the test that called NewPostgres via t.Cleanup.
type Postgres struct {
	Pool      *pgxpool.Pool
	container testcontainers.Container
}

// NewPostgres starts a Postgres 17 container, applies all migrations, and
// returns a ready-to-use pool. The container is terminated automatically when
// the calling test finishes.
func NewPostgres(ctx context.Context, t testing.TB) *Postgres {
	t.Helper()

	pgC, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err, "start postgres container")

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "container connection string")

	if err := migrateUp(dsn); err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("migrate up: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err, "new pgx pool")

	pg := &Postgres{Pool: pool, container: pgC}
	t.Cleanup(func() {
		pool.Close()
		_ = pgC.Terminate(context.Background())
	})
	return pg
}

// Truncate empties every domain table and restarts identity sequences. Use
// from SetupTest of a suite to get a clean slate per test.
func (p *Postgres) Truncate(ctx context.Context, t testing.TB) {
	t.Helper()
	_, err := p.Pool.Exec(ctx, `
		TRUNCATE
			subscriptions,
			repositories,
			confirmation_notifications,
			release_notifications
		RESTART IDENTITY CASCADE
	`)
	require.NoError(t, err, "truncate tables")
}

// WaitForLastSeen polls the repositories row for repo ("owner/name") until
// last_seen_tag equals tag, or fails the test after within elapses. Use this
// in place of magic time.Sleep waits for scanner ticks: the row update is
// the observable side effect that proves at least one scanner.Tick has
// completed.
func (p *Postgres) WaitForLastSeen(ctx context.Context, t testing.TB, repo, tag string, within time.Duration) {
	t.Helper()
	owner, name, ok := strings.Cut(repo, "/")
	require.True(t, ok, "repo must be in owner/name form, got %q", repo)

	deadline := time.Now().Add(within)
	var got *string
	var lastErr error
	for {
		lastErr = p.Pool.QueryRow(ctx,
			`SELECT last_seen_tag FROM repositories WHERE owner=$1 AND name=$2`,
			owner, name).Scan(&got)
		if lastErr == nil && got != nil && *got == tag {
			return
		}
		if time.Now().After(deadline) {
			require.FailNowf(t, "scanner did not record last_seen_tag",
				"want %q for %s within %s; got err=%v val=%v",
				tag, repo, within, lastErr, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// AssertNoReleaseNotifications asserts the release_notifications table has
// zero rows for (repo, tag). Combined with WaitForLastSeen (which proves a
// scanner Tick ran), this is a deterministic negative check: if the scanner
// did not insert a row in the same transaction that updated last_seen_tag,
// nothing downstream can deliver an email.
func (p *Postgres) AssertNoReleaseNotifications(ctx context.Context, t testing.TB, repo, tag string) {
	t.Helper()
	owner, name, ok := strings.Cut(repo, "/")
	require.True(t, ok, "repo must be in owner/name form, got %q", repo)

	var count int
	err := p.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM release_notifications rn
		JOIN repositories r ON r.id = rn.repository_id
		WHERE r.owner = $1 AND r.name = $2 AND rn.release_tag = $3
	`, owner, name, tag).Scan(&count)
	require.NoError(t, err, "count release_notifications")
	require.Zero(t, count,
		"expected zero release_notifications for %s tag=%s, got %d", repo, tag, count)
}

func migrateUp(dsn string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("internal.migrateUp: iofs.New: %w", err)
	}

	// testcontainers-go's postgres module returns a postgres:// DSN. Rewrite
	// the scheme to pgx5:// so migrate picks the pgx-v5 driver we imported
	// above (mirrors internal/app/migrate.go).
	pgxDSN := "pgx5://" + strings.TrimPrefix(strings.TrimPrefix(dsn, "postgres://"), "postgresql://")

	m, err := migrate.NewWithSourceInstance("iofs", src, pgxDSN)
	if err != nil {
		return fmt.Errorf("internal.migrateUp: migrate.NewWithSourceInstance: %w", err)
	}
	defer func() {
		_, _ = m.Close()
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("internal.migrateUp: Migrate.Up: %w", err)
	}
	return nil
}
