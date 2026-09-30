// Package metrics exposes what the panel is doing, in the one format every
// monitoring system already reads.
//
// The panel watches the cluster and nothing watched the panel. On a self-hosted
// box that is the wrong way round: the cluster has a dozen tools that will tell
// you about it, and the single process holding every secret, every deployment
// and the only database had no way to say how it was getting on.
//
// Written by hand rather than pulled in. The exposition format is a hundred
// lines of text and the client library is a dependency tree, a registry with
// global state and a second HTTP handler, for a page that is four kinds of
// number. The same reasoning as the SQL and the TOTP elsewhere in this
// codebase.
package metrics

import (
	"fmt"
	"io"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Buckets are the request-duration boundaries, in seconds.
//
// Chosen around what this panel actually does: a page of JSON out of SQLite is
// single-digit milliseconds, a call that waits on the Kubernetes API is tens to
// hundreds, and anything past a second is worth a bucket of its own because it
// is the thing somebody is complaining about.
var Buckets = []float64{0.005, 0.025, 0.1, 0.5, 1, 5}

// Registry holds the panel's metrics.
//
// Safe for concurrent use: every handler writes to it and one reader formats
// it.
type Registry struct {
	mu sync.RWMutex

	descriptions map[string]description
	order        []string

	counters map[string]float64
	gauges   map[string]float64
	series   map[string]*histogram

	// collectors are run at scrape time for values that are read rather than
	// counted — how many apps exist, how large the database is. Sampling them
	// on a scrape rather than keeping them current means nothing has to
	// remember to update a gauge when a row is written.
	collectors []func(*Registry)
}

type description struct {
	help string
	kind string
}

type histogram struct {
	counts []uint64
	sum    float64
	total  uint64
}

// New builds an empty registry with the panel's own descriptions in it.
func New() *Registry {
	return &Registry{
		descriptions: map[string]description{},
		counters:     map[string]float64{},
		gauges:       map[string]float64{},
		series:       map[string]*histogram{},
	}
}

// Describe records what a metric means. A metric with no description is still
// exported, because leaving out a number because nobody wrote a sentence about
// it would be the wrong trade.
func (r *Registry) Describe(name, kind, help string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.descriptions[name] = description{help: help, kind: kind}
}

// Collect registers a function run at scrape time.
func (r *Registry) Collect(fn func(*Registry)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.collectors = append(r.collectors, fn)
}

// Inc adds one to a counter. Labels are alternating names and values.
func (r *Registry) Inc(name string, labels ...string) { r.Add(name, 1, labels...) }

// Add adds to a counter.
func (r *Registry) Add(name string, delta float64, labels ...string) {
	if r == nil {
		return
	}
	key := seriesKey(name, labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.remember(name, "counter")
	r.counters[key] += delta
}

// SetGauge records a value that goes up and down.
func (r *Registry) SetGauge(name string, value float64, labels ...string) {
	if r == nil {
		return
	}
	key := seriesKey(name, labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.remember(name, "gauge")
	r.gauges[key] = value
}

// Observe records one measurement in a histogram.
func (r *Registry) Observe(name string, value float64, labels ...string) {
	if r == nil {
		return
	}
	key := seriesKey(name, labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.remember(name, "histogram")

	h := r.series[key]
	if h == nil {
		h = &histogram{counts: make([]uint64, len(Buckets))}
		r.series[key] = h
	}
	h.total++
	h.sum += value
	for i, upper := range Buckets {
		if value <= upper {
			h.counts[i]++
		}
	}
}

// remember records the order a metric was first seen in, so the output is
// stable between scrapes. Caller holds the lock.
func (r *Registry) remember(name, kind string) {
	if _, seen := r.descriptions[name]; !seen {
		r.descriptions[name] = description{kind: kind}
		r.order = append(r.order, name)
		return
	}
	for _, existing := range r.order {
		if existing == name {
			return
		}
	}
	r.order = append(r.order, name)
}

// Write renders the registry in the Prometheus text exposition format.
//
// Not WriteTo: that name belongs to io.WriterTo, whose contract is a byte count
// this has no use for.
func (r *Registry) Write(w io.Writer) error {
	// Collectors run outside the lock they will take themselves.
	r.mu.RLock()
	collectors := append([]func(*Registry){}, r.collectors...)
	r.mu.RUnlock()
	for _, collect := range collectors {
		collect(r)
	}
	r.collectRuntime()

	r.mu.RLock()
	defer r.mu.RUnlock()

	var b strings.Builder
	for _, name := range r.order {
		desc := r.descriptions[name]
		if desc.help != "" {
			fmt.Fprintf(&b, "# HELP %s %s\n", name, desc.help)
		}
		if desc.kind != "" {
			fmt.Fprintf(&b, "# TYPE %s %s\n", name, desc.kind)
		}
		switch desc.kind {
		case "histogram":
			r.writeHistogram(&b, name)
		case "gauge":
			writeSimple(&b, name, r.gauges)
		default:
			writeSimple(&b, name, r.counters)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeSimple(b *strings.Builder, name string, values map[string]float64) {
	keys := make([]string, 0, len(values))
	for key := range values {
		if metricName(key) == name {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(b, "%s %s\n", key, formatValue(values[key]))
	}
}

func (r *Registry) writeHistogram(b *strings.Builder, name string) {
	keys := make([]string, 0, len(r.series))
	for key := range r.series {
		if metricName(key) == name {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	for _, key := range keys {
		h := r.series[key]
		labels := labelsOf(key)
		for i, upper := range Buckets {
			fmt.Fprintf(b, "%s_bucket%s %d\n",
				name, withLabel(labels, "le", strconv.FormatFloat(upper, 'g', -1, 64)), h.counts[i])
		}
		// The +Inf bucket is required, and is the total by definition.
		fmt.Fprintf(b, "%s_bucket%s %d\n", name, withLabel(labels, "le", "+Inf"), h.total)
		fmt.Fprintf(b, "%s_sum%s %s\n", name, labels, formatValue(h.sum))
		fmt.Fprintf(b, "%s_count%s %d\n", name, labels, h.total)
	}
}

// collectRuntime records what the Go runtime knows about this process.
//
// Goroutines and heap are the two numbers that say "this panel is leaking", and
// a leak in a process that is meant to run for months on a 2 GB box is the
// failure worth catching early.
func (r *Registry) collectRuntime() {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	r.SetGauge("skifity_goroutines", float64(runtime.NumGoroutine()))
	r.SetGauge("skifity_memory_heap_bytes", float64(mem.HeapAlloc))
	r.SetGauge("skifity_memory_resident_bytes", float64(mem.Sys))
	r.SetGauge("skifity_gc_total", float64(mem.NumGC))
}

// StartedAt records the process start, so uptime is a metric rather than
// something to work out from a log line.
var StartedAt = time.Now()

// seriesKey renders a metric name with its labels, which is also the line's
// own prefix in the output.
func seriesKey(name string, labels []string) string {
	if len(labels) < 2 {
		return name
	}
	pairs := make([]string, 0, len(labels)/2)
	for i := 0; i+1 < len(labels); i += 2 {
		// Quoted by hand rather than with %q, which escapes as Go does and
		// would double every backslash escape put in here.
		pairs = append(pairs, labels[i]+`="`+escape(labels[i+1])+`"`)
	}
	sort.Strings(pairs)
	return name + "{" + strings.Join(pairs, ",") + "}"
}

func metricName(key string) string {
	if i := strings.IndexByte(key, '{'); i >= 0 {
		return key[:i]
	}
	return key
}

func labelsOf(key string) string {
	if i := strings.IndexByte(key, '{'); i >= 0 {
		return key[i:]
	}
	return ""
}

// withLabel adds one more label to a rendered label set, which is how a
// histogram's bucket boundary is attached.
func withLabel(labels, name, value string) string {
	pair := name + `="` + escape(value) + `"`
	if labels == "" {
		return "{" + pair + "}"
	}
	return labels[:len(labels)-1] + "," + pair + "}"
}

// escape makes a label value safe for the exposition format, which is
// line-based and quotes its values.
func escape(value string) string {
	return strings.NewReplacer("\\", `\\`, "\n", `\n`, `"`, `\"`).Replace(value)
}

// formatValue renders a number the way the format expects.
//
// Plain decimal rather than 'g': the exposition format accepts an exponent, but
// a byte count that reads 8.345864e+06 in a terminal is one somebody has to
// convert in their head, and this page is read by people as often as by
// Prometheus.
func formatValue(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
