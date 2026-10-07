package kube

import (
	"testing"
)

// TestAPodCannotReachTheMachineItRunsOn is the hole this file exists for: the
// egress rule excepted the private ranges and one metadata address, and a node
// that holds its public address on its network card was therefore reachable
// from every tenant pod, on every port.
func TestAPodCannotReachTheMachineItRunsOn(t *testing.T) {
	nodes := []string{"203.0.113.10", "10.0.0.5", "198.51.100.7", "203.0.113.10", "not an address", "fe80::1"}
	rules := InternetEgress(nodes)

	for _, c := range []struct {
		address string
		port    int
		want    bool
		why     string
	}{
		{"8.8.8.8", 443, true, "the internet, which is what egress is for"},
		{"93.184.216.34", 22, true, "any port on a host that is not ours (a git server over SSH)"},
		{"203.0.113.10", 80, true, "a node's web port, which is how an app calls another by its hostname"},
		{"203.0.113.10", 443, true, "a node's HTTPS port"},
		{"198.51.100.7", 443, true, "a second node's HTTPS port"},
		{"203.0.113.10", 6443, false, "the Kubernetes API on a node's public address"},
		{"203.0.113.10", 10250, false, "the kubelet"},
		{"203.0.113.10", 22, false, "SSH on the node"},
		{"203.0.113.10", 30500, false, "the registry's NodePort"},
		{"198.51.100.7", 30500, false, "the registry's NodePort on another node"},
		{"10.0.0.5", 6443, false, "a node's private address"},
		{"10.43.0.1", 443, false, "the Kubernetes API's Service address"},
		{"172.20.1.1", 80, false, "a private network"},
		{"192.168.1.1", 80, false, "a private network"},
		{"100.100.100.200", 80, false, "Alibaba's metadata service"},
		{"100.64.0.9", 80, false, "a Tailscale or carrier-grade NAT address"},
		{"169.254.169.254", 80, false, "the cloud metadata service"},
		{"169.254.170.2", 80, false, "the container metadata service some clouds add"},
		{"192.0.0.192", 80, false, "Oracle's metadata service"},
		{"168.63.129.16", 80, false, "Azure's wire server"},
	} {
		if got := EgressAllows(rules, c.address, c.port); got != c.want {
			t.Errorf("%s:%d (%s): allowed=%v, want %v", c.address, c.port, c.why, got, c.want)
		}
	}
}

func TestOnlyPublicIPv4NodeAddressesAreNamed(t *testing.T) {
	got := PublicNodeAddresses([]string{
		"203.0.113.10", "10.0.0.5", "100.64.1.1", "169.254.1.1", "127.0.0.1", "0.0.0.0",
		"::1", "2001:db8::1", "198.51.100.7", "203.0.113.10", "", "nope", "::ffff:198.51.100.9",
	})
	want := []string{"198.51.100.7", "198.51.100.9", "203.0.113.10"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	// No nodes known, no second rule: the same policy as before they were read.
	if rules := InternetEgress(nil); len(rules) != 1 {
		t.Errorf("with no nodes there are %d egress rules, want the one", len(rules))
	}
}

// TestEveryNamespaceKindGetsTheSameFence: environments, builds and plugins
// each had their own copy of the rule, and a fix to one is no fix to the rest.
func TestTheEnvironmentPolicyKeepsPodsOffTheNode(t *testing.T) {
	policies := BuildNetworkPolicies("acme-shop-production", "skifity-system", []string{"203.0.113.10"})
	allow := policies[1]
	if EgressAllows(allow.Spec.Egress, "203.0.113.10", 6443) {
		t.Error("an app can reach the Kubernetes API on the node's public address")
	}
	if !EgressAllows(allow.Spec.Egress, "203.0.113.10", 443) {
		t.Error("an app can no longer call a sibling by its public hostname")
	}
	if !EgressAllows(allow.Spec.Egress, "1.1.1.1", 443) {
		t.Error("an app cannot reach the internet")
	}
}

func TestThePluginPolicyKeepsPodsOffTheNode(t *testing.T) {
	spec := samplePlugin()
	spec.NodeAddresses = []string{"203.0.113.10"}
	_, _, _, policies := pluginParts(t, BuildPluginObjects(spec))
	for _, policy := range policies {
		if policy.Name == "default-deny" {
			continue
		}
		if EgressAllows(policy.Spec.Egress, "203.0.113.10", 30500) {
			t.Error("a plugin can reach the registry on the node's public address")
		}
		if !EgressAllows(policy.Spec.Egress, "1.1.1.1", 443) {
			t.Error("a plugin cannot reach the internet")
		}
	}
}
