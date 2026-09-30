package backup

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"skifity/internal/dbsvc"
)

func TestObjectKeyIsSortableAndSafe(t *testing.T) {
	at, _ := time.Parse(time.RFC3339, "2026-09-16T03:00:00Z")
	key := ObjectKey("database", "db_abc", "My Shop DB!", at)

	if !strings.HasPrefix(key, "skifity/database/db_abc/") {
		t.Fatalf("key is %q", key)
	}
	// Timestamp first inside the folder, so a bucket listing sorts by age.
	if !strings.Contains(key, "20260916-030000") {
		t.Fatalf("key has no sortable timestamp: %q", key)
	}
	if strings.ContainsAny(key, " !") {
		t.Fatalf("the name was not sanitised: %q", key)
	}
	if !strings.HasSuffix(key, ".gz") {
		t.Fatalf("key does not say it is compressed: %q", key)
	}
}

func TestSplitEndpoint(t *testing.T) {
	cases := []struct {
		endpoint string
		host     string
		secure   bool
	}{
		{"https://s3.eu-central-1.amazonaws.com", "s3.eu-central-1.amazonaws.com", true},
		{"http://minio.default.svc:9000", "minio.default.svc:9000", false},
		// A bare host defaults to TLS: guessing plaintext would silently send
		// credentials in the clear.
		{"s3.example.com", "s3.example.com", true},
	}
	for _, tc := range cases {
		host, secure, err := splitEndpoint(tc.endpoint)
		if err != nil {
			t.Errorf("splitEndpoint(%q): %v", tc.endpoint, err)
			continue
		}
		if host != tc.host || secure != tc.secure {
			t.Errorf("splitEndpoint(%q) = %q/%v, want %q/%v", tc.endpoint, host, secure, tc.host, tc.secure)
		}
	}
}

func backupSpec(engine string, restore bool) JobSpec {
	return JobSpec{
		Name: "backup-main-abc", Namespace: "acme-shop-production",
		Engine: engine, CredentialsSecret: "main-credentials",
		URLSecret: "backup-main-abc-url", BackupID: "bak_1", Restore: restore,
	}
}

// scriptsOf returns a job's two scripts: the init container's, then the main
// container's. A backup dumps then uploads; a restore downloads then loads.
func scriptsOf(t *testing.T, job *batchv1.Job) (string, string) {
	t.Helper()
	pod := job.Spec.Template.Spec
	if len(pod.InitContainers) != 1 || len(pod.Containers) != 1 {
		t.Fatalf("the job has %d init containers and %d containers, want one of each",
			len(pod.InitContainers), len(pod.Containers))
	}
	return pod.InitContainers[0].Args[0], pod.Containers[0].Args[0]
}

func TestBackupJobKeepsCredentialsOutOfTheSpec(t *testing.T) {
	for _, engine := range []string{dbsvc.EnginePostgres, dbsvc.EngineMySQL, dbsvc.EngineRedis} {
		job, err := BuildJob(backupSpec(engine, false))
		if err != nil {
			t.Fatalf("%s: BuildJob: %v", engine, err)
		}
		pod := job.Spec.Template.Spec

		// Every value must come from a Secret. A presigned URL is a credential
		// too: it grants bucket access until it expires.
		for _, container := range append(append([]corev1.Container{}, pod.InitContainers...), pod.Containers...) {
			for _, env := range container.Env {
				if env.Value != "" {
					t.Errorf("%s: %s is a literal value in the Job spec", engine, env.Name)
				}
				if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil {
					t.Errorf("%s: %s does not come from a Secret", engine, env.Name)
				}
			}
			if strings.Contains(container.Args[0], "https://") {
				t.Errorf("%s: a URL appears literally in the %s script", engine, container.Name)
			}
		}
	}
}

