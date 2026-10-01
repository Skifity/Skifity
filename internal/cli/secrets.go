package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"

	"skifity/internal/errdoc"
	"skifity/internal/secretmgr"
	"skifity/internal/store"
	"skifity/internal/version"
)

// The team's secret managers, from the command line:
//
//	skifity secrets connections list
//	skifity secrets connections add company-vault --kind vault --address https://vault.example.com < creds.json
//	skifity secrets connections limit company-vault --allow-path shop --allow-project shop
//	skifity secrets connections test company-vault
//	skifity secrets connections remove company-vault
//
// A credential is never an argument: an argument is in the shell's history
// and in every process listing on the machine. It is read from a file, from
// standard input, or asked for without echoing.

// secretsStdin is where credentials are read from when they are piped in. A
// variable, so a test can pipe them.
var secretsStdin io.Reader = os.Stdin

// secretsInteractive says whether credentials can be asked for.
var secretsInteractive = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

const secretsUsage = `Usage:
  %[1]s secrets connections list                 The team's secret managers
  %[1]s secrets connections add NAME --kind KIND [settings] [limits] [--credentials-file FILE]
  %[1]s secrets connections limit NAME [limits] [--force]
                                                 Change what it may be used for; alone, show it
  %[1]s secrets connections test NAME            Check that it still signs in
  %[1]s secrets connections remove NAME          Remove one nothing reads any more

Limits (both repeatable; left out, a connection reads any path for every project):
  --allow-path PREFIX      A path a variable may read at or under, written like
                           a reference's path: shop (Vault, under the mount; or
                           secret/data/shop), /shop (Infisical), SHOP (Doppler,
                           a name prefix), prod/shop (AWS). app never allows app-b.
  --allow-project PROJECT  A project, by name or id, whose variables may read it.
  --any-path, --any-project  Lift that limit (limit only).
  --force                  Save limits that leave variables which read it now
                           outside them; those stop resolving at the next deploy.
  The limit is the panel's. The secret manager's own policy is the real boundary.

Kinds and their settings:
  vault       --address URL [--mount secret] [--namespace NS] [--auth token|approle] [--approle-mount approle]
  infisical   [--site-url URL] --project ID --environment SLUG
  doppler     (the service token names the project and config)
  aws         --region REGION [--endpoint URL]

Credentials come from --credentials-file (JSON, or KEY=value lines; - is
standard input), from standard input when it is piped, or are asked for:
  vault       token, or role_id and secret_id with --auth approle
  infisical   client_id and client_secret
  doppler     token
  aws         access_key_id and secret_access_key, and session_token if it has one

A variable is then read from one with:
  %[1]s env set STRIPE_KEY --from NAME:path#key
`

func cmdSecrets(ctx context.Context, args []string, out io.Writer) error {
	if len(args) >= 2 && args[0] == "connections" {
		rest := args[2:]
		switch args[1] {
		case "list", "ls":
			return cmdSecretsList(ctx, rest, out)
		case "add":
			return cmdSecretsAdd(ctx, rest, out)
		case "remove", "rm", "delete":
			return cmdSecretsRemove(ctx, rest, out)
		case "test":
			return cmdSecretsTest(ctx, rest, out)
		case "limit":
			return cmdSecretsLimit(ctx, rest, out)
		}
	}
	flags := flag.NewFlagSet("secrets", flag.ContinueOnError)
	flags.SetOutput(out)
	asJSON := flags.Bool("json", false, "print the result as JSON")
	flags.Usage = func() { fmt.Fprintf(out, secretsUsage, version.Binary) }
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) == 1 && positional[0] == "connections" {
		// `secrets connections` alone lists them.
		if *asJSON {
			return cmdSecretsList(ctx, []string{"--json"}, out)
		}
		return cmdSecretsList(ctx, nil, out)
	}
	if len(positional) > 0 {
		return errdoc.BadRequest(fmt.Sprintf("%q is not a secrets command. Run `%s secrets --help`.",
			strings.Join(positional, " "), version.Binary))
	}
	if *asJSON {
		return writeJSON(out, map[string]any{"commands": []string{
			"secrets connections list", "secrets connections add", "secrets connections limit",
			"secrets connections test", "secrets connections remove",
		}})
	}
	fmt.Fprintf(out, secretsUsage, version.Binary)
	return nil
}

