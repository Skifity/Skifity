package watch

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"skifity/internal/api"
	"skifity/internal/notify"
	"skifity/internal/store"
)

// What servers used, their disks included, and when to say something about it.

// recordServers keeps a minute of what each ready server used, and says when
// one crosses a threshold.
func (w *Watcher) recordServers(ctx context.Context, servers []store.Server, nodes map[string]api.NodeInfo, now time.Time) {
	disks, err := w.cluster.NodeDisks(ctx)
	if err != nil {
		// The disk is one number of several; the rest are still worth keeping.
		w.log.Debug("could not read the servers' disks", "error", err)
	}
	for _, server := range servers {
		node, present := nodes[server.NodeName]
		if server.NodeName == "" || !present || !node.Ready {
			continue
		}
		// metrics-server had nothing for the node: its usage is not zero,
		// it is not known, and a minute recorded as 0% drew a false dip in
		// the graph and ended an alert exactly when the server was under
		// pressure. The minute is left out; the graph shows a gap.
		if !node.UsageKnown {
			continue
		}
		sample := store.ServerSample{
			At: now, CPUM: node.CPUUsedM, CPUCapacityM: node.CPUCapacityM,
			MemoryMB: node.MemUsedMB, MemoryCapacityMB: node.MemCapacityMB, Pods: node.PodCount,
		}
		if disk, ok := disks[server.NodeName]; ok {
			sample.DiskUsedMB, sample.DiskCapacityMB = disk.UsedMB, disk.CapacityMB
		}
		if err := w.db.RecordServerSample(ctx, server.ID, sample); err != nil {
			w.log.Warn("could not record a server's usage", "server", server.ID, "error", err)
			continue
		}
		w.checkServerAlerts(ctx, server, now)
	}
	if now.Minute() == 0 {
		if err := w.db.PruneServerSamples(ctx, now.Add(-SampleRetention)); err != nil {
			w.log.Warn("could not forget old server samples", "error", err)
		}
	}
}

// clearMargin is how far under a threshold a reading has to stay for the
// warning to end: a disk hovering at 85% would otherwise warn and un-warn
// every few minutes.
const clearMargin = 5

// evaluateServer decides which of a server's thresholds are crossed and which
// are clearly clear, from its recent samples, oldest first. A threshold that
// is neither — too few readings, one not known, or one between the line and
// the margin under it — is left as it was. Like evaluate, a function of its
// arguments.
func evaluateServer(thresholds store.ServerAlerts, recent []store.ServerSample) (crossed []alert, clear []string) {
	last := recent
	if len(last) > sustained {
		last = last[len(last)-sustained:]
	}
	// state is "above" when every one of the last few minutes was at or over
	// the threshold, "below" when every one was under it by the margin, and
	// "" otherwise. A minute whose number is not known is neither.
	state := func(threshold int, share func(store.ServerSample) (int, bool)) (string, int) {
		if threshold <= 0 || len(last) < sustained {
			return "", 0
		}
		above, below, latest := true, true, 0
		for _, s := range last {
			pct, known := share(s)
			if !known {
				return "", 0
			}
			above = above && pct >= threshold
			below = below && pct < threshold-clearMargin
			latest = pct
		}
		switch {
		case above:
			return "above", latest
		case below:
			return "below", latest
		}
		return "", latest
	}
	disk := func(s store.ServerSample) (int, bool) {
		return store.Percent(s.DiskUsedMB, s.DiskCapacityMB), s.DiskCapacityMB > 0
	}
	memory := func(s store.ServerSample) (int, bool) {
		return store.Percent(s.MemoryMB, s.MemoryCapacityMB), s.MemoryCapacityMB > 0
	}
	cpu := func(s store.ServerSample) (int, bool) {
		return store.Percent(s.CPUM, s.CPUCapacityM), s.CPUCapacityM > 0
	}

	if where, now := state(thresholds.DiskPct, disk); where == "above" {
		latest := last[len(last)-1]
		crossed = append(crossed, alert{"disk", fmt.Sprintf(
			"Its disk has been %d%% full or more for %d minutes; now %d%%, %s of %s. "+
				"At 90%% Kubernetes starts stopping instances on it to free space. "+
				"Old images and logs are the usual cause.",
			thresholds.DiskPct, sustained, now, gigabytes(latest.DiskUsedMB), gigabytes(latest.DiskCapacityMB))})
	} else if where == "below" || thresholds.DiskPct <= 0 {
		clear = append(clear, "disk")
	}
	if where, now := state(thresholds.MemoryPct, memory); where == "above" {
		crossed = append(crossed, alert{"memory", fmt.Sprintf(
			"Its memory has been %d%% used or more for %d minutes; now %d%%. "+
				"When it runs out, instances on it are stopped to make room.",
			thresholds.MemoryPct, sustained, now)})
	} else if where == "below" || thresholds.MemoryPct <= 0 {
		clear = append(clear, "memory")
	}
	if where, now := state(thresholds.CPUPct, cpu); where == "above" {
		crossed = append(crossed, alert{"cpu", fmt.Sprintf(
			"Its CPU has been %d%% busy or more for %d minutes; now %d%%. "+
				"Apps on it are slowed down, not stopped.",
			thresholds.CPUPct, sustained, now)})
	} else if where == "below" || thresholds.CPUPct <= 0 {
		clear = append(clear, "cpu")
	}
	return crossed, clear
}

