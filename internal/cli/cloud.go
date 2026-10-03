package cli

import (
	"bytes"
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
	"skifity/internal/store"
	"skifity/internal/version"
)

// cloudProviderAnswer is one of the team's cloud connections as the panel
// lists it. There is no token in it: the panel never sends one back.
type cloudProviderAnswer struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Name      string    `json:"name"`
	TokenHint string    `json:"token_hint"`
	CheckedAt time.Time `json:"checked_at"`
	Servers   int       `json:"servers"`
}

// cloudStdin is where `--token-file -` reads from; a test replaces it.
var cloudStdin io.Reader = os.Stdin

// cmdCloud manages the team's connections to cloud providers.
//
//	skifity cloud providers                                   the team's connections
//	skifity cloud providers add --token-file token.txt        connect a Hetzner Cloud project
//	skifity cloud providers add --name eu --token-file - < token.txt
//	skifity cloud providers test NAME                         ask the provider again
//	skifity cloud providers remove NAME
//
// The token is read from a file or from standard input, never from the
// command line, where it would be in the shell's history and in every
// process listing on the machine.
func cmdCloud(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("cloud", flag.ContinueOnError)
	flags.SetOutput(out)
	kind := flags.String("kind", "hetzner", "the provider: hetzner or digitalocean")
	name := flags.String("name", "", "what to call the connection; the provider's name when left out")
	tokenFile := flags.String("token-file", "", "a file holding an API token that can write, or - for standard input")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) == 0 || positional[0] != "providers" {
		return errdoc.BadRequest(fmt.Sprintf("Use `%s cloud providers`, with list, add, test or remove.", version.Binary))
	}
	positional = positional[1:]
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
	path := "/api/teams/" + url.PathEscape(teamID) + "/cloud-providers"

	switch action {
	case "list", "ls":
		listed, err := listCloudProviders(ctx, client, path)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, listed)
		}
		if len(listed) == 0 {
			fmt.Fprintf(out, "The team has no cloud connections. Add one with `%s cloud providers add --token-file FILE`.\n", version.Binary)
			return nil
		}
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "NAME\tPROVIDER\tTOKEN\tCHECKED\tSERVERS")
		for _, p := range listed {
			checked := "-"
			if !p.CheckedAt.IsZero() {
				checked = p.CheckedAt.UTC().Format(time.DateOnly)
			}
			fmt.Fprintf(table, "%s\t%s\t…%s\t%s\t%d\n", p.Name, p.Title, p.TokenHint, checked, p.Servers)
		}
		return table.Flush()

	case "add":
		if *tokenFile == "" {
			return errdoc.BadRequest("Name the file holding the token with --token-file, or - to read it from standard input. " +
				"The token is never given on the command line.")
		}
		token, err := readToken(*tokenFile)
		if err != nil {
			return err
		}
		var added cloudProviderAnswer
		body := map[string]string{"kind": *kind, "name": *name, "token": token}
		if err := client.Do(ctx, "POST", path, body, &added); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, added)
		}
		fmt.Fprintf(out, "Connected %s as %s. The token was checked and can create servers.\n", added.Title, added.Name)
		return nil

	case "test", "check", "remove", "rm", "delete":
		if len(positional) != 1 {
			return errdoc.BadRequest(fmt.Sprintf("Name the connection: `%s cloud providers %s NAME`.", version.Binary, action))
		}
		listed, err := listCloudProviders(ctx, client, path)
		if err != nil {
			return err
		}
		connection, err := pickCloudProvider(listed, positional[0])
		if err != nil {
			return err
		}
		if action == "test" || action == "check" {
			var checked cloudProviderAnswer
			if err := client.Do(ctx, "POST", path+"/"+url.PathEscape(connection.ID)+"/test", nil, &checked); err != nil {
				return err
			}
			if *asJSON {
				return writeJSON(out, checked)
			}
			fmt.Fprintf(out, "%s still accepts the token of %s, and it can create servers.\n", checked.Title, checked.Name)
			return nil
		}
		if err := client.Do(ctx, "DELETE", path+"/"+url.PathEscape(connection.ID), nil, nil); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, map[string]string{"removed": connection.Name})
		}
		fmt.Fprintf(out, "%s is removed. The token is gone from the panel; revoke it in the provider's console too.\n", connection.Name)
		return nil
	}
	return errdoc.BadRequest(fmt.Sprintf("cloud providers takes list, add, test or remove, not %q.", action))
}

func listCloudProviders(ctx context.Context, client *Client, path string) ([]cloudProviderAnswer, error) {
	var listed struct {
		Items []cloudProviderAnswer `json:"items"`
	}
	if err := client.Do(ctx, "GET", path, nil, &listed); err != nil {
		return nil, err
	}
	return listed.Items, nil
}

// pickCloudProvider finds a connection by id or name, or the only one of a
// kind when the kind is what was given.
func pickCloudProvider(listed []cloudProviderAnswer, named string) (cloudProviderAnswer, error) {
	var ofKind []cloudProviderAnswer
	for _, p := range listed {
		if p.ID == named || strings.EqualFold(p.Name, named) {
			return p, nil
		}
		if strings.EqualFold(p.Kind, named) {
			ofKind = append(ofKind, p)
		}
	}
	switch len(ofKind) {
	case 1:
		return ofKind[0], nil
	case 0:
		return cloudProviderAnswer{}, errdoc.BadRequest(fmt.Sprintf(
			"The team has no cloud connection called %q. `%s cloud providers` lists the ones it has.", named, version.Binary))
	}
	names := make([]string, 0, len(ofKind))
	for _, p := range ofKind {
		names = append(names, p.Name)
	}
	return cloudProviderAnswer{}, errdoc.BadRequest(fmt.Sprintf(
		"The team has more than one %s connection: %s. Name the one to use.", named, strings.Join(names, ", ")))
}

