package cli

import (
	"context"
	"fmt"
	"strings"

	"skifity/internal/store"
)

// LiveDeployment is the version an app is serving: its newest deployment
// that succeeded, from a history read newest first.
//
// Not the highest number. A rollback is a new deployment carrying an old
// image, and a deployment after the live one may have failed or still be
// under way; either way the newest one that succeeded is what is running.
func LiveDeployment(history []store.Deployment) (store.Deployment, bool) {
	for _, deployment := range history {
		if deployment.Status == store.DeploySucceeded {
			return deployment, true
		}
	}
	return store.Deployment{}, false
}

// readLive reads an app's live deployment. Found is false when nothing has
// gone live, and also when the history could not be read: the line it fills
// is beside the app's status, which is worth printing without it.
func readLive(ctx context.Context, client *Client, appID string) (live store.Deployment, found bool) {
	var history struct {
		Items []store.Deployment `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/apps/"+appID+"/deployments"+Query("limit", "50"), nil, &history); err != nil {
		return store.Deployment{}, false
	}
	return LiveDeployment(history.Items)
}

// liveLine says which version is live: its number, its commit, who wrote
// that commit and when it went live, leaving out what the deployment does
// not have — an image app has no commit.
func liveLine(deployment store.Deployment) string {
	var line strings.Builder
	fmt.Fprintf(&line, "#%d", deployment.Number)
	if sha := deployment.CommitSHA; sha != "" {
		line.WriteString(" " + sha[:min(7, len(sha))])
	}
	if author := strings.TrimSpace(deployment.CommitAuthor); author != "" {
		line.WriteString(" by " + author)
	}
	when := deployment.FinishedAt
	if when.IsZero() {
		when = deployment.CreatedAt
	}
	line.WriteString(", " + humanTime(when))
	return line.String()
}
