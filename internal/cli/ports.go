package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// portAnswer is a public port as the panel lists it.
type portAnswer struct {
	ID         string   `json:"id"`
	Port       int      `json:"port"`
	Protocol   string   `json:"protocol"`
	PublicPort int      `json:"public_port"`
	Addresses  []string `json:"addresses"`
}

// cmdPorts lists, opens and closes an app's ports that are not HTTP.
//
//	skifity ports                          what is open, and where
//	skifity ports open 25565               the same number on every server
//	skifity ports open 19132/udp
//	skifity ports open 5432 --public 15432
//	skifity ports close 25565/tcp
func cmdPorts(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("ports", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	public := flags.Int("public", 0, "the port opened on every server, when not the app's own")
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
	app, err := resolveApp(ctx, client, cfg, *appID)
	if err != nil {
		return err
	}
	path := "/api/apps/" + app + "/ports"

	var listed struct {
		Items []portAnswer `json:"items"`
	}
	switch action {
	case "list", "ls":
		if err := client.Do(ctx, "GET", path, nil, &listed); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, listed.Items)
		}
		if len(listed.Items) == 0 {
			fmt.Fprintf(out, "No ports besides HTTP. Open one with `%s ports open 25565`.\n", version.Binary)
			return nil
		}
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "PUBLIC\tAPP\tREACHED AT")
		for _, p := range listed.Items {
			where := strings.Join(p.Addresses, ", ")
			if where == "" {
				where = "any server's address"
			}
			fmt.Fprintf(table, "%d/%s\t%d\t%s\n", p.PublicPort, p.Protocol, p.Port, where)
		}
		return table.Flush()

	case "open", "add":
		if len(positional) != 1 {
			return errdoc.BadRequest(fmt.Sprintf("Name the port the app listens on: `%s ports open 25565`, or 19132/udp.", version.Binary))
		}
		port, protocol, err := parsePortArg(positional[0])
		if err != nil {
			return err
		}
		body := map[string]any{"port": port, "protocol": protocol}
		if *public > 0 {
			body["public_port"] = *public
		}
		var opened portAnswer
		if err := client.Do(ctx, "POST", path, body, &opened); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, opened)
		}
		where := strings.Join(opened.Addresses, ", ")
		if where == "" {
			where = fmt.Sprintf("any of your servers, port %d", opened.PublicPort)
		}
		fmt.Fprintf(out, "%d/%s is open on every server: reach it at %s.\n", opened.PublicPort, opened.Protocol, where)
		return nil

	case "close", "rm":
		if len(positional) != 1 {
			return errdoc.BadRequest(fmt.Sprintf("Name the public port: `%s ports close 25565/tcp`.", version.Binary))
		}
		port, protocol, err := parsePortArg(positional[0])
		if err != nil {
			return err
		}
		if err := client.Do(ctx, "GET", path, nil, &listed); err != nil {
			return err
		}
		i := slices.IndexFunc(listed.Items, func(p portAnswer) bool { return p.PublicPort == port && p.Protocol == protocol })
		if i < 0 {
			return errdoc.BadRequest(fmt.Sprintf("This app has no public port %d/%s. `%s ports` lists the ones it has.", port, protocol, version.Binary))
		}
		if err := client.Do(ctx, "DELETE", path+"/"+listed.Items[i].ID, nil, nil); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, map[string]string{"closed": fmt.Sprintf("%d/%s", port, protocol)})
		}
		fmt.Fprintf(out, "%d/%s is closed on every server.\n", port, protocol)
		return nil
	}
	return errdoc.BadRequest(fmt.Sprintf("ports takes ls, open or close, not %q.", action))
}

// parsePortArg reads 25565 or 19132/udp.
func parsePortArg(arg string) (int, string, error) {
	number, protocol, found := strings.Cut(strings.ToLower(arg), "/")
	if !found {
		protocol = "tcp"
	}
	port, err := strconv.Atoi(number)
	if err != nil || port < 1 || port > 65535 || (protocol != "tcp" && protocol != "udp") {
		return 0, "", errdoc.BadRequest(fmt.Sprintf("%q is not a port: write 25565, or 19132/udp.", arg))
	}
	return port, protocol, nil
}
