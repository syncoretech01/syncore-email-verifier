package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func render(r *Registry) string {
	var b strings.Builder
	r.WriteText(&b)
	return b.String()
}

func TestCounter_AccumulatesByLabel(t *testing.T) {
	r := New()
	r.CounterInc("verifications_total", "verifications", map[string]string{"status": "valid"})
	r.CounterInc("verifications_total", "verifications", map[string]string{"status": "valid"})
	r.CounterInc("verifications_total", "verifications", map[string]string{"status": "invalid"})

	out := render(r)
	assert.Contains(t, out, "# TYPE verifications_total counter")
	assert.Contains(t, out, `verifications_total{status="valid"} 2`)
	assert.Contains(t, out, `verifications_total{status="invalid"} 1`)
}

func TestCounter_NoLabels(t *testing.T) {
	r := New()
	r.CounterAdd("events_total", "events", nil, 5)
	assert.Contains(t, render(r), "events_total 5")
}

func TestHistogram_BucketsAndSum(t *testing.T) {
	r := New()
	r.ObserveLatency("verify_seconds", "verify latency", 30*time.Millisecond)  // <= 0.05
	r.ObserveLatency("verify_seconds", "verify latency", 300*time.Millisecond) // <= 0.5
	out := render(r)
	require.Contains(t, out, "# TYPE verify_seconds histogram")
	assert.Contains(t, out, `verify_seconds_bucket{le="0.05"} 1`) // only the 30ms sample
	assert.Contains(t, out, `verify_seconds_bucket{le="0.5"} 2`)  // both samples
	assert.Contains(t, out, `verify_seconds_bucket{le="+Inf"} 2`)
	assert.Contains(t, out, "verify_seconds_count 2")
}

func TestConcurrentSafe(t *testing.T) {
	r := New()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				r.CounterInc("c_total", "c", map[string]string{"k": "v"})
				r.ObserveLatency("h_seconds", "h", time.Millisecond)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	assert.Contains(t, render(r), `c_total{k="v"} 800`)
}
