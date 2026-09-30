package builder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The two halves of an upload build are shell: the container that waits, and
// the command the panel runs inside it with the archive on standard input. They
// are tested the way the clone script is, by running them, with the paths they
// use moved into a temporary directory.
type receiveRig struct {
	dir, workspace string
	rewrite        func(string) string
}

func newReceiveRig(t *testing.T) receiveRig {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell here")
	}
	dir := t.TempDir()
	ws := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	replacer := strings.NewReplacer(
		sourceReady, filepath.Join(dir, "ready"),
		sourceFailed, filepath.Join(dir, "failed"),
		workspace, ws,
	)
	return receiveRig{dir: dir, workspace: ws, rewrite: replacer.Replace}
}

// wait starts the waiting container's script and returns a function that
// reports how it ended.
func (r receiveRig) wait(t *testing.T, script string) func() (string, error) {
	t.Helper()
	cmd := exec.Command("sh", "-c", r.rewrite(script))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return func() (string, error) {
		select {
		case err := <-done:
			return out.String(), err
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
			t.Fatal("the waiting script never finished")
			return "", nil
		}
	}
}

func (r receiveRig) deliver(t *testing.T, archive []byte) error {
	t.Helper()
	command := ReceiveCommand()
	cmd := exec.Command(command[0], command[1], r.rewrite(command[2]))
	cmd.Stdin = bytes.NewReader(archive)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("delivery said: %s", out)
	}
	return err
}

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestAnUploadIsDeliveredIntoTheWaitingBuild(t *testing.T) {
	rig := newReceiveRig(t)
	finished := rig.wait(t, receiveScript())

	if err := rig.deliver(t, tarball(t, map[string]string{
		"package.json": `{"name":"app"}`, "src/index.js": "console.log(1)",
	})); err != nil {
		t.Fatalf("delivery failed: %v", err)
	}
	out, err := finished()
	if err != nil {
		t.Fatalf("the build did not go on after the code arrived: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Received 2 files") {
		t.Fatalf("the log does not say what arrived:\n%s", out)
	}
	if body, err := os.ReadFile(filepath.Join(rig.workspace, "src/index.js")); err != nil || string(body) != "console.log(1)" {
		t.Fatalf("the code is not in the workspace: %q, %v", body, err)
	}
	for _, marker := range []string{"ready", "failed"} {
		if _, err := os.Stat(filepath.Join(rig.workspace, marker)); err == nil {
			t.Fatalf("the %s marker is in the workspace, and so in the image", marker)
		}
	}
}

func TestAStreamCutHalfwayStopsTheBuildAtOnce(t *testing.T) {
	rig := newReceiveRig(t)
	finished := rig.wait(t, receiveScript())

	whole := tarball(t, map[string]string{"big.txt": strings.Repeat("abcdefgh", 8192)})
	if err := rig.deliver(t, whole[:len(whole)/2]); err == nil {
		t.Fatal("a cut stream was reported as delivered")
	}
	start := time.Now()
	out, err := finished()
	if err == nil {
		t.Fatalf("the build went on with half its code:\n%s", out)
	}
	if !strings.Contains(out, "could not be unpacked") {
		t.Fatalf("the build log does not say why:\n%s", out)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the build waited out its timeout instead of stopping")
	}
}

func TestABuildNobodyDeliversToGivesUp(t *testing.T) {
	rig := newReceiveRig(t)
	script := strings.Replace(receiveScript(), "-ge 900", "-ge 1", 1)
	if script == receiveScript() {
		t.Fatal("the wait is no longer 900 seconds; update this test")
	}
	out, err := rig.wait(t, script)()
	if err == nil || !strings.Contains(out, "never delivered") {
		t.Fatalf("a build nobody delivered to did not give up with a reason: %v\n%s", err, out)
	}
}

func TestAnUploadBuildHasNoRepository(t *testing.T) {
	spec := JobSpec{
		Name: "build-web-1", Namespace: "skifity-builds", Image: "registry/web:abc",
		BuildKitAddress: "tcp://buildkit:1234", Builder: BuilderRailpack,
		SourceUpload: true, CommitSHA: strings.Repeat("ab", 32),
	}
	job, err := BuildJob(spec)
	if err != nil {
		t.Fatalf("an upload build was refused: %v", err)
	}
	first := job.Spec.Template.Spec.InitContainers[0]
	if first.Name != SourceContainer || first.Args[0] != receiveScript() {
		t.Fatalf("the first container does not wait for the upload: %s", first.Name)
	}
	for _, env := range first.Env {
		if env.Name == "REPO_URL" || env.Name == "GIT_TOKEN" {
			t.Fatalf("an upload build was given %s", env.Name)
		}
	}

	noSHA := spec
	noSHA.CommitSHA = ""
	if _, err := BuildJob(noSHA); err == nil {
		t.Fatal("an upload build that does not say which upload was accepted")
	}
	withRepo := spec
	withRepo.RepoURL = "https://example.test/a.git"
	if _, err := BuildJob(withRepo); err == nil {
		t.Fatal("an upload build that also names a repository was accepted")
	}
}
