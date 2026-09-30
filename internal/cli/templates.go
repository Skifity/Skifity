package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// catalogueAnswer is a team's template catalogue as the panel lists it. The
// panel never answers the value of the header it is fetched with, and there
// is no field here for one.
type catalogueAnswer struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	URL            string    `json:"url"`
	AuthHeaderName string    `json:"auth_header_name,omitempty"`
	Format         string    `json:"format,omitempty"`
	FetchedAt      time.Time `json:"fetched_at"`
	AttemptedAt    time.Time `json:"attempted_at"`
	LastError      string    `json:"last_error,omitempty"`
	Templates      int       `json:"templates"`
	Problems       []struct {
		File   string   `json:"file"`
		ID     string   `json:"id,omitempty"`
		Name   string   `json:"name,omitempty"`
		Errors []string `json:"errors"`
	} `json:"problems"`
}

// templateAnswer is the part of a template the list prints.
type templateAnswer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Catalogue *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"catalogue,omitempty"`
}

// cmdTemplates lists the templates a team can install, and manages the
// team's own catalogues of them.
//
//	skifity templates                                   what the team can install
//	skifity templates list --search wiki
//	skifity templates catalogues                        the team's own catalogues
//	skifity templates catalogues add Acme https://git.acme.example/raw/catalogue.yaml
//	skifity templates catalogues add Acme <url> --header PRIVATE-TOKEN   asks for the value
//	skifity templates catalogues refresh Acme
//	skifity templates catalogues remove Acme
//
// Installing one is done in the panel or by an assistant, where its inputs
// are asked for one by one.
func cmdTemplates(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("templates", flag.ContinueOnError)
	flags.SetOutput(out)
	search := flags.String("search", "", "only templates whose name, category or catalogue contains this")
	header := flags.String("header", "", "catalogues add: the header a private Git host reads a token from, such as PRIVATE-TOKEN; its value is asked for")
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

	switch action {
	case "list", "ls":
		return listTemplates(ctx, client, teamID, *search, *asJSON, out)
	case "catalogues", "catalogs", "catalogue", "catalog":
		return templateCatalogues(ctx, client, teamID, positional, *header, *asJSON, out)
	}
	return errdoc.BadRequest(fmt.Sprintf("templates takes list or catalogues, not %q.", action))
}

func listTemplates(ctx context.Context, client *Client, teamID, search string, asJSON bool, out io.Writer) error {
	var listed struct {
		Items []templateAnswer `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/teams/"+url.PathEscape(teamID)+"/templates", nil, &listed); err != nil {
		return err
	}
	needle := strings.ToLower(strings.TrimSpace(search))
	shown := []templateAnswer{}
	for _, template := range listed.Items {
		haystack := strings.ToLower(template.ID + " " + template.Name + " " + template.Category)
		if template.Catalogue != nil {
			haystack += " " + strings.ToLower(template.Catalogue.Name)
		}
		if needle == "" || strings.Contains(haystack, needle) {
			shown = append(shown, template)
		}
	}
	if asJSON {
		return writeJSON(out, shown)
	}
	if len(shown) == 0 {
		fmt.Fprintln(out, "No template matches.")
		return nil
	}
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tNAME\tCATEGORY\tCATALOGUE")
	for _, template := range shown {
		catalogue := "built in"
		if template.Catalogue != nil {
			catalogue = template.Catalogue.Name
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", template.ID, template.Name, template.Category, catalogue)
	}
	return table.Flush()
}

