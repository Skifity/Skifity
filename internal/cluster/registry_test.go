package cluster

import "testing"

func TestTheSweepLandsWhereTheRegistryIs(t *testing.T) {
	job := registryGCJobSpec("skifity-builds")

	// The registry's volume is ReadWriteOnce and, with the storage class k3s
	// ships, node-local. A sweep scheduled anywhere else sits Pending until it
	// times out, and the disk it was meant to free never moves — silently,
	// because a Job that never ran reports nothing.
	affinity := job.Spec.Template.Spec.Affinity
	if affinity == nil || affinity.PodAffinity == nil ||
		len(affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution) == 0 {
		t.Fatal("the sweep is not pinned to the node the registry runs on")
	}
	term := affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0]
	if term.TopologyKey != "kubernetes.io/hostname" {
		t.Errorf("the sweep is pinned by %q, which is not one node", term.TopologyKey)
	}
	if term.LabelSelector == nil ||
		term.LabelSelector.MatchLabels["app.kubernetes.io/name"] != RegistryService {
		t.Errorf("the sweep is pinned to %v, not to the registry", term.LabelSelector)
	}

	container := job.Spec.Template.Spec.Containers[0]
	// Without --delete-untagged the collector keeps every manifest the panel
	// has just untagged, and frees nothing at all.
	hasFlag := false
	for _, arg := range container.Command {
		if arg == "--delete-untagged" {
			hasFlag = true
		}
	}
	if !hasFlag {
		t.Errorf("the collector keeps untagged manifests, so it frees nothing: %v", container.Command)
	}

	if len(container.VolumeMounts) == 0 || container.VolumeMounts[0].MountPath != "/var/lib/registry" {
		t.Error("the collector cannot see the registry's storage")
	}
	if job.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != RegistryService {
		t.Error("the collector mounts something other than the registry's own volume")
	}

	// A retry must not be refused by a finished Job of the same name, and the
	// records must not accumulate either.
	if job.Spec.TTLSecondsAfterFinished == nil {
		t.Error("finished sweeps would pile up in the namespace")
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Error("a failed sweep would be retried, which is not something to do unattended")
	}
}

func TestTheSweepAndABuildCannotOverlap(t *testing.T) {
	// The registry's own documentation says garbage collection against a
	// concurrent upload can delete a blob a push has written and not yet
	// referenced. The panel is the only thing that starts builds, so it holds
	// them rather than hoping for a quiet hour.
	c := &Cluster{}

	done := c.BeginBuild()
	swept := make(chan struct{})
	go func() {
		c.registryMu.Lock()
		close(swept)
		c.registryMu.Unlock()
	}()

	select {
	case <-swept:
		t.Fatal("the sweep started while a build was running")
	default:
	}

	done()
	<-swept
}
