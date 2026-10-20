package builder

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testAuthority = "-----BEGIN CERTIFICATE-----\nnot-a-real-certificate\n-----END CERTIFICATE-----\n"

// The clone of a repository on a Git host whose certificate the operator's own
// authority signed trusts that authority beside the public ones: GitHub, for a
// submodule, is still verified as it was.
func TestTheCloneTrustsTheOperatorsAuthoritiesBesideThePublicOnes(t *testing.T) {
	spec := baseJob()
	spec.RepoURL = "https://git.home.arpa/acme/shop"
	spec.GitAuthorities = testAuthority
	spec.GitAuthoritiesConfigMap = "build-dep-1-git-ca"

	job, err := BuildJob(spec)
	if err != nil {
		t.Fatalf("BuildJob: %v", err)
	}
	pod := job.Spec.Template.Spec
	mounted := map[string]bool{}
	for _, container := range append(pod.InitContainers, pod.Containers...) {
		for _, mount := range container.VolumeMounts {
			if mount.Name == "git-authorities" {
				mounted[container.Name] = mount.ReadOnly && mount.MountPath == gitAuthoritiesDir
			}
		}
	}
	if len(mounted) != 1 || !mounted[SourceContainer] {
		t.Errorf("the authorities are mounted in %v, want read-only in the clone alone", mounted)
	}
	found := false
	for _, volume := range pod.Volumes {
		if volume.Name == "git-authorities" && volume.ConfigMap != nil && volume.ConfigMap.Name == "build-dep-1-git-ca" {
			found = true
		}
	}
	if !found {
		t.Error("no volume reads the authorities' ConfigMap")
	}
	configMap := GitAuthoritiesConfigMapObject(spec)
	if configMap == nil || configMap.Name != "build-dep-1-git-ca" || configMap.Data[GitAuthoritiesKey] != testAuthority {
		t.Fatalf("the ConfigMap is %+v, want the authorities under %s", configMap, GitAuthoritiesKey)
	}

	// Run as the clone runs it, against files standing in for the image's
	// public authorities and the operator's.
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell here")
	}
	dir := t.TempDir()
	system := filepath.Join(dir, "system.pem")
	operator := filepath.Join(dir, "operator")
	combined := filepath.Join(dir, "combined.pem")
	if err := os.MkdirAll(operator, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(system, []byte("PUBLIC\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(operator, GitAuthoritiesKey), []byte(testAuthority), 0o644); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dir, "cainfo")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// A stub git that writes down which bundle it was told to verify with.
	stub := "#!/bin/sh\nprintf '%s\\n' \"$GIT_SSL_CAINFO\" >> " + record + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Skipf("cannot create %s here: %v", workspace, err)
	}
	script := strings.NewReplacer(systemAuthorities, system, gitAuthoritiesDir, operator, cloneAuthorities, combined).
		Replace(cloneScript(spec))
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "REPO_URL="+spec.RepoURL, "GIT_REF=main")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the clone script failed: %v\n%s", err, out)
	}
	told, err := os.ReadFile(record)
	if err != nil || strings.TrimSpace(strings.Split(string(told), "\n")[0]) != combined {
		t.Errorf("git verified with %q, want %s", told, combined)
	}
	bundle, err := os.ReadFile(combined)
	if err != nil || string(bundle) != "PUBLIC\n"+testAuthority {
		t.Errorf("the bundle holds %q, want the public authorities and then the operator's", bundle)
	}
}

// Without authorities nothing about the clone changes, and an upload, which
// is not cloned, is given none.
func TestNoAuthoritiesLeaveTheCloneAsItWas(t *testing.T) {
	if strings.Contains(cloneScript(baseJob()), "GIT_SSL_CAINFO") {
		t.Error("a clone with no authorities to trust still sets a bundle")
	}
	upload := baseJob()
	upload.SourceUpload, upload.RepoURL = true, ""
	upload.GitAuthorities, upload.GitAuthoritiesConfigMap = testAuthority, "build-dep-1-git-ca"
	if GitAuthoritiesConfigMapObject(upload) != nil {
		t.Error("an upload, which clones nothing, was given the authorities")
	}
	job, err := BuildJob(upload)
	if err != nil {
		t.Fatalf("BuildJob: %v", err)
	}
	for _, volume := range job.Spec.Template.Spec.Volumes {
		if volume.Name == "git-authorities" {
			t.Error("an upload's build mounts the authorities")
		}
	}

	missing := baseJob()
	missing.GitAuthorities = testAuthority
	if _, err := BuildJob(missing); err == nil {
		t.Error("a build given authorities and no ConfigMap to read them from was accepted")
	}
}
