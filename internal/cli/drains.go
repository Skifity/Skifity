package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// drainAnswer is a log drain as the panel lists it. The panel never answers a
// drain's secrets, and there is no field here for one.
type drainAnswer struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	Destination   string            `json:"destination"`
	Status        string            `json:"status"`
	Enabled       bool              `json:"enabled"`
	Scoped        bool              `json:"scoped"`
	Projects      []string          `json:"projects"`
	IncludeBuilds bool              `json:"include_builds"`
	Settings      map[string]string `json:"settings,omitempty"`
	Secrets       []string          `json:"secrets,omitempty"`
	TestedAt      time.Time         `json:"tested_at"`
	TestError     string            `json:"test_error,omitempty"`
}

type drainKind struct {
	Kind   string `json:"kind"`
	Fields []struct {
		Key      string   `json:"key"`
		Secret   bool     `json:"secret"`
		Required bool     `json:"required"`
		Options  []string `json:"options"`
		Default  string   `json:"default"`
	} `json:"fields"`
}

type drainList struct {
	Items     []drainAnswer `json:"items"`
	Kinds     []drainKind   `json:"kinds"`
	Collector struct {
		Configuration string `json:"configuration"`
		Error         string `json:"error"`
		Live          *struct {
			State   string `json:"state"`
			Desired int    `json:"desired"`
			Ready   int    `json:"ready"`
		} `json:"live"`
	} `json:"collector"`
}

// cmdDrains lists, adds, tests and removes the team's log drains.
//
//	skifity drains                                   the team's drains, and the collector
//	skifity drains add Axiom --kind axiom --set dataset=apps
//	                                                  asks for the token, or reads it piped in
//	skifity drains add Loki --kind loki --set url=https://logs.example.com --set username=1234 \
//	    --secret-file password=./token.txt --project shop
//	skifity drains test Axiom
//	skifity drains remove Axiom
//
// A credential is never a flag's value, so it is not in the shell's history:
// it is read from a file, from stdin, or asked for.
func cmdDrains(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("drains", flag.ContinueOnError)
	flags.SetOutput(out)
	kind := flags.String("kind", "", "add: http, loki, elasticsearch, datadog, axiom, betterstack, newrelic or syslog")
	var sets, secretFiles, secrets, projects repeated
	flags.Var(&sets, "set", "add: a setting that is not secret, key=value; repeat for each")
	flags.Var(&secretFiles, "secret-file", "add: a secret read from a file, key=path, - for stdin; repeat for each")
	flags.Var(&secrets, "secret", "add: a secret to be asked for, by its key; required ones are asked for anyway")
	flags.Var(&projects, "project", "add: send only this project's logs, by name or id; repeat for several")
	builds := flags.Bool("builds", false, "add: send the apps' build logs too")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	action := "list"
	if len(positional) > 0 {
		action, positional = positional[0], positional[1:]
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	teamID, err := resolveTeam(ctx, client, cfg)
	if err != nil {
		return err
	}
	path := "/api/teams/" + url.PathEscape(teamID) + "/log-drains"
	var listed drainList
	list := func() error { return client.Do(ctx, "GET", path, nil, &listed) }

	switch action {
	case "list", "ls":
		if err := list(); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, listed)
		}
		printDrains(out, listed)
		return nil

	case "add":
		if len(positional) != 1 || *kind == "" {
			return errdoc.BadRequest(fmt.Sprintf("Name the drain and give its kind: `%s drains add Axiom --kind axiom --set dataset=apps`.",
				version.Binary))
		}
		if err := list(); err != nil {
			return err
		}
		body, err := drainBody(ctx, client, teamID, listed.Kinds, positional[0], *kind, sets, secretFiles, secrets, projects, out)
		if err != nil {
			return err
		}
		body["include_builds"] = *builds
		var added drainAnswer
		// Adding sends the test line first, which may wait on the service.
		if err := client.DoLong(ctx, "POST", path, body, &added); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, added)
		}
		fmt.Fprintf(out, "Added %s: the test line reached %s, and the collector is being given it.\n", added.Name, added.Destination)
		return nil

	case "test", "remove", "rm":
		if len(positional) != 1 {
			return errdoc.BadRequest(fmt.Sprintf("Name the drain: `%s drains %s <name>`.", version.Binary, action))
		}
		if err := list(); err != nil {
			return err
		}
		drain, err := findDrain(listed.Items, positional[0])
		if err != nil {
			return err
		}
		if action == "test" {
			var tested struct {
				OK       bool      `json:"ok"`
				TestedAt time.Time `json:"tested_at"`
			}
			if err := client.DoLong(ctx, "POST", path+"/"+url.PathEscape(drain.ID)+"/test", nil, &tested); err != nil {
				return err
			}
			if *asJSON {
				return writeJSON(out, map[string]any{"ok": tested.OK, "tested_at": tested.TestedAt, "drain": drain.Name})
			}
			fmt.Fprintf(out, "The test line reached %s. This proves the address and the credentials, not the collector: "+
				"`%s drains` says whether that is running.\n", drain.Destination, version.Binary)
			return nil
		}
		if err := client.Do(ctx, "DELETE", path+"/"+url.PathEscape(drain.ID), nil, nil); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, map[string]string{"removed": drain.Name, "id": drain.ID})
		}
		fmt.Fprintf(out, "Removed %s. Nothing more is sent to it.\n", drain.Name)
		return nil
	}
	return errdoc.BadRequest(fmt.Sprintf("drains takes list, add, test or remove, not %q.", action))
}

