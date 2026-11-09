package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// Changing an app after it was created: its settings, a restart, stopping a
// deploy that is under way.
//
// Deleting an app is not a tool. DELETE /api/apps/{id} takes the app's disks
// and everything on them with it, and nothing brings them back: the same
// reason a restore is left out (see databases.go). An assistant deciding to
// delete may be acting on a log line or a file somebody else wrote, and a
// destructive hint only helps when the client honours it. The person deletes
// an app in the panel, or with `skifity apps rm`, which asks for its name.

type appSettingsInput struct {
	AppID string `json:"app_id" jsonschema:"the app's id"`
	// Each is a pointer: left out, the setting keeps what it has. Only what
	// is given is sent.
	Name                 *string `json:"name,omitempty" jsonschema:"what the app is called"`
	Branch               *string `json:"branch,omitempty" jsonschema:"the branch a deploy builds"`
	RootDir              *string `json:"root_dir,omitempty" jsonschema:"the folder in the repository the app is in"`
	Builder              *string `json:"builder,omitempty" jsonschema:"how it is built: auto, railpack, dockerfile or static"`
	DockerfilePath       *string `json:"dockerfile_path,omitempty" jsonschema:"the Dockerfile to build with, from the root"`
	BuildCommand         *string `json:"build_command,omitempty" jsonschema:"the command that builds it, such as npm run build"`
	StaticDir            *string `json:"static_dir,omitempty" jsonschema:"the folder a static site serves, such as dist"`
	Image                *string `json:"image,omitempty" jsonschema:"the image an image app runs"`
	Port                 *int    `json:"port,omitempty" jsonschema:"the port the app listens on"`
	StartCommand         *string `json:"start_command,omitempty" jsonschema:"the command it starts with"`
	ReleaseCommand       *string `json:"release_command,omitempty" jsonschema:"a command run before each new version takes traffic, such as a migration"`
	HealthPath           *string `json:"health_path,omitempty" jsonschema:"the path the health check asks, such as /healthz"`
	HealthCheck          *string `json:"health_check,omitempty" jsonschema:"how an instance is checked: http asks the path, tcp connects to the port, none checks nothing"`
	HealthStartSeconds   *int    `json:"health_start_seconds,omitempty" jsonschema:"how long a new instance may take to answer, in seconds"`
	HealthTimeoutSeconds *int    `json:"health_timeout_seconds,omitempty" jsonschema:"how long one check waits, in seconds"`
	CPURequestM          *int    `json:"cpu_request_m,omitempty" jsonschema:"the CPU to reserve for each instance, in millicores"`
	CPULimitM            *int    `json:"cpu_limit_m,omitempty" jsonschema:"the CPU each instance may use, in millicores; 0 for no limit"`
	MemRequestMB         *int    `json:"mem_request_mb,omitempty" jsonschema:"the memory to reserve for each instance, in MB"`
	MemLimitMB           *int    `json:"mem_limit_mb,omitempty" jsonschema:"the memory each instance may use, in MB; an app killed for its memory needs more here"`
	RunAsUser            *int    `json:"run_as_user,omitempty" jsonschema:"the uid an image somebody else built runs as; 0 leaves it to the image"`
	Internal             *bool   `json:"internal,omitempty" jsonschema:"reachable only from the environment's other apps, with no address of its own"`
}

type appSettingsOutput struct {
	RolledOut  []string `json:"rolled_out"`
	NextDeploy []string `json:"next_deploy"`
	Note       string   `json:"note"`
}

type promoteInput struct {
	AppID         string `json:"app_id" jsonschema:"the app whose version is promoted, in the environment it runs in now, such as staging's"`
	ToEnvironment string `json:"to_environment" jsonschema:"the environment to run it in, by name or id, such as production"`
	DeploymentID  string `json:"deployment_id,omitempty" jsonschema:"the version to promote; omitted means the one the app runs now"`
	Force         bool   `json:"force,omitempty" jsonschema:"run the image even though the other app would build it differently; only when the person has agreed to that"`
}

type promoteOutput struct {
	DeploymentID string `json:"deployment_id"`
	Number       int    `json:"number"`
	AppID        string `json:"app_id"`
	Environment  string `json:"environment"`
	Note         string `json:"note"`
}

type cancelDeploymentInput struct {
	AppID        string `json:"app_id" jsonschema:"the app's id"`
	DeploymentID string `json:"deployment_id,omitempty" jsonschema:"the deployment to stop; omitted means the one under way"`
}

