package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"text/tabwriter"

	"skifity/internal/errdoc"
	"skifity/internal/gitsrc"
	"skifity/internal/store"
	"skifity/internal/version"
)

// cmdGit shows what the team's Git connections can read: the same lists the
// panel's form offers when an app is created from one.
//
//	skifity git                              the team's connections
//	skifity git repos acme-github            what one of them can read
//	skifity git repos acme-github --search shop
//	skifity git branches acme-github acme/shop
//
// A connection is named as it is listed, or by its id. Nothing here changes
// anything; connecting an account is done in the panel, where the token is
// typed into a form rather than into a shell's history.
func cmdGit(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("git", flag.ContinueOnError)
	flags.SetOutput(out)
	search := flags.String("search", "", "only names containing this")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	action := "sources"
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
	var sources struct {
		Items []store.GitSource `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/teams/"+teamID+"/git-sources", nil, &sources); err != nil {
		return err
	}

	switch action {
	case "sources", "connections", "ls", "list":
		if *asJSON {
			return writeJSON(out, sources.Items)
		}
		if len(sources.Items) == 0 {
			fmt.Fprintln(out, "This team has no Git connections. Connect one in the panel under Settings, then Git.")
			return nil
		}
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "NAME\tKIND\tHOST\tID")
		for _, source := range sources.Items {
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", source.Name, source.Kind, gitHost(source), source.ID)
		}
		return table.Flush()

	case "repos", "repositories":
		if len(positional) != 1 {
			return errdoc.BadRequest(fmt.Sprintf("Name the connection: `%s git repos <connection>`.", version.Binary))
		}
		source, err := findGitSource(sources.Items, positional[0])
		if err != nil {
			return err
		}
		var listing gitsrc.Listing[gitsrc.Repository]
		path := "/api/teams/" + teamID + "/git-sources/" + url.PathEscape(source.ID) + "/repositories?q=" + url.QueryEscape(*search)
		// A listing walks the host's pages one after another, which can take
		// longer than one ordinary request is given.
		if err := client.DoLong(ctx, "GET", path, nil, &listing); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, listing)
		}
		if len(listing.Items) == 0 {
			fmt.Fprintf(out, "%s can read no repositories%s.\n", source.Name, matching(*search))
			return nil
		}
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "REPOSITORY\tDEFAULT BRANCH\tVISIBILITY\tURL")
		for _, repo := range listing.Items {
			visibility := "public"
			if repo.Private {
				visibility = "private"
			}
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", repo.FullName, repo.DefaultBranch, visibility, repo.URL)
		}
		if err := table.Flush(); err != nil {
			return err
		}
		if listing.Truncated {
			fmt.Fprintf(out, "\nOnly the %d most recently active are listed. Narrow them with --search.\n", len(listing.Items))
		}
		return nil

	case "branches":
		if len(positional) != 2 {
			return errdoc.BadRequest(fmt.Sprintf("Name the connection and the repository: `%s git branches <connection> owner/name`.",
				version.Binary))
		}
		source, err := findGitSource(sources.Items, positional[0])
		if err != nil {
			return err
		}
		var listing gitsrc.Listing[gitsrc.Branch]
		path := "/api/teams/" + teamID + "/git-sources/" + url.PathEscape(source.ID) + "/branches?repo=" +
			url.QueryEscape(positional[1]) + "&q=" + url.QueryEscape(*search)
		if err := client.DoLong(ctx, "GET", path, nil, &listing); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, listing)
		}
		if len(listing.Items) == 0 {
			fmt.Fprintf(out, "%s has no branches%s.\n", positional[1], matching(*search))
			return nil
		}
		for _, branch := range listing.Items {
			fmt.Fprintln(out, branch.Name)
		}
		if listing.Truncated {
			fmt.Fprintf(out, "\nOnly the first %d are listed. Narrow them with --search.\n", len(listing.Items))
		}
		return nil
	}
	return errdoc.BadRequest(fmt.Sprintf("%q is not something `%s git` does: it lists connections, repos and branches.",
		action, version.Binary))
}

// findGitSource finds a connection by its id or its name.
func findGitSource(sources []store.GitSource, named string) (store.GitSource, error) {
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		if source.ID == named || strings.EqualFold(source.Name, named) {
			return source, nil
		}
		names = append(names, source.Name)
	}
	if len(names) == 0 {
		return store.GitSource{}, errdoc.BadRequest("This team has no Git connections. Connect one in the panel under Settings, then Git.")
	}
	return store.GitSource{}, errdoc.BadRequest(fmt.Sprintf("The team has no Git connection called %q. It has: %s.",
		named, strings.Join(names, ", ")))
}

// gitHost is where a connection's repositories live, for the list.
func gitHost(source store.GitSource) string {
	if source.BaseURL != "" {
		return strings.TrimPrefix(source.BaseURL, "https://")
	}
	switch source.Kind {
	case "github_pat":
		return "github.com"
	case "gitlab":
		return "gitlab.com"
	case "bitbucket":
		return "bitbucket.org"
	}
	return "-"
}

func matching(search string) string {
	if search == "" {
		return ""
	}
	return fmt.Sprintf(" matching %q", search)
}
