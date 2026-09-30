package kube

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Upgrading k3s.
//
// The installer puts a version of k3s on every server and nothing ever moved
// it: Kubernetes supports a minor version for about fourteen months, and a
// panel installed today would be running an unsupported one before its second
// birthday, with nothing in it saying so. Kubero and Epinio both leave this to
// the operator; k3s documents one way to do it from inside the cluster,
// Rancher's system-upgrade-controller, and this is that way with the checks in
// front of it that the documentation leaves to the reader.
//
// The controller does the work. It is given two Plans — the servers, one at a
// time, then the agents, one at a time, each waiting for the servers — and
// replaces each node's k3s binary with the target's, cordoning the node while
// it does. What this file adds is the plan a person reads first: which node
// goes from what to what, in what order, and every reason not to start.

// UpgradeNamespace is where the system-upgrade-controller and its Plans live.
const UpgradeNamespace = "system-upgrade"

// K3sUpgradeImage is what the controller runs on each node. Its tag is the
// target version, which the controller writes from the Plan's version.
const K3sUpgradeImage = "rancher/k3s-upgrade"

// K3sVersion is a k3s release, v1.31.4+k3s1.
type K3sVersion struct {
	Major, Minor, Patch, Revision int
}

var k3sVersion = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:\+k3s(\d+))?$`)

// ParseK3sVersion reads a k3s version as a node reports it and as releases are
// named. A release candidate is not something to upgrade a panel's cluster to,
// so one does not parse. One written without its +k3s part is the first k3s
// release of that Kubernetes version, which is what String names it: read as
// revision 0, v1.31.4 was older than the v1.31.4+k3s1 it upgrades to, and a
// node already there was taken for one to be downgraded.
func ParseK3sVersion(text string) (K3sVersion, bool) {
	match := k3sVersion.FindStringSubmatch(strings.TrimSpace(text))
	if match == nil {
		return K3sVersion{}, false
	}
	number := func(s string) int { n, _ := strconv.Atoi(s); return n }
	v := K3sVersion{Major: number(match[1]), Minor: number(match[2]), Patch: number(match[3]), Revision: max(number(match[4]), 1)}
	return v, true
}

// String is the version as k3s names it.
func (v K3sVersion) String() string {
	return fmt.Sprintf("v%d.%d.%d+k3s%d", v.Major, v.Minor, v.Patch, max(v.Revision, 1))
}

// Compare orders two versions: -1, 0 or 1.
func (v K3sVersion) Compare(other K3sVersion) int {
	for _, pair := range [][2]int{{v.Major, other.Major}, {v.Minor, other.Minor}, {v.Patch, other.Patch}, {v.Revision, other.Revision}} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// UpgradeNode is a node as the plan needs to know it.
type UpgradeNode struct {
	Name         string `json:"name"`
	ControlPlane bool   `json:"control_plane"`
	Ready        bool   `json:"ready"`
	Version      string `json:"version"`
}

// K3sUpgradeStep is one node in the order it is upgraded.
type K3sUpgradeStep struct {
	Node string `json:"node"`
	Role string `json:"role"`
	From string `json:"from"`
	To   string `json:"to"`
	// Upgrade is false for a node already at the target.
	Upgrade bool `json:"upgrade"`
}

// UpgradeReason is one reason not to start, or one thing worth knowing: a
// code and its values, for the panel to say in the reader's language, and the
// English for everything else.
type UpgradeReason struct {
	Code   string            `json:"code"`
	Params map[string]string `json:"params,omitempty"`
	Text   string            `json:"text"`
}

func reason(code, text string, params ...string) UpgradeReason {
	r := UpgradeReason{Code: code, Text: text, Params: map[string]string{}}
	for i := 0; i+1 < len(params); i += 2 {
		r.Params[params[i]] = params[i+1]
	}
	return r
}

// K3sUpgradePlan is what an upgrade would do, and why it cannot start when it
// cannot.
type K3sUpgradePlan struct {
	Target   string           `json:"target"`
	Steps    []K3sUpgradeStep `json:"steps"`
	Blockers []UpgradeReason  `json:"blockers"`
	Warnings []UpgradeReason  `json:"warnings"`
	// Nothing is true when every node already runs the target.
	Nothing bool `json:"nothing"`
}

// PlanK3sUpgrade works out an upgrade of these nodes to a target version.
func PlanK3sUpgrade(nodes []UpgradeNode, target string) K3sUpgradePlan {
	plan := K3sUpgradePlan{Target: target, Steps: []K3sUpgradeStep{}, Blockers: []UpgradeReason{}, Warnings: []UpgradeReason{}}
	want, ok := ParseK3sVersion(target)
	if !ok {
		plan.Blockers = append(plan.Blockers, reason("not_a_release",
			fmt.Sprintf("%q is not a k3s release; they are written like v1.31.4+k3s1", target), "target", target))
		return plan
	}
	plan.Target = want.String()
	if len(nodes) == 0 {
		plan.Blockers = append(plan.Blockers, reason("no_servers", "the cluster reported no servers"))
		return plan
	}

	// Servers first, then agents: an agent newer than the API server it talks
	// to is the one skew Kubernetes does not allow.
	ordered := append([]UpgradeNode(nil), nodes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].ControlPlane != ordered[j].ControlPlane {
			return ordered[i].ControlPlane
		}
		return ordered[i].Name < ordered[j].Name
	})

	lowestMinor := -1
	servers, pending := 0, 0
	for _, node := range ordered {
		role := "worker"
		if node.ControlPlane {
			role = "control-plane"
			servers++
		}
		have, ok := ParseK3sVersion(node.Version)
		step := K3sUpgradeStep{Node: node.Name, Role: role, From: node.Version, To: plan.Target}
		switch {
		case !ok:
			plan.Blockers = append(plan.Blockers, reason("unknown_version",
				fmt.Sprintf("%s reports %q, which is not a k3s version this can reason about", node.Name, node.Version),
				"server", node.Name, "version", node.Version))
		case have.Compare(want) > 0:
			plan.Blockers = append(plan.Blockers, reason("downgrade",
				fmt.Sprintf("%s already runs %s, newer than %s; k3s is not downgraded", node.Name, node.Version, plan.Target),
				"server", node.Name, "version", node.Version, "target", plan.Target))
		case have.Compare(want) == 0:
			step.To = node.Version
		default:
			step.Upgrade = true
			pending++
		}
		if ok && have.Major == want.Major && (lowestMinor < 0 || have.Minor < lowestMinor) {
			lowestMinor = have.Minor
		}
		if ok && have.Major != want.Major {
			plan.Blockers = append(plan.Blockers, reason("major",
				fmt.Sprintf("%s runs %s, a different major version from %s", node.Name, node.Version, plan.Target),
				"server", node.Name, "version", node.Version, "target", plan.Target))
		}
		if !node.Ready {
			plan.Blockers = append(plan.Blockers, reason("not_ready",
				fmt.Sprintf("%s is not ready; every server has to be, since each is taken out in turn and has to come back", node.Name),
				"server", node.Name))
		}
		plan.Steps = append(plan.Steps, step)
	}
	if lowestMinor >= 0 && want.Minor > lowestMinor+1 {
		next := fmt.Sprintf("v%d.%d", want.Major, lowestMinor+1)
		plan.Blockers = append(plan.Blockers, reason("skips_minor", fmt.Sprintf(
			"%s skips %s: Kubernetes is upgraded one minor version at a time, so upgrade to the latest %s first",
			plan.Target, next, next), "target", plan.Target, "next", next))
	}
	if pending == 0 && len(plan.Blockers) == 0 {
		plan.Nothing = true
		return plan
	}
	if servers == 1 {
		plan.Warnings = append(plan.Warnings, reason("one_control_plane",
			"There is one control plane, so the Kubernetes API is unreachable for about a minute while it restarts. "+
				"Apps keep serving; the panel reconnects by itself."))
	}
	if len(nodes) == 1 {
		plan.Warnings = append(plan.Warnings, reason("only_server",
			"This is the only server. Nothing new is scheduled while it is cordoned, so a deploy during the upgrade waits for it."))
	}
	return plan
}

// BuildK3sUpgradePlans renders the system-upgrade-controller's two Plans for
// a target version: servers one at a time, then agents one at a time once the
// servers are done. Cordoned, not drained, as the k3s documentation has it: a
// k3s restart keeps its containers running, and a drain on a small cluster
// evicts apps to nowhere.
func BuildK3sUpgradePlans(target K3sVersion) []*unstructured.Unstructured {
	version := target.String()
	plan := func(name string, spec map[string]any) *unstructured.Unstructured {
		spec["concurrency"] = int64(1)
		spec["cordon"] = true
		spec["serviceAccountName"] = "system-upgrade"
		spec["version"] = version
		spec["upgrade"] = map[string]any{"image": K3sUpgradeImage}
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "upgrade.cattle.io/v1",
			"kind":       "Plan",
			"metadata": map[string]any{
				"name":      name,
				"namespace": UpgradeNamespace,
				"labels":    map[string]any{"app.kubernetes.io/managed-by": "skifity"},
			},
			"spec": spec,
		}}
	}
	return []*unstructured.Unstructured{
		plan("k3s-server", map[string]any{
			"nodeSelector": map[string]any{"matchExpressions": []any{map[string]any{
				"key": "node-role.kubernetes.io/control-plane", "operator": "In", "values": []any{"true"},
			}}},
		}),
		plan("k3s-agent", map[string]any{
			"nodeSelector": map[string]any{"matchExpressions": []any{map[string]any{
				"key": "node-role.kubernetes.io/control-plane", "operator": "DoesNotExist",
			}}},
			// Waits until the server plan has finished everywhere.
			"prepare": map[string]any{"image": K3sUpgradeImage, "args": []any{"prepare", "k3s-server"}},
		}),
	}
}

// ForeignPlans names the upgrade Plans this panel did not write, such as the
// server-plan and agent-plan the k3s documentation has an operator apply by
// hand. Beside the panel's own, each set upgrades one node at a time — so two
// control-plane servers can be down at once, or pulled to two versions.
func ForeignPlans(plans []unstructured.Unstructured) []string {
	var names []string
	for _, plan := range plans {
		if plan.GetLabels()["app.kubernetes.io/managed-by"] != "skifity" {
			names = append(names, plan.GetName())
		}
	}
	sort.Strings(names)
	return names
}

// Texts is the English of a list of reasons, for a message.
func Texts(reasons []UpgradeReason) []string {
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		out = append(out, r.Text)
	}
	return out
}
