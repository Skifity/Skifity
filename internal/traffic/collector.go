package traffic

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"skifity/internal/kube"
	"skifity/internal/store"
)

// The metrics read. Traefik's service-level series: requests by status code,
// method and protocol, and the time each took as a histogram with the same
// labels. The histogram's _sum and _count are not needed: the count is the
// +Inf bucket, and the requests are their own counter.
const (
	requestsMetric = "traefik_service_requests_total"
	bucketsMetric  = "traefik_service_request_duration_seconds_bucket"
)

// MaxGap is the longest two reads can be apart and still be compared. The
// watcher reads once a minute; a pass that was skipped, or a panel with no
// apps for a while, leaves counters whose growth belongs to more than one
// minute, and putting it all in one would draw a spike that never happened.
const MaxGap = 150 * time.Second

// counter is one series of one pod: a request counter, or one bucket of a
// latency histogram.
type counter struct {
	service string
	code    string
	bucket  bool
	le      float64
	// stream is a WebSocket or an event stream. Its duration is how long the
	// connection stayed open, not how long an answer took, so it is counted
	// as a request and left out of the latency.
	stream bool
	value  float64
}

// reading is one pod's counters, by series.
type reading map[string]counter

// readPod parses one pod's answer into its counters.
func readPod(body []byte) (reading, error) {
	samples, err := Parse(bytes.NewReader(body), func(name string) bool {
		return name == requestsMetric || name == bucketsMetric
	})
	if err != nil {
		return nil, err
	}
	out := make(reading, len(samples))
	for _, s := range samples {
		// A counter is never negative and never not a number; one that is
		// means this is not the answer it looks like.
		if s.Value < 0 || math.IsNaN(s.Value) || math.IsInf(s.Value, 0) {
			return nil, fmt.Errorf("%s is %v, which a counter never is", s.Name, s.Value)
		}
		service := s.Labels["service"]
		if service == "" {
			continue
		}
		c := counter{
			service: service,
			code:    s.Labels["code"],
			bucket:  s.Name == bucketsMetric,
			stream:  s.Labels["protocol"] == "websocket" || s.Labels["protocol"] == "sse",
			value:   s.Value,
		}
		if c.bucket {
			le, err := strconv.ParseFloat(s.Labels["le"], 64)
			if err != nil || math.IsNaN(le) {
				return nil, fmt.Errorf("%s has a bucket bound of %q", s.Name, s.Labels["le"])
			}
			c.le = le
		}
		key := seriesKey(s.Name, s.Labels)
		if _, twice := out[key]; twice {
			return nil, fmt.Errorf("%s is written twice", key)
		}
		out[key] = c
	}
	return out, nil
}

// seriesKey names a series by its metric and its labels, in a fixed order, so
// the same series read twice has the same key.
func seriesKey(name string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteString(name)
	for _, k := range keys {
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(labels[k]))
	}
	return b.String()
}

// increase is how much each counter grew between two reads of the same
// process.
//
// A counter that was not there before is new since then, and all of it
// counts: Traefik creates a series on its first request. A counter lower than
// it was has started again, and what it reads now is all new.
//
// Within one process that happens one way: Traefik deletes every series of a
// service that has left its configuration — an Ingress removed, an app with
// nothing to route to — and starts them from zero when it comes back. They go
// and come back together, so one of a service's counters going down means all
// of them started again, including the ones that happen to read more than
// they did, which would otherwise be counted as a small increase. With
// nothing before, everything counts.
func increase(before, now reading) map[string]float64 {
	restarted := map[string]bool{}
	for key, c := range now {
		if was, ok := before[key]; ok && c.value < was.value {
			restarted[c.service] = true
		}
	}
	out := make(map[string]float64, len(now))
	for key, c := range now {
		was, ok := before[key]
		if !ok || restarted[c.service] {
			out[key] = c.value
			continue
		}
		out[key] = c.value - was.value
	}
	return out
}

// Totals is one app's minute, summed across every Traefik pod.
type Totals struct {
	Requests, Status2xx, Status3xx, Status4xx, Status5xx float64
	// buckets is the latency histogram's growth, by upper bound in seconds:
	// cumulative, as Prometheus's are.
	buckets map[float64]float64
}

func (t *Totals) add(c counter, grew float64) {
	if grew <= 0 {
		return
	}
	if c.bucket {
		if c.stream {
			return
		}
		if t.buckets == nil {
			t.buckets = map[float64]float64{}
		}
		t.buckets[c.le] += grew
		return
	}
	t.Requests += grew
	if len(c.code) == 3 {
		switch c.code[0] {
		case '2':
			t.Status2xx += grew
		case '3':
			t.Status3xx += grew
		case '4':
			t.Status4xx += grew
		case '5':
			t.Status5xx += grew
		}
	}
}

