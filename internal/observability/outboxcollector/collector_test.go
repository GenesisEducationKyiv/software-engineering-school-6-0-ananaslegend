package outboxcollector_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ananaslegend/reposeetory/internal/observability/outboxcollector"
)

// fakeQuerier returns predetermined counts per table (via the SQL string match).
type fakeQuerier struct {
	releaseN      int64
	confirmationN int64
	releaseErr    error
}

func (f *fakeQuerier) QueryRow(_ context.Context, sql string, _ ...any) outboxcollector.Row {
	switch {
	case strings.Contains(sql, "release_notifications"):
		return &fakeRow{n: f.releaseN, err: f.releaseErr}
	case strings.Contains(sql, "confirmation_notifications"):
		return &fakeRow{n: f.confirmationN}
	default:
		return &fakeRow{err: errors.New("unexpected table")}
	}
}

type fakeRow struct {
	n   int64
	err error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 0 {
		return errors.New("no scan target")
	}
	if p, ok := dest[0].(*int64); ok {
		*p = r.n
		return nil
	}
	return errors.New("unsupported scan target type")
}

func TestCollector_ReportsPendingCounts(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(outboxcollector.New(&fakeQuerier{releaseN: 3, confirmationN: 5}))

	const expected = `
# HELP release_notifications_pending Number of release_notifications rows with sent_at IS NULL.
# TYPE release_notifications_pending gauge
release_notifications_pending 3
# HELP confirmation_notifications_pending Number of confirmation_notifications rows with sent_at IS NULL.
# TYPE confirmation_notifications_pending gauge
confirmation_notifications_pending 5
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"release_notifications_pending", "confirmation_notifications_pending"); err != nil {
		t.Fatal(err)
	}
}

func TestCollector_QueryError_IncrementsErrorCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(outboxcollector.New(&fakeQuerier{releaseErr: errors.New("boom"), confirmationN: 0}))

	// GatherAndCompare triggers a scrape, which runs Collect once and increments
	// the error counter for the failing release_notifications query.
	const expected = `
# HELP outbox_collector_errors_total Total number of failures while querying outbox depth.
# TYPE outbox_collector_errors_total counter
outbox_collector_errors_total 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"outbox_collector_errors_total"); err != nil {
		t.Fatal(err)
	}
}
