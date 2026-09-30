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

// evaluateServer decides which of a server's thresholds are crossed, from its
// recent samples, oldest first. Like evaluate, a function of its arguments.
func evaluateServer(thresholds store.ServerAlerts, recent []store.ServerSample) []alert {
	last := recent
	if len(last) > sustained {
		last = last[len(last)-sustained:]
	}
	// A minute whose number is not known is not a minute above the
	// threshold: three minutes means three readings.
	above := func(threshold int, share func(store.ServerSample) (int, bool)) (bool, int) {
		if threshold <= 0 || len(last) < sustained {
			return false, 0
		}
		latest := 0
		for _, s := range last {
			pct, known := share(s)
			if !known || pct < threshold {
				return false, 0
			}
			latest = pct
		}
		return true, latest
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

	var out []alert
	if crossed, now := above(thresholds.DiskPct, disk); crossed {
		latest := last[len(last)-1]
		out = append(out, alert{"disk", fmt.Sprintf(
			"Its disk has been %d%% full or more for %d minutes; now %d%%, %s of %s. "+
				"At 90%% Kubernetes starts stopping instances on it to free space. "+
				"Old images and logs are the usual cause.",
			thresholds.DiskPct, sustained, now, gigabytes(latest.DiskUsedMB), gigabytes(latest.DiskCapacityMB))})
	}
	if crossed, now := above(thresholds.MemoryPct, memory); crossed {
		out = append(out, alert{"memory", fmt.Sprintf(
			"Its memory has been %d%% used or more for %d minutes; now %d%%. "+
				"When it runs out, instances on it are stopped to make room.",
			thresholds.MemoryPct, sustained, now)})
	}
	if crossed, now := above(thresholds.CPUPct, cpu); crossed {
		out = append(out, alert{"cpu", fmt.Sprintf(
			"Its CPU has been %d%% busy or more for %d minutes; now %d%%. "+
				"Apps on it are slowed down, not stopped.",
			thresholds.CPUPct, sustained, now)})
	}
	return out
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
	crossed := evaluateServer(thresholds, recent)

	names := make([]string, 0, len(crossed))
	for _, a := range crossed {
		names = append(names, a.name)
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