// drainBody is what `drains add` sends: the settings from --set, the secrets
// from files, stdin or a prompt, and the projects by id.
func drainBody(ctx context.Context, client *Client, teamID string, kinds []drainKind, name, kind string,
	sets, secretFiles, secrets, projects repeated, out io.Writer) (map[string]any, error) {
	var spec *drainKind
	names := make([]string, 0, len(kinds))
	for i := range kinds {
		names = append(names, kinds[i].Kind)
		if kinds[i].Kind == kind {
			spec = &kinds[i]
		}
	}
	if spec == nil {
		return nil, errdoc.BadRequest(fmt.Sprintf("%q is not a kind of drain. The kinds are %s.", kind, strings.Join(names, ", ")))
	}
	secret := map[string]bool{}
	for _, field := range spec.Fields {
		secret[field.Key] = field.Secret
	}

	settings := map[string]string{}
	for _, pair := range sets {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, errdoc.BadRequest(fmt.Sprintf("--set takes key=value, not %q.", pair))
		}
		if secret[key] {
			return nil, errdoc.BadRequest(fmt.Sprintf("%s is a secret, and a flag's value is kept in the shell's history: "+
				"give it with --secret-file %s=<path>, or --secret %s to be asked for it.", key, key, key))
		}
		settings[key] = value
	}
	for _, pair := range secretFiles {
		key, file, ok := strings.Cut(pair, "=")
		if !ok || !secret[key] {
			return nil, errdoc.BadRequest(fmt.Sprintf("--secret-file takes one of this kind's secrets and a path, key=path, not %q.", pair))
		}
		value, err := readSecretFile(file)
		if err != nil {
			return nil, err
		}
		settings[key] = value
	}
	asked := map[string]bool{}
	for _, key := range secrets {
		if !secret[key] {
			return nil, errdoc.BadRequest(fmt.Sprintf("%s is not one of the secrets of a %s drain.", key, kind))
		}
		asked[key] = true
	}
	for _, field := range spec.Fields {
		if !field.Secret || settings[field.Key] != "" || !(field.Required || asked[field.Key]) {
			continue
		}
		value := promptSecret(out, field.Key+": ")
		if value == "" {
			return nil, errdoc.BadRequest(fmt.Sprintf("A %s drain needs its %s, typed at the prompt, piped in, or from --secret-file.", kind, field.Key))
		}
		settings[field.Key] = value
	}

	body := map[string]any{"name": name, "kind": kind, "settings": settings}
	if len(projects) > 0 {
		ids, err := projectIDs(ctx, client, teamID, projects)
		if err != nil {
			return nil, err
		}
		body["projects"] = ids
	}
	return body, nil
}

// readSecretFile reads a secret from a file, or from stdin for "-", without
// the line break a file usually ends with.
func readSecretFile(path string) (string, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return "", errdoc.BadRequest(fmt.Sprintf("The secret could not be read from %s: %v", path, err))
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", errdoc.BadRequest(fmt.Sprintf("%s is empty.", path))
	}
	return value, nil
}

// projectIDs turns project names or ids into ids.
func projectIDs(ctx context.Context, client *Client, teamID string, named []string) ([]string, error) {
	var listed struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/teams/"+url.PathEscape(teamID)+"/projects", nil, &listed); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(named))
	for _, want := range named {
		found := ""
		for _, project := range listed.Items {
			if project.ID == want || strings.EqualFold(project.Name, want) || project.Slug == want {
				found = project.ID
			}
		}
		if found == "" {
			return nil, errdoc.BadRequest(fmt.Sprintf("The team has no project called %q.", want))
		}
		out = append(out, found)
	}
	return out, nil
}

func printDrains(out io.Writer, listed drainList) {
	if len(listed.Items) == 0 {
		fmt.Fprintf(out, "This team ships its logs nowhere. Add a drain with `%s drains add <name> --kind <kind>`.\n", version.Binary)
		return
	}
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "NAME\tKIND\tSTATUS\tSENDS\tTO")
	for _, drain := range listed.Items {
		what := "every project"
		if drain.Scoped {
			what = fmt.Sprintf("%d project(s)", len(drain.Projects))
		}
		if drain.IncludeBuilds {
			what += ", builds"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", drain.Name, drain.Kind, drain.Status, what, drain.Destination)
	}
	_ = table.Flush()
	collector := listed.Collector
	switch {
	case collector.Error != "":
		fmt.Fprintf(out, "\nThe collector could not be brought up to date: %s\n", collector.Error)
	case collector.Live != nil:
		fmt.Fprintf(out, "\nThe collector is %s on %d of %d server(s).\n", collector.Live.State, collector.Live.Ready, collector.Live.Desired)
	}
	for _, drain := range listed.Items {
		if drain.TestError != "" {
			fmt.Fprintf(out, "\n%s: the last test failed: %s\n", drain.Name, drain.TestError)
		}
	}
}

func findDrain(drains []drainAnswer, named string) (drainAnswer, error) {
	names := make([]string, 0, len(drains))
	for _, drain := range drains {
		if drain.ID == named || strings.EqualFold(drain.Name, named) {
			return drain, nil
		}
		names = append(names, drain.Name)
	}
	if len(names) == 0 {
		return drainAnswer{}, errdoc.BadRequest("This team has no log drains.")
	}
	return drainAnswer{}, errdoc.BadRequest(fmt.Sprintf("The team has no drain called %q. It has: %s.", named, strings.Join(names, ", ")))
}
