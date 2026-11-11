package deploy

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"skifity/internal/api"
	"skifity/internal/auth"
	"skifity/internal/config"
	"skifity/internal/events"
	"skifity/internal/store"
)

// What the panel writes when a Git connection's token is replaced is what a
// build reads: the API seals it, and the deployer opens it — for a commit
// status, and for the Secret a build clones with — under the connection's
// name, which can change in the same request.
func TestABuildClonesWithTheTokenThatReplacedTheOldOne(t *testing.T) {
	d, db, app, env := testDeployer(t)
	ctx := t.Context()
	project, err := db.GetProject(ctx, env.ProjectID)
	if err != nil {
		t.Fatal(err)
	}

	// A connection from before each had a webhook secret of its own.
	const old = "ghp_faketokenfortests8888888888888888"
	const replacement = "ghp_faketokenfortests9999999999999999"
	sealed, err := d.keyring.Seal([]byte(`{"token":"`+old+`"}`), "git_source:"+project.TeamID+":acme-github")
	if err != nil {
		t.Fatal(err)
	}
	source := store.GitSource{TeamID: project.TeamID, Kind: "github_pat", Name: "acme-github", ConfigEnc: sealed}
	if err := db.CreateGitSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	app.GitSourceID = source.ID
	if err := db.UpdateApp(ctx, &app); err != nil {
		t.Fatal(err)
	}

	user := store.User{Email: "owner@example.test", Name: "owner", PasswordHash: "x"}
	if err := db.CreateUser(ctx, &user); err != nil {
		t.Fatal(err)
	}
	if err := db.AddMember(ctx, project.TeamID, user.ID, store.RoleOwner); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(db, d.keyring, time.Hour, false)
	_, token, err := authService.CreateAPIToken(ctx, user.ID, project.TeamID, "test", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	panel := httptest.NewServer(api.New(api.Options{
		Config: config.Config{}, DB: db, Keyring: d.keyring, Auth: authService,
		Hub: events.NewHub(16), Logger: slog.New(slog.DiscardHandler),
	}))
	t.Cleanup(panel.Close)
	body, _ := json.Marshal(map[string]string{"token": replacement, "name": "Work GitHub"})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPatch,
		panel.URL+"/api/teams/"+project.TeamID+"/git-sources/"+source.ID, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	answer, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("replacing the token answered %d: %s", response.StatusCode, answer)
	}

	replaced, err := db.GetGitSource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _, err := d.gitCredentials(replaced); err != nil || got != replacement {
		t.Fatalf("the deployer reads %q (%v), want the new token", got, err)
	}

	applied := &appliedObjects{}
	dynamic := syncableDynamic()
	dynamic.PrependReactor("patch", "*", applied.react)
	withCluster(t, d, db, fake.NewSimpleClientset(), dynamic)
	if err := d.ensureCloneSecret(ctx, source.ID, "skifity-builds"); err != nil {
		t.Fatal(err)
	}
	applied.mu.Lock()
	defer applied.mu.Unlock()
	if len(applied.objects) != 1 || applied.objects[0].GetKind() != "Secret" {
		t.Fatalf("applied %d objects", len(applied.objects))
	}
	data, _ := applied.objects[0].Object["stringData"].(map[string]any)
	if data["token"] != replacement {
		t.Errorf("the build's clone Secret carries %v, want the new token", data["token"])
	}
}