// TestTheDumpNeverGoesThroughAPipeToCurl is the bug this shape exists for:
// curl reading from a pipe has no length to declare, so it sends
// Transfer-Encoding: chunked, and S3 answers 501 to a chunked presigned PUT.
func TestTheDumpNeverGoesThroughAPipeToCurl(t *testing.T) {
	job, _ := BuildJob(backupSpec(dbsvc.EnginePostgres, false))
	dump, upload := scriptsOf(t, job)

	if strings.Contains(dump, "curl") {
		t.Fatalf("the dump pipes into curl:\n%s", dump)
	}
	if strings.Contains(upload, "--upload-file -") {
		t.Fatalf("curl is reading the upload from stdin:\n%s", upload)
	}
	if !strings.Contains(upload, "--upload-file /work/dump.gz") {
		t.Fatalf("curl is not uploading the staged file:\n%s", upload)
	}
	if !strings.Contains(upload, "--fail") {
		t.Fatal("curl would report success on an HTTP error, so a failed upload would look like a good backup")
	}
	// An empty dump uploaded happily is the worst outcome: a backup that
	// exists, restores nothing, and is only discovered when it is needed.
	if !strings.Contains(upload, "! -s /work/dump.gz") {
		t.Fatalf("an empty dump would be uploaded as a backup:\n%s", upload)
	}
}

// TestNothingIsInstalledAtRunTime: the job used to apk-add curl, which needs
// root, which the namespace's restricted Pod Security profile refuses. The pod
// was rejected before it ran a line.
func TestNothingIsInstalledAtRunTime(t *testing.T) {
	for _, engine := range []string{dbsvc.EnginePostgres, dbsvc.EngineMySQL, dbsvc.EngineRedis} {
		for _, restore := range []bool{false, true} {
			job, err := BuildJob(backupSpec(engine, restore))
			if err != nil {
				t.Fatalf("%s: BuildJob: %v", engine, err)
			}
			first, second := scriptsOf(t, job)
			for _, script := range []string{first, second} {
				for _, installer := range []string{"apk add", "apt-get", "yum", "microdnf"} {
					if strings.Contains(script, installer) {
						t.Errorf("%s (restore=%v): the job installs packages with %s", engine, restore, installer)
					}
				}
			}
		}
	}
}

// TestBackupPodSatisfiesRestrictedPodSecurity: every environment namespace
// enforces the restricted profile, so a pod that does not satisfy it is not
// scheduled, not merely warned about.
func TestBackupPodSatisfiesRestrictedPodSecurity(t *testing.T) {
	for _, engine := range []string{dbsvc.EnginePostgres, dbsvc.EngineMySQL, dbsvc.EngineRedis} {
		for _, restore := range []bool{false, true} {
			job, err := BuildJob(backupSpec(engine, restore))
			if err != nil {
				t.Fatalf("%s: BuildJob: %v", engine, err)
			}
			pod := job.Spec.Template.Spec

			if pod.SecurityContext == nil ||
				pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot {
				t.Errorf("%s (restore=%v): the pod does not declare runAsNonRoot", engine, restore)
			}
			if pod.SecurityContext == nil || pod.SecurityContext.SeccompProfile == nil ||
				pod.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
				t.Errorf("%s (restore=%v): the pod does not set the default seccomp profile", engine, restore)
			}

			for _, container := range append(append([]corev1.Container{}, pod.InitContainers...), pod.Containers...) {
				sc := container.SecurityContext
				if sc == nil {
					t.Errorf("%s: %s has no security context", engine, container.Name)
					continue
				}
				if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
					t.Errorf("%s: %s may escalate privileges", engine, container.Name)
				}
				if sc.Capabilities == nil || len(sc.Capabilities.Drop) == 0 ||
					sc.Capabilities.Drop[0] != "ALL" {
					t.Errorf("%s: %s does not drop every capability", engine, container.Name)
				}
				// These images default to root and drop privileges in an
				// entrypoint a job never reaches, so the uid has to be named.
				if sc.RunAsUser == nil || *sc.RunAsUser == 0 {
					t.Errorf("%s: %s does not name a non-root user", engine, container.Name)
				}
			}
		}
	}
}

func TestPostgresDumpIsRestorable(t *testing.T) {
	job, _ := BuildJob(backupSpec(dbsvc.EnginePostgres, false))
	dump, _ := scriptsOf(t, job)
	// Without --clean --if-exists, restoring over an existing database fails
	// on every object that already exists.
	if !strings.Contains(dump, "--clean") || !strings.Contains(dump, "--if-exists") {
		t.Fatalf("the dump could not be restored over an existing database:\n%s", dump)
	}
	if !strings.Contains(dump, "--no-owner") {
		t.Fatal("the dump records ownership, which breaks a restore into a different role")
	}
}