func (s *Server) registerApps() {
	addTool(s, &mcp.Tool{
		Name: "update_app_settings",
		// Destructive: a rollout replaces the running instances, and a value
		// it overwrites is gone. Idempotent: the same settings twice are the
		// same settings.
		Annotations: changes("Change an app's settings", true, true),
		InputSchema: inputSchema[appSettingsInput](func(p map[string]*jsonschema.Schema) {
			oneOf(p["health_check"], "http", "tcp", "none")
			oneOf(p["builder"], "auto", "railpack", "dockerfile", "static")
			portRange(p["port"])
		}),
		Description: "Change an app's settings: the fixes a first deploy most often needs — the port, the start command, the health check, the folder, the build command, the memory — and the rest of its settings tab. " +
			"Only what is given changes. What the running instances read is rolled out at once, without a rebuild; what goes into the image " +
			"(branch, folder, builder, Dockerfile, build command, output folder, an image app's image) and the release command wait for the next deploy. " +
			"The answer says which was which: call deploy_app for what waits.",
	}, s.updateAppSettings)

	addTool(s, &mcp.Tool{
		Name: "restart_app",
		// Destructive: every instance is replaced, and whatever one held in
		// memory goes with it.
		Annotations: changes("Restart an app", true, false),
		Description: "Replace an app's instances one at a time, on the version and settings it has. Nothing is rebuilt or changed. " +
			"For an app that is stuck, or that reads something only when it starts.",
	}, s.restartApp)

	addTool(s, &mcp.Tool{
		Name: "promote_app",
		// Destructive: the other environment's running version is replaced.
		Annotations: changes("Promote a version", true, false),
		Description: "Run the exact image an app runs — staging's, say — in the same app in another environment of the project, such as production, without building it again. " +
			"The other app keeps its own variables, domains and scaling. A version goes to the app of the same name and kind; when the environment has none, " +
			"the answer says so, and the person creates it there or clones the environment in the panel. " +
			"When the other app would build the image differently — its build-time variables differ — it is refused with the reason: tell the person, " +
			"and call again with force only if they want it run as it was built.",
	}, s.promoteApp)

	addTool(s, &mcp.Tool{
		Name: "cancel_deployment",
		// Destructive: a build that is stopped is work thrown away, and the
		// version somebody was shipping does not ship.
		Annotations: changes("Cancel a deployment", true, true),
		Description: "Stop a deployment that is building or rolling out. The version that was running keeps running. " +
			"Without deployment_id, the one under way is stopped.",
	}, s.cancelDeployment)
}

func (s *Server) updateAppSettings(ctx context.Context, _ *mcp.CallToolRequest, in appSettingsInput) (*mcp.CallToolResult, appSettingsOutput, error) {
	body := map[string]any{}
	for key, value := range map[string]any{
		"name": in.Name, "branch": in.Branch, "root_dir": in.RootDir, "builder": in.Builder,
		"dockerfile_path": in.DockerfilePath, "build_command": in.BuildCommand, "static_dir": in.StaticDir,
		"image": in.Image, "port": in.Port, "start_command": in.StartCommand, "release_command": in.ReleaseCommand,
		"health_path": in.HealthPath, "health_check": in.HealthCheck, "health_start_seconds": in.HealthStartSeconds,
		"health_timeout_seconds": in.HealthTimeoutSeconds, "cpu_request_m": in.CPURequestM, "cpu_limit_m": in.CPULimitM,
		"mem_request_mb": in.MemRequestMB, "mem_limit_mb": in.MemLimitMB, "run_as_user": in.RunAsUser, "internal": in.Internal,
	} {
		switch v := value.(type) {
		case *string:
			if v != nil {
				body[key] = *v
			}
		case *int:
			if v != nil {
				body[key] = *v
			}
		case *bool:
			if v != nil {
				body[key] = *v
			}
		}
	}
	if len(body) == 0 {
		return errorResult(errdoc.BadRequest("Give at least one setting to change, such as port or start_command.")), appSettingsOutput{}, nil
	}
	var response struct {
		RolledOut  []string `json:"rolled_out"`
		NextDeploy []string `json:"next_deploy"`
	}
	if err := s.client.Do(ctx, "PATCH", appPath(in.AppID, ""), body, &response); err != nil {
		return errorResult(err), appSettingsOutput{}, nil
	}
	out := appSettingsOutput{RolledOut: orEmpty(response.RolledOut), NextDeploy: orEmpty(response.NextDeploy)}
	var notes []string
	if len(out.RolledOut) > 0 {
		notes = append(notes, "Rolled out to the running instances without a rebuild: "+strings.Join(out.RolledOut, ", ")+".")
	}
	if len(out.NextDeploy) > 0 {
		notes = append(notes, "These take effect at the next deploy, which builds with them: "+
			strings.Join(out.NextDeploy, ", ")+". Call deploy_app when the person wants them live.")
	}
	if len(notes) == 0 {
		notes = append(notes, "Saved. Nothing the app runs with changed.")
	}
	out.Note = strings.Join(notes, " ")
	return textResult(out.Note), out, nil
}

