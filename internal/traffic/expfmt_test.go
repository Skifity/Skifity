package traffic

import (
	"math"
	"os"
	"strings"
	"testing"
)

// Traefik's own output, as its integration tests see it: the service counters
// are found, their labels read, and everything else skipped — including a
// negative gauge and a summary, which are not counters and are not ours.
func TestTraefiksOwnOutputIsRead(t *testing.T) {
	body, err := os.ReadFile("testdata/traefik-v3.6.txt")
	if err != nil {
		t.Fatal(err)
	}
	samples, err := Parse(strings.NewReader(string(body)), func(name string) bool {
		return name == requestsMetric || name == bucketsMetric
	})
	if err != nil {
		t.Fatalf("Traefik's output did not parse: %v", err)
	}
	requests, buckets := 0, 0
	for _, s := range samples {
		switch s.Name {
		case requestsMetric:
			requests++
		case bucketsMetric:
			buckets++
		default:
			t.Errorf("%s was kept", s.Name)
		}
	}
	if requests != 6 || buckets != 5 {
		t.Fatalf("%d request counters and %d buckets, want 6 and 5", requests, buckets)
	}
	first := samples[5]
	if first.Name != requestsMetric || first.Value != 2 || first.Labels["service"] != "whoami1@docker" ||
		first.Labels["code"] != "200" || first.Labels["method"] != "GET" || first.Labels["protocol"] != "http" {
		t.Fatalf("the first request counter is %+v", first)
	}
	if last := samples[4]; last.Labels["le"] != "+Inf" || last.Value != 2 {
		t.Fatalf("the +Inf bucket is %+v", last)
	}

	// Everything else in the file is still the format, and reads.
	all, err := Parse(strings.NewReader(string(body)), nil)
	if err != nil || len(all) != 27 {
		t.Fatalf("the whole file is %d samples: %v", len(all), err)
	}
}

// What the format allows is read, and read right.
func TestTheExpositionFormatsCornersAreRead(t *testing.T) {
	cases := []struct {
		name, line string
		value      float64
		labels     map[string]string
	}{
		{"no labels", "m 3", 3, map[string]string{}},
		{"a timestamp", `m{a="b"} 3 1712345678000`, 3, map[string]string{"a": "b"}},
		{"escapes", `m{a="say \"hi\"\\n\n"} 1`, 1, map[string]string{"a": "say \"hi\"\\n\n"}},
		{"a trailing comma", `m{a="1",b="2",} 1`, 1, map[string]string{"a": "1", "b": "2"}},
		{"blanks between tokens", "m { a = \"1\" , b=\"2\" }\t4", 4, map[string]string{"a": "1", "b": "2"}},
		{"exponent", "m 1.5e+06", 1.5e6, map[string]string{}},
		{"a comma and a brace inside a value", `m{a="x,}y"} 2`, 2, map[string]string{"a": "x,}y"}},
		{"infinity", "m +Inf", math.Inf(1), map[string]string{}},
	}
	for _, tc := range cases {
		samples, err := Parse(strings.NewReader(tc.line+"\n"), nil)
		if err != nil || len(samples) != 1 {
			t.Errorf("%s: %q: %v", tc.name, tc.line, err)
			continue
		}
		s := samples[0]
		if s.Name != "m" || s.Value != tc.value || len(s.Labels) != len(tc.labels) {
			t.Errorf("%s: read %+v", tc.name, s)
			continue
		}
		for k, v := range tc.labels {
			if s.Labels[k] != v {
				t.Errorf("%s: %s is %q, want %q", tc.name, k, s.Labels[k], v)
			}
		}
	}
}

// A line that is not the format refuses the whole answer: a pod's counters are
// compared as a set, and one misread is a minute counted wrong.
func TestALineThatIsNotTheFormatIsRefused(t *testing.T) {
	bad := []string{
		`traefik_service_requests_total{code="200" 1`,
		`traefik_service_requests_total{code="200} 1`,
		`traefik_service_requests_total{code=200} 1`,
		`traefik_service_requests_total{code} 1`,
		`traefik_service_requests_total{code="\t"} 1`,
		`traefik_service_requests_total{code="1",code="2"} 1`,
		`traefik_service_requests_total{1code="2"} 1`,
		`traefik_service_requests_total{code="200"}`,
		`traefik_service_requests_total{code="200"} one`,
		`traefik_service_requests_total{code="200"} 1 2 3`,
		`traefik_service_requests_total{code="200"} 1 yesterday`,
		`traefik_service_requests_total`,
		// Not the format at all: what a proxy in the way answers.
		`<!DOCTYPE html>`,
		// A line of a metric nobody asked for is still read as far as its name.
		`9lives 1`,
	}
	keep := func(name string) bool { return name == requestsMetric }
	for _, line := range bad {
		if _, err := Parse(strings.NewReader("# HELP x y\n"+line+"\n"), keep); err == nil {
			t.Errorf("%q was read", line)
		}
	}

	// A metric nobody asked for is not read past its name, so something odd in
	// it cannot make the metrics that are needed unreadable.
	body := "go_info{version=\"go1.26\",weird\n" + `traefik_service_requests_total{code="200",service="a@kubernetes"} 5` + "\n"
	samples, err := Parse(strings.NewReader(body), keep)
	if err != nil || len(samples) != 1 || samples[0].Value != 5 {
		t.Fatalf("a metric that was not asked for got in the way: %v %+v", err, samples)
	}
}
