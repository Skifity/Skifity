package deploy

import (
	"testing"

	"skifity/internal/api"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// A promoted version runs the image it was built as, and nothing is built —
// when this app would have built it the same way. When it would not, running
// it has to be asked for.
func TestAPromotionRunsTheImageOnlyWhenItWouldHaveBeenBuiltTheSame(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	const commit = "0123456789abcdef0123456789abcdef01234567"
	same := kube.BuildFingerprint(app.RepoURL, commit, app.Builder, app.DockerfilePath, app.RootDir, "", "", map[string]string{})
	promote := func(fingerprint string, force bool) (store.Deployment, error) {
		return d.Deploy(t.Context(), api.DeployRequest{
			AppID: app.ID, Trigger: "promote", CommitSHA: commit, CommitMessage: "Fix the basket",
			CommitAuthor: "ada", Image: "registry.local/acme-shop-staging/web:0123456789ab",
			Fingerprint: fingerprint, Force: force,
		})
	}

	deployment, err := promote(same, false)
	if err != nil {
		t.Fatalf("promoting a version built the same way: %v", err)
	}
	if deployment.Image != "registry.local/acme-shop-staging/web:0123456789ab" || deployment.Trigger != "promote" ||
		deployment.CommitMessage != "Fix the basket" || deployment.BuildFingerprint != same {
		t.Fatalf("the deployment is %+v", deployment)
	}
	markBuilt(t, db, deployment.ID, deployment.Image)

	// Staging's build-time variables, which this app does not have.
	built := kube.BuildFingerprint(app.RepoURL, commit, app.Builder, app.DockerfilePath, app.RootDir, "", "",
		map[string]string{"NEXT_PUBLIC_API_URL": "https://api.staging.example"})
	if _, err := promote(built, false); problemCode(err) != "promote.built_differently" {
		t.Fatalf("an image built differently answered %v", err)
	}
	forced, err := promote(built, true)
	if err != nil || forced.Image == "" {
		t.Fatalf("promoting anyway answered %+v, %v", forced, err)
	}
}
