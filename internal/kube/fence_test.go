package kube

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

const panelImage = "ghcr.io/skifity/skifity:v0.1.1"

// TestEveryPodThatRunsATeamsCodeWaitsForItsFence: the second real cluster
// showed a new pod reach the Kubernetes API in the moment before its policy was
// written. Whatever runs a team's code — the app, a one-off command, a
// scheduled one, a plugin — starts after the fence container.
func TestEveryPodThatRunsATeamsCodeWaitsForItsFence(t *testing.T) {
	app := baseSpec()
	app.FenceImage = panelImage
	run, err := BuildRunJob(RunSpec{App: app, Name: "web-run-1", Command: "npm run migrate"})
	if err != nil {
		t.Fatal(err)
	}
	cron, err := BuildCronJob(RunSpec{App: app, Name: "web-nightly", Command: "npm run report"}, "0 3 * * *")
	if err != nil {
		t.Fatal(err)
	}
	plugin := samplePlugin()
	plugin.FenceImage = panelImage
	_, pluginDeployment, _, _ := pluginParts(t, BuildPluginObjects(plugin))

	pods := map[string]corev1.PodSpec{
		"the app":            BuildDeployment(app).Spec.Template.Spec,
		"a one-off command":  run.Spec.Template.Spec,
		"a scheduled one":    cron.Spec.JobTemplate.Spec.Template.Spec,
		"a plugin":           pluginDeployment.Spec.Template.Spec,
		"one of its process": BuildDeployment(ProcessSpec(app, "worker", "node worker.js", 1)).Spec.Template.Spec,
	}
	for name, pod := range pods {
		if len(pod.InitContainers) == 0 || pod.InitContainers[0].Name != FenceContainerName {
			t.Errorf("%s does not start with the fence", name)
			continue
		}
		fence := pod.InitContainers[0]
		if fence.Image != panelImage || strings.Join(fence.Command, " ") != "/usr/local/bin/skifity wait-for-fence" {
			t.Errorf("%s's fence runs %s %v", name, fence.Image, fence.Command)
		}
		if len(fence.Env) != 1 || fence.Env[0].ValueFrom == nil || fence.Env[0].ValueFrom.FieldRef.FieldPath != "status.hostIP" {
			t.Errorf("%s's fence is not told its server's address: %+v", name, fence.Env)
		}
	}

	// A panel outside the cluster has no image to run it from, and its pods
	// start as they always did.
	if pod := BuildDeployment(baseSpec()).Spec.Template.Spec; len(pod.InitContainers) != 0 {
		t.Errorf("with no image the app has %d init containers", len(pod.InitContainers))
	}
}

// TestTheFenceIsWelcomeInTheStrictestNamespace: it runs in every environment,
// whose pods are held to the restricted profile, and it must not be what
// pushes a pod over its environment's quota.
func TestTheFenceIsWelcomeInTheStrictestNamespace(t *testing.T) {
	fence := FenceContainer(panelImage)
	sc := fence.SecurityContext
	switch {
	case sc == nil:
		t.Fatal("the fence has no security context")
	case sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot || sc.RunAsUser == nil || *sc.RunAsUser == 0:
		t.Error("the fence may run as root")
	case sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation:
		t.Error("the fence may gain privilege")
	case sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL":
		t.Error("the fence keeps capabilities")
	case sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault:
		t.Error("the fence has no seccomp profile")
	case sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem:
		t.Error("the fence can write to its image")
	}
	// Every resource the environment's quota counts is set, so nothing is
	// filled in from the LimitRange's defaults — a whole core's limit, for a
	// container that waits a second.
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourceEphemeralStorage} {
		request, limit := fence.Resources.Requests[name], fence.Resources.Limits[name]
		if request.IsZero() || limit.IsZero() {
			t.Errorf("the fence does not set its %s", name)
		}
	}
	if limit := fence.Resources.Limits[corev1.ResourceCPU]; limit.MilliValue() > 100 {
		t.Errorf("the fence may use %s of a core", limit.String())
	}
}
