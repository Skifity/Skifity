package backup

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"skifity/internal/dbsvc"
)

func names(containers []corev1.Container) string {
	var out []string
	for _, c := range containers {
		out = append(out, c.Name)
	}
	return strings.Join(out, ",")
}

// With a passphrase, the dump is sealed after it is made and before it is
// uploaded, and opened after it is downloaded and before it is loaded, by the
// panel's own image; without one, the job is what it was.
func TestASealedJobSealsBetweenTheDumpAndTheUpload(t *testing.T) {
	const image = "ghcr.io/example/skifity:1.0.0"
	spec := JobSpec{Name: "backup-shop", Namespace: "acme-shop-production", Engine: dbsvc.EnginePostgres,
		CredentialsSecret: "shop-credentials", URLSecret: "backup-shop-url", BackupID: "bak_1"}

	plain, err := BuildJob(spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(plain.Spec.Template.Spec.InitContainers); got != "dump" {
		t.Fatalf("an unsealed backup runs %s", got)
	}

	spec.SealImage = image
	backup, err := BuildJob(spec)
	if err != nil {
		t.Fatal(err)
	}
	pod := backup.Spec.Template.Spec
	if got := names(pod.InitContainers) + "|" + names(pod.Containers); got != "dump,seal|upload" {
		t.Fatalf("a sealed backup runs %s", got)
	}
	seal := pod.InitContainers[1]
	if seal.Image != image || strings.Join(seal.Args, " ") != "backup-seal "+dumpFile || len(seal.Command) != 0 {
		t.Fatalf("the seal step is %+v", seal)
	}
	if env := seal.Env[0]; env.Name != "SKIFITY_BACKUP_PASSPHRASE" || env.ValueFrom.SecretKeyRef.Name != "backup-shop-url" ||
		env.ValueFrom.SecretKeyRef.Key != "passphrase" {
		t.Fatalf("the passphrase comes from %+v", env)
	}
	if sc := seal.SecurityContext; *sc.RunAsNonRoot != true || *sc.ReadOnlyRootFilesystem != true || *sc.AllowPrivilegeEscalation {
		t.Fatalf("the seal step runs as %+v", sc)
	}

	spec.Restore = true
	restore, err := BuildJob(spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(restore.Spec.Template.Spec.InitContainers) + "|" + names(restore.Spec.Template.Spec.Containers); got != "download,open|load" {
		t.Fatalf("a sealed restore runs %s", got)
	}

	volume, err := BuildVolumeJob(VolumeJobSpec{Name: "backup-files", Namespace: "n", ClaimName: "web-data",
		URLSecret: "u", BackupID: "bak_2", SealImage: image})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(volume.Spec.Template.Spec.InitContainers); got != "archive,seal" {
		t.Fatalf("a sealed volume backup runs %s", got)
	}
}

func TestTheJobSecretCarriesThePassphraseOnlyWhenThereIsOne(t *testing.T) {
	if _, ok := JobSecret("s", "n", "https://bucket/x", "").StringData["passphrase"]; ok {
		t.Fatal("an unsealed job was given a passphrase")
	}
	if got := JobSecret("s", "n", "https://bucket/x", "p4ss phrase").StringData["passphrase"]; got != "p4ss phrase" {
		t.Fatalf("the passphrase is %q", got)
	}
}
