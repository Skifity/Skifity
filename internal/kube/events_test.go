package kube

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func event(name, kind, object, eventType, reason, message string, count int32, last time.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: "acme-shop-production"},
		InvolvedObject: corev1.ObjectReference{Kind: kind, Name: object, Namespace: "acme-shop-production"},
		Type:           eventType, Reason: reason, Message: message, Count: count,
		FirstTimestamp: metav1.NewTime(last.Add(-10 * time.Minute)),
		LastTimestamp:  metav1.NewTime(last),
	}
}

// webScope is what the cluster adapter builds for an app called web: its
// Deployment by name, its pods and ReplicaSets by how Kubernetes names them,
// and the pods that exist now by label.
func webScope() EventScope {
	pods := NewWorkloadPods("web", "web--worker")
	names := map[string]bool{"Deployment/web": true, "Deployment/web--worker": true, "Ingress/web": true,
		"HorizontalPodAutoscaler/web-hpa": true, "PersistentVolumeClaim/web-data": true}
	return EventScope{
		Owns:     func(kind, name string) bool { return names[kind+"/"+name] || pods.Match(kind, name) },
		Selector: "skifity.com/app-id=app_123",
	}
}

func TestAnAppsEventsAreItsOwnNewestFirstAndFolded(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	run := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "web-run-x7k2p9q4-abcde", Namespace: "acme-shop-production",
		Labels: map[string]string{"skifity.com/app-id": "app_123"},
	}}
	objects := []any{
		run,
		event("e1", "Pod", "web-7d4f8b9c5-x2x9q", corev1.EventTypeWarning, "BackOff",
			"Back-off restarting failed container web in pod web-7d4f8b9c5-x2x9q", 14, now.Add(-time.Minute)),
		// The same thing about the same pod, written twice: one row.
		event("e2", "Pod", "web-7d4f8b9c5-x2x9q", corev1.EventTypeWarning, "BackOff",
			"Back-off restarting failed container web in pod web-7d4f8b9c5-x2x9q", 3, now.Add(-2*time.Minute)),
		event("e3", "ReplicaSet", "web-7d4f8b9c5", corev1.EventTypeNormal, "SuccessfulCreate",
			"Created pod: web-7d4f8b9c5-x2x9q", 1, now.Add(-20*time.Minute)),
		event("e4", "Deployment", "web", corev1.EventTypeNormal, "ScalingReplicaSet",
			"Scaled up replica set web-7d4f8b9c5 to 1", 1, now.Add(-21*time.Minute)),
		event("e5", "Pod", "web-7d4f8b9c5-x2x9q", corev1.EventTypeWarning, "FailedScheduling",
			"0/1 nodes are available: 1 Insufficient memory.", 2, now.Add(-30*time.Minute)),
		// A pod that no longer exists, found by its name.
		event("e6", "Pod", "web--worker-5c8d7f6b4-q8w2e", corev1.EventTypeWarning, "OOMKilling",
			"Memory cgroup out of memory: Killed process 4242 (node)", 1, now.Add(-5*time.Minute)),
		// A one-off run's pod, found because it exists now and carries the label.
		event("e7", "Pod", "web-run-x7k2p9q4-abcde", corev1.EventTypeNormal, "Started",
			"Started container run", 1, now.Add(-3*time.Minute)),
		// Somebody else's: web-api is another app, and so is its pod.
		event("e8", "Pod", "web-api-6b9f7c8d4-zzzzz", corev1.EventTypeWarning, "BackOff", "not ours", 1, now),
		event("e9", "Deployment", "web-api", corev1.EventTypeNormal, "ScalingReplicaSet", "not ours", 1, now),
		event("e10", "ReplicaSet", "web-api-6b9f7c8d4", corev1.EventTypeNormal, "SuccessfulCreate", "not ours", 1, now),
	}
	clientset := fake.NewSimpleClientset()
	for _, obj := range objects {
		switch o := obj.(type) {
		case *corev1.Pod:
			if _, err := clientset.CoreV1().Pods(o.Namespace).Create(t.Context(), o, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
		case *corev1.Event:
			if _, err := clientset.CoreV1().Events(o.Namespace).Create(t.Context(), o, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	c := &Client{clientset: clientset, systemNamespace: "skifity-system"}

	events, err := c.Events(t.Context(), "acme-shop-production", webScope())
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var got []string
	for _, e := range events {
		got = append(got, e.Kind+"/"+e.Name+" "+e.Reason)
		if strings.Contains(e.Message, "not ours") {
			t.Errorf("another app's event was listed: %+v", e)
		}
	}
	want := []string{
		"Pod/web-7d4f8b9c5-x2x9q BackOff",
		"Pod/web-run-x7k2p9q4-abcde Started",
		"Pod/web--worker-5c8d7f6b4-q8w2e OOMKilling",
		"ReplicaSet/web-7d4f8b9c5 SuccessfulCreate",
		"Deployment/web ScalingReplicaSet",
		"Pod/web-7d4f8b9c5-x2x9q FailedScheduling",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the feed is\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	backoff := events[0]
	if backoff.Count != 17 {
		t.Errorf("the two back-offs were folded to %d, want 17", backoff.Count)
	}
	if !backoff.LastSeen.Equal(now.Add(-time.Minute)) || !backoff.FirstSeen.Equal(now.Add(-12*time.Minute)) {
		t.Errorf("the folded span is %s to %s", backoff.FirstSeen, backoff.LastSeen)
	}
	if backoff.Explanation.Code != "crash_backoff" {
		t.Errorf("a crash loop is explained as %+v", backoff.Explanation)
	}
	if events[2].Explanation.Code != "oom" {
		t.Errorf("an out-of-memory kill is explained as %+v", events[2].Explanation)
	}
	if last := events[len(events)-1]; last.Explanation.Code != "scheduling_memory" {
		t.Errorf("a scheduling failure is explained as %+v", last.Explanation)
	}
	if events[1].Explanation.Code != "" {
		t.Errorf("a Normal event was given an explanation: %+v", events[1].Explanation)
	}
}

func TestTheCommonWarningsAreExplained(t *testing.T) {
	cases := []struct{ reason, message, code string }{
		{"FailedScheduling", "0/3 nodes are available: 3 Insufficient cpu.", "scheduling_cpu"},
		{"BackOff", "Back-off restarting failed container web in pod web-1", "crash_backoff"},
		{"BackOff", `Back-off pulling image "registry.internal/acme/web:abc"`, "pull_failed"},
		{"Unhealthy", "Readiness probe failed: HTTP probe failed with statuscode: 503", "probe_readiness"},
		{"Unhealthy", "Liveness probe failed: Get \"http://10.42.0.7:3000/healthz\": context deadline exceeded", "probe_liveness"},
		{"Unhealthy", "Startup probe failed: dial tcp 10.42.0.7:3000: connect: connection refused", "probe_startup"},
		{"FailedMount", `MountVolume.SetUp failed for volume "files" : secret "web-files" not found`, "mount_config_missing"},
		{"FailedMount", "Unable to attach or mount volumes: unmounted volumes=[data]: timed out waiting for the condition", "mount_failed"},
		{"FailedAttachVolume", "Multi-Attach error for volume \"pvc-1\" Volume is already exclusively attached to one node", "attach_failed"},
		{"FailedCreatePodSandBox", "Failed to create pod sandbox: rpc error: code = Unknown", "sandbox_failed"},
		{"OOMKilling", "Memory cgroup out of memory: Killed process 1 (node)", "oom"},
		{"OOMKilled", "", "oom"},
		{"Evicted", "The node was low on resource: memory.", "evicted"},
		{"ErrImagePull", "rpc error: code = NotFound desc = manifest unknown", "pull_missing"},
		{"FailedPull", "unauthorized: authentication required", "pull_unauthorized"},
		{"Failed", `Failed to pull image "registry.internal/web": dial tcp: lookup registry.internal: no such host`, "pull_unreachable"},
		{"Failed", "Error: container has runAsNonRoot and image will run as root", "runs_as_root"},
		{"FailedCreate", `pods "web-7d4" is forbidden: exceeded quota: environment`, "replicas_quota"},
	}
	for _, tc := range cases {
		got := ExplainEvent(tc.reason, tc.message)
		if got.Code != tc.code {
			t.Errorf("%s %q is explained as %q, want %q", tc.reason, tc.message, got.Code, tc.code)
		}
		if got.Code != "" && strings.TrimSpace(got.Text) == "" {
			t.Errorf("%s has a code and no words", tc.reason)
		}
	}
	// The secret a mount could not find is named, so somebody can tell which.
	if got := ExplainEvent("FailedMount", `secret "web-files" not found`); !strings.Contains(got.Text, "web-files") ||
		len(got.Args) != 1 || got.Args[0] != "web-files" {
		t.Errorf("the missing secret is not named: %+v", got)
	}
	// And everything else keeps Kubernetes' own words, with no explanation.
	if got := ExplainEvent("Pulled", "Successfully pulled image"); got.Code != "" || got.Text != "" {
		t.Errorf("an ordinary event was explained: %+v", got)
	}
}

// TestEveryExplanationHasItsWords: the server explains once, in English, and
// the panel looks its own words up by the explanation's code, the way it does
// an error's. A sentence added to the table without its words in every
// language would be English on a Russian page; one whose words drop a value
// would lose the name of the thing that failed.
func TestEveryExplanationHasItsWords(t *testing.T) {
	locales, err := filepath.Glob(filepath.Join("..", "..", "web", "src", "locales", "*.json"))
	if err != nil || len(locales) < 5 {
		t.Fatalf("find the locales: %v", err)
	}
	placeholder := regexp.MustCompile(`\{\{\s*(\d+)\s*\}\}`)
	// i18next reads a key ending in a plural category as one form of a
	// plural, so a code called scheduling_other would be looked up as the
	// plural of "scheduling" and never found.
	for code := range explanations {
		for _, suffix := range []string{"_zero", "_one", "_two", "_few", "_many", "_other"} {
			if strings.HasSuffix(code, suffix) {
				t.Errorf("the explanation code %q ends in %s, which i18next reads as a plural form", code, suffix)
			}
		}
	}
	for _, path := range locales {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var locale struct {
			Events struct {
				Explain map[string]string `json:"explain"`
			} `json:"events"`
		}
		if err := json.Unmarshal(body, &locale); err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		language := strings.TrimSuffix(filepath.Base(path), ".json")
		for code, format := range explanations {
			words, ok := locale.Events.Explain[code]
			if !ok || strings.TrimSpace(words) == "" {
				t.Errorf("%s: the explanation %q has no words: add events.explain.%s", language, code, code)
				continue
			}
			if want, got := strings.Count(format, "%s"), len(placeholder.FindAllString(words, -1)); want != got {
				t.Errorf("%s: events.explain.%s has %d values and the English has %d", language, code, got, want)
			}
		}
		for code := range locale.Events.Explain {
			if _, ok := explanations[code]; !ok {
				t.Errorf("%s: events.explain.%s is not an explanation the panel gives", language, code)
			}
		}
	}
}
