package mcpserver

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// The one-click catalogue: finding a template, and installing it.
//
// The catalogue is the one built into the panel and the team's own: a team
// can add catalogues of its own templates, which the panel lists for that team
// beside the built-in ones. An id is unique within a catalogue and not across
// them, so a template from a team catalogue carries the catalogue's id, and
// installing it passes that id back.

type listTemplatesInput struct {
	Query    string `json:"query,omitempty" jsonschema:"words to look for in the name and description, such as wordpress or analytics; every word has to match"`
	Category string `json:"category,omitempty" jsonschema:"only this category, such as cms, analytics or monitoring; the answer lists every category there is"`
	Limit    int    `json:"limit,omitempty" jsonschema:"how many to return, up to 50; 20 when left out"`
}

type listTemplatesOutput struct {
	Templates []templateSummary `json:"templates"`
	// Matches is how many matched, which is more than were returned when
	// the limit cut the list short.
	Matches    int      `json:"matches"`
	Categories []string `json:"categories"`
}

type templateSummary struct {
	ID string `json:"id"`
	// CatalogueID and Catalogue name the team catalogue a template is in,
	// and are empty for a built-in one.
	CatalogueID string          `json:"catalogue_id,omitempty"`
	Catalogue   string          `json:"catalogue,omitempty"`
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	Beta        bool            `json:"beta,omitempty"`
	Apps        []string        `json:"apps"`
	Databases   []string        `json:"databases,omitempty"`
	Inputs      []templateInput `json:"inputs,omitempty"`
	Notes       string          `json:"notes,omitempty"`
}

type templateInput struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Help     string `json:"help,omitempty"`
	Default  string `json:"default,omitempty"`
	Required bool   `json:"required,omitempty"`
	Secret   bool   `json:"secret,omitempty"`
	Generate bool   `json:"generate,omitempty"`
}

