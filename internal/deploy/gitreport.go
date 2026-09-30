package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// Reporting a deployment back to the repository it came from.
//
// A preview environment was built for every pull request, and its address was
// only in the panel. The reviewer — the person who most needs it, and the one
// least likely to have an account — had no way to find it. Now the commit gets
// a status with a link, and a pull request gets one comment with the preview's
// address, edited on every push rather than added to.
//
// Never allowed to change the outcome. The report is sent after the database
// already says what happened, with its own short timeout, and a refusal goes
// into the deployment's log as a line somebody can act on.

// gitReportTimeout bounds each conversation with the Git host, so a slow or
// unreachable host delays the end of a deployment by seconds, not minutes.
const gitReportTimeout = 15 * time.Second

// gitHost is how a report reaches the Git host. A Deployer uses the real one;
// a test swaps in a recorder, because what is tested here is which deployments
// are reported, with which token, to which host — the protocol itself is
// tested in internal/gitsrc.
type gitHost interface {
	ReportStatus(context.Context, gitsrc.StatusRequest) error
	UpsertComment(context.Context, gitsrc.CommentRequest) error
	ReadTree(context.Context, gitsrc.TreeRequest) (gitsrc.FileTree, error)
}

type realGitHost struct{}

func (realGitHost) ReportStatus(ctx context.Context, req gitsrc.StatusRequest) error {
	return gitsrc.ReportStatus(ctx, req)
}

func (realGitHost) UpsertComment(ctx context.Context, req gitsrc.CommentRequest) error {
	return gitsrc.UpsertComment(ctx, req)
}

func (realGitHost) ReadTree(ctx context.Context, req gitsrc.TreeRequest) (gitsrc.FileTree, error) {
	return gitsrc.ReadTree(ctx, req)
}

func (d *Deployer) git() gitHost {
	if d.gitHost == nil {
		return realGitHost{}
	}
	return d.gitHost
}

// reportToGit tells the Git host where this deployment stands.
func (d *Deployer) reportToGit(ctx context.Context, deployment store.Deployment, state gitsrc.State, reason string) {
	// A rollback redeploys a commit that was already reported, and saying
	// "success" on it again would suggest it was just tested. Deployments with
	// no commit — an image, an upload — have nothing to report against.
	if deployment.CommitSHA == "" || deployment.Trigger == "rollback" {
		return
	}
	app, err := d.db.GetApp(ctx, deployment.AppID)
	if err != nil || app.SourceType != "git" || app.GitSourceID == "" || app.RepoURL == "" {
		return
	}
	env, err := d.db.GetEnvironment(ctx, app.EnvironmentID)
	if err != nil {
		return
	}
	source, err := d.db.GetGitSource(ctx, app.GitSourceID)
	// A generic connection is plain Git with no API behind it: there is
	// nowhere to report to, and saying so on every deploy would be noise.
	if err != nil || source.Kind == "generic" {
		return
	}
	// The same rule as the clone: the team's token only ever goes to the host
	// it was issued for. An app pointed at somebody else's repository with
	// this connection selected would otherwise hand them the token in an
	// Authorization header.
	if !gitsrc.SameHost(app.RepoURL, source.BaseURL) {
		return
	}
	token, err := d.gitToken(source)
	if err != nil || token == "" {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, gitReportTimeout)
	defer cancel()

	logs := ""
	if panel := d.panelURL(ctx); panel != "" {
		logs = panel + "/apps/" + app.ID + "/deployments"
	}
	live := ""
	if state == gitsrc.StateSuccess {
		live = d.appURL(ctx, app.ID)
	}
	target := logs
	if live != "" {
		target = live
	}

	checkName := "skifity/" + env.Slug + "/" + app.Slug
	if env.Kind == store.EnvPreview {
		// One name for every pull request, so a branch protection rule can
		// require it without naming each preview.
		checkName = "skifity/preview/" + app.Slug
	}

	err = d.git().ReportStatus(ctx, gitsrc.StatusRequest{
		RepoURL: app.RepoURL, Kind: source.Kind, BaseURL: source.BaseURL, Token: token,
		CommitSHA: deployment.CommitSHA, State: state, Context: checkName,
		Description: statusDescription(state, env, reason), TargetURL: target,
	})
	// Only the final answer is worth a line in the log: a token that cannot
	// write statuses fails both times, and saying it once is enough.
	if err != nil && state != gitsrc.StatePending {
		d.appendLog(ctx, deployment.ID, "Could not report this deployment to the Git host: "+err.Error()+".")
	}

	number := pullRequestNumber(env)
	if number == 0 {
		return
	}
	err = d.git().UpsertComment(ctx, gitsrc.CommentRequest{
		RepoURL: app.RepoURL, Kind: source.Kind, BaseURL: source.BaseURL, Token: token,
		PullRequest: number,
		// Keyed by the preview app, which is one per pull request per app:
		// two apps built from one repository get a comment each.
		Marker: "skifity-preview:" + app.ID,
		Body: gitsrc.PreviewComment{
			App: app.Name, State: state, URL: live, LogsURL: logs,
			Commit: deployment.CommitSHA, Reason: reason,
		}.Markdown(),
	})
	if err != nil && state != gitsrc.StatePending {
		d.appendLog(ctx, deployment.ID, "Could not comment on the pull request: "+err.Error()+".")
	}
}

