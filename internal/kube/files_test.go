package kube

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// An app's files, mounted into every container it runs.

func withFiles() AppSpec {
	s := baseSpec()
	s.Files = []FileMount{
		{Key: FileKey("/etc/nginx/nginx.conf"), Path: "/etc/nginx/nginx.conf"},
		{Key: FileKey("/docker-entrypoint.d/10-setup.sh"), Path: "/docker-entrypoint.d/10-setup.sh", Executable: true},
	}
	return s
}

// mountsOf finds the files' mounts in a container and the volume behind them.
func mountsOf(t *testing.T, pod corev1.PodSpec) (map[string]corev1.VolumeMount, *corev1.Volume) {
	t.Helper()
	mounts := map[string]corev1.VolumeMount{}
	for _, m := range pod.Containers[0].VolumeMounts {
		if m.Name == filesVolume {
			mounts[m.MountPath] = m
		}
	}
	for i := range pod.Volumes {
		if pod.Volumes[i].Name == filesVolume {
			return mounts, &pod.Volumes[i]
		}
	}
	return mounts, nil
}

// Each file is its own read-only subPath mount, so the rest of the directory
// it lands in — /etc/nginx's mime.types, conf.d — is still the image's.
func TestEachFileIsMountedAtItsPathAndNothingElseMoves(t *testing.T) {
	s := withFiles()
	mounts, volume := mountsOf(t, BuildDeployment(s).Spec.Template.Spec)
	if volume == nil || volume.Secret == nil || volume.Secret.SecretName != "web-files" {
		t.Fatalf("the files do not come from the app's files Secret: %+v", volume)
	}
	for _, f := range s.Files {
		m, ok := mounts[f.Path]
		if !ok {
			t.Fatalf("%s is not mounted: %+v", f.Path, mounts)
		}
		if m.SubPath != f.Key || !m.ReadOnly {
			t.Errorf("%s is mounted as %+v, want its own key, read-only", f.Path, m)
		}
	}
	modes := map[string]int32{}
	for _, item := range volume.Secret.Items {
		modes[item.Key] = *item.Mode
	}
	if modes[s.Files[0].Key] != 0o444 || modes[s.Files[1].Key] != 0o555 {
		t.Errorf("modes = %v, want the script executable and the configuration not", modes)
	}
}

// A worker, a release command and a nightly job read the same settings the
// app does; one that could not was a migration run against defaults.
func TestProcessesAndCommandsGetTheFilesToo(t *testing.T) {
	s := withFiles()
	process := BuildProcessDeployment(s, "worker", "bin/worker", 1)
	mounts, volume := mountsOf(t, process.Spec.Template.Spec)
	if len(mounts) != 2 || volume == nil || volume.Secret.SecretName != "web-files" {
		t.Errorf("the worker does not read the app's files: %+v %+v", mounts, volume)
	}
	job, err := BuildRunJob(RunSpec{App: s, Name: "web-release-1", Command: "bin/migrate", Kind: RunKindRelease})
	if err != nil {
		t.Fatal(err)
	}
	mounts, volume = mountsOf(t, job.Spec.Template.Spec)
	if len(mounts) != 2 || volume == nil {
		t.Errorf("the release command does not read the app's files: %+v", mounts)
	}
}

func TestAnAppWithoutFilesHasNoFilesVolume(t *testing.T) {
	s := baseSpec()
	if _, volume := mountsOf(t, BuildDeployment(s).Spec.Template.Spec); volume != nil {
		t.Error("an app with no files mounts a Secret that does not exist, and its pods cannot start")
	}
	if BuildFilesSecret(s, nil) != nil {
		t.Error("an app with no files is given a Secret")
	}
}

// The key is what a Secret allows whatever the path holds, and the same for
// the same path, so a change of content is a change of that key's value.
func TestAFilesKeyIsValidAndStable(t *testing.T) {
	for _, p := range []string{"/etc/nginx/nginx.conf", "/app/config/settings (prod).yml", "/x/" + strings.Repeat("a", 200)} {
		key := FileKey(p)
		if errs := validation.IsConfigMapKey(key); len(errs) > 0 {
			t.Errorf("%q gives the key %q, which a Secret refuses: %v", p, key, errs)
		}
		if FileKey(p) != key {
			t.Errorf("%q gives a different key each time", p)
		}
	}
	if FileKey("/a/config.yml") == FileKey("/b/config.yml") {
		t.Error("two files with the same name in different directories share a key")
	}
}

func TestAFileCannotGoWhereItWouldBreakTheContainer(t *testing.T) {
	for _, bad := range []string{
		"", "etc/app.conf", "/etc/../etc/passwd", "/etc/app/", "/", "/etc/hosts", "/etc/resolv.conf",
		"/proc/self/environ", "/dev/null", "/sys/x", "/a\nb",
	} {
		if ValidateFilePath(bad) == nil {
			t.Errorf("%q was accepted as a file's path", bad)
		}
	}
	for _, good := range []string{"/etc/nginx/nginx.conf", "/app/.env", "/devices.json", "/procfile"} {
		if err := ValidateFilePath(good); err != nil {
			t.Errorf("%q was refused: %v", good, err)
		}
	}

	s := withFiles()
	s.Volumes = []VolumeSpec{{Name: "data", MountPath: "/etc/nginx/nginx.conf", SizeGB: 1}}
	if s.Validate() == nil {
		t.Error("a file at a volume's own path was accepted")
	}
	s = withFiles()
	s.Files = append(s.Files, s.Files[0])
	if s.Validate() == nil {
		t.Error("two files at one path were accepted")
	}
}

// A changed file restarts the app, like a changed variable: a subPath mount
// never sees the Secret change, and a process reads its configuration once.
func TestAChangedFileChangesTheHash(t *testing.T) {
	s := withFiles()
	key := s.Files[0].Key
	before := FilesHash(s.Files, map[string][]byte{key: []byte("worker_processes 1;")})
	after := FilesHash(s.Files, map[string][]byte{key: []byte("worker_processes 4;")})
	if before == after {
		t.Error("a changed file does not change the hash, so nothing restarts")
	}
	s.Files[1].Executable = false
	if FilesHash(s.Files, map[string][]byte{key: []byte("worker_processes 1;")}) == before {
		t.Error("a file that stopped being executable does not change the hash")
	}
}
