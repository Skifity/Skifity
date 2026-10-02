package backup

import (
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"skifity/internal/kube"
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

// It used to be uid 1000 whatever the app ran as, so a directory another uid
// owned with mode 0700 could not be archived, and a restore made every file
// 1000's — an app whose files are its own user's got its data back and could
// not write to it.
func TestTheBackupRunsAsWhoeverOwnsTheAppsFiles(t *testing.T) {
	cases := []struct {
		name        string
		confinement kube.Confinement
		root        bool
		uid         int64
	}{
		{"an image Skifity built", kube.Confinement{Level: kube.PodSecurityRestricted, BuiltHere: true}, false, 1000},
		{"an image whose uid somebody gave", kube.Confinement{Level: kube.PodSecurityRestricted, User: 33}, false, 33},
		{"an image that decides for itself", kube.Confinement{Level: kube.PodSecurityRestricted}, false, 1000},
		{"anything where root is allowed", kube.Confinement{Level: kube.PodSecurityBaseline}, true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			owner := VolumeOwnerFor(c.confinement)
			if owner.Root != c.root || (!c.root && owner.UID != c.uid) {
				t.Fatalf("owner is %+v, want root=%v uid=%d", owner, c.root, c.uid)
			}
			for _, restore := range []bool{false, true} {
				spec := baseVolumeJob()
				spec.Restore = restore
				spec.Owner = owner
				job, err := BuildVolumeJob(spec)
				if err != nil {
					t.Fatalf("BuildVolumeJob: %v", err)
				}
				pod := job.Spec.Template.Spec
				if !slices.Contains(pod.SecurityContext.SupplementalGroups, 1000) {
					t.Error("the app's group is not among the pod's, so group-readable files are not read")
				}
				files := filesContainer(t, pod)
				if !c.root {
					if pod.SecurityContext.RunAsUser == nil || *pod.SecurityContext.RunAsUser != c.uid {
						t.Errorf("the pod runs as %v, want %d", pod.SecurityContext.RunAsUser, c.uid)
					}
					if files.SecurityContext.RunAsUser != nil {
						t.Error("the files container names a user of its own instead of the app's")
					}
					continue
				}
				// Root for the files, with exactly what reading and writing
				// anybody's files takes, all of it in the runtime's default
				// set, which is what the baseline level allows.
				if pod.SecurityContext.RunAsNonRoot != nil {
					t.Error("the pod promises non-root while its files container runs as root")
				}
				security := files.SecurityContext
				if security.RunAsUser == nil || *security.RunAsUser != 0 {
					t.Errorf("the files container runs as %v, want root", security.RunAsUser)
				}
				added := security.Capabilities.Add
				for _, want := range []corev1.Capability{"DAC_OVERRIDE", "CHOWN", "FOWNER", "FSETID"} {
					if !slices.Contains(added, want) {
						t.Errorf("the files container cannot %s", want)
					}
				}
				if len(added) != 4 || !slices.Contains(security.Capabilities.Drop, "ALL") {
					t.Errorf("the files container has more than it needs: drop %v, add %v",
						security.Capabilities.Drop, added)
				}
				if security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation {
					t.Error("the files container may escalate its privileges")
				}
				// The upload or download step never needs root.
				for _, other := range append(pod.InitContainers, pod.Containers...) {
					if other.Name == files.Name {
						continue
					}
					if other.SecurityContext.RunAsNonRoot == nil || !*other.SecurityContext.RunAsNonRoot {
						t.Errorf("%s does not promise to be non-root", other.Name)
					}
				}
			}
		})
	}
}

// A backup that cannot read something stops, and says what to change.
func TestAnUnreadableFileSaysWhatToChange(t *testing.T) {
	script := archiveScript()
	if !strings.Contains(script, "if ! tar czf") || !strings.Contains(script, "uid the") {
		t.Errorf("the archive does not explain a failure to read:\n%s", script)
	}
}

func filesContainer(t *testing.T, pod corev1.PodSpec) corev1.Container {
	t.Helper()
	for _, container := range append(pod.InitContainers, pod.Containers...) {
		for _, mount := range container.VolumeMounts {
			if mount.Name == "data" {
				return container
			}
		}
	}
	t.Fatal("no container mounts the volume")
	return corev1.Container{}
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