// statusDescription is the one line a Git host shows beside the check.
func statusDescription(state gitsrc.State, env store.Environment, reason string) string {
	switch state {
	case gitsrc.StateSuccess:
		if env.Kind == store.EnvPreview {
			return "The preview is live"
		}
		return "Live in " + env.Name
	case gitsrc.StateFailure:
		if strings.TrimSpace(reason) != "" {
			return "Failed: " + reason
		}
		return "The deployment failed"
	}
	return "Building and deploying"
}

// pullRequestNumber reads the pull request a preview environment was made for.
// Zero for anything else, including a preview of a branch with no pull request.
func pullRequestNumber(env store.Environment) int {
	if env.Kind != store.EnvPreview {
		return 0
	}
	digits, ok := strings.CutPrefix(env.SourceRef, "pr-")
	if !ok {
		return 0
	}
	number, err := strconv.Atoi(digits)
	if err != nil || number <= 0 {
		return 0
	}
	return number
}

// appURL is the address a reviewer should open: a domain of the team's own when
// there is one, otherwise the one the panel gave it.
func (d *Deployer) appURL(ctx context.Context, appID string) string {
	domains, err := d.db.ListDomains(ctx, appID)
	if err != nil || len(domains) == 0 {
		return ""
	}
	chosen := domains[0]
	for _, domain := range domains {
		if !domain.Auto {
			chosen = domain
			break
		}
	}
	scheme := "http://"
	if chosen.TLS {
		scheme = "https://"
	}
	path := strings.TrimSuffix(chosen.Path, "/")
	return scheme + chosen.Hostname + path
}

func (d *Deployer) panelURL(ctx context.Context) string {
	if d.PanelURL == nil {
		return ""
	}
	return strings.TrimSuffix(strings.TrimSpace(d.PanelURL(ctx)), "/")
}

// gitToken reads a Git connection's token out of its sealed configuration.
// Empty, with no error, for a connection that has none.
func (d *Deployer) gitToken(source store.GitSource) (string, error) {
	if source.ConfigEnc == "" {
		return "", nil
	}
	raw, err := d.keyring.Open(source.ConfigEnc, "git_source:"+source.TeamID+":"+source.Name)
	if err != nil {
		return "", fmt.Errorf("read the Git credentials: %w", err)
	}
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		return "", fmt.Errorf("read the Git credentials: %w", err)
	}
	return config["token"], nil
}
