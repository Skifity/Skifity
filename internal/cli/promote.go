package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/version"
)

// Promoting a version: the exact image this app runs, deployed to the same
// app in another environment of the project, without building it again.

// promoteTarget is an app a version can be promoted to, as GET .../promote
// lists them.
type promoteTarget struct {
	AppID           string `json:"app_id"`
	AppName         string `json:"app_name"`
	EnvironmentID   string `json:"environment_id"`
	EnvironmentName string `json:"environment_name"`
}

// cmdPromote runs a version of this app in another environment:
// `skifity promote --to production`.
func cmdPromote(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("promote", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id: the one whose version is promoted")
	to := flags.String("to", "", "the environment to promote it to, by name or id")
	deploymentFlag := flags.String("deployment", "", "the version to promote, by number or id; the one running now when left out")
	force := flags.Bool("force", false, "promote it even though the other app would build it differently")
	follow := flags.Bool("follow", true, "stream the rollout until it finishes")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*to) == "" {
		return errdoc.BadRequest(fmt.Sprintf("Say where to, for example `%s promote --to production`.", version.Binary))
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	app, err := resolveApp(ctx, client, cfg, *appID)
	if err != nil {
		return err
	}

	var targets struct {
		Items []promoteTarget `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/apps/"+app+"/promote", nil, &targets); err != nil {
		return err
	}
	target, err := chooseTarget(targets.Items, *to)
	if err != nil {
		return err
	}
	source, err := promotedVersion(ctx, client, app, *deploymentFlag)
	if err != nil {
		return err
	}

	path := "/api/apps/" + target.AppID + "/promote"
	var deployment store.Deployment
	err = client.Do(ctx, "POST", path, map[string]any{"deployment_id": source.ID}, &deployment)
	var problem *errdoc.Problem
	if errors.As(err, &problem) && problem.Code == "promote.built_differently" {
		if !*force {
			if !*asJSON {
				fmt.Fprintf(out, "Nothing was promoted. To run the image as it was built anyway, run this again with --force.\n")
			}
			return err
		}
		// Asked for: said, and done.
		if !*asJSON {
			fmt.Fprintf(out, "%s\nPromoting it anyway, as --force asks.\n", problem.Cause)
		}
		err = client.Do(ctx, "POST", path, map[string]any{"deployment_id": source.ID, "force": true}, &deployment)
	}
	if err != nil {
		return err
	}
	if *asJSON && !*follow {
		return writeJSON(out, deployment)
	}
	if !*asJSON {
		fmt.Fprintf(out, "Promoting #%d to %s in %s. Deployment #%d started there; nothing is rebuilt.\n",
			source.Number, target.AppName, target.EnvironmentName, deployment.Number)
	}
	if !*follow {
		return nil
	}
	return followStarted(ctx, client, target.AppID, deployment, out, *asJSON)
}

// chooseTarget is the app in the environment named. None is a refusal that
// says where the version can go, and how to make the one asked for possible.
func chooseTarget(targets []promoteTarget, to string) (promoteTarget, error) {
	to = strings.TrimSpace(to)
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.EnvironmentID == to || strings.EqualFold(target.EnvironmentName, to) {
			return target, nil
		}
		names = append(names, target.EnvironmentName)
	}
	where := "nowhere: no other environment of the project has an app of the same name and kind"
	if len(names) > 0 {
		where = strings.Join(names, ", ")
	}
	return promoteTarget{}, errdoc.New("cli.promote_no_target", "There is no app to promote to there").
		WithCause("A version is promoted to the app of the same name in another environment of the project, and %s has none. This app's versions can go to %s.", to, where).
		WithImpact("Nothing was promoted.").
		WithFix("Create the app in %s with the same name, or clone this environment in the panel, then promote again.", to).
		WithDocs("/docs/concepts#promoting-a-version")
}

// promotedVersion is the deployment named, by number or id, or the one the
// app runs now.
func promotedVersion(ctx context.Context, client *Client, app, named string) (store.Deployment, error) {
	var history struct {
		Items []store.Deployment `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/apps/"+app+"/deployments"+Query("limit", "50"), nil, &history); err != nil {
		return store.Deployment{}, err
	}
	named = strings.TrimPrefix(strings.TrimSpace(named), "#")
	for _, deployment := range history.Items {
		switch {
		case named == "" && deployment.Status == store.DeploySucceeded:
			return deployment, nil
		case named != "" && (deployment.ID == named || strconv.Itoa(deployment.Number) == named):
			return deployment, nil
		}
	}
	if named == "" {
		return store.Deployment{}, errdoc.BadRequest("This app has not deployed a version yet, so there is nothing to promote.")
	}
	return store.Deployment{}, errdoc.BadRequest(fmt.Sprintf("This app has no deployment %s among its last 50.", named))
}
