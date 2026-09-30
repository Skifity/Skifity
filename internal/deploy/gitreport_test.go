package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// Reporting a deployment back to the repository: the commit gets a status, and
// a preview's pull request gets one comment with the address in it. These test
// the deployer's half — which deployments are reported, with which token, to
// which host. How each host is spoken to is tested in internal/gitsrc.

const reportedCommit = "89abcdef0123456789abcdef0123456789abcdef"

// recordingHost stands in for the Git host and remembers what it was asked.
type recordingHost struct {
	mu       sync.Mutex
	statuses []gitsrc.StatusRequest
	comments []gitsrc.CommentRequest
	reads    []gitsrc.TreeRequest
	tree     gitsrc.FileTree
	refuse   error
}

func (h *recordingHost) ReportStatus(_ context.Context, req gitsrc.StatusRequest) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.statuses = append(h.statuses, req)
	return h.refuse
}

func (h *recordingHost) UpsertComment(_ context.Context, req gitsrc.CommentRequest) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.comments = append(h.comments, req)
	return h.refuse
}

func (h *recordingHost) ReadTree(_ context.Context, req gitsrc.TreeRequest) (gitsrc.FileTree, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reads = append(h.reads, req)
	return h.tree, h.refuse
}

// connectRepository gives the app a Git connection with a sealed token, the way
// the panel stores one.
func connectRepository(t *testing.T, d *Deployer, db *store.DB, app *store.App, baseURL, repoURL string) {
	t.Helper()
	teamID, err := db.TeamIDForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	source := store.GitSource{TeamID: teamID, Kind: "github_pat", Name: "work", BaseURL: baseURL}
	config, _ := json.Marshal(map[string]string{"token": "ghp_team_token"})
	source.ConfigEnc, err = d.keyring.Seal(config, "git_source:"+teamID+":"+source.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateGitSource(t.Context(), &source); err != nil {
		t.Fatalf("CreateGitSource: %v", err)
	}
	app.GitSourceID = source.ID
	app.RepoURL = repoURL
	if _, err := db.Exec(t.Context(), `UPDATE apps SET git_source_id = ?, repo_url = ? WHERE id = ?`,
		source.ID, repoURL, app.ID); err != nil {
		t.Fatal(err)
	}
}

// previewOf makes the preview environment and app a pull request would get.
func previewOf(t *testing.T, db *store.DB, app store.App, env store.Environment, number string) store.App {
	t.Helper()
	preview := store.Environment{
		ProjectID: env.ProjectID, Name: "Pull request #" + number, Slug: "pr-" + number,
		Kind: store.EnvPreview, SourceRef: "pr-" + number, Namespace: "acme-shop-pr-" + number,
	}
	if err := db.CreateEnvironment(t.Context(), &preview); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	copied := app
	copied.ID = ""
	copied.EnvironmentID = preview.ID
	if err := db.CreateApp(t.Context(), &copied); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if err := db.CreateDomain(t.Context(), &store.Domain{
		AppID: copied.ID, Hostname: "web-pr-" + number + ".203-0-113-7.sslip.io", Auto: true,
	}); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	return copied
}

func deploymentFor(t *testing.T, db *store.DB, appID, trigger string) store.Deployment {
	t.Helper()
	deployment := store.Deployment{AppID: appID, Status: store.DeployQueued, Trigger: trigger, CommitSHA: reportedCommit}
	if err := db.CreateDeployment(t.Context(), &deployment); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return deployment
}

func TestAPreviewTellsItsPullRequestWhereItIs(t *testing.T) {
	d, db, app, env := testDeployer(t)
	host := &recordingHost{}
	d.gitHost = host
	connectRepository(t, d, db, &app, "https://git.example.test", "https://git.example.test/acme/shop")
	d.PanelURL = func(context.Context) string { return "https://panel.example.test/" }

	preview := previewOf(t, db, app, env, "12")
	d.reportToGit(t.Context(), deploymentFor(t, db, preview.ID, "preview"), gitsrc.StateSuccess, "")

	if len(host.statuses) != 1 || len(host.comments) != 1 {
		t.Fatalf("expected one status and one comment, got %d and %d", len(host.statuses), len(host.comments))
	}
	status := host.statuses[0]
	if status.Token != "ghp_team_token" || status.BaseURL != "https://git.example.test" {
		t.Errorf("the status went to %s with token %q", status.BaseURL, status.Token)
	}
	if status.CommitSHA != reportedCommit || status.State != gitsrc.StateSuccess {
		t.Errorf("the status was %s for %s", status.State, status.CommitSHA)
	}
	// One name for every pull request, so branch protection can require it.
	if status.Context != "skifity/preview/web" {
		t.Errorf("the check is named %q", status.Context)
	}
	// A reviewer clicking "Details" wants the preview, not the panel.
	if status.TargetURL != "http://web-pr-12.203-0-113-7.sslip.io" {
		t.Errorf("the status links to %q", status.TargetURL)
	}

	comment := host.comments[0]
	if comment.PullRequest != 12 {
		t.Errorf("the comment went to pull request %d", comment.PullRequest)
	}
	// Keyed by the preview app, so the next push edits this comment and a
	// second app from the same repository gets a comment of its own.
	if comment.Marker != "skifity-preview:"+preview.ID {
		t.Errorf("the comment is marked %q", comment.Marker)
	}
	for _, want := range []string{
		"http://web-pr-12.203-0-113-7.sslip.io",
		"https://panel.example.test/apps/" + preview.ID + "/deployments",
	} {
		if !strings.Contains(comment.Body, want) {
			t.Errorf("the comment is missing %q:\n%s", want, comment.Body)
		}
	}
}

// A push to the main branch gets a status, and no comment: there is no pull
// request to write one on.
func TestAProductionDeployOnlySetsAStatus(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	host := &recordingHost{}
	d.gitHost = host
	connectRepository(t, d, db, &app, "https://git.example.test", "https://git.example.test/acme/shop")

	d.reportToGit(t.Context(), deploymentFor(t, db, app.ID, "push"), gitsrc.StateFailure, "the build failed")

	if len(host.statuses) != 1 || len(host.comments) != 0 {
		t.Fatalf("expected one status and no comment, got %d and %d", len(host.statuses), len(host.comments))
	}
	status := host.statuses[0]
	if status.State != gitsrc.StateFailure || status.Context != "skifity/production/web" {
		t.Errorf("the status was %+v", status)
	}
	if !strings.Contains(status.Description, "the build failed") {
		t.Errorf("the status does not say why it failed: %q", status.Description)
	}
}

// The token is for one host. An app whose repository is somewhere else —
// pointed there by a member, with the team's connection selected — must never
// cause the token to be sent anywhere.
func TestTheTokenIsNeverSentToAnotherHost(t *testing.T) {
	d, db, app, env := testDeployer(t)
	host := &recordingHost{}
	d.gitHost = host
	connectRepository(t, d, db, &app, "https://git.example.test", "https://attacker.example.test/acme/shop")

	preview := previewOf(t, db, app, env, "3")
	d.reportToGit(t.Context(), deploymentFor(t, db, preview.ID, "preview"), gitsrc.StateSuccess, "")
	d.reportToGit(t.Context(), deploymentFor(t, db, app.ID, "push"), gitsrc.StateSuccess, "")

	if len(host.statuses)+len(host.comments) != 0 {
		t.Fatalf("a report was made for a repository on another host: %+v %+v", host.statuses, host.comments)
	}
}

// A rollback redeploys a commit that was reported when it was first deployed.
// Saying success on it again would read as a fresh test.
func TestARollbackIsNotReported(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	host := &recordingHost{}
	d.gitHost = host
	connectRepository(t, d, db, &app, "https://git.example.test", "https://git.example.test/acme/shop")

	d.reportToGit(t.Context(), deploymentFor(t, db, app.ID, "rollback"), gitsrc.StateSuccess, "")

	if len(host.statuses)+len(host.comments) != 0 {
		t.Fatalf("a rollback was reported: %+v", host.statuses)
	}
}

// Plain Git has no API to report to, and saying so on every deploy would bury
// the log in a line nobody can act on.
func TestAGenericConnectionIsNotReported(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	host := &recordingHost{}
	d.gitHost = host
	connectRepository(t, d, db, &app, "https://git.example.test", "https://git.example.test/acme/shop")
	if _, err := db.Exec(t.Context(), `UPDATE git_sources SET kind = 'generic' WHERE id = ?`, app.GitSourceID); err != nil {
		t.Fatal(err)
	}

	deployment := deploymentFor(t, db, app.ID, "push")
	d.reportToGit(t.Context(), deployment, gitsrc.StateSuccess, "")

	if len(host.statuses)+len(host.comments) != 0 {
		t.Fatalf("a generic connection was reported to: %+v", host.statuses)
	}
	if lines, _ := db.ListBuildLogs(t.Context(), deployment.ID, 0, 0); len(lines) != 0 {
		t.Fatalf("the log gained lines for a connection with nothing to report to: %+v", lines)
	}
}

// A token that can clone but not write statuses is a normal token to have. The
// refusal belongs in the deployment's log, where somebody reading why nothing
// appeared on the pull request will look — once, for the final answer.
func TestARefusalIsWrittenToTheDeploymentLog(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	host := &recordingHost{refuse: errors.New("the token cannot write commit statuses or comments on this repository")}
	d.gitHost = host
	connectRepository(t, d, db, &app, "https://git.example.test", "https://git.example.test/acme/shop")

	deployment := deploymentFor(t, db, app.ID, "push")
	d.reportToGit(t.Context(), deployment, gitsrc.StatePending, "")
	d.reportToGit(t.Context(), deployment, gitsrc.StateSuccess, "")

	lines, err := db.ListBuildLogs(t.Context(), deployment.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListBuildLogs: %v", err)
	}
	var found int
	for _, line := range lines {
		if strings.Contains(line.Line, "Could not report this deployment to the Git host") &&
			strings.Contains(line.Line, "cannot write commit statuses") {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("the refusal should be in the deployment log once, found %d: %+v", found, lines)
	}
}

func TestAPullRequestNumberIsOnlyReadFromAPreview(t *testing.T) {
	cases := []struct {
		env  store.Environment
		want int
	}{
		{store.Environment{Kind: store.EnvPreview, SourceRef: "pr-42"}, 42},
		{store.Environment{Kind: store.EnvPreview, SourceRef: "branch-feature-x"}, 0},
		{store.Environment{Kind: store.EnvPreview, SourceRef: "pr-"}, 0},
		{store.Environment{Kind: store.EnvPreview, SourceRef: "pr--1"}, 0},
		{store.Environment{SourceRef: "pr-42"}, 0},
	}
	for _, tc := range cases {
		if got := pullRequestNumber(tc.env); got != tc.want {
			t.Errorf("%+v read as pull request %d, want %d", tc.env, got, tc.want)
		}
	}
}

// A Bitbucket API token is sent with its account's email, which is sealed
// beside it; a preview's status names the pull request's branch, which is how
// Bitbucket puts it on the pull request; and the comment has no HTML, which
// Bitbucket would print as text.
func TestABitbucketPreviewIsReportedTheWayBitbucketShowsIt(t *testing.T) {
	d, db, app, env := testDeployer(t)
	host := &recordingHost{}
	d.gitHost = host
	teamID, err := db.TeamIDForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	source := store.GitSource{TeamID: teamID, Kind: "bitbucket", Name: "bitbucket", BaseURL: gitsrc.BitbucketURL}
	config, _ := json.Marshal(map[string]string{"token": "ATATT-fake", "email": "ada@example.test", "webhook_secret": "s"})
	if source.ConfigEnc, err = d.keyring.Seal(config, "git_source:"+teamID+":"+source.Name); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateGitSource(t.Context(), &source); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `UPDATE apps SET git_source_id = ?, repo_url = ? WHERE id = ?`,
		source.ID, "https://bitbucket.org/acme/shop", app.ID); err != nil {
		t.Fatal(err)
	}
	app.GitSourceID, app.RepoURL = source.ID, "https://bitbucket.org/acme/shop"

	preview := previewOf(t, db, app, env, "42")
	if _, err := db.Exec(t.Context(), `UPDATE apps SET branch = ? WHERE id = ?`, "feature/checkout", preview.ID); err != nil {
		t.Fatal(err)
	}
	d.reportToGit(t.Context(), deploymentFor(t, db, preview.ID, "preview"), gitsrc.StateSuccess, "")
	d.reportToGit(t.Context(), deploymentFor(t, db, app.ID, "push"), gitsrc.StateSuccess, "")

	if len(host.statuses) != 2 || len(host.comments) != 1 {
		t.Fatalf("expected two statuses and one comment, got %d and %d", len(host.statuses), len(host.comments))
	}
	status := host.statuses[0]
	if status.Kind != "bitbucket" || status.Token != "ATATT-fake" || status.Email != "ada@example.test" || status.Ref != "feature/checkout" {
		t.Errorf("the preview's status was sent as %+v", status)
	}
	if host.statuses[1].Ref != "" {
		t.Errorf("a deploy of the app's own branch named %q as a pull request's", host.statuses[1].Ref)
	}
	comment := host.comments[0]
	if comment.Email != "ada@example.test" || comment.PullRequest != 42 {
		t.Errorf("the comment was sent as %+v", comment)
	}
	if strings.Contains(comment.Body, "<sub>") {
		t.Errorf("the comment has HTML Bitbucket would print as text:\n%s", comment.Body)
	}
}