// Quantile estimates the q-quantile of the minute's response times, in
// seconds, the way Prometheus's histogram_quantile does: find the bucket the
// rank falls in, and assume its requests are spread evenly between its bounds.
// The first bucket starts at zero. A rank past the last finite bound can only
// be said to be at least that bound, which is what is returned.
//
// It is an estimate, and only as fine as the buckets: with Traefik's defaults
// (0.1, 0.3, 1.2 and 5 seconds) every app that answers in under a tenth of a
// second has a median of about 50 ms. The installer asks for finer ones.
//
// ok is false when nothing was timed.
func (t Totals) Quantile(q float64) (float64, bool) {
	bounds := make([]float64, 0, len(t.buckets))
	for le := range t.buckets {
		bounds = append(bounds, le)
	}
	slices.Sort(bounds)
	if len(bounds) < 2 || !math.IsInf(bounds[len(bounds)-1], 1) {
		return 0, false
	}
	// Buckets are cumulative, so each holds at least as many as the one before.
	// Pods whose buckets were set differently, or a series that restarted
	// between reads, can break that; Prometheus assumes it, and so does this.
	counts := make([]float64, len(bounds))
	for i, le := range bounds {
		counts[i] = t.buckets[le]
		if i > 0 {
			counts[i] = max(counts[i], counts[i-1])
		}
	}
	total := counts[len(counts)-1]
	if total <= 0 {
		return 0, false
	}
	rank := q * total
	last := len(bounds) - 1
	i := 0
	for i < last && counts[i] < rank {
		i++
	}
	if i == last {
		return bounds[last-1], true
	}
	lower, below := 0.0, 0.0
	if i > 0 {
		lower, below = bounds[i-1], counts[i-1]
	}
	if counts[i] == below {
		return bounds[i], true
	}
	return lower + (bounds[i]-lower)*(rank-below)/(counts[i]-below), true
}

// Sample is the minute as the panel keeps it.
func (t Totals) Sample(at time.Time, partial bool) store.AppTraffic {
	sample := store.AppTraffic{
		At:        at,
		Requests:  int64(math.Round(t.Requests)),
		Status2xx: int64(math.Round(t.Status2xx)),
		Status3xx: int64(math.Round(t.Status3xx)),
		Status4xx: int64(math.Round(t.Status4xx)),
		Status5xx: int64(math.Round(t.Status5xx)),
		Partial:   partial,
	}
	if p50, ok := t.Quantile(0.5); ok {
		ms := math.Round(p50*1000*10) / 10
		sample.P50Ms = &ms
	}
	if p95, ok := t.Quantile(0.95); ok {
		ms := math.Round(p95*1000*10) / 10
		sample.P95Ms = &ms
	}
	return sample
}

// Minute is what one read found.
type Minute struct {
	// State is the ingress's, for the panel to say why nothing is drawn.
	State string
	// Counted is false when nothing could be counted — the first read, no pod
	// answering — and the minute is not one to keep.
	Counted bool
	// Partial is true when some of the minute's traffic could not be counted:
	// a pod that did not answer, one read for the first time, one that went
	// away since the last read. What was counted is still right; it is not all.
	Partial bool
	// Apps holds the apps anything was counted for. An app that is not here
	// had no requests.
	Apps map[string]*Totals
	// Unreadable says why each pod that answered something unreadable could
	// not be read, for the log.
	Unreadable []error
}

// Collector turns reads a minute apart into minutes. It remembers each pod's
// counters from the last read, and nothing else; a restarted panel starts
// again with a read to compare the next one with.
type Collector struct {
	pods map[string]baseline
	last time.Time
}

type baseline struct {
	instance string
	counters reading
}

// Forget drops what was remembered, when the pods could not even be listed:
// the next read is compared with nothing rather than across the gap.
func (c *Collector) Forget() {
	c.pods, c.last = nil, time.Time{}
}

// Read turns one read of every Traefik pod into a minute.
//
// Each pod counts only the requests that arrived on its node, so the minute is
// the sum of every pod's growth since the last read. A pod is compared with
// what it said last time when it is the same process; one that started since
// the last read counts everything it has; one that is neither — read for the
// first time, or last time did not answer — is only remembered, and the minute
// says it is partial.
func (c *Collector) Read(at time.Time, pods []kube.TraefikPod, index Index) Minute {
	minute := Minute{Apps: map[string]*Totals{}}
	previous := c.last
	if !previous.IsZero() && at.Sub(previous) > MaxGap {
		c.pods, previous = nil, time.Time{}
	}
	c.last = at
	if len(pods) == 0 {
		c.pods = nil
		minute.State = StateNoTraefik
		return minute
	}

	next := map[string]baseline{}
	answered := 0
	for _, pod := range pods {
		if pod.Err != nil {
			minute.Partial = true
			continue
		}
		now, err := readPod(pod.Metrics)
		if err != nil {
			minute.Partial = true
			minute.Unreadable = append(minute.Unreadable, fmt.Errorf("the Traefik pod %s: %w", pod.Name, err))
			continue
		}
		answered++
		next[pod.Name] = baseline{instance: pod.Instance, counters: now}

		var grew map[string]float64
		before, known := c.pods[pod.Name]
		switch {
		case known && before.instance == pod.Instance:
			grew = increase(before.counters, now)
		case !previous.IsZero() && pod.Started.After(previous):
			// Started since the last read: every counter began at zero after
			// it, so all of it is this minute's.
			grew = increase(nil, now)
		default:
			minute.Partial = true
			continue
		}
		minute.Counted = true
		for key, delta := range grew {
			series := now[key]
			app, ok := index.AppFor(series.service)
			if !ok {
				continue
			}
			totals := minute.Apps[app]
			if totals == nil {
				totals = &Totals{}
				minute.Apps[app] = totals
			}
			totals.add(series, delta)
		}
	}
	// A pod that went away took with it whatever it counted since the last
	// read.
	for name := range c.pods {
		if _, still := next[name]; !still {
			minute.Partial = true
		}
	}
	c.pods = next

	switch {
	case answered == 0:
		minute.State = StateUnreachable
	case !minute.Counted:
		minute.State = StateStarting
	default:
		minute.State = StateReading
	}
	return minute
}
