package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skifity/internal/api"
	"skifity/internal/store"
)

// An app's GPUs are a runtime setting (ADR-0007): which card an instance gets
// is not in its image, so giving an app one, or changing how many, rolls it
// out and never rebuilds it.
func TestAGPUChangeReusesTheExistingImage(t *testing.T) {
	d, db, app, _ := testDeployer(t)

	first, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	markBuilt(t, db, first.ID, "registry/acme/web:abc123def456")

	if err := db.SetAppGPU(t.Context(), store.AppGPU{AppID: app.ID, Count: 2, Vendor: "nvidia", Product: "NVIDIA-A10"}); err != nil {
		t.Fatal(err)
	}
	second, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if second.BuildFingerprint != first.BuildFingerprint {
		t.Fatal("giving the app a GPU changed its build fingerprint")
	}
	if second.Image != "registry/acme/web:abc123def456" {
		t.Fatalf("giving the app a GPU rebuilt it: %q", second.Image)
	}
}

// And the build is never given one: nothing that makes the build Job reads
// the app's GPUs. builder.TestABuildNeverHasAGPU holds the Job itself to it.
func TestTheBuildNeverReadsTheAppsGPUs(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "deploy", "build.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "GPU") {
		t.Error("internal/deploy/build.go reads an app's GPUs; a build must never be given one")
	}
}
