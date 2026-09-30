package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// cmdAPI is for somebody building on the panel rather than using it: a client
// of their own, generated from the description the panel serves.
func cmdAPI(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintf(out, `%s api - the panel's HTTP API

Usage:
  %s api spec             print the OpenAPI 3.1 description of the panel you are signed in to
  %s api spec --url URL   print another panel's, without signing in

The panel serves the same document at /api/openapi.json, with no token needed.
`, version.Binary, version.Binary, version.Binary)
		return nil
	}
	switch args[0] {
	case "spec":
		return apiSpec(ctx, args[1:], out)
	default:
		return errdoc.BadRequest(fmt.Sprintf("%q is not an api command. Try `%s api help`.",
			args[0], version.Binary))
	}
}

// apiSpec prints the OpenAPI description a panel serves.
//
// It is asked for without the token. The route is open and describes no data,
// and a token narrowed to some resources is refused every route outside them —
// this one included — so sending it could only make the request fail.
func apiSpec(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("api spec", flag.ContinueOnError)
	flags.SetOutput(out)
	panelURL := flags.String("url", "", "a panel to read it from instead of the one you are signed in to")
	// Every command takes --json. This one prints JSON whether it is given or not.
	flags.Bool("json", false, "print the result as JSON, which it always is")
	if err := flags.Parse(args); err != nil {
		return err
	}

	base := strings.TrimSpace(*panelURL)
	if base == "" {
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		base = cfg.PanelURL
	} else if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}

	var document json.RawMessage
	if err := NewClient(Config{PanelURL: base}).Do(ctx, "GET", "/api/openapi.json", nil, &document); err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, document, "", "  "); err != nil {
		return fmt.Errorf("read the panel's answer: %w", err)
	}
	pretty.WriteByte('\n')
	_, err := pretty.WriteTo(out)
	return err
}
