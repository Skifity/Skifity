package backup

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func baseVolumeJob() VolumeJobSpec {
	return VolumeJobSpec{
		Name: "backup-web-data-abc123", Namespace: "acme-prod",
		ClaimName: "web-data", URLSecret: "backup-web-data-abc123-url",
		BackupID: "bkp_123",
	}
}

func TestAVolumeBackupCannotWriteToWhatItIsBackingUp(t *testing.T) {
	// A backup that can write to the thing it is copying is one bug away from
	// being what destroyed it.
	job, err := BuildVolumeJob(baseVolumeJob())
	if err != nil {
		t.Fatalf("BuildVolumeJob: %v", err)
	}

	var data *corev1.Volume
	for i, volume := range job.Spec.Template.Spec.Volumes {
		if volume.Name == "data" {
			data = &job.Spec.Template.Spec.Volumes[i]
		}
	}
	if data == nil || data.PersistentVolumeClaim == nil {
		t.Fatal("the app's volume is not mounted, so there is nothing to back up")
	}
	if data.PersistentVolumeClaim.ClaimName != "web-data" {
		t.Errorf("the wrong claim is mounted: %s", data.PersistentVolumeClaim.ClaimName)
	}
	if !data.PersistentVolumeClaim.ReadOnly {
		t.Error("a backup mounts the volume it is copying as writable")
	}

	archive := job.Spec.Template.Spec.InitContainers[0]
	for _, mount := range archive.VolumeMounts {
		if mount.Name == "data" && !mount.ReadOnly {
			t.Error("the container copying the volume can write to it")
		}
	}

	// And a restore has to be able to write, or it does nothing at all.
	restoring := baseVolumeJob()
	restoring.Restore = true
	job, err = BuildVolumeJob(restoring)
	if err != nil {
		t.Fatalf("BuildVolumeJob: %v", err)
	}
	for _, volume := range job.Spec.Template.Spec.Volumes {
		if volume.Name == "data" && volume.PersistentVolumeClaim.ReadOnly {
			t.Error("a restore mounts the volume read-only, so it could never restore anything")
		}
	}
}

func TestTheBackupRunsAsTheAppsOwnUser(t *testing.T) {
	// The files on the volume belong to uid 1000, which is what every image
	// the builders produce runs as. A different account reads nothing and
	// writes a valid, empty archive — a backup that looks like it worked.
	job, err := BuildVolumeJob(baseVolumeJob())
	if err != nil {
		t.Fatalf("BuildVolumeJob: %v", err)
	}
	security := job.Spec.Template.Spec.SecurityContext
	if security.RunAsUser == nil || *security.RunAsUser != 1000 {
		t.Fatalf("the job runs as %v, want the app's own user", security.RunAsUser)
	}
	// The namespace enforces the restricted profile, which refuses a pod that
	// names no seccomp profile — the same thing that stopped the database
	// backup from ever running.
	if security.SeccompProfile == nil {
		t.Error("no seccomp profile, so Pod Security would refuse the pod")
	}
	if security.RunAsNonRoot == nil || !*security.RunAsNonRoot {
		t.Error("the pod does not promise to be non-root")
	}
}

func TestARestoreChecksTheArchiveBeforeDeletingAnything(t *testing.T) {
	// Unpacking a truncated archive over live data leaves half the old files
	// and half the new, which is worse than either.
	restoring := baseVolumeJob()
	restoring.Restore = true
	job, err := BuildVolumeJob(restoring)
	if err != nil {
		t.Fatalf("BuildVolumeJob: %v", err)
	}
	script := job.Spec.Template.Spec.Containers[0].Args[0]

	checkAt := strings.Index(script, "tar tzf")
	deleteAt := strings.Index(script, "find /data -mindepth 1 -delete")
	if checkAt < 0 || deleteAt < 0 {
		t.Fatalf("the restore neither verifies nor clears:\n%s", script)
	}
	if checkAt > deleteAt {
		t.Fatalf("the restore deletes before it has verified the archive:\n%s", script)
	}
	if !strings.Contains(script, "gzip -t") {
		t.Error("a truncated archive would only be noticed half way through unpacking it")
	}
}

func TestTheBackupLandsOnTheNodeHoldingTheVolume(t *testing.T) {
	// A volume is ReadWriteOnce. With the storage class k3s ships this is free
	// — the PersistentVolume carries its own node affinity — but on a networked
	// volume already attached elsewhere the pod sits in a Multi-Attach error
	// until it times out.
	spec := baseVolumeJob()
	spec.CoLocateWith = map[string]string{
		"app.kubernetes.io/name": "web", "app.kubernetes.io/instance": "app_1",
	}
	job, err := BuildVolumeJob(spec)
	if err != nil {
		t.Fatalf("BuildVolumeJob: %v", err)
	}
	affinity := job.Spec.Template.Spec.Affinity
	if affinity == nil || affinity.PodAffinity == nil ||
		len(affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution) == 0 {
		t.Fatal("the backup is not placed next to the app that holds the volume")
	}
	if affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].TopologyKey !=
		"kubernetes.io/hostname" {
		t.Error("the affinity is not per node, which is what ReadWriteOnce needs")
	}

	// With nothing running there is nothing to be next to, and an affinity to
	// pods that do not exist can never be satisfied: the backup would sit
	// Pending forever instead of simply running.
	job, err = BuildVolumeJob(baseVolumeJob())
	if err != nil {
		t.Fatalf("BuildVolumeJob: %v", err)
	}
	if job.Spec.Template.Spec.Affinity != nil {
		t.Error("a stopped app's volume backup is pinned to pods that do not exist")
	}
}

func TestAVolumeBackupIsRefusedWithoutSomewhereToPutIt(t *testing.T) {
	for _, broken := range []func(*VolumeJobSpec){
		func(s *VolumeJobSpec) { s.ClaimName = "" },
		func(s *VolumeJobSpec) { s.URLSecret = "" },
		func(s *VolumeJobSpec) { s.Namespace = "" },
	} {
		spec := baseVolumeJob()
		broken(&spec)
		if _, err := BuildVolumeJob(spec); err == nil {
			t.Errorf("a job with a missing field was accepted: %+v", spec)
		}
	}
}

func TestTheUploadSendsALengthRatherThanAPipe(t *testing.T) {
	// curl reading from a pipe has no length to declare and sends
	// Transfer-Encoding: chunked, which S3 answers with 501 on a presigned PUT.
	// This is the bug the database backup had, and it must not come back here.
	job, err := BuildVolumeJob(baseVolumeJob())
	if err != nil {
		t.Fatalf("BuildVolumeJob: %v", err)
	}
	script := job.Spec.Template.Spec.Containers[0].Args[0]
	if !strings.Contains(script, "--upload-file "+archiveFile) {
		t.Fatalf("the upload does not send a file with a length:\n%s", script)
	}
	if strings.Contains(script, "| curl") || strings.Contains(script, "curl -T -") {
		t.Errorf("the upload reads from a pipe:\n%s", script)
	}
}
