package cluster

import (
	"context"
	"regexp"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/logging"
	"skifity/internal/store"
	"skifity/internal/version"
)

// AppEvents lists what Kubernetes said about an app's objects in the last
// hour or so, newest first, with every message kept free of the app's secrets.
func (c *Cluster) AppEvents(ctx context.Context, app store.App, env store.Environment) ([]api.ObjectEvent, error) {
	processes, err := c.db.ListProcesses(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	volumes, err := c.db.ListVolumes(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	jobs, err := c.db.ListAppJobs(ctx, app.ID)
	if err != nil {
		return nil, err
	}

	// The app's own workloads, whose pods and ReplicaSets are recognised by
	// how Kubernetes names them — including the ones already gone, which is
	// most of what a crash or an eviction leaves behind.
	workloads := []string{app.Slug}
	for _, process := range processes {
		workloads = append(workloads, kube.ProcessDeploymentName(app.Slug, process.Name))
	}
	pods := kube.NewWorkloadPods(workloads...)

	names := map[string]map[string]bool{
		"Deployment": set(workloads...),
		"Service":    set(app.Slug, kube.PortsServiceName(app.Slug), kube.InterceptorServiceName(app.Slug)),
		"Ingress":    set(app.Slug),
		"HorizontalPodAutoscaler": set(kube.ResourceName(app.Slug, "hpa"),
			// KEDA's own, for an app that scales to zero.
			"keda-hpa-"+app.Slug),
		"PodDisruptionBudget":   set(kube.ResourceName(app.Slug, "pdb")),
		"HTTPScaledObject":      set(app.Slug),
		"ScaledObject":          set(app.Slug),
		"NetworkPolicy":         set(kube.PortsPolicyName(app.Slug)),
		"PersistentVolumeClaim": set(),
		"CronJob":               set(),
	}
	for _, volume := range volumes {
		names["PersistentVolumeClaim"][kube.ResourceName(app.Slug, volume.Name)] = true
	}
	for _, job := range jobs {
		names["CronJob"][kube.CronJobName(app.Slug, job.Name)] = true
	}

	scope := kube.EventScope{
		Owns: func(kind, name string) bool {
			return names[kind][name] || pods.Match(kind, name)
		},
		// A one-off run's pod carries the app's labels and a name nothing
		// predicts.
		Selector: version.LabelKey("app-id") + "=" + app.ID,
	}
	return c.events(ctx, env.Namespace, scope, c.SecretValues(ctx, app, env))
}

// DatabaseEvents lists what Kubernetes said about a managed database's
// objects: its StatefulSet or CloudNativePG cluster, their pods, their volumes.
func (c *Cluster) DatabaseEvents(ctx context.Context, database store.Database, env store.Environment) ([]api.ObjectEvent, error) {
	pods := kube.NewWorkloadPods(database.Slug)
	// A StatefulSet's claims are data-<name>-<ordinal>; CloudNativePG's are
	// <cluster>-<instance>.
	claims := regexp.MustCompile(`^(?:data-)?` + regexp.QuoteMeta(database.Slug) + `-[0-9]+$`)
	names := map[string]map[string]bool{
		"StatefulSet": set(database.Slug),
		"Cluster":     set(database.Slug),
		"Service":     set(database.Slug, database.Slug+"-rw", database.Slug+"-ro", database.Slug+"-r"),
	}
	scope := kube.EventScope{
		Owns: func(kind, name string) bool {
			return names[kind][name] || pods.Match(kind, name) ||
				kind == "PersistentVolumeClaim" && claims.MatchString(name)
		},
		Selector: version.LabelKey("database-id") + "=" + database.ID,
	}
	return c.events(ctx, env.Namespace, scope, nil)
}

func (c *Cluster) events(ctx context.Context, namespace string, scope kube.EventScope, secrets []string) ([]api.ObjectEvent, error) {
	if c.client == nil {
		return nil, errdoc.ClusterUnreachable(nil)
	}
	raw, err := c.client.Events(ctx, namespace, scope)
	if err != nil {
		if kube.IsUnreachable(err) {
			return nil, errdoc.ClusterUnreachable(err)
		}
		return nil, errdoc.EventsUnreadable(namespace, err.Error(), err)
	}
	out := make([]api.ObjectEvent, 0, len(raw))
	for _, event := range raw {
		// A message is the cluster's own words, and the cluster has an app's
		// variables: a failed mount or a probe can quote one.
		args := make([]string, len(event.Explanation.Args))
		for i, arg := range event.Explanation.Args {
			args[i] = logging.RedactValues(arg, secrets)
		}
		out = append(out, api.ObjectEvent{
			Type: event.Type, Reason: event.Reason, Kind: event.Kind, Name: event.Name,
			Message: logging.RedactValues(event.Message, secrets), Count: event.Count,
			FirstSeen: event.FirstSeen, LastSeen: event.LastSeen,
			Explanation:     logging.RedactValues(event.Explanation.Text, secrets),
			ExplanationCode: event.Explanation.Code,
			ExplanationArgs: args,
		})
	}
	return out, nil
}

// SecretValues are the values of an app's variables that are secret — its
// own, and the project's shared ones — for keeping out of anything the
// cluster wrote that is shown to a person: an event that quotes one, a field
// somebody set to one with kubectl. A variable that cannot be opened is left
// out rather than failing the page that asked.
func (c *Cluster) SecretValues(ctx context.Context, app store.App, env store.Environment) []string {
	if c.keyring == nil {
		return nil
	}
	var out []string
	if shared, err := c.db.ListSharedVariables(ctx, env.ProjectID); err == nil {
		for _, row := range shared {
			plaintext, err := c.keyring.Open(row.Sealed, "shared_variable:"+env.ProjectID+":"+row.Key)
			if err != nil {
				continue
			}
			if row.IsSecret || logging.LooksSecret(row.Key, string(plaintext)) {
				out = append(out, string(plaintext))
			}
		}
	}
	if own, err := c.db.ListVariables(ctx, app.ID); err == nil {
		for _, row := range own {
			plaintext, err := c.keyring.Open(row.Sealed, "variable:"+app.ID+":"+row.Key)
			if err != nil {
				continue
			}
			if row.IsSecret || logging.LooksSecret(row.Key, string(plaintext)) {
				out = append(out, string(plaintext))
			}
		}
	}
	return out
}

func set(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}
