package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/errdoc"
	"skifity/internal/secretmgr"
	"skifity/internal/store"
)

// Changing several variables at once, and removing one. set_variable and
// list_variables are in server.go.

type setVariablesInput struct {
	AppID string           `json:"app_id" jsonschema:"the app's id"`
	Set   []variableChange `json:"set,omitempty" jsonschema:"the variables to set"`
	Unset []string         `json:"unset,omitempty" jsonschema:"the names of variables to remove"`
}

type variableChange struct {
	Key   string `json:"key" jsonschema:"the variable's name, in CAPITALS_WITH_UNDERSCORES"`
	Value string `json:"value" jsonschema:"the value; empty when from is given"`
	From  string `json:"from,omitempty" jsonschema:"read the value from one of the team's secret managers instead, written connection:path or connection:path#key"`
	// Pointers, for the reason set_variable's are.
	IsSecret  *bool `json:"is_secret,omitempty" jsonschema:"store it encrypted and never show it again; leave it out to keep an existing secret a secret and let the panel decide for a new one"`
	BuildTime *bool `json:"build_time,omitempty" jsonschema:"the value is needed while building, so setting it causes a rebuild; leave it out to keep what the variable was"`
}

type setVariablesOutput struct {
	Set             []setVariableSummary `json:"set"`
	Unset           []string             `json:"unset"`
	RequiresRebuild bool                 `json:"requires_rebuild"`
	Note            string               `json:"note"`
}

type setVariableSummary struct {
	Key      string `json:"key"`
	IsSecret bool   `json:"is_secret"`
}

type deleteVariableInput struct {
	AppID string `json:"app_id" jsonschema:"the app's id"`
	Key   string `json:"key" jsonschema:"the variable's name"`
}

func (s *Server) registerVariables() {
	addTool(s, &mcp.Tool{
		Name:        "set_variables",
		Annotations: changes("Set several variables", true, true),
		Description: "Set and remove several of an app's environment variables at once: all of them or none, and one rollout for the lot rather than one per variable. " +
			"Use it for more than one change, such as the settings from an .env.example; set_variable is for one. " +
			"Runtime variables roll out without rebuilding; the answer says when a build-time one means the next deploy rebuilds.",
	}, s.setVariables)

	addTool(s, &mcp.Tool{
		Name:        "refresh_variables",
		Annotations: changes("Refresh variables from secret managers", true, true),
		Description: "Read an app's variables from the secret managers they come from again, and roll the app out if any changed: " +
			"a rollout for runtime values, a rebuild of the running version when one the build reads changed. Nothing happens when nothing changed. " +
			"The answer names what changed; it never holds a value.",
	}, s.refreshVariables)

	addTool(s, &mcp.Tool{
		Name:        "delete_variable",
		Annotations: changes("Remove a variable", true, true),
		Description: "Remove one of an app's environment variables and roll the app out without it. A secret's value is gone for good, so ask the person before removing one. " +
			"A variable a linked database provides comes from link_database: removing it leaves the app without its connection string.",
	}, s.deleteVariable)
}

