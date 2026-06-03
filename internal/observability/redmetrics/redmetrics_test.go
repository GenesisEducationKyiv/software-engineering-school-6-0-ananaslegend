package redmetrics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ananaslegend/reposeetory/internal/observability/redmetrics"
)

func TestNew_RegistersMetricsWithCorrectNames(t *testing.T) {
	reg := prometheus.NewRegistry()
	red := redmetrics.New(redmetrics.Config{Subsystem: "demo", Registry: reg})

	red.Observe("ok", 10*time.Millisecond)

	const expected = `
# HELP demo_requests_total Total number of demo operations.
# TYPE demo_requests_total counter
demo_requests_total{result="ok"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "demo_requests_total"); err != nil {
		t.Fatal(err)
	}
}

func TestNew_NilRegistryDoesNotPanic(t *testing.T) {
	red := redmetrics.New(redmetrics.Config{Subsystem: "demo"})
	red.Observe("ok", 5*time.Millisecond) // must not panic
}

func TestNew_WithExtraLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	red := redmetrics.New(redmetrics.Config{
		Subsystem:   "email",
		ExtraLabels: []string{"driver"},
		Registry:    reg,
	})

	red.Observe("ok", 5*time.Millisecond, "resend")
	red.Observe("error", 5*time.Millisecond, "smtp")

	const expected = `
# HELP email_requests_total Total number of email operations.
# TYPE email_requests_total counter
email_requests_total{driver="resend",result="ok"} 1
email_requests_total{driver="smtp",result="error"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "email_requests_total"); err != nil {
		t.Fatal(err)
	}
}