// readToken reads a token from a file, or standard input for -.
//
// A value that looks like the token itself is refused rather than used: it
// means the token was typed on the command line, and is now in the shell's
// history.
func readToken(value string) (string, error) {
	var data []byte
	var err error
	if value == "-" {
		data, err = io.ReadAll(io.LimitReader(cloudStdin, 4096))
	} else {
		data, err = os.ReadFile(value)
		if os.IsNotExist(err) && len(value) >= 32 && !strings.ContainsAny(value, "/.\\") {
			return "", errdoc.BadRequest("--token-file takes the name of a file, or - for standard input, not the token itself. " +
				"Something passed on the command line is in the shell's history; if that was a token, revoke it and make another.")
		}
	}
	if err != nil {
		return "", errdoc.BadRequest(fmt.Sprintf("--token-file could not be read: %s.", err))
	}
	token := string(bytes.TrimSpace(data))
	if token == "" {
		return "", errdoc.BadRequest("--token-file named something empty.")
	}
	return token, nil
}

// cmdServersCreate orders a server from one of the team's cloud connections
// and follows it until it has joined.
//
//	skifity servers create web-1 --provider hetzner --location fsn1 --type cx22
//	skifity servers create arm-1 --provider eu --location fsn1 --type cax11 --image debian-12
//	skifity servers create web-2 --provider digitalocean --location fra1 --type s-2vcpu-4gb
func cmdServersCreate(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("servers create", flag.ContinueOnError)
	flags.SetOutput(out)
	provider := flags.String("provider", "", "the cloud connection, by name or id, or hetzner or digitalocean for the team's only one of that kind")
	location := flags.String("location", "", "where, as `cloud providers` options name it, such as fsn1 or fra1")
	serverType := flags.String("type", "", "the server type, such as cx22, cax11 for arm64, or s-2vcpu-4gb at DigitalOcean")
	image := flags.String("image", "ubuntu-24.04", "ubuntu-24.04 or debian-12")
	ssh := flags.String("ssh", "anywhere", "who can reach port 22: anywhere, or cluster for the cluster's servers only")
	controlPlane := flags.Bool("control-plane", false, "join as a control plane member, for high availability")
	follow := flags.Bool("follow", true, "follow the server until it has joined the cluster")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errdoc.BadRequest(fmt.Sprintf("Name the server: `%s servers create NAME --provider hetzner --location fsn1 --type cx22`.", version.Binary))
	}
	if *provider == "" || *location == "" || *serverType == "" {
		return errdoc.BadRequest("--provider, --location and --type are all needed.")
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
	listed, err := listCloudProviders(ctx, client, "/api/teams/"+url.PathEscape(teamID)+"/cloud-providers")
	if err != nil {
		return err
	}
	connection, err := pickCloudProvider(listed, *provider)
	if err != nil {
		return err
	}

	var op store.Operation
	body := map[string]any{
		"provider_id": connection.ID, "name": positional[0], "location": *location, "server_type": *serverType,
		"image": *image, "ssh_access": *ssh, "control_plane": *controlPlane,
	}
	if err := client.Do(ctx, "POST", "/api/teams/"+url.PathEscape(teamID)+"/servers/cloud", body, &op); err != nil {
		return err
	}
	if !*follow {
		if *asJSON {
			return writeJSON(out, op)
		}
		fmt.Fprintf(out, "Ordering %s at %s. Follow it in the panel, or with `%s servers`.\n", positional[0], connection.Title, version.Binary)
		return nil
	}
	if !*asJSON {
		fmt.Fprintf(out, "Ordering %s at %s: %s in %s.\n", positional[0], connection.Title, *serverType, *location)
	}
	finished, err := followOperation(ctx, client, op, out, !*asJSON)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, finished)
	}
	if finished.Status != store.OpSucceeded {
		return errdoc.CloudCreateFailed(positional[0], orNone(finished.ErrorMsg))
	}
	fmt.Fprintf(out, "%s has joined the cluster.\n", positional[0])
	return nil
}

// followOperation polls an operation until it finishes, printing each step
// as it completes.
func followOperation(ctx context.Context, client *Client, op store.Operation, out io.Writer, show bool) (store.Operation, error) {
	printed := map[string]store.StepStatus{}
	for {
		if err := client.Do(ctx, "GET", "/api/operations/"+url.PathEscape(op.ID), nil, &op); err != nil {
			return op, err
		}
		if show {
			for _, step := range op.Steps {
				if step.Status == printed[step.Key] || step.Status == store.StepPending || step.Status == store.StepRunning {
					continue
				}
				printed[step.Key] = step.Status
				fmt.Fprintf(out, "  %-9s %s %s\n", step.Status, step.Key, step.Message)
			}
		}
		switch op.Status {
		case store.OpSucceeded, store.OpFailed, store.OpCancelled:
			return op, nil
		}
		select {
		case <-ctx.Done():
			return op, ctx.Err()
		case <-time.After(followInterval):
		}
	}
}

// followInterval is how often an operation is asked about; a test shortens it.
var followInterval = 3 * time.Second
