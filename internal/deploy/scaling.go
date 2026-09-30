package deploy

import (
	"context"
	"strings"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// The scaling readiness checker.
//
// Most apps that break when scaled break for one of a handful of reasons, and
// every one of them is visible in the app's own configuration. Saying so before
// the user turns on autoscaling is worth more than a good error afterwards,
// because the failure mode is "some requests behave differently", which is
// almost impossible to debug from the outside.

// ScalingReadiness looks for patterns that break with more than one instance.
func (d *Deployer) ScalingReadiness(ctx context.Context, appID string) ([]api.ScalingFinding, error) {
	app, err := d.db.GetApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	findings := []api.ScalingFinding{}

	// A ReadWriteOnce volume can only be mounted by one node at a time, so a
	// second instance either shares the disk or cannot start at all.
	volumes, err := d.db.ListVolumes(ctx, appID)
	if err != nil {
		return nil, err
	}
	if len(volumes) > 0 {
		paths := make([]string, 0, len(volumes))
		for _, v := range volumes {
			paths = append(paths, v.MountPath)
		}
		detail, args := errdoc.Sprintf(
			"A volume is mounted at %s. That storage can be attached to one server at a time, so a second instance would either fail to start or end up on the same server.",
			strings.Join(paths, ", "))
		findings = append(findings, api.ScalingFinding{
			Code:     "volume",
			Severity: "error",
			Title:    "This app writes to a disk that only one instance can use",
			Detail:   detail,
			Fix:      "Move uploads to object storage such as S3 or MinIO, or turn on cross-node storage in Settings so the volume can be shared. Until then, keep this app at one instance.",
			Args:     api.ScalingArgs{Detail: args},
		})
	}

	// Configuration that names a local file or an in-memory store.
	rows, err := d.db.ListVariables(ctx, appID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		finding, found := inspectVariable(row.Key, row.IsSecret, func() string {
			if row.IsSecret {
				// A secret's value is never read for a heuristic; the key name
				// is enough to spot the pattern.
				return ""
			}
			plaintext, err := d.keyring.Open(row.Sealed, "variable:"+appID+":"+row.Key)
			if err != nil {
				return ""
			}
			return string(plaintext)
		})
		if found {
			findings = append(findings, finding)
		}
	}

	// No health path means Kubernetes cannot tell a starting instance from a
	// ready one, so a rollout sends traffic to an instance that is not up yet.
	if app.HealthPath == "" && app.Port > 0 {
		findings = append(findings, api.ScalingFinding{
			Code:     "no_health_check",
			Severity: "warning",
			Title:    "There is no health check path",
			Detail:   "Without one, Skifity can only check that the port is open, which happens before most frameworks are ready to serve.",
			Fix:      "Add a path such as /healthz that returns 200 once the app is ready, and set it under the app's settings.",
		})
	}

	// Autoscaling on CPU or memory reads the metrics API. Without it the HPA
	// sits at `<unknown>/70%` forever: the app never scales up under load and
	// never scales back down, and neither Kubernetes nor the panel says why.
	// This is the failure this whole checker exists for — something that looks
	// configured and does nothing.
	if app.Autoscale && !app.ScaleToZero && (app.CPUTarget > 0 || app.MemoryTarget > 0) &&
		d.cluster != nil && !d.cluster.Client().MetricsAvailable(ctx) {
		findings = append(findings, api.ScalingFinding{
			Code:     "no_metrics",
			Severity: "error",
			Title:    "The cluster is not reporting CPU and memory",
			Detail: "Autoscaling on a CPU or memory target reads those numbers from metrics-server, " +
				"and nothing is answering. The app will stay at its minimum number of instances " +
				"however busy it gets, and nothing will say so.",
			Fix: "k3s installs metrics-server by default. Check that it is running in kube-system, " +
				"or set a fixed number of instances until it is.",
		})
	}

	// A percentage target is a percentage of the request, not of the server.
	//
	// This is the arithmetic nobody does, and it is why "autoscaling is on and
	// the app sits at its maximum" is the second most common complaint about
	// an HPA after "it never scales at all". A new app requests 50m of CPU and
	// 128Mi of memory — sensible numbers for packing a small server — so a 70%
	// target fires at 35m and 90Mi, which almost any framework is over before
	// it has served a single request.
	if app.Autoscale {
		if trip, ok := firstTripwire(app.CPUTarget, app.CPURequestM, 100); ok {
			detail, args := errdoc.Sprintf(
				"The target is a percentage of what this app reserves, not of the server. It reserves %dm of CPU and the target is %d%%, so a new instance is added above %dm — about a tenth of one core, which most frameworks pass while idle.",
				app.CPURequestM, app.CPUTarget, trip)
			findings = append(findings, api.ScalingFinding{
				Code:     "target_below_idle",
				Severity: "warning",
				Title:    "The CPU target is reached before the app does anything",
				Detail:   detail,
				Fix:      "Raise the reserved CPU under the app's settings to what it actually uses when busy, so the percentage means something. The reservation is what the app is guaranteed, not a limit on it.",
				Args:     api.ScalingArgs{Detail: args},
			})
		}
		if trip, ok := firstTripwire(app.MemoryTarget, app.MemRequestMB, 256); ok {
			detail, args := errdoc.Sprintf(
				"The target is a percentage of what this app reserves. It reserves %dMi and the target is %d%%, so a new instance is added above %dMi — which a Node or JVM process passes at startup.",
				app.MemRequestMB, app.MemoryTarget, trip)
			findings = append(findings, api.ScalingFinding{
				Code:     "memory_target_below_idle",
				Severity: "warning",
				Title:    "The memory target is reached before the app does anything",
				Detail:   detail,
				Fix:      "Raise the reserved memory under the app's settings to what the app uses when it is idle, plus room to work. Memory does not fall once it has been claimed, so a target below the idle figure never comes back down.",
				Args:     api.ScalingArgs{Detail: args},
			})
		}
	}

	// A single instance with no autoscaling has no redundancy at all.
	if app.Replicas == 1 && !app.Autoscale {
		findings = append(findings, api.ScalingFinding{
			Code:     "single_instance",
			Severity: "info",
			Title:    "This app runs as a single instance",
			Detail:   "If the server it is on restarts, the app is unavailable until Kubernetes starts it somewhere else.",
			Fix:      "Raise the number of instances to two, or turn on autoscaling, once the points above are addressed.",
		})
	}

	// Asking for more than one instance with a volume is the combination that
	// actually breaks, so it is called out separately.
	if (app.Replicas > 1 || app.Autoscale) && len(volumes) > 0 {
		findings = append(findings, api.ScalingFinding{
			Code:     "volume_with_replicas",
			Severity: "error",
			Title:    "More than one instance is configured, and this app has a volume",
			Detail:   "The extra instances will stay pending, or will be scheduled onto the same server and write to the same files at once.",
			Fix:      "Set the instance count back to one, or enable cross-node storage, or move the data out of the volume.",
		})
	}

	return findings, nil
}

// firstTripwire is the usage at which an autoscaler adds an instance, and
// whether that point is low enough to be worth saying out loud.
//
// floor is the level under which the answer is "this fires at idle": a tenth of
// a core, or a quarter of a gigabyte. Above it the operator has chosen a
// reservation that means something and does not need to be told twice.
func firstTripwire(targetPercent, request, floor int) (int, bool) {
	if targetPercent <= 0 || request <= 0 {
		return 0, false
	}
	trip := request * targetPercent / 100
	return trip, trip < floor
}

// inspectVariable spots configuration that will not survive being scaled.
//
// readValue is a function rather than a value so that a secret's plaintext is
// never read when only the key name is needed.
func inspectVariable(key string, isSecret bool, readValue func() string) (api.ScalingFinding, bool) {
	upper := strings.ToUpper(key)
	value := strings.ToLower(readValue())

	switch {
	case strings.Contains(upper, "SESSION") && (strings.Contains(value, "memory") || value == "file" || strings.Contains(value, "filesystem")):
		detail, args := errdoc.Sprintf("%s is set to %q. Each instance would keep its own sessions, so users would be signed out at random as requests land on different instances.", key, readValue())
		return api.ScalingFinding{
			Code:     "in_memory_sessions",
			Severity: "error",
			Title:    "Sessions are kept in memory or on disk",
			Detail:   detail,
			Fix:      "Create a Redis database in this environment and point the session store at it. Skifity links it in as a variable for you.",
			Args:     api.ScalingArgs{Detail: args},
		}, true

	case strings.Contains(upper, "CACHE") && (value == "file" || strings.Contains(value, "filesystem")):
		detail, args := errdoc.Sprintf("%s is set to %q, so each instance would have its own cache and they would disagree.", key, readValue())
		return api.ScalingFinding{
			Code:     "file_cache",
			Severity: "warning",
			Title:    "The cache is stored on local disk",
			Detail:   detail,
			Fix:      "Use Redis for the cache, or accept that cached values differ between instances.",
			Args:     api.ScalingArgs{Detail: args},
		}, true

	case strings.Contains(value, "sqlite") || strings.HasSuffix(value, ".db") || strings.HasSuffix(value, ".sqlite3"):
		detail, args := errdoc.Sprintf("%s points at a SQLite file. SQLite is a single file on one disk, so instances on different servers cannot share it.", key)
		return api.ScalingFinding{
			Code:     "sqlite",
			Severity: "error",
			Title:    "This app uses SQLite",
			Detail:   detail,
			Fix:      "Create a PostgreSQL database in this environment and point the app at it. Skifity injects the connection string for you.",
			Args:     api.ScalingArgs{Detail: args},
		}, true

	case upper == "UPLOAD_DIR" || upper == "UPLOADS_PATH" || strings.Contains(upper, "STORAGE_PATH"):
		detail, args := errdoc.Sprintf("%s points at a directory inside the container. Files written by one instance would be invisible to the others, and would disappear on the next deploy.", key)
		return api.ScalingFinding{
			Code:     "local_uploads",
			Severity: "error",
			Title:    "Uploaded files are written to local disk",
			Detail:   detail,
			Fix:      "Store uploads in S3-compatible object storage. MinIO is available as a one-click template if you would rather not use a cloud provider.",
			Args:     api.ScalingArgs{Detail: args},
		}, true

	case strings.Contains(upper, "CRON") && value == "true":
		detail, args := errdoc.Sprintf("%s is on. With several instances, every instance runs the schedule, so each job runs several times.", key)
		return api.ScalingFinding{
			Code:     "in_process_cron",
			Severity: "warning",
			Title:    "Scheduled jobs run inside the app",
			Detail:   detail,
			Fix:      "Run the scheduler as a separate app with one instance, or use a lock so only one instance runs each job.",
			Args:     api.ScalingArgs{Detail: args},
		}, true
	}
	return api.ScalingFinding{}, false
}

// AppsNeedingAttention lists apps whose live state does not match what was
// asked for, which is what the dashboard's warning badge is built from.
func (d *Deployer) AppsNeedingAttention(ctx context.Context, envID string) ([]store.App, error) {
	apps, err := d.db.ListApps(ctx, envID)
	if err != nil {
		return nil, err
	}
	var out []store.App
	for _, app := range apps {
		if app.Status == "failed" || app.Status == "crashing" {
			out = append(out, app)
		}
	}
	return out, nil
}
