package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// cmdPreview starts, or with --close removes, the preview of a branch: the
// one a pull request from that branch would get, without the pull request.
//
//	skifity preview feature/checkout
//	skifity preview feature/checkout --close
func cmdPreview(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("preview", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	closing := flags.Bool("close", false, "remove the branch's preview instead of starting it")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		return errdoc.BadRequest(fmt.Sprintf("Name the branch, for example `%s preview feature/checkout`.", version.Binary))
	}
	branch := strings.TrimSpace(positional[0])

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	app, err := resolveApp(ctx, client, cfg, *appID)
	if err != nil {
		return err
	}
	body := map[string]string{"branch": branch}
	if *closing {
		if err := client.Do(ctx, "POST", "/api/apps/"+app+"/previews/close", body, nil); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, map[string]any{"branch": branch, "closed": true})
		}
		fmt.Fprintf(out, "The preview of %s is removed, with its databases.\n", branch)
		return nil
	}
	var started struct {
		EnvironmentID string `json:"environment_id"`
		AppID         string `json:"app_id"`
		DeploymentID  string `json:"deployment_id"`
	}
	if err := client.Do(ctx, "POST", "/api/apps/"+app+"/previews", body, &started); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, started)
	}
	fmt.Fprintf(out, "Building a preview of %s (deployment %s).\nFollow it with `%s logs --app %s`; remove it with `%s preview %s --close`.\n",
		branch, started.DeploymentID, version.Binary, started.AppID, version.Binary, branch)
	return nil
}
