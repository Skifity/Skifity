package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"skifity/internal/kube"
)

// logCluster answers a log read with two instances' lines, one of which
// prints a credential, and remembers what it was asked for.
type logCluster struct {
	Cluster
	asked []LogOptions
}

const leakedToken = "Bearer abcdefghijklmnopqrstuvwxyz0123"

func (c *logCluster) AppLogs(_ context.Context, _, _ string, opts LogOptions) (*kube.LogStream, error) {
	c.asked = append(c.asked, opts)
	switch opts.Instance {
	case "", "shop-5d8f-aaaaa":
	case "shop-5d8f-ccccc":
		return nil, &kube.NotStarted{Instance: opts.Instance, Reason: "it is still starting."}
	default:
		return nil, &kube.UnknownInstance{Name: opts.Instance}
	}
	at := time.Date(2026, 11, 9, 10, 0, 1, 0, time.UTC)
	return kube.NewLogStream([]string{"shop-5d8f-aaaaa", "shop-5d8f-bbbbb"}, 1, []kube.LogEntry{
		{Instance: "shop-5d8f-aaaaa", At: at, Line: "listening on 8080"},
		{Instance: "shop-5d8f-bbbbb", At: at.Add(time.Second), Line: "calling the API with " + leakedToken},
	}), nil
}

func TestAnAppsLogSaysWhichInstanceWroteEachLine(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	cluster := &logCluster{}
	h.withCluster(cluster)
	app := h.app(acme, "shop")
	path := "/api/apps/" + app.ID + "/logs"

	status, body := h.do(acme, http.MethodGet, path+"?since=10m&tail=50", nil)
	if status != http.StatusOK {
		t.Fatalf("reading the log answered %d: %s", status, body)
	}
	var read struct {
		Lines            []string   `json:"lines"`
		Entries          []logEntry `json:"entries"`
		Instances        []string   `json:"instances"`
		InstancesOmitted int        `json:"instances_omitted"`
	}
	if err := json.Unmarshal([]byte(body), &read); err != nil {
		t.Fatal(err)
	}
	if len(read.Entries) != 2 || read.Entries[1].Instance != "shop-5d8f-bbbbb" || read.Entries[0].At.IsZero() {
		t.Fatalf("the entries are %+v", read.Entries)
	}
	if len(read.Lines) != 2 || read.Lines[0] != "listening on 8080" {
		t.Fatalf("the lines are %q", read.Lines)
	}
	if len(read.Instances) != 2 || read.InstancesOmitted != 1 {
		t.Fatalf("the read names %v and %d left out", read.Instances, read.InstancesOmitted)
	}
	if strings.Contains(body, "abcdefghijklmnopqrstuvwxyz0123") {
		t.Fatalf("a credential an app printed reached the browser: %s", body)
	}
	if got := cluster.asked[0]; got.SinceSeconds != 600 || got.TailLines != 50 || got.Instance != "" {
		t.Fatalf("the cluster was asked for %+v", got)
	}

	// With timestamps, each plain line carries its time, as kubectl's does.
	_, body = h.do(acme, http.MethodGet, path+"?timestamps=true", nil)
	if !strings.Contains(body, `"2026-11-09T10:00:01Z listening on 8080"`) {
		t.Fatalf("a timestamped read answered %s", body)
	}

	for query, want := range map[string]int{
		"?since=1h":                  http.StatusOK,
		"?since=2d":                  http.StatusOK,
		"?since=yesterday":           http.StatusBadRequest,
		"?since=31d":                 http.StatusBadRequest,
		"?since=-5m":                 http.StatusBadRequest,
		"?since=0s":                  http.StatusBadRequest,
		"?instance=shop-5d8f-aaaaa":  http.StatusOK,
		"?instance=web-other-xxxxx":  http.StatusNotFound,
		"?instance=shop-5d8f-ccccc":  http.StatusConflict,
		"?instance=x&process=absent": http.StatusNotFound,
	} {
		if status, answer := h.do(acme, http.MethodGet, path+query, nil); status != want {
			t.Errorf("%s answered %d, want %d: %s", query, status, want, answer)
		}
	}
	if _, answer := h.do(acme, http.MethodGet, path+"?instance=web-other-xxxxx", nil); !strings.Contains(answer, "resource.not_found") {
		t.Errorf("an instance that is not the app's answered %s", answer)
	}
	// Not "no instance of this app has started" while the others are serving.
	if _, answer := h.do(acme, http.MethodGet, path+"?instance=shop-5d8f-ccccc", nil); !strings.Contains(answer, "app.instance_not_started") {
		t.Errorf("an instance that has not started answered %s", answer)
	}
}

// Followed, each line is an entry event saying which instance wrote it, and
// a log event beside it with the line alone, for a CLI written before
// entries existed. Both are scrubbed.
func TestAFollowedLogSendsEntriesAndTheOldLines(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	h.withCluster(&logCluster{})
	app := h.app(acme, "shop")

	status, body := h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/logs?follow=true", nil)
	if status != http.StatusOK {
		t.Fatalf("following the log answered %d: %s", status, body)
	}
	events := map[string][]string{}
	var name string
	for line := range strings.SplitSeq(body, "\n") {
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			events[name] = append(events[name], strings.TrimPrefix(line, "data: "))
		}
	}
	if got := events["log"]; len(got) != 2 || got[0] != `"listening on 8080"` {
		t.Fatalf("the log events are %q", got)
	}
	if got := events["entry"]; len(got) != 2 || !strings.Contains(got[0], `"instance":"shop-5d8f-aaaaa"`) ||
		!strings.Contains(got[0], `"at":"2026-11-09T10:00:01Z"`) {
		t.Fatalf("the entry events are %q", got)
	}
	if got := events["instances"]; len(got) != 1 ||
		got[0] != `{"instances":["shop-5d8f-aaaaa","shop-5d8f-bbbbb"],"omitted":1}` {
		t.Fatalf("the instances events are %q", got)
	}
	if strings.Contains(body, "abcdefghijklmnopqrstuvwxyz0123") {
		t.Fatalf("a credential an app printed reached the browser: %s", body)
	}
}
