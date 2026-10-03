package confirmer

import "github.com/prometheus/client_golang/prometheus"

type confirmerMetrics struct {
	emailsSent *prometheus.CounterVec
}

func newConfirmerMetrics(reg *prometheus.Registry) confirmerMetrics {
	m := confirmerMetrics{
		emailsSent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "confirmer_emails_sent_total",
			Help: "Total number of confirmation emails attempted.",
		}, []string{"result"}),
	}
	if reg != nil {
		reg.MustRegister(m.emailsSent)
	}
	return m
}
