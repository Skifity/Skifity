package deploy

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"skifity/internal/gitsrc"
)

// The commit being built is read for the versions it installs, with the app's
// own credentials, and a known critical advisory is said in the build log.
func TestABuildSaysWhenItInstallsAVulnerableFramework(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	host := &recordingHost{tree: gitsrc.FileTree{
		Files: []string{"package.json", "package-lock.json"},
		Contents: map[string]string{
			"package.json":      `{"dependencies": {"next": "^15.1.0"}}`,
			"package-lock.json": `{"packages": {"node_modules/next": {"version": "15.1.0"}}}`,
		},
	}}
	d.gitHost = host
	connectRepository(t, d, db, &app, "https://git.example.test", "https://git.example.test/acme/shop")
	deployment := deploymentFor(t, db, app.ID, "push")

	d.warnAboutAdvisories(t.Context(), &deployment, app)

	if len(host.reads) != 1 {
		t.Fatalf("the repository was read %d times", len(host.reads))
	}
	read := host.reads[0]
	if read.Ref != reportedCommit || read.Token != "ghp_team_token" || !slices.Contains(read.Read, "package-lock.json") {
		t.Errorf("read %+v", read)
	}
	if len(read.Read) > 8 {
		t.Errorf("a check that needs a few files asked for %d", len(read.Read))
	}
	lines, err := db.ListBuildLogs(t.Context(), deployment.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var log []string
	for _, line := range lines {
		log = append(log, line.Line)
	}
	joined := strings.Join(log, "\n")
	for _, want := range []string{"next 15.1.0", "CVE-2025-29927", "15.2.3", "CVE-2025-55182", "15.1.9"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the build log does not say %q:\n%s", want, joined)
		}
	}
}

// A repository that cannot be read, or one with nothing known against it,
// adds nothing to the log and does not stop the deploy.
func TestNothingIsSaidWhenThereIsNothingToSay(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	host := &recordingHost{refuse: errors.New("rate limited")}
	d.gitHost = host
	connectRepository(t, d, db, &app, "https://git.example.test", "https://git.example.test/acme/shop")
	deployment := deploymentFor(t, db, app.ID, "push")
	d.warnAboutAdvisories(t.Context(), &deployment, app)

	host.refuse = nil
	host.tree = gitsrc.FileTree{Contents: map[string]string{
		"package-lock.json": `{"packages": {"node_modules/next": {"version": "16.0.7"}}}`,
	}}
	d.warnAboutAdvisories(t.Context(), &deployment, app)

	if lines, _ := db.ListBuildLogs(t.Context(), deployment.ID, 0, 100); len(lines) != 0 {
		t.Fatalf("the log gained %d lines", len(lines))
	}

	// An uploaded folder has no repository to read.
	app.SourceType, app.RepoURL = "upload", ""
	d.warnAboutAdvisories(t.Context(), &deployment, app)
	if len(host.reads) != 2 {
		t.Fatalf("an upload was looked up in a repository: %d reads", len(host.reads))
	}
}
