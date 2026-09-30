package traffic

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"skifity/internal/kube"
)

const web = "acme-shop-production-web-80@kubernetes"

var testIndex, _ = NewIndex([]App{{ID: "app_web", Namespace: "acme-shop-production", Slug: "web"}})

// metrics writes a pod's answer: request counters by status code, and one
// latency histogram, as Traefik would.
func metrics(requests map[string]float64, buckets map[string]float64) []byte {
	var b strings.Builder
	b.WriteString("# TYPE traefik_service_requests_total counter\n")
	for code, n := range requests {
		fmt.Fprintf(&b, "traefik_service_requests_total{code=%q,method=\"GET\",protocol=\"http\",service=%q} %v\n", code, web, n)
	}
	b.WriteString("# TYPE traefik_service_request_duration_seconds histogram\n")
	for le, n := range buckets {
		fmt.Fprintf(&b, "traefik_service_request_duration_seconds_bucket{code=\"200\",method=\"GET\",protocol=\"http\",service=%q,le=%q} %v\n", web, le, n)
	}
	return []byte(b.String())
}

func pod(name, instance string, started time.Time, body []byte) kube.TraefikPod {
	return kube.TraefikPod{Name: name, Instance: instance, Started: started, Metrics: body}
}

// The first read is only something to compare with; the second is a minute,
// and it is the growth between them.
func TestAMinuteIsTheGrowthBetweenTwoReads(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	var c Collector
	first := c.Read(start, []kube.TraefikPod{
		pod("traefik-a", "uid-a/0", start.Add(-time.Hour), metrics(map[string]float64{"200": 100, "502": 4}, nil)),
	}, testIndex)
	if first.Counted || first.State != StateStarting {
		t.Fatalf("the first read counted: %+v", first)
	}

	second := c.Read(start.Add(time.Minute), []kube.TraefikPod{
		pod("traefik-a", "uid-a/0", start.Add(-time.Hour), metrics(map[string]float64{"200": 130, "502": 9, "404": 2}, nil)),
	}, testIndex)
	if !second.Counted || second.Partial || second.State != StateReading {
		t.Fatalf("the second read is %+v", second)
	}
	got := second.Apps["app_web"]
	// 30 more 200s, 5 more 502s, and two 404s that did not exist a minute ago:
	// a series Traefik created since is new, and all of it counts.
	if got == nil || got.Requests != 37 || got.Status2xx != 30 || got.Status5xx != 5 || got.Status4xx != 2 {
		t.Fatalf("the minute is %+v", got)
	}
}

// Several nodes each run Traefik and each counts the requests that arrived on
// it: the minute is the sum.
func TestSeveralTraefikPodsAreSummed(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	read := func(a, b float64) []kube.TraefikPod {
		return []kube.TraefikPod{
			pod("traefik-a", "uid-a/0", start.Add(-time.Hour), metrics(map[string]float64{"200": a},
				map[string]float64{"0.1": a, "+Inf": a})),
			pod("traefik-b", "uid-b/0", start.Add(-time.Hour), metrics(map[string]float64{"200": b},
				map[string]float64{"0.1": b / 2, "+Inf": b})),
		}
	}
	var c Collector
	c.Read(start, read(10, 20), testIndex)
	minute := c.Read(start.Add(time.Minute), read(15, 60), testIndex)
	got := minute.Apps["app_web"]
	if got == nil || got.Requests != 45 || got.buckets[0.1] != 25 || got.buckets[math.Inf(1)] != 45 {
		t.Fatalf("the minute is %+v", got)
	}
}

