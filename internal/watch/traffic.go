package watch

import (
	"context"
	"fmt"
	"time"

	"skifity/internal/api"
	"skifity/internal/store"
	"skifity/internal/traffic"
)

// The requests apps answered, and when to say something about the ones they
// failed.
//
// Read from Traefik's own counters, once a pass, before the apps are looked at
// one by one, so a minute's requests are recorded by the time its thresholds
// are checked. The read goes through the API server to every Traefik pod and
// has only ever been run against fakes: see kube.ScrapeTraefik.

// errorWindowRequests is how many requests the three minutes a server-error
// threshold is judged over must hold between them before it is judged at
// all. One failed request out of three on a quiet app is 33%, and it is not
// an outage. The help text under the threshold says twenty; change both.
const errorWindowRequests = 20

// recordTraffic reads Traefik and keeps a minute of requests for every app.
func (w *Watcher) recordTraffic(ctx context.Context, apps []store.DeployedApp, now time.Time) {
	pods, err := w.cluster.ScrapeTraefik(ctx)
	if err != nil {
		// Not even a list of pods: nothing to compare the next read with, and
		// nothing to say about the ingress except that it could not be read.
		w.log.Debug("could not list the ingress to count requests", "error", err)
		w.traffic.Forget()
		w.setTrafficState(traffic.StateUnreachable)
		return
	}
	known := make([]traffic.App, 0, len(apps))
	for _, app := range apps {
		known = append(known, traffic.App{ID: app.ID, Namespace: app.Namespace, Slug: app.Slug})
	}
	index, shared := traffic.NewIndex(known)
	minute := w.traffic.Read(now, pods, index)
	w.setTrafficState(minute.State)
	for _, err := range minute.Unreadable {
		w.log.Warn("could not read the ingress's metrics", "error", err)
	}
	if !minute.Counted {
		return
	}
	for _, app := range apps {
		if shared[app.ID] {
			// Traefik cannot tell this app from another one; see NewIndex.
			// No row is better than one showing another team's requests.
			w.log.Debug("an app's requests cannot be told from another app's", "app", app.ID)
			continue
		}
		totals := minute.Apps[app.ID]
		if totals == nil {
			totals = &traffic.Totals{}
		}
		if err := w.db.RecordAppTraffic(ctx, app.ID, totals.Sample(now, minute.Partial)); err != nil {
			w.log.Warn("could not record an app's requests", "app", app.ID, "error", err)
		}
	}
}

// TrafficSource says whether the ingress could be read on the last pass, so a
// page with no requests drawn can say why.
func (w *Watcher) TrafficSource() string {
	w.trafficMu.Lock()
	defer w.trafficMu.Unlock()
	return w.trafficState
}

func (w *Watcher) setTrafficState(state string) {
	w.trafficMu.Lock()
	defer w.trafficMu.Unlock()
	w.trafficState = state
}

// The watcher is what the API asks.
var _ api.TrafficSource = (*Watcher)(nil)

// evaluateServerErrors decides whether an app's server-error threshold is
// crossed or clearly clear, from its recent minutes of requests, oldest first.
//
// Crossed is every one of the last three minutes at the threshold or above,
// with enough requests between them to mean something. Clear is the same
// three minutes, as busy, each well under it. Anything else — a minute
// missing, too few requests, one minute between the line and the margin under
// it — leaves it as it was: an app that stopped being asked has not stopped
// failing, it has stopped being measured.
func evaluateServerErrors(threshold int, recent []store.AppTraffic, now time.Time) (crossed *alert, clear bool) {
	if threshold <= 0 {
		return nil, true
	}
	if len(recent) < sustained {
		return nil, false
	}
	last := recent[len(recent)-sustained:]
	// Three minutes in a row, the newest of them this pass's or the last's: a
	// minute the ingress could not be read is a gap, and three minutes either
	// side of a gap are not three minutes running.
	if now.Sub(last[len(last)-1].At) > 2*time.Minute {
		return nil, false
	}
	for i := 1; i < len(last); i++ {
		if last[i].At.Sub(last[i-1].At) != time.Minute {
			return nil, false
		}
	}
	var requests, failed int64
	lowest, highest := 100.0, 0.0
	for _, minute := range last {
		if minute.Requests == 0 {
			return nil, false
		}
		requests += minute.Requests
		failed += minute.Status5xx
		pct := float64(minute.Status5xx) * 100 / float64(minute.Requests)
		lowest, highest = min(lowest, pct), max(highest, pct)
	}
	if requests < errorWindowRequests {
		return nil, false
	}
	switch {
	case lowest >= float64(threshold):
		return &alert{"errors", fmt.Sprintf(
			"At least %d%% of the requests that reached it were answered with a server error (5xx) "+
				"every minute for %d minutes: %d of %d. The reason is usually in its logs.",
			threshold, sustained, failed, requests)}, false
	case highest < float64(threshold-min(clearMargin, threshold/2)):
		return nil, true
	}
	return nil, false
}