// catalogueEntry is the part of a template the tools read. The panel sends
// more — every service's image, variables and files — which is what installing
// does with it, and not what choosing one needs.
type catalogueEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Beta        bool   `json:"beta"`
	Services    []struct {
		Name string `json:"name"`
	} `json:"services"`
	Databases []struct {
		Engine string `json:"engine"`
	} `json:"databases"`
	Inputs []templateInput `json:"inputs"`
	Notes  string          `json:"notes"`
	// Catalogue is the team catalogue it came from; nil for a built-in one.
	// Only its id and name are read: nothing else about a catalogue is an
	// assistant's business.
	Catalogue *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"catalogue"`
}

type installTemplateInput struct {
	TemplateID    string            `json:"template_id" jsonschema:"the template's id, as returned by list_templates"`
	CatalogueID   string            `json:"catalogue_id,omitempty" jsonschema:"the catalogue_id list_templates returned with it, for a template from the team's own catalogue; left out for a built-in one"`
	EnvironmentID string            `json:"environment_id" jsonschema:"where to install it, as returned by list_projects"`
	Name          string            `json:"name,omitempty" jsonschema:"what to call the app instead of the template's own name; only for a template with one app, and how a second copy of it gets a name of its own"`
	Values        map[string]string `json:"values,omitempty" jsonschema:"the template's inputs, by key. Leave out one marked generate and a random secret is made for it"`
}

type installTemplateOutput struct {
	Apps      []installedApp    `json:"apps"`
	Databases []databaseSummary `json:"databases"`
	// Notes are the template's own: the steps it could not do for you.
	Notes string `json:"notes,omitempty"`
	Note  string `json:"note"`
}

type installedApp struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Image    string `json:"image"`
	Internal bool   `json:"internal,omitempty"`
}

func (s *Server) registerTemplates() {
	addTool(s, &mcp.Tool{
		Name:        "list_templates",
		Annotations: reads("List templates"),
		Description: "Search the one-click catalogue of self-hosted software — WordPress, n8n, Uptime Kuma, Plausible and a few hundred more — by words or by category. " +
			"It includes the templates of the team's own catalogues, each with a catalogue_id and the catalogue's name; " +
			"the same id can be in two catalogues, so pass catalogue_id on to install_template. " +
			"Returns each match's id, the apps and databases it creates, the inputs install_template takes, and its notes. " +
			"Look here before building well-known software by hand: a template comes with its database, volumes and settings already wired together.",
	}, s.listTemplates)

	addTool(s, &mcp.Tool{
		Name:        "install_template",
		Annotations: changes("Install a template", false, false),
		Description: "Install a template from list_templates into an environment. Its apps, databases and volumes are created and linked, and its apps start deploying from prebuilt images. " +
			"For a template from the team's own catalogue, pass the catalogue_id list_templates gave with it; without one, the built-in template of that id is installed. " +
			"Pass its inputs as values; ask the person for the ones marked required rather than inventing them, and leave out the ones marked generate so a random secret is made. " +
			"Returns the apps and databases created, and the template's notes: the steps it could not do automatically, which the person needs to hear. " +
			"An environment has one app of a name, so installing the same template there again is refused; a second copy of a one-app template needs a name of its own.",
	}, s.installTemplate)
}

func (s *Server) listTemplates(ctx context.Context, _ *mcp.CallToolRequest, in listTemplatesInput) (*mcp.CallToolResult, listTemplatesOutput, error) {
	var response struct {
		Items []catalogueEntry `json:"items"`
	}
	// The catalogue as the team sees it, its own catalogues' templates
	// among the built-in ones. A token that cannot say which team is still
	// shown the built-in catalogue, and told why that is all.
	path, only := "/api/templates", ""
	if team, err := s.teamID(ctx); err == nil {
		path = "/api/teams/" + url.PathEscape(team) + "/templates"
	} else {
		only = " These are the built-in templates only; the team's own catalogues need a team: " + errdoc.From(err).Title + "."
	}
	if err := s.client.Do(ctx, "GET", path, nil, &response); err != nil {
		return errorResult(err), listTemplatesOutput{}, nil
	}

	// The panel answers the whole catalogue and has no search of its own, so
	// the search is here. Three hundred templates with their inputs would be
	// most of an assistant's context, spent on a list it only wanted one
	// line of.
	query := strings.ToLower(strings.TrimSpace(in.Query))
	words := strings.Fields(query)
	category := strings.ToLower(strings.TrimSpace(in.Category))

	var matches []catalogueEntry
	categories := map[string]bool{}
	for _, entry := range response.Items {
		categories[entry.Category] = true
		if category != "" && strings.ToLower(entry.Category) != category {
			continue
		}
		haystack := strings.ToLower(entry.ID + " " + entry.Name + " " + entry.Description + " " + entry.Category)
		if entry.Catalogue != nil {
			haystack += " " + strings.ToLower(entry.Catalogue.Name)
		}
		if !containsAll(haystack, words) {
			continue
		}
		matches = append(matches, entry)
	}
	// "wordpress" finds WordPress before WordPress with MariaDB: the one
	// that is called what was asked for comes first.
	slices.SortStableFunc(matches, func(a, b catalogueEntry) int {
		return cmp.Compare(closeness(a, query), closeness(b, query))
	})

	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 50)

	out := listTemplatesOutput{Matches: len(matches), Templates: []templateSummary{}}
	for name := range categories {
		out.Categories = append(out.Categories, name)
	}
	slices.Sort(out.Categories)
	for _, entry := range matches[:min(limit, len(matches))] {
		out.Templates = append(out.Templates, summariseTemplate(entry))
	}

	text := fmt.Sprintf("%d template(s) match.", len(matches))
	switch {
	case len(matches) == 0:
		text = "No template matches. Try fewer words, or one of the categories listed."
	case len(matches) > len(out.Templates):
		text = fmt.Sprintf("%d template(s) match; the first %d are here. Narrow the search to see others.",
			len(matches), len(out.Templates))
	}
	return textResult(text + only), out, nil
}

func containsAll(haystack string, words []string) bool {
	for _, word := range words {
		if !strings.Contains(haystack, word) {
			return false
		}
	}
	return true
}

// closeness orders a match: called exactly what was asked for, then called
// something containing it, then the rest.
func closeness(entry catalogueEntry, query string) int {
	id, name := strings.ToLower(entry.ID), strings.ToLower(entry.Name)
	switch {
	case query == "" || id == query || name == query:
		return 0
	case strings.Contains(id, query) || strings.Contains(name, query):
		return 1
	default:
		return 2
	}
}

func summariseTemplate(entry catalogueEntry) templateSummary {
	summary := templateSummary{
		ID: entry.ID, Name: entry.Name, Category: entry.Category, Description: entry.Description,
		Beta: entry.Beta, Notes: entry.Notes, Apps: []string{},
	}
	if entry.Catalogue != nil {
		summary.CatalogueID, summary.Catalogue = entry.Catalogue.ID, entry.Catalogue.Name
	}
	for _, service := range entry.Services {
		summary.Apps = append(summary.Apps, service.Name)
	}
	for _, database := range entry.Databases {
		summary.Databases = append(summary.Databases, database.Engine)
	}
	for _, input := range entry.Inputs {
		// No catalogue entry gives a secret input a default today. One that
		// did would be a password printed in a public repository, and the
		// assistant is better off not being handed it as a suggestion.
		if input.Secret {
			input.Default = ""
		}
		summary.Inputs = append(summary.Inputs, input)
	}
	return summary
}

func (s *Server) installTemplate(ctx context.Context, _ *mcp.CallToolRequest, in installTemplateInput) (*mcp.CallToolResult, installTemplateOutput, error) {
	body := map[string]any{"environment_id": in.EnvironmentID}
	if in.CatalogueID != "" {
		body["catalogue_id"] = in.CatalogueID
	}
	if in.Name != "" {
		body["name"] = in.Name
	}
	if len(in.Values) > 0 {
		body["values"] = in.Values
	}
	// The generated secrets are set on the apps and not sent back, so there
	// is nothing in this answer to keep from the assistant.
	var response struct {
		Apps      []store.App      `json:"apps"`
		Databases []store.Database `json:"databases"`
		Notes     string           `json:"notes"`
	}
	if err := s.client.Do(ctx, "POST", "/api/templates/"+url.PathEscape(in.TemplateID)+"/install", body, &response); err != nil {
		return errorResult(err), installTemplateOutput{}, nil
	}

	out := installTemplateOutput{
		Apps: []installedApp{}, Databases: []databaseSummary{}, Notes: response.Notes,
		Note: "Each app is deploying from a prebuilt image, which usually takes a minute or two. " +
			"Call get_app_status on each for its state and its address.",
	}
	var names []string
	for _, app := range response.Apps {
		out.Apps = append(out.Apps, installedApp{ID: app.ID, Name: app.Name, Image: app.Image, Internal: app.Internal})
		names = append(names, fmt.Sprintf("%s (%s)", app.Name, app.ID))
	}
	for _, database := range response.Databases {
		out.Databases = append(out.Databases, summariseDatabase(database))
		names = append(names, fmt.Sprintf("the %s database %s (%s)", database.Engine, database.Name, database.ID))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Installed %s: %s. %s", in.TemplateID, strings.Join(names, ", "), out.Note)
	if response.Notes != "" {
		fmt.Fprintf(&b, "\n\nThe template's notes, which are steps it could not do for you. Tell the person:\n%s", response.Notes)
	}
	return textResult(b.String()), out, nil
}
