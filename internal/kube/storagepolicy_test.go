package kube

import (
	"reflect"
	"testing"

	"skifity/internal/version"
)

// TestABucketTheEnvironmentKeepsPodsFromIsNamed: what the backup jobs are
// given is exactly what the environment's own policy would refuse them, and
// never a metadata service, whatever a bucket's name resolves to.
func TestABucketTheEnvironmentKeepsPodsFromIsNamed(t *testing.T) {
	nodes := []string{"203.0.113.10", "10.0.0.5"}
	resolved := []string{
		"10.1.2.3",        // MinIO on the office network
		"192.168.1.20",    // at home
		"100.101.102.103", // over Tailscale
		"203.0.113.10",    // on one of the cluster's own servers
		"10.0.0.5",        // the same server, by its private address
		"10.1.2.3",        // said twice
		"8.8.8.8",         // on the internet, which every pod reaches already
		"169.254.169.254", // the usual metadata service
		"168.63.129.16",   // Azure's wire server
		"192.0.0.192",     // Oracle Cloud's
		"100.100.100.200", // Alibaba Cloud's
		"127.0.0.1",
		"fd00::1",
		"not an address",
	}
	got := StorageAddresses(resolved, nodes)
	want := []string{"10.0.0.5", "10.1.2.3", "100.101.102.103", "192.168.1.20", "203.0.113.10"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("StorageAddresses = %v, want %v", got, want)
	}
	if got := StorageAddresses([]string{"8.8.8.8", "93.184.216.34"}, nodes); len(got) != 0 {
		t.Fatalf("a bucket on the internet needs no hole, got %v", got)
	}
}

// TestOnlyThePanelsBackupJobsReachTheBucket: the policy selects the jobs by
// labels only the panel writes, and lets them reach the bucket on its port and
// nowhere else that the environment's policy refuses.
func TestOnlyThePanelsBackupJobsReachTheBucket(t *testing.T) {
	policy := BackupStoragePolicy("team-env", []string{"10.1.2.3", "203.0.113.10"}, 9000)

	if policy.Name != StoragePolicyName || policy.Namespace != "team-env" {
		t.Fatalf("policy is %s/%s", policy.Namespace, policy.Name)
	}
	selector := policy.Spec.PodSelector.MatchLabels
	if selector["app.kubernetes.io/managed-by"] != version.Binary || selector["app.kubernetes.io/component"] != BackupJobComponent || len(selector) != 2 {
		t.Fatalf("the policy selects %v, which is not the panel's backup jobs and nothing else", selector)
	}
	if len(policy.Spec.PolicyTypes) != 1 || policy.Spec.PolicyTypes[0] != "Egress" {
		t.Fatalf("the policy is about %v; it may only add egress, never take ingress away", policy.Spec.PolicyTypes)
	}

	rules := policy.Spec.Egress
	for _, c := range []struct {
		address string
		port    int
		want    bool
	}{
		{"10.1.2.3", 9000, true},
		{"203.0.113.10", 9000, true},
		{"10.1.2.3", 22, false},
		{"203.0.113.10", 6443, false},
		{"10.1.2.4", 9000, false},
		{"169.254.169.254", 80, false},
	} {
		if got := EgressAllows(rules, c.address, c.port); got != c.want {
			t.Errorf("a backup job reaching %s:%d = %v, want %v", c.address, c.port, got, c.want)
		}
	}
}