func (s *Server) setVariables(ctx context.Context, _ *mcp.CallToolRequest, in setVariablesInput) (*mcp.CallToolResult, setVariablesOutput, error) {
	if len(in.Set)+len(in.Unset) == 0 {
		return errorResult(errdoc.BadRequest("Give at least one variable to set or to remove.")), setVariablesOutput{}, nil
	}
	body := map[string]any{}
	if len(in.Set) > 0 {
		set := make([]map[string]any, 0, len(in.Set))
		for _, change := range in.Set {
			entry := map[string]any{"key": change.Key, "value": change.Value}
			if change.From != "" {
				from, err := referenceBody(change.From)
				if err != nil {
					return errorResult(err), setVariablesOutput{}, nil
				}
				entry = map[string]any{"key": change.Key, "from": from}
			}
			if change.IsSecret != nil {
				entry["is_secret"] = *change.IsSecret
			}
			if change.BuildTime != nil {
				entry["build_time"] = *change.BuildTime
			}
			set = append(set, entry)
		}
		body["set"] = set
	}
	if len(in.Unset) > 0 {
		body["unset"] = in.Unset
	}

	var response struct {
		Set             []store.Variable `json:"set"`
		Unset           []string         `json:"unset"`
		RequiresRebuild bool             `json:"requires_rebuild"`
	}
	if err := s.client.Do(ctx, "POST", appPath(in.AppID, "/variables/batch"), body, &response); err != nil {
		return errorResult(err), setVariablesOutput{}, nil
	}

	// Names and whether each is a secret. The panel sends no values back,
	// and nothing here would pass one on if it did.
	out := setVariablesOutput{
		Set: make([]setVariableSummary, 0, len(response.Set)), Unset: response.Unset,
		RequiresRebuild: response.RequiresRebuild,
		Note:            "Rolled out to the running instances without rebuilding.",
	}
	if out.Unset == nil {
		out.Unset = []string{}
	}
	var keys []string
	for _, variable := range response.Set {
		out.Set = append(out.Set, setVariableSummary{Key: variable.Key, IsSecret: variable.IsSecret})
		keys = append(keys, variable.Key)
	}
	if response.RequiresRebuild {
		out.Note = "At least one of these is used during the build, so the next deploy will rebuild the image."
	}
	return textResult(fmt.Sprintf("Set %d and removed %d: %s. %s",
		len(out.Set), len(out.Unset), strings.Join(append(keys, out.Unset...), ", "), out.Note)), out, nil
}

// referenceBody is a reference as the API takes it, from the way the CLI
// writes one.
func referenceBody(text string) (map[string]string, error) {
	connection, path, key, err := secretmgr.ParseReference(text)
	if err != nil {
		return nil, errdoc.BadRequest(err.Error())
	}
	return map[string]string{"connection": connection, "path": path, "key": key}, nil
}

type refreshVariablesOutput struct {
	Changed          []string `json:"changed"`
	BuildTimeChanged []string `json:"build_time_changed"`
	RolledOut        bool     `json:"rolled_out"`
	DeploymentID     string   `json:"deployment_id,omitempty"`
	Note             string   `json:"note"`
}

func (s *Server) refreshVariables(ctx context.Context, _ *mcp.CallToolRequest, in appIDInput) (*mcp.CallToolResult, refreshVariablesOutput, error) {
	var response struct {
		References       int               `json:"references"`
		Changed          []string          `json:"changed"`
		BuildTimeChanged []string          `json:"build_time_changed"`
		RolledOut        bool              `json:"rolled_out"`
		Deployment       *store.Deployment `json:"deployment"`
		NotDeployed      bool              `json:"not_deployed"`
	}
	if err := s.client.Do(ctx, "POST", appPath(in.AppID, "/variables/refresh"), nil, &response); err != nil {
		return errorResult(err), refreshVariablesOutput{}, nil
	}
	out := refreshVariablesOutput{
		Changed: response.Changed, BuildTimeChanged: response.BuildTimeChanged, RolledOut: response.RolledOut,
	}
	if out.Changed == nil {
		out.Changed = []string{}
	}
	if out.BuildTimeChanged == nil {
		out.BuildTimeChanged = []string{}
	}
	switch {
	case response.References == 0:
		out.Note = "None of this app's variables is read from a secret manager."
	case len(out.Changed) == 0:
		out.Note = "Nothing changed in the secret managers, so nothing was rolled out."
	case response.NotDeployed:
		out.Note = "Values changed, and the app has not been deployed yet: the first deploy reads them."
	case response.Deployment != nil:
		out.DeploymentID = response.Deployment.ID
		out.Note = fmt.Sprintf("%s changed and the build reads it, so the running version is being rebuilt with it (deployment %s).",
			strings.Join(out.BuildTimeChanged, ", "), response.Deployment.ID)
	default:
		out.Note = fmt.Sprintf("%s changed and was rolled out without rebuilding.", strings.Join(out.Changed, ", "))
	}
	return textResult(out.Note), out, nil
}

func (s *Server) deleteVariable(ctx context.Context, _ *mcp.CallToolRequest, in deleteVariableInput) (*mcp.CallToolResult, doneOutput, error) {
	if err := s.client.Do(ctx, "DELETE", appPath(in.AppID, "/variables/"+url.PathEscape(in.Key)), nil, nil); err != nil {
		return errorResult(err), doneOutput{}, nil
	}
	return done(in.Key + " removed. The app is being rolled out without it.")
}