// secretManager is a connection as the panel lists it.
type secretManager struct {
	store.SecretConnection
	Credentials      []string `json:"credentials"`
	UsedBy           int      `json:"used_by"`
	StoppedResolving []string `json:"stopped_resolving,omitempty"`
}

// repeated is a flag that may be given more than once, such as --allow-path.
type repeated []string

func (r *repeated) String() string { return strings.Join(*r, ", ") }

func (r *repeated) Set(value string) error {
	*r = append(*r, value)
	return nil
}

// teamProjects are the team's projects, for naming them.
func teamProjects(ctx context.Context, client *Client, teamID string) ([]store.Project, error) {
	var projects struct {
		Items []store.Project `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/teams/"+teamID+"/projects", nil, &projects); err != nil {
		return nil, err
	}
	return projects.Items, nil
}

// projectIDsFor finds each project named on the command line, by id, slug or
// name, among the team's.
func projectIDsFor(projects []store.Project, named []string) ([]string, error) {
	out := make([]string, 0, len(named))
	for _, name := range named {
		name = strings.TrimSpace(name)
		var found []string
		for _, project := range projects {
			if project.ID == name {
				found = []string{project.ID}
				break
			}
			if project.Slug == name || project.Name == name {
				found = append(found, project.ID)
			}
		}
		switch len(found) {
		case 1:
			out = append(out, found[0])
		case 0:
			names := make([]string, 0, len(projects))
			for _, project := range projects {
				names = append(names, project.Name)
			}
			return nil, errdoc.BadRequest(fmt.Sprintf("This team has no project called %q. It has: %s.", name, orDefault(strings.Join(names, ", "), "none")))
		default:
			return nil, errdoc.BadRequest(fmt.Sprintf("More than one project is called %q. Name it by its id.", name))
		}
	}
	return out, nil
}

// projectNames are a connection's projects by name, as far as they are known.
func projectNames(projects []store.Project, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		name := id + " (removed)"
		for _, project := range projects {
			if project.ID == id {
				name = project.Name
			}
		}
		out = append(out, name)
	}
	return out
}

// limitsText says what a connection may be used for, in a line.
func limitsText(item secretManager, projects []store.Project) string {
	paths := "any path"
	if len(item.AllowedPaths) > 0 {
		paths = "paths " + strings.Join(item.AllowedPaths, ", ")
	}
	scope := "every project"
	if len(item.AllowedProjectIDs) > 0 {
		scope = "projects " + strings.Join(projectNames(projects, item.AllowedProjectIDs), ", ")
	}
	return paths + "; " + scope
}

func secretsClient(ctx context.Context) (*Client, string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, "", err
	}
	client := NewClient(cfg)
	teamID, err := resolveTeam(ctx, client, cfg)
	if err != nil {
		return nil, "", err
	}
	return client, teamID, nil
}

func cmdSecretsList(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("secrets connections list", flag.ContinueOnError)
	flags.SetOutput(out)
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	client, teamID, err := secretsClient(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Items []secretManager `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/teams/"+teamID+"/secret-managers", nil, &response); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, response.Items)
	}
	if len(response.Items) == 0 {
		fmt.Fprintf(out, "This team has no secret managers. Connect one with `%s secrets connections add`, or in the panel under Settings.\n",
			version.Binary)
		return nil
	}
	// The projects' names, for the limits; without them, their ids.
	projects, _ := teamProjects(ctx, client, teamID)
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "NAME\tKIND\tWHERE\tLIMITED TO\tREFRESH\tUSED BY\tLAST ERROR")
	for _, item := range response.Items {
		refresh := "off"
		if item.RefreshMinutes > 0 {
			refresh = "every " + strconv.Itoa(item.RefreshMinutes) + "m"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", item.Name, secretmgr.KindLabel(item.Kind), where(item),
			limitsText(item, projects), refresh, item.UsedBy, orDefault(item.LastError, "-"))
	}
	return table.Flush()
}

