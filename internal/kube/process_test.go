package kube

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/fake"

	"skifity/internal/version"
)

func TestAProcessIsTheAppsImageUnderAnotherCommand(t *testing.T) {
	web := baseSpec()
	web.EnvFromSecret = ResourceName(web.Name, "env")
	web.PlainEnv = map[string]string{"LOG_LEVEL": "info"}
	web.Volumes = []VolumeSpec{{Name: "data", MountPath: "/data", SizeGB: 1}}
	web.Domains = []DomainSpec{{Hostname: "shop.example.com"}}
	web.Autoscale, web.MinReplicas, web.MaxReplicas = true, 2, 5

	worker := BuildProcessDeployment(web, "worker", "celery -A shop worker", 3)

	if worker.Name != "web--worker" || worker.Namespace != web.Namespace {
		t.Fatalf("the worker is %s/%s", worker.Namespace, worker.Name)
	}
	container := worker.Spec.Template.Spec.Containers[0]
	if container.Image != web.Image {
		t.Fatalf("the worker runs %s, not the app's own build %s", container.Image, web.Image)
	}
	if strings.Join(container.Command, " ") != "/bin/sh -c" || strings.Join(container.Args, " ") != "celery -A shop worker" {
		t.Fatalf("the worker runs %v %v", container.Command, container.Args)
	}
	if len(container.EnvFrom) != 1 || container.EnvFrom[0].SecretRef.Name != web.EnvFromSecret {
		t.Fatal("the worker does not read the app's variables")
	}
	env := map[string]string{}
	for _, variable := range container.Env {
		env[variable.Name] = variable.Value
	}
	// SKIFITY_APP is the app's name in every one of its processes, not the
	// worker's Deployment's.
	if env["SKIFITY_PROCESS"] != "worker" || env["SKIFITY_APP"] != "web" || env["LOG_LEVEL"] != "info" {
		t.Fatalf("the worker's own variables are %v", env)
	}
	if _, leaked := web.PlainEnv["SKIFITY_PROCESS"]; leaked {
		t.Fatal("the worker's variable was written into the app's")
	}

	// Its own instance count, not the autoscaler's: a worker is not scaled on
	// the web's CPU.
	if worker.Spec.Replicas == nil || *worker.Spec.Replicas != 3 {
		t.Fatalf("the worker has %v instances, want 3", worker.Spec.Replicas)
	}
	if len(container.Ports) != 0 || container.ReadinessProbe != nil || container.LivenessProbe != nil {
		t.Fatal("a process with no port has a port or an HTTP probe")
	}
	if len(worker.Spec.Template.Spec.Volumes) != 0 || len(container.VolumeMounts) != 0 {
		t.Fatal("a ReadWriteOnce volume was mounted into a second Deployment")
	}

	// The part that matters most: the app's Service must never send a request
	// to a worker, and the two Deployments must not claim each other's pods.
	service := labels.SelectorFromSet(BuildService(web).Spec.Selector)
	if service.Matches(labels.Set(worker.Spec.Template.Labels)) {
		t.Fatal("the app's Service selects the worker's pods")
	}
	webSelector := labels.SelectorFromSet(BuildDeployment(web).Spec.Selector.MatchLabels)
	if webSelector.Matches(labels.Set(worker.Spec.Template.Labels)) {
		t.Fatal("the app's Deployment claims the worker's pods")
	}
	workerSelector := labels.SelectorFromSet(worker.Spec.Selector.MatchLabels)
	if workerSelector.Matches(labels.Set(BuildDeployment(web).Spec.Template.Labels)) {
		t.Fatal("the worker's Deployment claims the app's pods")
	}
	if worker.Spec.Template.Labels[version.LabelKey("process-of")] != "web" {
		t.Fatal("the worker cannot be found again by its app")
	}
}

func TestAProcessNameCannotBeAnotherApps(t *testing.T) {
	// Slugify never writes two hyphens in a row, so no app is called this.
	if Slugify("web--worker") == "web--worker" {
		t.Fatal("an app can be called what a process's Deployment is called")
	}
	long := strings.Repeat("a", 60)
	name := ProcessDeploymentName(long, "scheduler")
	if !ValidLabel(name) || !strings.HasSuffix(name, "--scheduler") {
		t.Fatalf("the process of a long-named app is %q", name)
	}
	if ProcessDeploymentName(long, "scheduler") == ProcessDeploymentName(long[:59]+"b", "scheduler") {
		t.Fatal("two long-named apps' processes share a Deployment")
	}

	for name, want := range map[string]bool{
		"worker": true, "celery-beat": true, "clock2": true,
		"web": false, "release": false, "": false, "Worker": false, "-x": false, "x-": false,
		"a-very-long-process-name": false, "2fast": false, "under_score": false,
	} {
		if ValidProcessName(name) != want {
			t.Errorf("ValidProcessName(%q) = %v, want %v", name, !want, want)
		}
	}
}

func TestProcessesNoLongerWantedAreRemoved(t *testing.T) {
	web := baseSpec()
	other := baseSpec()
	other.Name, other.AppID = "api", "app_456"
	c := &Client{clientset: fake.NewSimpleClientset(
		BuildDeployment(web),
		BuildProcessDeployment(web, "worker", "run worker", 1),
		BuildProcessDeployment(web, "clock", "run clock", 1),
		BuildProcessDeployment(other, "worker", "run worker", 1),
	)}

	if err := c.PruneProcesses(t.Context(), web.Namespace, "web", map[string]bool{"worker": true}); err != nil {
		t.Fatal(err)
	}
	left := remaining(t, c, web.Namespace)
	if strings.Join(left, " ") != "api--worker web web--worker" {
		t.Fatalf("left %v; only web's clock should have gone", left)
	}

	// With the app.
	if err := c.PruneProcesses(t.Context(), web.Namespace, "web", nil); err != nil {
		t.Fatal(err)
	}
	if left := remaining(t, c, web.Namespace); strings.Join(left, " ") != "api--worker web" {
		t.Fatalf("left %v after removing web's processes", left)
	}
}

func TestARestartReachesTheAppsProcessesAndNoOneElses(t *testing.T) {
	web := baseSpec()
	other := baseSpec()
	other.Name, other.AppID = "api", "app_456"
	c := &Client{clientset: fake.NewSimpleClientset(
		BuildDeployment(web),
		BuildProcessDeployment(web, "worker", "run worker", 1),
		BuildProcessDeployment(other, "worker", "run worker", 1),
	)}
	if err := c.RestartProcesses(t.Context(), web.Namespace, "web"); err != nil {
		t.Fatal(err)
	}
	restarted := func(name string) bool {
		deployment, err := c.clientset.AppsV1().Deployments(web.Namespace).Get(t.Context(), name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return deployment.Spec.Template.Annotations[version.LabelKey("restarted-at")] != ""
	}
	if !restarted("web--worker") {
		t.Fatal("web's worker was not restarted")
	}
	if restarted("api--worker") || restarted("web") {
		t.Fatal("something other than web's processes was restarted")
	}
}

func remaining(t *testing.T, c *Client, namespace string) []string {
	t.Helper()
	list, err := c.clientset.AppsV1().Deployments(namespace).List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range list.Items {
		names = append(names, d.Name)
	}
	sortStrings(names)
	return names
}
