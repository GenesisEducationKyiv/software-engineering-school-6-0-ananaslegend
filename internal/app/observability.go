package app

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/ananaslegend/reposeetory/internal/observability/redmetrics"
)

// reds bundles all RED metric pairs constructed at composition time.
type reds struct {
	GithubClient *redmetrics.RED
	Email        *redmetrics.RED
	Scanner      *redmetrics.RED
	Notifier     *redmetrics.RED
	Confirmer    *redmetrics.RED
}

var longBuckets = []float64{0.05, 0.1, 0.5, 1, 5, 10, 30, 60, 120}

func newREDs(reg *prometheus.Registry) reds {
	return reds{
		GithubClient: redmetrics.New(redmetrics.Config{Subsystem: "github_client", Registry: reg}),
		Email:        redmetrics.New(redmetrics.Config{Subsystem: "email", ExtraLabels: []string{"driver"}, Registry: reg}),
		Scanner:      redmetrics.New(redmetrics.Config{Subsystem: "scanner", Buckets: longBuckets, Registry: reg}),
		Notifier:     redmetrics.New(redmetrics.Config{Subsystem: "notifier", Buckets: longBuckets, Registry: reg}),
		Confirmer:    redmetrics.New(redmetrics.Config{Subsystem: "confirmer", Buckets: longBuckets, Registry: reg}),
	}
}
