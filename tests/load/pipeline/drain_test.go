//go:build loadtest

package pipeline_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	testinternal "github.com/ananaslegend/reposeetory/tests/internal"
	"github.com/ananaslegend/reposeetory/tests/load/pipeline"
	"github.com/ananaslegend/reposeetory/tests/load/seeder/seedlib"
)

// TestPipeline_DrainsOutboxWithinSLA measures end-to-end background pipeline
// throughput. The clock starts when we POST /admin/release-all on the stub
// (which atomically bumps every repo to a new tag) and stops when
// release_notifications.sent_at IS NULL = 0.
//
// Tuning knobs (env, all optional):
//   - LOAD_SUBSCRIPTIONS — confirmed subscriptions to seed (default 10000)
//   - LOAD_REPOS         — distinct owner-X/repo-X pool size (default 1000)
//   - LOAD_DRAIN_TIMEOUT — hard cap before the test fails (default 5m)
//   - LOAD_DRAIN_SLA_SECONDS — drain duration assertion ceiling (default 60s)
func TestPipeline_DrainsOutboxWithinSLA(t *testing.T) {
	subs := envInt("LOAD_SUBSCRIPTIONS", 10000)
	repos := envInt("LOAD_REPOS", 1000)
	timeout := envDur("LOAD_DRAIN_TIMEOUT", 5*time.Minute)
	slaSec := envInt("LOAD_DRAIN_SLA_SECONDS", 60)

	ctx := context.Background()
	pg := testinternal.NewPostgres(ctx, t)
	stub := pipeline.NewGitHubStub(t, repos)

	_, err := seedlib.Run(ctx, pg.Pool, seedlib.Options{
		ConfirmedCount: subs,
		RepoPoolSize:   repos,
	})
	require.NoError(t, err, "seed confirmed subscriptions")

	app := testinternal.NewE2EApp(t, testinternal.E2EAppConfig{
		Pool:           pg.Pool,
		Mailpit:        nil, // use StubMailer via the new code path
		GitHubRESTURL:  stub.URL,
		GitHubGraphURL: stub.URL + "/graphql",
		ScannerTick:    50 * time.Millisecond,
		DrainerTick:    50 * time.Millisecond,
	})

	// shouldNotify returns false when last_seen_tag IS NULL, so the FIRST
	// observed tag merely establishes the baseline. If we POST /admin/release-all
	// before the baseline tick fires, the scanner sees v2 first, stores it as
	// baseline, and notifications never fire (no further tag bumps come).
	//
	// Wait until every seeded repo has last_checked_at set before triggering.
	baselineDeadline := time.Now().Add(timeout)
	for {
		var checked int
		err := app.Pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM repositories WHERE last_checked_at IS NOT NULL").
			Scan(&checked)
		require.NoError(t, err)
		if checked >= repos {
			break
		}
		if time.Now().After(baselineDeadline) {
			t.Fatalf("scanner did not establish baseline for all %d repos within %s (checked=%d)",
				repos, timeout, checked)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Baseline established — now bump every repo's tag.
	t.Logf("baseline established for %d repos", repos)
	resp, err := http.Post(stub.URL+"/admin/release-all", "application/json", nil)
	require.NoError(t, err, "trigger release-all")
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Wait for the first release_notifications INSERT to land (i.e. scanner
	// observed the bump). Until then a naïve `WHERE sent_at IS NULL = 0` would
	// mis-report drain completion at t=0.
	firstRowDeadline := time.Now().Add(timeout)
	for {
		var total int
		err := app.Pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM release_notifications").Scan(&total)
		require.NoError(t, err)
		if total > 0 {
			break
		}
		if time.Now().After(firstRowDeadline) {
			t.Fatalf("scanner did not produce any release_notifications within %s", timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}

	start := time.Now()
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		var total, pending int
		err := app.Pool.QueryRow(ctx, `
			SELECT
				COUNT(*),
				COUNT(*) FILTER (WHERE sent_at IS NULL)
			FROM release_notifications`).Scan(&total, &pending)
		require.NoError(t, err)
		// Drain is "all rows the scanner produced have sent_at set", and the
		// scanner must have produced at least one row per seeded subscription.
		if total >= subs && pending == 0 {
			drain := time.Since(start)
			t.Logf("drain complete in %s (total=%d, %.1f rows/s)",
				drain, total, float64(total)/drain.Seconds())
			require.LessOrEqualf(t, drain.Seconds(), float64(slaSec),
				"drain took %.1fs which exceeds SLO of %ds (total=%d)", drain.Seconds(), slaSec, total)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("drain did not complete within %s", timeout)
}

func envInt(key string, dflt int) int {
	v := os.Getenv(key)
	if v == "" {
		return dflt
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drain_test: invalid %s=%q, using default %d\n", key, v, dflt)
		return dflt
	}
	return n
}

func envDur(key string, dflt time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return dflt
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drain_test: invalid %s=%q, using default %s\n", key, v, dflt)
		return dflt
	}
	return d
}