// Counters start again at zero whenever Traefik does, and whenever it drops a
// service from its configuration and brings it back. What they read then is
// all new, never a negative number of requests.
func TestACounterThatStartsAgainIsCountedFromZero(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	long := start.Add(-time.Hour)

	t.Run("a service dropped and brought back", func(t *testing.T) {
		var c Collector
		c.Read(start, []kube.TraefikPod{pod("traefik-a", "uid-a/0", long,
			metrics(map[string]float64{"200": 405}, map[string]float64{"0.1": 400, "0.3": 402, "+Inf": 405}))}, testIndex)
		// Traefik dropped the service and brought it back, and it has answered
		// 450 since. Only the fastest bucket reads less than before; the rest
		// read more, and would look like 45 new requests rather than 450.
		minute := c.Read(start.Add(time.Minute), []kube.TraefikPod{pod("traefik-a", "uid-a/0", long,
			metrics(map[string]float64{"200": 450}, map[string]float64{"0.1": 380, "0.3": 440, "+Inf": 450}))}, testIndex)
		got := minute.Apps["app_web"]
		if got == nil || got.Requests != 450 || got.buckets[0.1] != 380 || got.buckets[0.3] != 440 ||
			got.buckets[math.Inf(1)] != 450 {
			t.Fatalf("the minute is %+v", got)
		}
		// A counter that only grew is not taken for one that started again.
		minute = c.Read(start.Add(2*time.Minute), []kube.TraefikPod{pod("traefik-a", "uid-a/0", long,
			metrics(map[string]float64{"200": 460}, map[string]float64{"0.1": 389, "0.3": 450, "+Inf": 460}))}, testIndex)
		if got := minute.Apps["app_web"]; got == nil || got.Requests != 10 || got.buckets[0.1] != 9 {
			t.Fatalf("the next minute is %+v", got)
		}
	})

	t.Run("Traefik restarted since the last read", func(t *testing.T) {
		var c Collector
		c.Read(start, []kube.TraefikPod{pod("traefik-a", "uid-a/0", long, metrics(map[string]float64{"200": 500}, nil))}, testIndex)
		// Its container restarted thirty seconds ago: every counter began at
		// zero then, so the 12 it has are all this minute's — even though 12
		// is less than 500, and even if it had been more.
		minute := c.Read(start.Add(time.Minute), []kube.TraefikPod{pod("traefik-a", "uid-a/1", start.Add(30*time.Second),
			metrics(map[string]float64{"200": 12}, nil))}, testIndex)
		if got := minute.Apps["app_web"]; got == nil || got.Requests != 12 || minute.Partial {
			t.Fatalf("the minute is %+v, partial %v", got, minute.Partial)
		}
	})

	t.Run("a new pod that was already running", func(t *testing.T) {
		var c Collector
		c.Read(start, []kube.TraefikPod{pod("traefik-a", "uid-a/0", long, metrics(map[string]float64{"200": 5}, nil))}, testIndex)
		// A pod that started before the last read and was not read then has
		// counted for longer than a minute: it is remembered, not counted, and
		// the minute says it is missing something.
		minute := c.Read(start.Add(time.Minute), []kube.TraefikPod{
			pod("traefik-a", "uid-a/0", long, metrics(map[string]float64{"200": 9}, nil)),
			pod("traefik-b", "uid-b/0", long, metrics(map[string]float64{"200": 9000}, nil)),
		}, testIndex)
		if got := minute.Apps["app_web"]; got == nil || got.Requests != 4 || !minute.Partial {
			t.Fatalf("the minute is %+v, partial %v", got, minute.Partial)
		}
	})
}

// A pod that does not answer makes the minute partial and is compared with
// nothing next time; none answering is said; none running is said.
func TestPodsThatDoNotAnswer(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	long := start.Add(-time.Hour)
	var c Collector
	c.Read(start, []kube.TraefikPod{
		pod("traefik-a", "uid-a/0", long, metrics(map[string]float64{"200": 10}, nil)),
		pod("traefik-b", "uid-b/0", long, metrics(map[string]float64{"200": 10}, nil)),
	}, testIndex)

	minute := c.Read(start.Add(time.Minute), []kube.TraefikPod{
		pod("traefik-a", "uid-a/0", long, metrics(map[string]float64{"200": 13}, nil)),
		{Name: "traefik-b", Instance: "uid-b/0", Err: errors.New("connection refused")},
	}, testIndex)
	if got := minute.Apps["app_web"]; got == nil || got.Requests != 3 || !minute.Partial || minute.State != StateReading {
		t.Fatalf("the minute is %+v, partial %v, state %s", got, minute.Partial, minute.State)
	}

	// An answer that is not the format is the same as no answer, and is said.
	minute = c.Read(start.Add(2*time.Minute), []kube.TraefikPod{
		pod("traefik-a", "uid-a/0", long, []byte("<html>502 Bad Gateway</html>")),
		pod("traefik-b", "uid-b/0", long, metrics(map[string]float64{"200": 20}, nil)),
	}, testIndex)
	if minute.Counted || len(minute.Unreadable) != 1 || minute.State != StateStarting {
		t.Fatalf("an unreadable pod and one read again for the first time counted: %+v", minute)
	}

	minute = c.Read(start.Add(3*time.Minute), []kube.TraefikPod{
		{Name: "traefik-a", Err: errors.New("timeout")}, {Name: "traefik-b", Err: errors.New("timeout")},
	}, testIndex)
	if minute.Counted || minute.State != StateUnreachable {
		t.Fatalf("nothing answering is %+v", minute)
	}
	if minute = c.Read(start.Add(4*time.Minute), nil, testIndex); minute.State != StateNoTraefik {
		t.Fatalf("no Traefik at all is %s", minute.State)
	}
}

// Reads too far apart are not compared: their difference belongs to several
// minutes, and drawing it as one is a spike that never happened.
func TestReadsTooFarApartAreNotCompared(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	long := start.Add(-time.Hour)
	var c Collector
	c.Read(start, []kube.TraefikPod{pod("traefik-a", "uid-a/0", long, metrics(map[string]float64{"200": 10}, nil))}, testIndex)
	minute := c.Read(start.Add(10*time.Minute), []kube.TraefikPod{pod("traefik-a", "uid-a/0", long,
		metrics(map[string]float64{"200": 900}, nil))}, testIndex)
	if minute.Counted {
		t.Fatalf("ten minutes were drawn as one: %+v", minute.Apps["app_web"])
	}
}

