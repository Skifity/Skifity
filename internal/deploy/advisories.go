package deploy

import (
	"context"
	"fmt"
	"time"

	"skifity/internal/builder"
	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// advisoryLookupTime bounds reading the commit's lockfile. The deploy does
// not wait on a slow Git host for a warning.
const advisoryLookupTime = 10 * time.Second

// warnAboutAdvisories says in the build log when the commit being built
// installs a framework version with a known critical vulnerability.
//
// The new-app form says it once, and an app lives for years after: the
// version that was fine when it was created is the one that is not when the
// next advisory is published, and the deploy is where somebody is looking.
// It is a warning and never a refusal — the version may be what somebody is
// deploying on the way to the fix, and a panel that refuses a deploy during an
// incident is worse than one that says so. Anything that goes wrong reading
// the repository is not the deploy's problem and is not reported.
func (d *Deployer) warnAboutAdvisories(ctx context.Context, deployment *store.Deployment, app store.App) {
	if app.SourceType != "git" || app.RepoURL == "" {
		return
	}
	ref := deployment.CommitSHA
	if ref == "" {
		ref = app.Branch
	}
	req := gitsrc.TreeRequest{
		RepoURL: app.RepoURL, Ref: ref, RootDir: app.RootDir,
		Read: append([]string{"package.json"}, builder.LockFiles...),
	}
	if app.GitSourceID != "" {
		if source, err := d.db.GetGitSource(ctx, app.GitSourceID); err == nil && gitsrc.SameHost(app.RepoURL, source.BaseURL) {
			req.Kind, req.BaseURL = source.Kind, source.BaseURL
			req.Token, req.Email, _ = d.gitCredentials(source)
		}
	}

	lookup, cancel := context.WithTimeout(ctx, advisoryLookupTime)
	defer cancel()
	tree, err := d.git().ReadTree(lookup, req)
	if err != nil {
		d.log.Debug("could not read the commit to check for advisories", "app", app.ID, "error", err)
		return
	}
	for _, a := range builder.FindAdvisories(builder.Tree{Files: tree.Files, Contents: tree.Contents}) {
		d.appendLog(ctx, deployment.ID, fmt.Sprintf(
			"Warning: %s %s has a critical vulnerability, %s. Upgrade to %s or later on that line: %s",
			a.Package, a.Version, a.ID, a.FixedIn, a.URL))
	}
}
