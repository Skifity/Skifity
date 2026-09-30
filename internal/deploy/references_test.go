package deploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/secretmgr"
	"skifity/internal/store"
)

// Variables read from a secret manager, through the deployer: when they are
// read, what they do to the build fingerprint, what a failure does, and what
// a refresh rolls out.

const vaultToken = "hvs.fake-token-for-deploy-tests"

// fakeManager is a Vault whose one secret, shop, a test can change.
type fakeManager struct {
	server *httptest.Server
	reads  atomic.Int32
	mu     sync.Mutex
	fields map[string]any
}

func newFakeManager(t *testing.T, fields map[string]any) *fakeManager {
	t.Helper()
	f := &fakeManager{fields: fields}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Vault-Token") != vaultToken {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
			return
		}
		if r.URL.Path != "/v1/secret/data/shop" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[]}`))
			return
		}
		f.reads.Add(1)
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"data": f.fields, "metadata": map[string]any{"version": 1},
		}})
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeManager) set(key string, value any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fields[key] = value
}

// withManager gives the test deployer a connection to a fake Vault, for the
// team the test app belongs to.
func withManager(t *testing.T, d *Deployer, db *store.DB, app store.App, fields map[string]any) (*fakeManager, store.SecretConnection) {
	t.Helper()
	f := newFakeManager(t, fields)
	d.Secrets.Client = &http.Client{Timeout: 5 * time.Second}
	teamID, err := db.TeamIDForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings, credentials, err := secretmgr.Normalize(secretmgr.KindVault,
		map[string]string{"address": f.server.URL}, map[string]string{"token": vaultToken})
	if err != nil {
		t.Fatal(err)
	}
	c := store.SecretConnection{ID: store.NewID("sm"), TeamID: teamID, Name: "company-vault", Kind: secretmgr.KindVault, Settings: settings}
	sealed, err := secretmgr.SealCredentials(d.keyring, c.ID, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSecretConnection(t.Context(), &c, sealed); err != nil {
		t.Fatal(err)
	}
	return f, c
}

func reference(t *testing.T, db *store.DB, appID, key string, connection store.SecretConnection, field string, buildTime bool) {
	t.Helper()
	v := store.Variable{AppID: appID, Key: key, IsSecret: true, BuildTime: buildTime,
		Reference: &store.SecretReference{ConnectionID: connection.ID, Path: "shop", Key: field}}
	if err := db.SetVariable(t.Context(), &v, ""); err != nil {
		t.Fatal(err)
	}
}

func settled(t *testing.T, db *store.DB, deploymentID string) store.Deployment {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		deployment, err := db.GetDeployment(t.Context(), deploymentID)
		if err != nil {
			t.Fatal(err)
		}
		if deployment.Status.Terminal() {
			return deployment
		}
		if time.Now().After(deadline) {
			t.Fatalf("the deployment was still %s", deployment.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestARuntimeReferenceNeverChangesTheBuildFingerprint(t *testing.T) {
	d, db, app, env := testDeployer(t)
	f, vault := withManager(t, d, db, app, map[string]any{"stripe_key": "sk_live_first"})
	reference(t, db, app.ID, "STRIPE_KEY", vault, "stripe_key", false)

	first, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	markBuilt(t, db, first.ID, "registry/acme/web:abc123def456")

	f.set("stripe_key", "sk_live_rotated")
	second, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if second.BuildFingerprint != first.BuildFingerprint || second.Image != "registry/acme/web:abc123def456" {
		t.Fatalf("a runtime value from a secret manager changed the build: %s vs %s, image %q",
			first.BuildFingerprint, second.BuildFingerprint, second.Image)
	}

	// And it is what the app is given.
	variables, err := d.runtimeVariables(t.Context(), app, env)
	if err != nil {
		t.Fatal(err)
	}
	if variables["STRIPE_KEY"] != "sk_live_rotated" {
		t.Errorf("STRIPE_KEY = %q", variables["STRIPE_KEY"])
	}
}

func TestABuildTimeReferenceIsPartOfTheFingerprint(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	f, vault := withManager(t, d, db, app, map[string]any{"public_key": "pk_first"})

	plain, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
	if err != nil {
		t.Fatal(err)
	}
	markBuilt(t, db, plain.ID, "registry/acme/web:1")

	reference(t, db, app.ID, "NEXT_PUBLIC_KEY", vault, "public_key", true)
	withKey, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
	if err != nil {
		t.Fatal(err)
	}
	if withKey.BuildFingerprint == plain.BuildFingerprint || withKey.Image != "" {
		t.Fatal("a new build-time variable read from a secret manager did not mean a new build")
	}
	markBuilt(t, db, withKey.ID, "registry/acme/web:2")

	again, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
	if err != nil {
		t.Fatal(err)
	}
	if again.BuildFingerprint != withKey.BuildFingerprint || again.Image != "registry/acme/web:2" {
		t.Fatal("the same build-time value was built again")
	}
	settled(t, db, again.ID)

	f.set("public_key", "pk_rotated")
	rotated, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.BuildFingerprint == withKey.BuildFingerprint || rotated.Image != "" {
		t.Fatal("a changed build-time value in the secret manager reused the old image")
	}
	// It reaches the build as a secret, never as a build argument.
	secret, err := d.secretBuildTimeVariables(t.Context(), app)
	if err != nil || !secret["NEXT_PUBLIC_KEY"] {
		t.Errorf("a build-time reference is not a secret build argument: %v %v", secret, err)
	}
}

func TestAReferenceThatCannotBeReadKeepsTheOldVersionAndNamesTheVariable(t *testing.T) {
	d, db, app, env := testDeployer(t)
	var logs bytes.Buffer
	d.log = slog.New(slog.NewTextHandler(&logs, nil))
	_, vault := withManager(t, d, db, app, map[string]any{"stripe_key": "sk_live_sentinel_value"})
	reference(t, db, app.ID, "STRIPE_KEY", vault, "stripe_key", false)

	good, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
	if err != nil {
		t.Fatal(err)
	}
	markBuilt(t, db, good.ID, "registry/acme/web:1")

	// A key that is not there, at runtime: the deployment is made, and fails
	// before anything is built or applied.
	reference(t, db, app.ID, "MAIL_PASSWORD", vault, "mail_password", false)
	broken, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
	if err != nil {
		t.Fatal(err)
	}
	failed := settled(t, db, broken.ID)
	if failed.Status != store.DeployFailed || failed.ErrorCode != "secrets.reference_unresolved" {
		t.Fatalf("the deployment ended %s with %q", failed.Status, failed.ErrorCode)
	}
	if !strings.Contains(failed.ErrorMessage, "MAIL_PASSWORD") || !strings.Contains(failed.ErrorMessage, "company-vault") {
		t.Errorf("the failure does not name the variable and the connection: %s", failed.ErrorMessage)
	}
	if last, err := db.LatestSuccessfulDeployment(t.Context(), app.ID); err != nil || last.ID != good.ID {
		t.Errorf("the version that was serving is no longer the latest that succeeded: %v %v", last.ID, err)
	}

	// A sync fails the same way, before the cluster, with the same problem.
	err = d.Sync(t.Context(), app.ID)
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "secrets.reference_unresolved" {
		t.Fatalf("a sync answered %v", err)
	}

	// A build-time one fails the deploy before it is even queued.
	reference(t, db, app.ID, "MAIL_PASSWORD", vault, "mail_password", true)
	if _, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"}); !errors.As(err, &problem) ||
		problem.Code != "secrets.reference_unresolved" {
		t.Fatalf("a build-time reference that cannot be read answered %v", err)
	}

	// Nothing that was read is in the deployment's log or the panel's.
	lines, err := db.ListBuildLogs(t.Context(), broken.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines {
		if strings.Contains(line.Line, "sk_live_sentinel_value") {
			t.Errorf("the build log carries a value: %s", line.Line)
		}
	}
	if strings.Contains(logs.String(), "sk_live_sentinel_value") {
		t.Errorf("the panel's log carries a value:\n%s", logs.String())
	}
	_ = env
}

func TestAForkPreviewIsGivenNoReferencedSecret(t *testing.T) {
	d, db, app, env := testDeployer(t)
	f, vault := withManager(t, d, db, app, map[string]any{"stripe_key": "sk_live_x"})
	shared := store.SharedVariable{ProjectID: env.ProjectID, Key: "SHARED_KEY", IsSecret: true,
		Reference: &store.SecretReference{ConnectionID: vault.ID, Path: "shop", Key: "stripe_key"}}
	if err := db.SetSharedVariable(t.Context(), &shared, ""); err != nil {
		t.Fatal(err)
	}
	fork := store.Environment{ProjectID: env.ProjectID, Name: "Pull request #9", Slug: "pr-9", Kind: store.EnvPreview,
		SourceRef: "pr-9", Namespace: "acme-shop-pr-9", FromFork: true}
	if err := db.CreateEnvironment(t.Context(), &fork); err != nil {
		t.Fatal(err)
	}
	copied := app
	copied.ID, copied.EnvironmentID = "", fork.ID
	if err := db.CreateApp(t.Context(), &copied); err != nil {
		t.Fatal(err)
	}
	// Even an own reference somebody set on the preview's copy.
	reference(t, db, copied.ID, "OWN_KEY", vault, "stripe_key", true)

	variables, err := d.runtimeVariables(t.Context(), copied, fork)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := variables["SHARED_KEY"]; ok {
		t.Error("a fork's preview was given a shared variable read from a secret manager")
	}
	if _, ok := variables["OWN_KEY"]; ok {
		t.Error("a fork's preview was given its own variable read from a secret manager")
	}
	if build, err := d.buildTimeVariables(t.Context(), copied); err != nil || build["OWN_KEY"] != "" {
		t.Errorf("a fork's build was given a reference: %v %v", build, err)
	}
	if n := f.reads.Load(); n != 0 {
		t.Errorf("the secret manager was asked %d times for a fork's preview", n)
	}

	// The environment it came from gets it.
	variables, err = d.runtimeVariables(t.Context(), app, env)
	if err != nil || variables["SHARED_KEY"] != "sk_live_x" {
		t.Errorf("production lost the shared reference: %v %v", variables, err)
	}
}

func TestARefreshRollsOutOnlyWhatChanged(t *testing.T) {
	d, db, app, env := testDeployer(t)
	f, vault := withManager(t, d, db, app, map[string]any{"stripe_key": "sk_1", "public_key": "pk_1"})
	reference(t, db, app.ID, "STRIPE_KEY", vault, "stripe_key", false)

	// Nothing deployed yet: a change is found and there is nothing to roll out.
	result, err := d.RefreshReferences(t.Context(), app.ID, "usr_1")
	if err != nil || !result.NotDeployed || result.References != 1 {
		t.Fatalf("before any deploy: %+v, %v", result, err)
	}

	deployed, err := d.Deploy(t.Context(), api.DeployRequest{AppID: app.ID, CommitSHA: "abc123def456"})
	if err != nil {
		t.Fatal(err)
	}
	markBuilt(t, db, deployed.ID, "registry/acme/web:1")
	// What the apply would have written to the app's Secret.
	applied, err := d.resolveVariables(t.Context(), app, env)
	if err != nil {
		t.Fatal(err)
	}
	d.recordReferences(t.Context(), app.ID, applied)

	result, err = d.RefreshReferences(t.Context(), app.ID, "usr_1")
	if err != nil || len(result.Changed) != 0 || result.RolledOut || result.Deployment != nil {
		t.Fatalf("nothing changed and the refresh answered %+v, %v", result, err)
	}

	f.set("stripe_key", "sk_2")
	result, err = d.RefreshReferences(t.Context(), app.ID, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(result.Changed, ",") != "STRIPE_KEY" || !result.RolledOut || result.Deployment != nil ||
		len(result.BuildTimeChanged) != 0 {
		t.Fatalf("a changed runtime value: %+v", result)
	}
	if list, _ := db.ListDeployments(t.Context(), app.ID, 10); len(list) != 1 {
		t.Errorf("a runtime change made a deployment: %d of them", len(list))
	}

	// A build-time one changing builds the version that is running again.
	reference(t, db, app.ID, "NEXT_PUBLIC_KEY", vault, "public_key", true)
	applied, _ = d.resolveVariables(t.Context(), app, env)
	d.recordReferences(t.Context(), app.ID, applied)
	f.set("public_key", "pk_2")
	result, err = d.RefreshReferences(t.Context(), app.ID, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(result.BuildTimeChanged, ",") != "NEXT_PUBLIC_KEY" || result.Deployment == nil {
		t.Fatalf("a changed build-time value: %+v", result)
	}
	if result.Deployment.Trigger != "refresh" || result.Deployment.CommitSHA != "abc123def456" ||
		result.Deployment.BuildFingerprint == deployed.BuildFingerprint || result.Deployment.Image != "" {
		t.Errorf("the rebuild is not of the running version with the new value: %+v", result.Deployment)
	}
	settled(t, db, result.Deployment.ID)

	// The digests are sealed, and none of them is a value.
	digests, err := db.ReferenceDigests(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for key, sealed := range digests {
		if strings.Contains(sealed, "sk_") || strings.Contains(sealed, "pk_") || strings.Contains(sealed, digest("sk_1")) {
			t.Errorf("what %s held is stored readable: %s", key, sealed)
		}
	}
}

func TestThePeriodicRefreshBacksOffAManagerThatFails(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	_, vault := withManager(t, d, db, app, map[string]any{"stripe_key": "sk_1"})
	reference(t, db, app.ID, "STRIPE_KEY", vault, "missing", false)

	row, _ := db.GetSecretConnection(t.Context(), vault.ID)
	row.RefreshMinutes = 5
	if err := db.UpdateSecretConnection(t.Context(), &row.SecretConnection, ""); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	d.RefreshDue(t.Context(), now)
	row, _ = db.GetSecretConnection(t.Context(), vault.ID)
	if row.RefreshFailures != 1 || !row.NextRefreshAt.Equal(now.Add(10*time.Minute)) || !strings.Contains(row.LastError, "STRIPE_KEY") {
		t.Fatalf("after one failure: failures %d, next %s, error %q", row.RefreshFailures, row.NextRefreshAt, row.LastError)
	}

	// Not due yet: nothing is asked.
	d.RefreshDue(t.Context(), now.Add(5*time.Minute))
	if again, _ := db.GetSecretConnection(t.Context(), vault.ID); again.RefreshFailures != 1 {
		t.Fatal("a connection that was not due was refreshed")
	}

	d.RefreshDue(t.Context(), now.Add(10*time.Minute))
	row, _ = db.GetSecretConnection(t.Context(), vault.ID)
	if row.RefreshFailures != 2 || !row.NextRefreshAt.Equal(now.Add(10*time.Minute).Add(20*time.Minute)) {
		t.Fatalf("after two failures: failures %d, next %s", row.RefreshFailures, row.NextRefreshAt)
	}

	// Fixed: back to the interval, and the error is gone.
	reference(t, db, app.ID, "STRIPE_KEY", vault, "stripe_key", false)
	at := now.Add(30 * time.Minute)
	d.RefreshDue(t.Context(), at)
	row, _ = db.GetSecretConnection(t.Context(), vault.ID)
	if row.RefreshFailures != 0 || row.LastError != "" || !row.NextRefreshAt.Equal(at.Add(5*time.Minute)) {
		t.Fatalf("after it worked: failures %d, next %s, error %q", row.RefreshFailures, row.NextRefreshAt, row.LastError)
	}
	if backoff(5*time.Minute, 20) != maxRefreshBackoff {
		t.Error("the backoff is not bounded")
	}
}