// A WebSocket or an event stream is a request, and how long it stayed open is
// not how long an answer took. 1xx answers — the WebSocket upgrade — are in
// the total and in none of the classes.
func TestStreamsAreCountedButNotTimed(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	long := start.Add(-time.Hour)
	body := func(n float64) []byte {
		return []byte(fmt.Sprintf(`traefik_service_requests_total{code="101",method="GET",protocol="websocket",service=%[1]q} %[2]v
traefik_service_request_duration_seconds_bucket{code="101",method="GET",protocol="websocket",service=%[1]q,le="5"} 0
traefik_service_request_duration_seconds_bucket{code="101",method="GET",protocol="websocket",service=%[1]q,le="+Inf"} %[2]v
traefik_service_requests_total{code="200",method="GET",protocol="http",service=%[1]q} %[2]v
traefik_service_request_duration_seconds_bucket{code="200",method="GET",protocol="http",service=%[1]q,le="5"} %[2]v
traefik_service_request_duration_seconds_bucket{code="200",method="GET",protocol="http",service=%[1]q,le="+Inf"} %[2]v
`, web, n))
	}
	var c Collector
	c.Read(start, []kube.TraefikPod{pod("traefik-a", "uid-a/0", long, body(1))}, testIndex)
	minute := c.Read(start.Add(time.Minute), []kube.TraefikPod{pod("traefik-a", "uid-a/0", long, body(11))}, testIndex)
	got := minute.Apps["app_web"]
	if got == nil || got.Requests != 20 || got.Status2xx != 10 || got.buckets[math.Inf(1)] != 10 {
		t.Fatalf("the minute is %+v", got)
	}
	if p95, ok := got.Quantile(0.95); !ok || p95 > 5 {
		t.Fatalf("a connection held open became a response time: %v", p95)
	}
}

// The percentile is estimated the way Prometheus's histogram_quantile does it,
// so it reads the same as it would in Grafana.
func TestPercentilesAreEstimatedFromTheBuckets(t *testing.T) {
	inf := math.Inf(1)
	cases := []struct {
		name    string
		buckets map[float64]float64
		q, want float64
		ok      bool
	}{
		// 50 of 100 under 0.1s, 90 under 0.3s, all under 1.2s.
		{"the median at a bound", map[float64]float64{0.1: 50, 0.3: 90, 1.2: 100, inf: 100}, 0.5, 0.1, true},
		// The 95th of 100 is half way through the ten between 0.3s and 1.2s.
		{"interpolated inside a bucket", map[float64]float64{0.1: 50, 0.3: 90, 1.2: 100, inf: 100}, 0.95, 0.75, true},
		// The first bucket starts at zero.
		{"inside the first bucket", map[float64]float64{0.1: 100, inf: 100}, 0.5, 0.05, true},
		// Past the last finite bound, all that can be said is "at least".
		{"in the +Inf bucket", map[float64]float64{0.1: 10, 5: 50, inf: 100}, 0.95, 5, true},
		// Pods whose buckets were set differently can make a later bucket
		// smaller than an earlier one; each counts as at least the one before.
		{"not quite cumulative", map[float64]float64{0.1: 60, 0.3: 40, 1.2: 100, inf: 100}, 0.5, 0.1 * 50 / 60, true},
		{"nothing timed", map[float64]float64{0.1: 0, inf: 0}, 0.5, 0, false},
		{"no +Inf bucket", map[float64]float64{0.1: 5}, 0.5, 0, false},
		{"no buckets", nil, 0.5, 0, false},
	}
	for _, tc := range cases {
		got, ok := Totals{buckets: tc.buckets}.Quantile(tc.q)
		if ok != tc.ok || math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: p%v is %v (%v), want %v (%v)", tc.name, tc.q*100, got, ok, tc.want, tc.ok)
		}
	}

	// And what is kept is in milliseconds, with nothing when nothing was timed.
	sample := Totals{Requests: 100, buckets: map[float64]float64{0.1: 50, 0.3: 90, 1.2: 100, inf: 100}}.Sample(time.Time{}, false)
	if sample.P50Ms == nil || *sample.P50Ms != 100 || sample.P95Ms == nil || *sample.P95Ms != 750 {
		t.Fatalf("kept as %+v", sample)
	}
	if sample := (Totals{Requests: 3}).Sample(time.Time{}, false); sample.P50Ms != nil || sample.P95Ms != nil {
		t.Fatalf("a minute nothing was timed in has percentiles: %+v", sample)
	}
}
