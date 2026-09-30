package metrics

import (
	"strings"
	"sync"
	"testing"
)

// render is what a scrape would see.
func render(t *testing.T, r *Registry) string {
	t.Helper()
	var b strings.Builder
	if err := r.Write(&b); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return b.String()
}

func TestTheOutputIsWhatPrometheusExpects(t *testing.T) {
	r := New()
	r.Describe("skifity_http_requests_total", "counter", "Requests, by route and status.")
	r.Inc("skifity_http_requests_total", "route", "/api/apps", "status", "200")
	r.Inc("skifity_http_requests_total", "route", "/api/apps", "status", "200")
	r.Inc("skifity_http_requests_total", "route", "/api/apps", "status", "500")

	out := render(t, r)
	for _, want := range []string{
		"# HELP skifity_http_requests_total Requests, by route and status.",
		"# TYPE skifity_http_requests_total counter",
		`skifity_http_requests_total{route="/api/apps",status="200"} 2`,
		`skifity_http_requests_total{route="/api/apps",status="500"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output is missing %q:\n%s", want, out)
		}
	}
}

func TestAHistogramCarriesItsBucketsSumAndCount(t *testing.T) {
	// A histogram missing its +Inf bucket, its sum or its count is not a
	// histogram: Prometheus reads all three and a dashboard built on two of
	// them is silently wrong.
	r := New()
	r.Describe("skifity_http_request_duration_seconds", "histogram", "How long requests took.")
	for _, seconds := range []float64{0.001, 0.05, 2} {
		r.Observe("skifity_http_request_duration_seconds", seconds, "route", "/api/apps")
	}

	out := render(t, r)
	for _, want := range []string{
		`skifity_http_request_duration_seconds_bucket{route="/api/apps",le="0.005"} 1`,
		`skifity_http_request_duration_seconds_bucket{route="/api/apps",le="0.1"} 2`,
		`skifity_http_request_duration_seconds_bucket{route="/api/apps",le="+Inf"} 3`,
		`skifity_http_request_duration_seconds_sum{route="/api/apps"} 2.051`,
		`skifity_http_request_duration_seconds_count{route="/api/apps"} 3`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the histogram is missing %q:\n%s", want, out)
		}
	}
}

func TestLabelsAreOrderedAndEscaped(t *testing.T) {
	// The same measurement written with its labels in a different order has to
	// land on the same series, or every caller has to remember an order.
	r := New()
	r.Inc("thing_total", "b", "2", "a", "1")
	r.Inc("thing_total", "a", "1", "b", "2")

	out := render(t, r)
	if !strings.Contains(out, `thing_total{a="1",b="2"} 2`) {
		t.Errorf("label order split one series into two:\n%s", out)
	}

	// A label value with a quote or a newline in it would otherwise end the
	// line early and corrupt everything after it.
	r.Inc("thing_total", "name", "a \"quoted\"\nvalue")
	out = render(t, r)
	if strings.Contains(out, "\nvalue") {
		t.Errorf("a newline in a label value broke the line:\n%s", out)
	}
	if !strings.Contains(out, `\"quoted\"`) {
		t.Errorf("a quote in a label value was not escaped:\n%s", out)
	}
}

func TestGaugesAreSampledOnEveryScrape(t *testing.T) {
	// A gauge something has to remember to update is a gauge that is wrong
	// after the first person adds a code path and forgets.
	r := New()
	value := 1.0
	r.Collect(func(r *Registry) { r.SetGauge("apps", value) })

	if out := render(t, r); !strings.Contains(out, "apps 1") {
		t.Fatalf("the collector did not run:\n%s", out)
	}
	value = 7
	if out := render(t, r); !strings.Contains(out, "apps 7") {
		t.Fatalf("the collector did not run again:\n%s", out)
	}
}

func TestTheRuntimeIsAlwaysThere(t *testing.T) {
	// Goroutines and heap are the two numbers that say "this panel is
	// leaking", and a leak in a process meant to run for months is the failure
	// worth catching early.
	out := render(t, New())
	for _, want := range []string{"skifity_goroutines", "skifity_memory_heap_bytes"} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is not reported:\n%s", want, out)
		}
	}
}

func TestANilRegistryIsSafe(t *testing.T) {
	// A deployer in a test has no monitoring, and it must not have to know.
	var r *Registry
	r.Inc("thing_total")
	r.Add("thing_total", 2)
	r.SetGauge("thing", 1)
	r.Observe("thing_seconds", 0.1)
}

func TestConcurrentWritersDoNotRace(t *testing.T) {
	// Every handler writes to this while one reader formats it. Run with -race,
	// which is what CI does.
	r := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				r.Inc("requests_total", "status", "200")
				r.Observe("duration_seconds", 0.01)
				r.SetGauge("apps", float64(j))
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			var b strings.Builder
			_ = r.Write(&b)
		}
	}()
	wg.Wait()

	if !strings.Contains(render(t, r), "requests_total{status=\"200\"} 1600") {
		t.Error("a concurrent write was lost")
	}
}
