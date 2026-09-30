package kube

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestAK3sVersionIsReadAsNodesReportIt(t *testing.T) {
	for text, want := range map[string]string{
		"v1.31.4+k3s1": "v1.31.4+k3s1", "1.31.4+k3s2": "v1.31.4+k3s2", "v1.30.0": "v1.30.0+k3s1",
	} {
		v, ok := ParseK3sVersion(text)
		if !ok || v.String() != want {
			t.Errorf("%q read as %v (%v), want %s", text, v, ok, want)
		}
	}
	for _, bad := range []string{"", "latest", "v1.31", "v1.31.4-rc1+k3s1", "v1.31.4+rke2r1"} {
		if _, ok := ParseK3sVersion(bad); ok {
			t.Errorf("%q was read as a release", bad)
		}
	}
	a, _ := ParseK3sVersion("v1.31.4+k3s1")
	b, _ := ParseK3sVersion("v1.31.4+k3s2")
	c, _ := ParseK3sVersion("v1.31.10+k3s1")
	if a.Compare(b) != -1 || c.Compare(b) != 1 || a.Compare(a) != 0 {
		t.Fatal("versions are not ordered numerically")
	}

	// Typed without its +k3s part, as the form takes when the releases cannot
	// be listed, a version is the release a node at +k3s1 already runs, and
	// not one older than it.
	plan := PlanK3sUpgrade([]UpgradeNode{{Name: "one", ControlPlane: true, Ready: true, Version: "v1.31.4+k3s1"}}, "v1.31.4")
	if !plan.Nothing || len(plan.Blockers) != 0 {
		t.Fatalf("v1.31.4 against a node at v1.31.4+k3s1: %+v", plan)
	}
}

func TestAK3sUpgradeGoesServersFirstOneMinorAtATime(t *testing.T) {
	nodes := []UpgradeNode{
		{Name: "worker-b", Ready: true, Version: "v1.30.6+k3s1"},
		{Name: "control", ControlPlane: true, Ready: true, Version: "v1.30.6+k3s1"},
		{Name: "worker-a", Ready: true, Version: "v1.31.2+k3s1"},
	}
	plan := PlanK3sUpgrade(nodes, "v1.31.2+k3s1")
	if len(plan.Blockers) != 0 || plan.Nothing {
		t.Fatalf("a one-minor upgrade: %+v", plan)
	}
	var order []string
	for _, step := range plan.Steps {
		order = append(order, step.Node)
	}
	if strings.Join(order, " ") != "control worker-a worker-b" || plan.Steps[0].Role != "control-plane" {
		t.Fatalf("the order is %v", order)
	}
	if plan.Steps[1].Upgrade || !plan.Steps[2].Upgrade {
		t.Fatalf("a node already at the target is upgraded, or one behind is not: %+v", plan.Steps)
	}
	if len(plan.Warnings) != 1 || plan.Warnings[0].Code != "one_control_plane" {
		t.Fatalf("one control plane is not said: %v", plan.Warnings)
	}

	for target, want := range map[string]string{
		"v1.32.1+k3s1": "skips v1.31",
		"v1.29.9+k3s1": "not downgraded",
		"v2.0.0+k3s1":  "different major",
		"stable":       "is not a k3s release",
	} {
		plan := PlanK3sUpgrade(nodes, target)
		if !strings.Contains(strings.Join(Texts(plan.Blockers), "; "), want) {
			t.Errorf("%s: the blockers %v do not say %q", target, plan.Blockers, want)
		}
	}

	nodes[0].Ready = false
	if plan := PlanK3sUpgrade(nodes, "v1.31.2+k3s1"); len(plan.Blockers) != 1 || plan.Blockers[0].Code != "not_ready" ||
		plan.Blockers[0].Params["server"] != "worker-b" {
		t.Fatalf("a node that is down: %v", plan.Blockers)
	}
	nodes[0].Ready = true

	done := PlanK3sUpgrade([]UpgradeNode{{Name: "only", ControlPlane: true, Ready: true, Version: "v1.31.2+k3s1"}}, "v1.31.2+k3s1")
	if !done.Nothing || len(done.Blockers) != 0 {
		t.Fatalf("a cluster already at the target: %+v", done)
	}
}

func TestTheUpgradePlansAreTheOnesK3sDocuments(t *testing.T) {
	target, _ := ParseK3sVersion("v1.31.2+k3s1")
	plans := BuildK3sUpgradePlans(target)
	if len(plans) != 2 {
		t.Fatalf("%d plans", len(plans))
	}
	server, agent := plans[0], plans[1]
	for _, plan := range plans {
		if plan.GetKind() != "Plan" || plan.GetNamespace() != UpgradeNamespace {
			t.Fatalf("a plan is %s in %s", plan.GetKind(), plan.GetNamespace())
		}
		spec := plan.Object["spec"].(map[string]any)
		if spec["version"] != "v1.31.2+k3s1" || spec["concurrency"] != int64(1) || spec["cordon"] != true ||
			spec["serviceAccountName"] != "system-upgrade" {
			t.Fatalf("%s: %+v", plan.GetName(), spec)
		}
		// Cordoned, never drained: a drain on a small cluster evicts apps to
		// nowhere.
		if _, drains := spec["drain"]; drains {
			t.Fatalf("%s drains", plan.GetName())
		}
	}
	prepare := agent.Object["spec"].(map[string]any)["prepare"].(map[string]any)
	if args := prepare["args"].([]any); len(args) != 2 || args[1] != server.GetName() {
		t.Fatalf("the agents do not wait for the servers: %v", prepare)
	}
	if _, err := ToUnstructured(server); err != nil {
		t.Fatalf("a plan cannot be applied: %v", err)
	}
}

func TestPlansThePanelDidNotWriteAreFound(t *testing.T) {
	target, _ := ParseK3sVersion("v1.31.4+k3s1")
	var plans []unstructured.Unstructured
	for _, plan := range BuildK3sUpgradePlans(target) {
		plans = append(plans, *plan)
	}
	byHand := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "upgrade.cattle.io/v1", "kind": "Plan",
		"metadata": map[string]any{"name": "server-plan", "namespace": UpgradeNamespace},
	}}
	plans = append(plans, byHand)
	if got := ForeignPlans(plans); len(got) != 1 || got[0] != "server-plan" {
		t.Fatalf("the plans not the panel's are %v", got)
	}
}