// templateCatalogues is `templates catalogues`, whose own flags are the ones
// cmdTemplates already read.
func templateCatalogues(ctx context.Context, client *Client, teamID string, positional []string,
	header string, asJSON bool, out io.Writer) error {
	action := "list"
	if len(positional) > 0 {
		action, positional = positional[0], positional[1:]
	}
	path := "/api/teams/" + url.PathEscape(teamID) + "/template-catalogues"

	var listed struct {
		Items []catalogueAnswer `json:"items"`
	}
	list := func() error { return client.Do(ctx, "GET", path, nil, &listed) }

	switch action {
	case "list", "ls":
		if err := list(); err != nil {
			return err
		}
		if asJSON {
			return writeJSON(out, listed.Items)
		}
		if len(listed.Items) == 0 {
			fmt.Fprintf(out, "This team has no catalogues of its own. Add one with `%s templates catalogues add <name> <https address>`.\n",
				version.Binary)
			return nil
		}
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "NAME\tTEMPLATES\tREFUSED\tDOWNLOADED\tADDRESS")
		for _, catalogue := range listed.Items {
			fmt.Fprintf(table, "%s\t%d\t%d\t%s\t%s\n", catalogue.Name, catalogue.Templates, len(catalogue.Problems),
				humanTime(catalogue.FetchedAt), catalogue.URL)
		}
		if err := table.Flush(); err != nil {
			return err
		}
		for _, catalogue := range listed.Items {
			printCatalogueTrouble(out, catalogue)
		}
		return nil

	case "add":
		if len(positional) != 2 {
			return errdoc.BadRequest(fmt.Sprintf("Name the catalogue and give its address: `%s templates catalogues add Acme https://…/catalogue.yaml`.",
				version.Binary))
		}
		body := map[string]any{"name": positional[0], "url": positional[1]}
		if header = strings.TrimSpace(header); header != "" {
			// Asked for rather than taken as a flag, so the token is not in
			// the shell's history. Piped in, it is read from stdin.
			value := promptSecret(out, header+": ")
			if value == "" {
				return errdoc.BadRequest(fmt.Sprintf("The header %s needs a value, typed at the prompt or piped in.", header))
			}
			body["auth_header_name"], body["auth_header_value"] = header, value
		}
		var added catalogueAnswer
		// Adding downloads and reads the catalogue first, logos and all.
		if err := client.DoLong(ctx, "POST", path, body, &added); err != nil {
			return err
		}
		if asJSON {
			return writeJSON(out, added)
		}
		fmt.Fprintf(out, "Added %s: %d template(s) can be installed.\n", added.Name, added.Templates)
		printCatalogueTrouble(out, added)
		return nil

	case "refresh", "remove", "rm":
		if len(positional) != 1 {
			return errdoc.BadRequest(fmt.Sprintf("Name the catalogue: `%s templates catalogues %s <name>`.", version.Binary, action))
		}
		if err := list(); err != nil {
			return err
		}
		catalogue, err := findCatalogue(listed.Items, positional[0])
		if err != nil {
			return err
		}
		if action == "refresh" {
			var refreshed catalogueAnswer
			if err := client.DoLong(ctx, "POST", path+"/"+url.PathEscape(catalogue.ID)+"/refresh", nil, &refreshed); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(out, refreshed)
			}
			fmt.Fprintf(out, "Downloaded %s again: %d template(s) can be installed.\n", refreshed.Name, refreshed.Templates)
			printCatalogueTrouble(out, refreshed)
			return nil
		}
		if err := client.Do(ctx, "DELETE", path+"/"+url.PathEscape(catalogue.ID), nil, nil); err != nil {
			return err
		}
		if asJSON {
			return writeJSON(out, map[string]string{"removed": catalogue.Name, "id": catalogue.ID})
		}
		fmt.Fprintf(out, "Removed %s. Apps installed from it keep running.\n", catalogue.Name)
		return nil
	}
	return errdoc.BadRequest(fmt.Sprintf("templates catalogues takes list, add, refresh or remove, not %q.", action))
}

// printCatalogueTrouble says why a catalogue's last refresh failed and why
// each of its refused templates was refused.
func printCatalogueTrouble(out io.Writer, catalogue catalogueAnswer) {
	if catalogue.LastError != "" {
		fmt.Fprintf(out, "\n%s: the last refresh failed, and the copy downloaded %s is still in use:\n  %s\n",
			catalogue.Name, humanTime(catalogue.FetchedAt), catalogue.LastError)
	}
	for _, problem := range catalogue.Problems {
		what := problem.File
		if problem.ID != "" {
			what = problem.ID + " (" + problem.File + ")"
		}
		fmt.Fprintf(out, "\n%s: %s cannot be installed:\n", catalogue.Name, what)
		for _, reason := range problem.Errors {
			fmt.Fprintf(out, "  - %s\n", reason)
		}
	}
}

func findCatalogue(catalogues []catalogueAnswer, named string) (catalogueAnswer, error) {
	names := make([]string, 0, len(catalogues))
	for _, catalogue := range catalogues {
		if catalogue.ID == named || strings.EqualFold(catalogue.Name, named) {
			return catalogue, nil
		}
		names = append(names, catalogue.Name)
	}
	if len(names) == 0 {
		return catalogueAnswer{}, errdoc.BadRequest("This team has no catalogues of its own.")
	}
	return catalogueAnswer{}, errdoc.BadRequest(fmt.Sprintf("The team has no catalogue called %q. It has: %s.",
		named, strings.Join(names, ", ")))
}
