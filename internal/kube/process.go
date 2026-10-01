package kube

import (
	"context"
	"fmt"
	"maps"
	"regexp"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"skifity/internal/version"
)

// An app's other processes: a worker, a clock, whatever its Procfile names
// beside web. Each is a Deployment of the app's own image, with its variables,
// under a command of its own — one build, several ways of running it, the way
// Heroku, Fly and Render do it. None has a port, a Service, an Ingress, a
// probe or a volume; nothing sends it traffic.

// ProcessComponent is the app.kubernetes.io/component of a process's objects.
const ProcessComponent = "process"

// validProcessName is a DNS label short enough that app, separator and
// process fit in one object name without the app's part being cut.
var validProcessName = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,18}[a-z0-9])?$`)

// ValidProcessName reports whether a process may be called this. "web" is
// the app itself and "release" runs once per deploy, so neither can be one.
func ValidProcessName(name string) bool {
	return validProcessName.MatchString(name) && name != "web" && name != "release"
}

// ProcessDeploymentName is the Deployment of an app's process.
//
// Two hyphens, because no app is ever called that: Slugify folds every run of
// separators into one. An app called web-worker and the worker process of an
// app called web would otherwise both be web-worker, and the second to be
// applied would take the first's Deployment over.
func ProcessDeploymentName(appSlug, process string) string {
	name := appSlug + "--" + process
	if len(name) <= maxLabelLength {
		return name
	}
	keep := maxLabelLength - len(process) - 2 - 7
	return trimDashes.ReplaceAllString(appSlug[:keep], "") + "-" + shortHash(appSlug, 6) + "--" + process
}

// ProcessSpec is the app's spec rewritten for one of its processes.
func ProcessSpec(app AppSpec, process, command string, instances int) AppSpec {
	s := app
	s.Name = ProcessDeploymentName(app.Name, process)
	s.ProcessOf = app.Name
	// Which is what decides whether it gets the app's GPUs: a worker doing
	// the inference may want the card the web in front of it does not.
	s.Process = process
	s.Command = []string{"/bin/sh", "-c"}
	s.Args = []string{command}
	s.Port, s.HealthPath = 0, ""
	s.Replicas = max(instances, 0)
	s.Autoscale, s.ScaleToZero = false, false
	s.MinReplicas, s.MaxReplicas = 0, 0
	// A volume is ReadWriteOnce: a second Deployment mounting it would sit in
	// ContainerCreating on any other server, and two writers on one is the
	// corruption Recreate exists to prevent.
	s.Volumes = nil
	s.Domains = nil
	// Connections from outside go to the app, as its domains' do.
	s.PublicPorts = nil
	s.PasswordUsers = ""
	s.Protected = false
	s.PlainEnv = maps.Clone(app.PlainEnv)
	if s.PlainEnv == nil {
		s.PlainEnv = map[string]string{}
	}
	s.PlainEnv["SKIFITY_PROCESS"] = process
	return s
}

// BuildProcessDeployment renders the Deployment of one of an app's processes.
func BuildProcessDeployment(app AppSpec, process, command string, instances int) *appsv1.Deployment {
	deployment := BuildDeployment(ProcessSpec(app, process, command, instances))
	for _, set := range []map[string]string{deployment.Labels, deployment.Spec.Template.Labels} {
		maps.Copy(set, processLabels(app.Name, process))
	}
	return deployment
}

// processLabels are what finds an app's processes again, to remove the ones
// that are no longer wanted. The selector is untouched: it is the one
// BuildDeployment derives from the process's own name, and it matches none of
// the app's pods, so the app's Service never sends a request to a worker.
func processLabels(appSlug, process string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/component":  ProcessComponent,
		version.LabelKey("process"):    process,
		version.LabelKey("process-of"): appSlug,
	}
}

// PruneProcesses deletes an app's process Deployments whose process is not
// in keep. Nil keep removes every one.
func (c *Client) PruneProcesses(ctx context.Context, namespace, appSlug string, keep map[string]bool) error {
	selector := labels.SelectorFromSet(map[string]string{
		"app.kubernetes.io/component":  ProcessComponent,
		version.LabelKey("process-of"): appSlug,
	}).String()
	deployments, err := c.clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("list the processes of %s: %w", appSlug, err)
	}
	// One at a time, by name, for the same reason deleteRuns does: a
	// collection delete is a selector away from emptying the namespace.
	for _, deployment := range deployments.Items {
		if keep[deployment.Labels[version.LabelKey("process")]] {
			continue
		}
		if err := c.clientset.AppsV1().Deployments(namespace).
			Delete(ctx, deployment.Name, metav1.DeleteOptions{}); err != nil && !IsNotFound(err) {
			return fmt.Errorf("remove the process %s: %w", deployment.Name, err)
		}
	}
	return nil
}

// RestartProcesses restarts every one of an app's processes, the way
// RestartApp restarts the app: a worker holding a connection pool to a
// database that was just restored needs a restart as much as the web does.
func (c *Client) RestartProcesses(ctx context.Context, namespace, appSlug string) error {
	selector := labels.SelectorFromSet(map[string]string{
		"app.kubernetes.io/component":  ProcessComponent,
		version.LabelKey("process-of"): appSlug,
	}).String()
	deployments, err := c.clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("list the processes of %s: %w", appSlug, err)
	}
	for _, deployment := range deployments.Items {
		if err := c.RestartApp(ctx, namespace, deployment.Name); err != nil {
			return err
		}
	}
	return nil
}
