// Package shellgen checks the shell the panel writes.
//
// The installer and the smoke tests are files, and scripts/lint-shell.sh lints
// them. The scripts that matter most are not files: the panel builds them as
// Go strings and runs them in somebody else's cluster, where nobody will ever
// read them. A clone, a build, a database dump, a restore, a volume archive.
//
// Nothing checked any of those, and it showed. The clone script expanded a
// credential unquoted and handed git a word of the header where it expects a
// subcommand, so every build from a private repository failed — and the line
// carried `# shellcheck disable=SC2086`, silencing exactly the warning that
// names it, for a linter that was never run anywhere.
//
// This runs shellcheck over every script the panel generates, by rendering the
// jobs through their exported builders and reading the scripts back out of the
// containers, so a script added tomorrow is covered without being listed.
package shellgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"

	"skifity/internal/backup"
	"skifity/internal/builder"
	"skifity/internal/dbsvc"
	"skifity/internal/kube"
)

func TestEveryScriptThePanelWritesPassesShellcheck(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		if os.Getenv("LINT_STRICT") == "1" {
			t.Fatal("shellcheck is not installed, and LINT_STRICT=1 says it has to be")
		}
		t.Skip("shellcheck is not installed, so the generated scripts were not linted")
	}

	scripts := generatedScripts(t)
	if len(scripts) < 5 {
		t.Fatalf("only %d scripts were found; this test is not reaching the generators", len(scripts))
	}

	dir := t.TempDir()
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, "/", "-")+".sh")
			// The shebang the containers run these under. busybox ash, not
			// bash: a bashism that works on a developer's machine is a failure
			// on a server.
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o600); err != nil {
				t.Fatal(err)
			}
			// --severity=style, which is everything.
			//
			// Not a taste for tidiness: SC2086, the unquoted expansion that
			// broke every private build, is reported at "info". A threshold of
			// "warning" reads as the sensible middle and lets through the one
			// finding this test exists for — which was checked, by putting the
			// broken script back and watching it pass.
			out, err := exec.Command("shellcheck", "--shell=sh", "--severity=style", path).CombinedOutput()
			if err != nil {
				t.Errorf("shellcheck refuses the %s script:\n%s\n--- the script ---\n%s",
					name, out, script)
			}
		})
	}
}

// generatedScripts renders every job the panel builds and reads the scripts
// back out of the containers.
func generatedScripts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}

	add := func(prefix string, job *batchv1.Job, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("render the %s job: %v", prefix, err)
		}
		for _, c := range job.Spec.Template.Spec.InitContainers {
			if len(c.Args) == 1 {
				out[prefix+"/"+c.Name] = c.Args[0]
			}
		}
		for _, c := range job.Spec.Template.Spec.Containers {
			if len(c.Args) == 1 {
				out[prefix+"/"+c.Name] = c.Args[0]
			}
		}
	}

	// A build from a private repository, which is the case that was broken.
	private := builderSpec()
	private.CloneSecret = "app-git"
	job, err := builder.BuildJob(private)
	add("build-private", job, err)

	job, err = builder.BuildJob(builderSpec())
	add("build-public", job, err)

	// A build from an uploaded folder: the first container waits for the
	// panel, and the panel runs a command of its own inside it.
	uploaded := builderSpec()
	uploaded.RepoURL = ""
	uploaded.SourceUpload = true
	uploaded.CommitSHA = strings.Repeat("ab", 32)
	job, err = builder.BuildJob(uploaded)
	add("build-upload", job, err)
	out["build-upload/deliver"] = builder.ReceiveCommand()[2]

	// A static site, whose Dockerfile is written by the script itself.
	static := builderSpec()
	static.Builder = builder.BuilderStatic
	static.BuildCommand = "npm run build"
	static.StaticDir = "dist"
	job, err = builder.BuildJob(static)
	add("build-static", job, err)

	for _, engine := range []string{dbsvc.EnginePostgres, dbsvc.EngineMySQL, dbsvc.EngineRedis} {
		spec := backupSpec(engine)
		job, err = backup.BuildJob(spec)
		add("backup-"+engine, job, err)

		spec.Restore = true
		job, err = backup.BuildJob(spec)
		add("restore-"+engine, job, err)
	}

	volume := volumeSpec()
	job, err = backup.BuildVolumeJob(volume)
	add("volume-backup", job, err)

	volume.Restore = true
	job, err = backup.BuildVolumeJob(volume)
	add("volume-restore", job, err)

	// A one-off command, which wraps what somebody typed into the panel.
	job, err = kube.BuildRunJob(runSpec())
	add("run", job, err)

	return out
}

func builderSpec() builder.JobSpec {
	return builder.JobSpec{
		Name: "build-dep-1", Namespace: "acme-shop-production",
		AppID: "app_1", DeploymentID: "dep_1",
		RepoURL: "https://github.com/acme/shop", CommitSHA: "a1b2c3d4e5f6a7b8",
		Builder:          builder.BuilderRailpack,
		Image:            "skifity-registry.skifity-system.svc.cluster.local:5000/acme-shop-production/web:a1b2c3d",
		RegistryInsecure: true,
		BuildKitAddress:  "tcp://skifity-buildkit.skifity-system.svc.cluster.local:1234",
	}
}

func backupSpec(engine string) backup.JobSpec {
	return backup.JobSpec{
		Name: "backup-main-abc", Namespace: "acme-shop-production",
		Engine: engine, CredentialsSecret: "main-credentials",
		URLSecret: "backup-main-abc-url", BackupID: "bak_1",
	}
}

func volumeSpec() backup.VolumeJobSpec {
	return backup.VolumeJobSpec{
		Name: "backup-web-data-abc123", Namespace: "acme-prod",
		ClaimName: "web-data", URLSecret: "backup-web-data-abc123-url",
		BackupID: "bkp_123",
	}
}

func runSpec() kube.RunSpec {
	return kube.RunSpec{
		App: kube.AppSpec{
			Name: "web", Namespace: "acme-shop-production",
			Image: "skifity-registry:5000/acme-shop-production/web:a1b2c3d",
		},
		Name:    "web-run-abc",
		Command: "npm run migrate",
	}
}
