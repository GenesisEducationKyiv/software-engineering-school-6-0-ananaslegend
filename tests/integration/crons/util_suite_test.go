//go:build integration

package crons_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

// labelsMatch reports whether observed contains every k=v in want. An empty
// want map matches any labelset.
func labelsMatch(observed []*dto.LabelPair, want map[string]string) bool {
	if len(want) == 0 {
		return true
	}
	have := make(map[string]string, len(observed))
	for _, p := range observed {
		have[p.GetName()] = p.GetValue()
	}
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

// requireCounter sums every observed sample of the counter named name whose
// labels include every k=v in want, and asserts the total equals expected.
// A counter that has never been incremented is absent from Gather() output,
// so the loop naturally yields 0 in that case.
func requireCounter(t *testing.T, reg *prometheus.Registry, name string, want map[string]string, expected float64) {
	t.Helper()
	mf, err := reg.Gather()
	require.NoError(t, err)

	var got float64
	for _, fam := range mf {
		if fam.GetName() != name {
			continue
		}
		for _, m := range fam.GetMetric() {
			if !labelsMatch(m.GetLabel(), want) {
				continue
			}
			got += m.GetCounter().GetValue()
		}
	}
	require.Equal(t, expected, got, "%s%v", name, want)
}