func orEmpty(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func (s *Server) restartApp(ctx context.Context, _ *mcp.CallToolRequest, in appIDInput) (*mcp.CallToolResult, doneOutput, error) {
	if err := s.client.Do(ctx, "POST", appPath(in.AppID, "/restart"), nil, nil); err != nil {
		return errorResult(err), doneOutput{}, nil
	}
	return done("The instances are being replaced one at a time. Nothing was rebuilt; call get_app_status to see them come back.")
}

func (s *Server) cancelDeployment(ctx context.Context, _ *mcp.CallToolRequest, in cancelDeploymentInput) (*mcp.CallToolResult, doneOutput, error) {
	target := in.DeploymentID
	if target == "" {
		var response struct {
			Items []store.Deployment `json:"items"`
		}
		if err := s.client.Do(ctx, "GET", appPath(in.AppID, "/deployments?limit=20"), nil, &response); err != nil {
			return errorResult(err), doneOutput{}, nil
		}
		for _, deployment := range response.Items {
			if !deployment.Status.Terminal() {
				target = deployment.ID
				break
			}
		}
		if target == "" {
			return errorResult(errdoc.BadRequest("Nothing is being deployed, so there is nothing to cancel.")), doneOutput{}, nil
		}
	}
	if err := s.client.Do(ctx, "POST", appPath(in.AppID, "/deployments/"+url.PathEscape(target)+"/cancel"), nil, nil); err != nil {
		return errorResult(err), doneOutput{}, nil
	}
	return done(fmt.Sprintf("Deployment %s is cancelled. The version that was running keeps running.", target))
}

func (s *Server) promoteApp(ctx context.Context, _ *mcp.CallToolRequest, in promoteInput) (*mcp.CallToolResult, promoteOutput, error) {
	var targets struct {
		Items []struct {
			AppID           string `json:"app_id"`
			EnvironmentID   string `json:"environment_id"`
			EnvironmentName string `json:"environment_name"`
		} `json:"items"`
	}
	if err := s.client.Do(ctx, "GET", appPath(in.AppID, "/promote"), nil, &targets); err != nil {
		return errorResult(err), promoteOutput{}, nil
	}
	to := strings.TrimSpace(in.ToEnvironment)
	out := promoteOutput{}
	var names []string
	for _, target := range targets.Items {
		if target.EnvironmentID == to || strings.EqualFold(target.EnvironmentName, to) {
			out.AppID, out.Environment = target.AppID, target.EnvironmentName
		}
		names = append(names, target.EnvironmentName)
	}
	if out.AppID == "" {
		where := "nowhere: no other environment of the project has an app of the same name and kind"
		if len(names) > 0 {
			where = strings.Join(names, ", ")
		}
		return errorResult(errdoc.PromotionNotPossible(fmt.Sprintf(
			"%s has no app of the same name and kind as this one, and a version is promoted to that app. This app's versions can go to %s. "+
				"The person can create the app there, or clone this environment in the panel.", to, where))), promoteOutput{}, nil
	}

	version := in.DeploymentID
	if version == "" {
		var history struct {
			Items []store.Deployment `json:"items"`
		}
		if err := s.client.Do(ctx, "GET", appPath(in.AppID, "/deployments?limit=20"), nil, &history); err != nil {
			return errorResult(err), promoteOutput{}, nil
		}
		for _, deployment := range history.Items {
			if deployment.Status == store.DeploySucceeded {
				version = deployment.ID
				break
			}
		}
		if version == "" {
			return errorResult(errdoc.PromotionNotPossible("This app has not deployed a version yet, so there is nothing to promote.")), promoteOutput{}, nil
		}
	}
	body := map[string]any{"deployment_id": version}
	if in.Force {
		body["force"] = true
	}
	var deployment store.Deployment
	// A refusal comes back whole: built_differently says why, and what the
	// person can choose.
	if err := s.client.Do(ctx, "POST", appPath(out.AppID, "/promote"), body, &deployment); err != nil {
		return errorResult(err), promoteOutput{}, nil
	}
	out.DeploymentID, out.Number = deployment.ID, deployment.Number
	out.Note = fmt.Sprintf("Deployment #%d runs the version in %s, without building it. Call get_app_status with app_id %s for the outcome.",
		deployment.Number, out.Environment, out.AppID)
	return textResult(out.Note), out, nil
}
