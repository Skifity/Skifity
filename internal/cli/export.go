package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// Leaving, as one command.
//
// A product that is easy to adopt and hard to leave is a trap with good
// onboarding. This writes a directory somebody can read without Skifity: the
// whole team as JSON, and every app as the Kubernetes objects it would be
// applied as, so the same apps run on any cluster with kubectl and nothing of
// ours.
//
// The secret values are not in it, and that is the point rather than a gap: the
// panel promises a stored secret is never shown again, and those values are
// already in the reader's own cluster as ordinary Kubernetes Secrets. The
// README the export writes says how to read them out of it.

// exportedDocument is only the parts the CLI has to walk to write files. The
// file on disk is whatever the panel sent, unchanged.
type exportedDocument struct {
	Team struct {
		Name string `json:"name"`
	} `json:"team"`
	Projects []struct {
		Name         string `json:"name"`
		Environments []struct {
			Namespace string `json:"namespace"`
			Apps      []struct {
				Slug          string `json:"slug"`
				Manifests     string `json:"manifests"`
				ManifestError string `json:"manifest_error"`
			} `json:"apps"`
		} `json:"environments"`
	} `json:"projects"`
}

func cmdExport(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(out)
	dir := flags.String("out", "skifity-export", "the directory to write into")
	teamID := flags.String("team", "", "the team id, when you are in more than one")
	asJSON := flags.Bool("json", false, "print the export to stdout instead of writing files")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)

	team := *teamID
	if team == "" {
		team, err = resolveTeam(ctx, client, cfg)
		if err != nil {
			return err
		}
	}

	var raw json.RawMessage
	if err := client.Do(ctx, "GET", "/api/teams/"+team+"/export", nil, &raw); err != nil {
		return err
	}
	if *asJSON {
		_, err := out.Write(append([]byte(raw), '\n'))
		return err
	}

	var document exportedDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("read the export: %w", err)
	}

	// 0700 and 0600: this holds every variable that is not a secret, every
	// hostname and every repository. None of it is a credential and all of it
	// is nobody else's business.
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return exportWriteFailed(*dir, err)
	}
	pretty, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("format the export: %w", err)
	}
	file := filepath.Join(*dir, "skifity-export.json")
	if err := os.WriteFile(file, append(pretty, '\n'), 0o600); err != nil {
		return exportWriteFailed(file, err)
	}

	apps, written := 0, 0
	for _, project := range document.Projects {
		for _, env := range project.Environments {
			for _, app := range env.Apps {
				apps++
				if app.Manifests == "" {
					continue
				}
				// The namespace and the slug come from the panel, which means
				// they are somebody else's strings: a panel that answered with
				// a namespace of "../../.ssh" would have this write outside the
				// directory the user named. It is the panel they signed in to,
				// so this is unlikely — and the check is a line, while what it
				// prevents is a file written over somewhere nobody looked.
				folder, err := underneath(*dir, "manifests", env.Namespace)
				if err != nil {
					return err
				}
				if err := os.MkdirAll(folder, 0o700); err != nil {
					return exportWriteFailed(folder, err)
				}
				path, err := underneath(folder, app.Slug+".yaml")
				if err != nil {
					return err
				}
				if err := os.WriteFile(path, []byte(app.Manifests), 0o600); err != nil {
					return exportWriteFailed(path, err)
				}
				written++
			}
		}
	}

	readme := filepath.Join(*dir, "README.md")
	if err := os.WriteFile(readme, []byte(exportReadme(document.Team.Name)), 0o600); err != nil {
		return exportWriteFailed(readme, err)
	}

	fmt.Fprintf(out, "Exported %s to %s\n", document.Team.Name, *dir)
	fmt.Fprintf(out, "  skifity-export.json   everything the panel knows about this team\n")
	fmt.Fprintf(out, "  manifests/            %d of %d apps as Kubernetes objects\n", written, apps)
	fmt.Fprintf(out, "  README.md             how to run them without %s\n", version.Name)
	if written < apps {
		fmt.Fprintf(out, "\n%d apps have no manifests. The panel could not reach the cluster to\n"+
			"render them; skifity-export.json says why, beside each app.\n", apps-written)
	}
	return nil
}

func exportWriteFailed(path string, err error) error {
	return errdoc.New("cli.export_write_failed", "The export could not be written").
		WithCause("Writing %s failed: %s", path, err.Error()).
		WithImpact("The export is incomplete.").
		WithFix("Choose a directory you can write to with --out, and check there is room on the disk.")
}

func exportReadme(team string) string {
	return fmt.Sprintf(`# %s, exported from %s

Written by `+"`%s export`"+`. Nothing here needs %s to read or to use.

## What is in it

* `+"`skifity-export.json`"+` — every project, environment, app, domain, database,
  volume, scheduled command and server this team had, with the settings each one
  was running with.
* `+"`manifests/<namespace>/<app>.yaml`"+` — the same apps as plain Kubernetes
  objects: Deployment, Service, Ingress, autoscaler, disruption budget, volume
  claims and any scheduled commands.

## Running an app somewhere else

    kubectl create namespace <namespace>
    kubectl apply -f manifests/<namespace>/<app>.yaml

That is the whole migration for an app that builds to an image you still have.
The objects reference the image the app last ran, so the registry holding it has
to be reachable from the new cluster — or point the Deployment at an image you
push yourself.

## The secrets are not in here, and they are not lost

A variable marked secret is listed by name with no value. %s promises that a
stored secret is never shown again, and a bulk download is exactly where such a
promise would quietly stop being true.

You do not need the panel for them. They are in your own cluster already, as an
ordinary Kubernetes Secret:

    kubectl -n <namespace> get secret <app>-env -o go-template='{{range $k,$v := .data}}{{$k}}={{$v | base64decode}}{{"\n"}}{{end}}'

That needs the access to your own servers that you already have.

## Databases

A managed database's data is not in here — it is in its backups. Take one from
the database's Backups tab, or with the CLI, and restore it wherever you are
going. The connection details for each database are in the JSON.
`, team, version.Name, version.Binary, version.Name, version.Name)
}

// underneath joins path elements and refuses anything that climbs out.
//
// filepath.Join cleans as it goes, so "a/../../b" becomes a path beside the
// directory rather than inside it, and the result looks perfectly ordinary.
func underneath(base string, elements ...string) (string, error) {
	joined := filepath.Join(append([]string{base}, elements...)...)
	relative, err := filepath.Rel(base, joined)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errdoc.New("cli.export_path", "The panel named a file outside the export directory").
			WithCause("%s is not inside %s.", joined, base).
			WithImpact("Nothing was written for it.").
			WithFix("This is the panel returning a name it should not. Report it, " +
				"and check the panel is the one you think it is.")
	}
	return joined, nil
}
