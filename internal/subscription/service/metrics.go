package service

import "github.com/prometheus/client_golang/prometheus"

type serviceMetrics struct {
	subscriptionsCreated   prometheus.Counter
	subscriptionsConfirmed prometheus.Counter
	subscriptionsDeleted   prometheus.Counter
}

func newServiceMetrics(reg *prometheus.Registry) serviceMetrics {
	m := serviceMetrics{
		subscriptionsCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "subscriptions_created_total",
			Help: "Total number of subscriptions created.",
		}),
		subscriptionsConfirmed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "subscriptions_confirmed_total",
			Help: "Total number of subscriptions confirmed.",
		}),
		subscriptionsDeleted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "subscriptions_deleted_total",
			Help: "Total number of subscriptions deleted (unsubscribe).",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.subscriptionsCreated, m.subscriptionsConfirmed, m.subscriptionsDeleted)
	}
	return m
}