// where is the one setting that says which manager a connection is.
func where(item secretManager) string {
	switch item.Kind {
	case secretmgr.KindVault:
		return item.Settings["address"] + " (" + item.Settings["mount"] + ")"
	case secretmgr.KindInfisical:
		return item.Settings["project_id"] + "/" + item.Settings["environment"]
	case secretmgr.KindAWS:
		return orDefault(item.Settings["endpoint"], item.Settings["region"])
	}
	return "-"
}

func cmdSecretsAdd(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("secrets connections add", flag.ContinueOnError)
	flags.SetOutput(out)
	kind := flags.String("kind", "", "vault, infisical, doppler or aws")
	credentialsFile := flags.String("credentials-file", "", "a file with the credentials, JSON or KEY=value lines; - for standard input")
	refresh := flags.Int("refresh", 0, "refresh the apps that read it every this many minutes (5 to 1440); 0 is never")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	var allowPaths, allowProjects repeated
	flags.Var(&allowPaths, "allow-path", "a path prefix variables may read at or under; repeatable")
	flags.Var(&allowProjects, "allow-project", "a project, by name or id, whose variables may read it; repeatable")
	settingFlags := map[string]*string{
		"address":       flags.String("address", "", "Vault: its address, such as https://vault.example.com:8200"),
		"mount":         flags.String("mount", "", "Vault: the KV version 2 mount, secret when left out"),
		"namespace":     flags.String("namespace", "", "Vault: the namespace, for Vault Enterprise or OpenBao"),
		"auth":          flags.String("auth", "", "Vault: token or approle"),
		"approle_mount": flags.String("approle-mount", "", "Vault: where AppRole is mounted, approle when left out"),
		"site_url":      flags.String("site-url", "", "Infisical: its address, https://app.infisical.com when left out"),
		"project_id":    flags.String("project", "", "Infisical: the project's id"),
		"environment":   flags.String("environment", "", "Infisical: the environment's slug, such as prod"),
		"region":        flags.String("region", "", "AWS: the region, such as eu-central-1"),
		"endpoint":      flags.String("endpoint", "", "AWS: another address, such as a VPC endpoint or LocalStack"),
	}
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errdoc.BadRequest(fmt.Sprintf("Name the connection: `%s secrets connections add company-vault --kind vault ...`.", version.Binary))
	}
	settings := map[string]string{}
	for name, value := range settingFlags {
		if v := strings.TrimSpace(*value); v != "" {
			settings[name] = v
		}
	}
	credentials, err := readCredentials(*kind, settings, *credentialsFile, out)
	if err != nil {
		return err
	}

	client, teamID, err := secretsClient(ctx)
	if err != nil {
		return err
	}
	body := map[string]any{"name": positional[0], "kind": *kind, "settings": settings, "credentials": credentials}
	if *refresh != 0 {
		body["refresh_minutes"] = *refresh
	}
	var projects []store.Project
	if len(allowPaths) > 0 {
		body["allowed_paths"] = []string(allowPaths)
	}
	if len(allowProjects) > 0 {
		if projects, err = teamProjects(ctx, client, teamID); err != nil {
			return err
		}
		ids, err := projectIDsFor(projects, allowProjects)
		if err != nil {
			return err
		}
		body["allowed_project_ids"] = ids
	}
	var created secretManager
	if err := client.Do(ctx, "POST", "/api/teams/"+teamID+"/secret-managers", body, &created); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, created)
	}
	fmt.Fprintf(out, "%s (%s) is connected: it signed in, and nothing was read.\n", created.Name, secretmgr.KindLabel(created.Kind))
	fmt.Fprintf(out, "It may be used for %s.\n", limitsText(created, projects))
	if len(created.AllowedPaths) == 0 {
		// The same nudge the panel's form gives: a connection nobody limited
		// reads whatever its credentials can, for anybody who sets a variable.
		fmt.Fprintf(out, "Anybody who sets a variable can read whatever its credentials can. Limit it with `%s secrets connections limit %s --allow-path PATH --allow-project PROJECT`.\n",
			version.Binary, created.Name)
	}
	fmt.Fprintf(out, "Read a variable from it with `%s env set KEY --from %s:path#key`.\n", version.Binary, created.Name)
	return nil
}