func TestMySQLDumpIsConsistent(t *testing.T) {
	job, _ := BuildJob(backupSpec(dbsvc.EngineMySQL, false))
	dump, _ := scriptsOf(t, job)
	// Without a single transaction, a dump of a live database is inconsistent
	// between tables.
	if !strings.Contains(dump, "--single-transaction") {
		t.Fatalf("the dump is not consistent:\n%s", dump)
	}
}

func TestRestoreScriptsInvert(t *testing.T) {
	for _, engine := range []string{dbsvc.EnginePostgres, dbsvc.EngineMySQL, dbsvc.EngineRedis} {
		job, err := BuildJob(backupSpec(engine, true))
		if err != nil {
			t.Fatalf("%s: BuildJob: %v", engine, err)
		}
		download, load := scriptsOf(t, job)
		if !strings.Contains(download, "curl") || !strings.Contains(download, "$BACKUP_URL") {
			t.Errorf("%s: the restore does not download the backup", engine)
		}
		// A truncated download loaded into a live database is worse than a
		// restore that refused to start.
		if !strings.Contains(download, "! -s /work/dump.gz") {
			t.Errorf("%s: an empty download would be restored", engine)
		}
		if !strings.Contains(load, "gzip -dc") {
			t.Errorf("%s: the restore does not decompress", engine)
		}
	}
	// PostgreSQL must stop at the first error, or a half-restored database
	// looks like a success.
	job, _ := BuildJob(backupSpec(dbsvc.EnginePostgres, true))
	if _, load := scriptsOf(t, job); !strings.Contains(load, "ON_ERROR_STOP=on") {
		t.Fatal("a failed statement during a restore would be ignored")
	}
}

func TestBackupJobDoesNotRetryBlindly(t *testing.T) {
	job, _ := BuildJob(backupSpec(dbsvc.EnginePostgres, false))
	if *job.Spec.BackoffLimit != 0 {
		t.Fatalf("backoffLimit is %d; a retry would upload to an expired URL", *job.Spec.BackoffLimit)
	}
	if job.Spec.ActiveDeadlineSeconds == nil {
		t.Fatal("a backup with no deadline could run forever")
	}
	if job.Spec.Template.Spec.AutomountServiceAccountToken == nil ||
		*job.Spec.Template.Spec.AutomountServiceAccountToken {
		t.Fatal("the backup pod mounts an API token it does not need")
	}
}

func TestBuildJobValidates(t *testing.T) {
	cases := map[string]func(*JobSpec){
		"no name":        func(s *JobSpec) { s.Name = "" },
		"no credentials": func(s *JobSpec) { s.CredentialsSecret = "" },
		"no url":         func(s *JobSpec) { s.URLSecret = "" },
		"bad engine":     func(s *JobSpec) { s.Engine = "mongo" },
	}
	for name, mutate := range cases {
		spec := backupSpec(dbsvc.EnginePostgres, false)
		mutate(&spec)
		if _, err := BuildJob(spec); err == nil {
			t.Errorf("%s: an invalid spec was accepted", name)
		}
	}
}

func TestGeneratedBackupScriptsAreValidShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell available")
	}
	for _, engine := range []string{dbsvc.EnginePostgres, dbsvc.EngineMySQL, dbsvc.EngineRedis} {
		for _, restore := range []bool{false, true} {
			job, err := BuildJob(backupSpec(engine, restore))
			if err != nil {
				t.Fatalf("%s: BuildJob: %v", engine, err)
			}
			checkShell(t, engine, job)
		}
	}
}

func checkShell(t *testing.T, name string, job *batchv1.Job) {
	t.Helper()
	first, second := scriptsOf(t, job)
	for _, script := range []string{first, second} {
		cmd := exec.Command("sh", "-n")
		cmd.Stdin = strings.NewReader(script)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s is not valid shell: %v\n%s\n---\n%s", name, err, output, script)
		}
	}
}

func TestJobName(t *testing.T) {
	name := JobName("backup-main", "bak_06gapwgqx49m4r2v66bk")
	if len(name) > 63 {
		t.Fatalf("job name is %d characters: %q", len(name), name)
	}
	if !strings.HasPrefix(name, "backup-main-") {
		t.Fatalf("job name is %q", name)
	}
	// Two backups of the same database must not collide.
	if name == JobName("backup-main", "bak_differentidhere") {
		t.Fatal("two backups produced the same job name")
	}
}
