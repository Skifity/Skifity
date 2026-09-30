package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// certificateAnswer is one of the team's own certificates as the panel lists
// it. There is no key in it: the panel never sends one.
type certificateAnswer struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Hostnames   []string  `json:"hostnames"`
	Issuer      string    `json:"issuer"`
	NotAfter    time.Time `json:"not_after"`
	Fingerprint string    `json:"fingerprint"`
	KeyType     string    `json:"key_type"`
	SelfSigned  bool      `json:"self_signed"`
	State       string    `json:"state"`
	Domains     []struct {
		Hostname string `json:"hostname"`
		AppName  string `json:"app_name"`
	} `json:"domains"`
}

// cmdCerts lists, uploads and removes the certificates the team brings for
// its own hostnames, in place of Let's Encrypt's.
//
//	skifity certs                                          what the team has
//	skifity certs add wildcard --cert fullchain.pem --key privkey.pem
//	skifity certs add wildcard --cert fullchain.pem --key - < privkey.pem
//	skifity certs remove wildcard
//
// The private key is read from a file or from standard input and never from
// the command line, where it would be in the shell's history and in every
// process listing on the machine. Adding under a name that exists uploads a
// new version of that certificate, which is how a renewal goes in.
func cmdCerts(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("certs", flag.ContinueOnError)
	flags.SetOutput(out)
	certPath := flags.String("cert", "", "the certificate and its intermediates, PEM: a file, or - for standard input")
	keyPath := flags.String("key", "", "the private key, PEM: a file, or - for standard input; never the key itself")
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
	path := "/api/teams/" + url.PathEscape(teamID) + "/certificates"

	var listed struct {
		Items []certificateAnswer `json:"items"`
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
			fmt.Fprintf(out, "No certificates of the team's own: every HTTPS domain gets one from Let's Encrypt.\n"+
				"Upload one with `%s certs add NAME --cert fullchain.pem --key privkey.pem`.\n", version.Binary)
			return nil
		}
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "NAME\tHOSTNAMES\tISSUER\tEXPIRES\tSTATE\tUSED BY")
		for _, c := range listed.Items {
			used := make([]string, 0, len(c.Domains))
			for _, d := range c.Domains {
				used = append(used, d.Hostname)
			}
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", c.Name, strings.Join(c.Hostnames, ", "), c.Issuer,
				c.NotAfter.UTC().Format(time.DateOnly), c.State, orNone(strings.Join(used, ", ")))
		}
		return table.Flush()

	case "add", "upload":
		if len(positional) != 1 {
			return errdoc.BadRequest(fmt.Sprintf("Name the certificate: `%s certs add NAME --cert fullchain.pem --key privkey.pem`.", version.Binary))
		}
		if *certPath == "" {
			return errdoc.BadRequest("Name the certificate file with --cert, or - to read it from standard input.")
		}
		if *keyPath == "" {
			return errdoc.BadRequest("Name the private key's file with --key, or - to read it from standard input. " +
				"The key is never given on the command line.")
		}
		if *certPath == "-" && *keyPath == "-" {
			return errdoc.BadRequest("Only one of --cert and --key can be read from standard input; give the other as a file.")
		}
		chain, err := readPEMArgument("--cert", *certPath)
		if err != nil {
			return err
		}
		key, err := readPEMArgument("--key", *keyPath)
		if err != nil {
			return err
		}
		var saved struct {
			Certificate certificateAnswer `json:"certificate"`
			Replaced    bool              `json:"replaced"`
			Reordered   bool              `json:"reordered"`
			Updating    int               `json:"updating"`
		}
		body := map[string]string{"name": positional[0], "certificate": string(chain), "private_key": string(key)}
		if err := client.Do(ctx, "POST", path, body, &saved); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, saved)
		}
		verb := "is uploaded"
		if saved.Replaced {
			verb = "is replaced by the new version"
		}
		fmt.Fprintf(out, "%s %s: %s, until %s.\n", saved.Certificate.Name, verb,
			strings.Join(saved.Certificate.Hostnames, ", "), saved.Certificate.NotAfter.UTC().Format(time.DateOnly))
		if saved.Reordered {
			fmt.Fprintln(out, "The certificates were put in order, yours first.")
		}
		if saved.Updating > 0 {
			fmt.Fprintf(out, "%d app(s) are being updated to use it.\n", saved.Updating)
		}
		return nil

	case "remove", "rm", "delete":
		if len(positional) != 1 {
			return errdoc.BadRequest(fmt.Sprintf("Name the certificate to remove: `%s certs remove NAME`.", version.Binary))
		}
		if err := client.Do(ctx, "GET", path, nil, &listed); err != nil {
			return err
		}
		i := slices.IndexFunc(listed.Items, func(c certificateAnswer) bool {
			return c.Name == positional[0] || c.ID == positional[0]
		})
		if i < 0 {
			return errdoc.BadRequest(fmt.Sprintf("The team has no certificate called %q. `%s certs` lists the ones it has.",
				positional[0], version.Binary))
		}
		var removed struct {
			Updating int `json:"updating"`
		}
		if err := client.Do(ctx, "DELETE", path+"/"+url.PathEscape(listed.Items[i].ID), nil, &removed); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, map[string]any{"removed": listed.Items[i].Name, "updating": removed.Updating})
		}
		fmt.Fprintf(out, "%s is removed. Its hostnames go back to Let's Encrypt", listed.Items[i].Name)
		if removed.Updating > 0 {
			fmt.Fprintf(out, "; %d app(s) are being updated", removed.Updating)
		}
		fmt.Fprintln(out, ".")
		return nil
	}
	return errdoc.BadRequest(fmt.Sprintf("certs takes ls, add or remove, not %q.", action))
}

// certsStdin is where `--cert -` and `--key -` read from; a test replaces it.
var certsStdin io.Reader = os.Stdin

// readPEMArgument reads a PEM file named by a flag, or standard input for -.
//
// A value that is itself PEM is refused rather than used: it means the key
// was typed on the command line, and is now in the shell's history.
func readPEMArgument(flagName, value string) ([]byte, error) {
	if strings.Contains(value, "-----BEGIN") {
		return nil, errdoc.BadRequest(flagName + " takes the name of a file, or - for standard input, not the PEM itself. " +
			"Something passed on the command line is in the shell's history; if that was a private key, " +
			"clear the history and consider the key seen.")
	}
	var data []byte
	var err error
	if value == "-" {
		data, err = io.ReadAll(io.LimitReader(certsStdin, 1<<20))
	} else {
		data, err = os.ReadFile(value)
	}
	if err != nil {
		return nil, errdoc.BadRequest(fmt.Sprintf("%s could not be read: %s.", flagName, err))
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errdoc.BadRequest(flagName + " named something empty.")
	}
	return data, nil
}

func orNone(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
