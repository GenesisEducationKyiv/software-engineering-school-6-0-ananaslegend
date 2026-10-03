package notifier

import "github.com/prometheus/client_golang/prometheus"

type notifierMetrics struct {
	emailsSent    *prometheus.CounterVec
	flushDuration prometheus.Histogram
}

func newNotifierMetrics(reg *prometheus.Registry) notifierMetrics {
	m := notifierMetrics{
		emailsSent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notifier_emails_sent_total",
			Help: "Total number of release emails attempted.",
		}, []string{"result"}),
		flushDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "notifier_flush_duration_seconds",
			Help:    "Duration of one notifier Flush() call in seconds.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30},
		}),
	}
	if reg != nil {
		reg.MustRegister(m.emailsSent, m.flushDuration)
	}
	return m
}