// cmdSecretsLimit changes what a connection may be used for, or shows it.
func cmdSecretsLimit(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("secrets connections limit", flag.ContinueOnError)
	flags.SetOutput(out)
	var allowPaths, allowProjects repeated
	flags.Var(&allowPaths, "allow-path", "a path prefix variables may read at or under; repeatable, and replaces the ones it has")
	flags.Var(&allowProjects, "allow-project", "a project, by name or id, whose variables may read it; repeatable, and replaces the ones it has")
	anyPath := flags.Bool("any-path", false, "lift the path limit: any path its credentials can read")
	anyProject := flags.Bool("any-project", false, "lift the project limit: every project of the team")
	force := flags.Bool("force", false, "save limits that leave variables which read it now outside them")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(allowPaths) > 0 && *anyPath {
		return errdoc.BadRequest("Give --allow-path or --any-path, not both.")
	}
	if len(allowProjects) > 0 && *anyProject {
		return errdoc.BadRequest("Give --allow-project or --any-project, not both.")
	}
	client, teamID, err := secretsClient(ctx)
	if err != nil {
		return err
	}
	path, err := connectionPath(teamID, positional, "limit")
	if err != nil {
		return err
	}
	projects, err := teamProjects(ctx, client, teamID)
	if err != nil {
		return err
	}

	body := map[string]any{}
	switch {
	case *anyPath:
		body["allowed_paths"] = []string{}
	case len(allowPaths) > 0:
		body["allowed_paths"] = []string(allowPaths)
	}
	switch {
	case *anyProject:
		body["allowed_project_ids"] = []string{}
	case len(allowProjects) > 0:
		ids, err := projectIDsFor(projects, allowProjects)
		if err != nil {
			return err
		}
		body["allowed_project_ids"] = ids
	}

	if len(body) == 0 {
		// Nothing to change: what it is limited to now.
		var response struct {
			Items []secretManager `json:"items"`
		}
		if err := client.Do(ctx, "GET", "/api/teams/"+teamID+"/secret-managers", nil, &response); err != nil {
			return err
		}
		for _, item := range response.Items {
			if item.Name == positional[0] || item.ID == positional[0] {
				if *asJSON {
					return writeJSON(out, item)
				}
				fmt.Fprintf(out, "%s: %s.\n", item.Name, limitsText(item, projects))
				return nil
			}
		}
		return errdoc.NotFound("secret manager", positional[0])
	}

	if *force {
		body["force"] = true
	}
	var updated secretManager
	if err := client.Do(ctx, "PATCH", path, body, &updated); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, updated)
	}
	fmt.Fprintf(out, "%s is limited to %s.\n", updated.Name, limitsText(updated, projects))
	if len(updated.StoppedResolving) > 0 {
		fmt.Fprintf(out, "These variables are outside the new limits and no longer resolve: %s. Their apps' next deploy, sync or refresh stops until they are pointed elsewhere.\n",
			strings.Join(updated.StoppedResolving, ", "))
	}
	return nil
}