func gigabytes(mb int64) string {
	return fmt.Sprintf("%.1f GB", float64(mb)/1024)
}

// checkServerAlerts says when a server crosses a threshold, and when it comes
// back, once each.
func (w *Watcher) checkServerAlerts(ctx context.Context, server store.Server, now time.Time) {
	thresholds, err := w.db.GetServerAlerts(ctx, server.ID)
	if err != nil {
		w.log.Warn("could not read a server's alerts", "server", server.ID, "error", err)
		return
	}
	recent, err := w.db.ServerSamples(ctx, server.ID, now.Add(-time.Duration(sustained+1)*time.Minute))
	if err != nil {
		w.log.Warn("could not read a server's recent usage", "server", server.ID, "error", err)
		return
	}
	crossed, clear := evaluateServer(thresholds, recent)

	// What fires now: what was firing and has not clearly ended, and what
	// has just crossed.
	names := make([]string, 0, len(crossed)+len(thresholds.Firing))
	for _, was := range thresholds.Firing {
		if !slices.Contains(clear, was) {
			names = append(names, was)
		}
	}
	for _, a := range crossed {
		if !slices.Contains(names, a.name) {
			names = append(names, a.name)
		}
		if !slices.Contains(thresholds.Firing, a.name) {
			w.notifyTeam(ctx, server.TeamID, notify.EventServerAlert, notify.Message{
				Title:  serverAlertTitle(server.Name, a.name),
				Body:   a.detail,
				Level:  "warning",
				Path:   "/servers/" + server.ID,
				Fields: map[string]string{"Server": server.Name},
			})
		}
	}
	for _, was := range thresholds.Firing {
		if !slices.Contains(names, was) {
			w.notifyTeam(ctx, server.TeamID, notify.EventServerAlert, notify.Message{
				Title:  server.Name + " is back under its " + alertNoun(was) + " threshold",
				Body:   "Nothing to do; this is the end of the earlier warning.",
				Level:  "success",
				Path:   "/servers/" + server.ID,
				Fields: map[string]string{"Server": server.Name},
			})
		}
	}
	slices.Sort(names)
	previous := slices.Clone(thresholds.Firing)
	slices.Sort(previous)
	if strings.Join(names, ",") != strings.Join(previous, ",") {
		if err := w.db.SetServerAlertsFiring(ctx, server.ID, names); err != nil {
			w.log.Warn("could not record a server's alerts", "server", server.ID, "error", err)
		}
		w.publish(server.TeamID, "server", map[string]any{"id": server.ID, "alerts": names})
	}
}

func serverAlertTitle(server, name string) string {
	switch name {
	case "disk":
		return server + " is running out of disk space"
	case "memory":
		return server + " is running out of memory"
	default:
		return server + " has been busy for a while"
	}
}
