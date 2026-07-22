// Package metrics is a tiny, dependency-free metrics registry that exposes the
// Prometheus text exposition format. It supports labeled counters and a fixed
// latency histogram — enough to observe the service without pulling in a client
// library. It is safe for concurrent use.
package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Registry holds all metrics.
type Registry struct {
	mu       sync.Mutex
	counters map[string]*counter // metric name -> counter family
	hists    map[string]*histogram
}

// New builds an empty registry.
func New() *Registry {
	return &Registry{counters: map[string]*counter{}, hists: map[string]*histogram{}}
}

type counter struct {
	help   string
	series map[string]float64 // canonical label string -> value
}

type histogram struct {
	help    string
	bounds  []float64
	buckets []uint64
	count   uint64
	sum     float64
}

// CounterInc increments a labeled counter by 1, creating it on first use.
func (r *Registry) CounterInc(name, help string, labels map[string]string) {
	r.CounterAdd(name, help, labels, 1)
}

// CounterAdd adds delta to a labeled counter.
func (r *Registry) CounterAdd(name, help string, labels map[string]string, delta float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.counters[name]
	if !ok {
		c = &counter{help: help, series: map[string]float64{}}
		r.counters[name] = c
	}
	c.series[canonicalLabels(labels)] += delta
}

// ObserveLatency records a duration in the named histogram (seconds).
func (r *Registry) ObserveLatency(name, help string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.hists[name]
	if !ok {
		bounds := []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}
		h = &histogram{help: help, bounds: bounds, buckets: make([]uint64, len(bounds))}
		r.hists[name] = h
	}
	sec := d.Seconds()
	for i, b := range h.bounds {
		if sec <= b {
			h.buckets[i]++
		}
	}
	h.count++
	h.sum += sec
}

// WriteText writes the Prometheus text exposition format.
func (r *Registry) WriteText(w *strings.Builder) {
	r.mu.Lock()
	defer r.mu.Unlock()

	names := make([]string, 0, len(r.counters))
	for n := range r.counters {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := r.counters[n]
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", n, c.help, n)
		series := make([]string, 0, len(c.series))
		for lbl := range c.series {
			series = append(series, lbl)
		}
		sort.Strings(series)
		for _, lbl := range series {
			fmt.Fprintf(w, "%s%s %g\n", n, lbl, c.series[lbl])
		}
	}

	hnames := make([]string, 0, len(r.hists))
	for n := range r.hists {
		hnames = append(hnames, n)
	}
	sort.Strings(hnames)
	for _, n := range hnames {
		h := r.hists[n]
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", n, h.help, n)
		for i, b := range h.bounds {
			fmt.Fprintf(w, "%s_bucket{le=\"%g\"} %d\n", n, b, h.buckets[i])
		}
		fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", n, h.count)
		fmt.Fprintf(w, "%s_sum %g\n", n, h.sum)
		fmt.Fprintf(w, "%s_count %d\n", n, h.count)
	}
}

// canonicalLabels renders labels as a sorted `{k="v",...}` string (or "" if none).
func canonicalLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%q", k, labels[k])
	}
	b.WriteByte('}')
	return b.String()
}