// readCredentials reads what a connection signs in with: from a file, from
// standard input when it is piped, or by asking for each without echoing.
func readCredentials(kind string, settings map[string]string, file string, out io.Writer) (map[string]string, error) {
	names := secretmgr.CredentialNames(kind, settings)
	if names == nil {
		return nil, errdoc.BadRequest(fmt.Sprintf("Give the kind with --kind: %s.", strings.Join(secretmgr.Kinds, ", ")))
	}
	var text []byte
	var err error
	switch {
	case file == "-":
		text, err = io.ReadAll(io.LimitReader(secretsStdin, 64*1024))
	case file != "":
		text, err = os.ReadFile(file)
	case !secretsInteractive():
		text, err = io.ReadAll(io.LimitReader(secretsStdin, 64*1024))
	default:
		credentials := map[string]string{}
		for _, name := range names {
			if value := promptSecret(out, name+": "); value != "" {
				credentials[name] = value
			}
		}
		return credentials, nil
	}
	if err != nil {
		return nil, errdoc.BadRequest(fmt.Sprintf("Could not read the credentials: %v", err))
	}
	credentials := map[string]string{}
	trimmed := strings.TrimSpace(string(text))
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal([]byte(trimmed), &credentials); err != nil {
			// The decoder's message can quote the credential itself.
			return nil, errdoc.BadRequest("The credentials are not a JSON object of strings, such as {\"token\": \"...\"}.")
		}
	} else {
		credentials = parseDotEnv(trimmed)
	}
	if len(credentials) == 0 {
		sort.Strings(names)
		return nil, errdoc.BadRequest(fmt.Sprintf("No credentials were given. This kind takes %s.", strings.Join(names, ", ")))
	}
	return credentials, nil
}

// connectionPath is one of the team's connections, by name or id.
func connectionPath(teamID string, positional []string, verb string) (string, error) {
	if len(positional) != 1 {
		return "", errdoc.BadRequest(fmt.Sprintf("Name the connection: `%s secrets connections %s company-vault`.", version.Binary, verb))
	}
	return "/api/teams/" + teamID + "/secret-managers/" + url.PathEscape(positional[0]), nil
}

func cmdSecretsRemove(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("secrets connections remove", flag.ContinueOnError)
	flags.SetOutput(out)
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	client, teamID, err := secretsClient(ctx)
	if err != nil {
		return err
	}
	path, err := connectionPath(teamID, positional, "remove")
	if err != nil {
		return err
	}
	if err := client.Do(ctx, "DELETE", path, nil, nil); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, map[string]any{"removed": positional[0]})
	}
	fmt.Fprintf(out, "%s is removed. Its credentials went with it.\n", positional[0])
	return nil
}

func cmdSecretsTest(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("secrets connections test", flag.ContinueOnError)
	flags.SetOutput(out)
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	client, teamID, err := secretsClient(ctx)
	if err != nil {
		return err
	}
	path, err := connectionPath(teamID, positional, "test")
	if err != nil {
		return err
	}
	if err := client.Do(ctx, "POST", path+"/test", nil, nil); err != nil {
		var problem *errdoc.Problem
		if *asJSON && errors.As(err, &problem) {
			return errors.Join(writeJSON(out, map[string]any{"ok": false, "error": problem}), err)
		}
		return err
	}
	if *asJSON {
		return writeJSON(out, map[string]any{"ok": true})
	}
	fmt.Fprintf(out, "%s signs in. Nothing was read.\n", positional[0])
	return nil
}

// referenceBody is a reference as the API takes it, from the way it is
// written on the command line.
func referenceBody(text string) (map[string]string, error) {
	connection, path, key, err := secretmgr.ParseReference(text)
	if err != nil {
		return nil, errdoc.BadRequest(capitalise(err.Error()) + ".")
	}
	return map[string]string{"connection": connection, "path": path, "key": key}, nil
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
