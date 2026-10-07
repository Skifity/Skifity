package api

import (
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/builder"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/gitsrc"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// A Compose file, deployed as what it describes: every service an app in one
// environment, created together, reaching each other by name and port the way
// they do in Compose.
//
// The form used to offer the services as a choice — pick one, fill the form,
// come back for the next — which for a file with a web app, a worker, a
// database and a cache was four trips through a form and a guess at which
// ports the others listened on. Coolify, CapRover and Render take the file as
// a stack; so does this.

// maxStackServices bounds one request. A Compose file with more services than
// this is a platform of its own.
const maxStackServices = 20

type stackRequest struct {
	// Where services with a build come from. A stack of images needs none.
	RepoURL     string `json:"repo_url,omitempty"`
	GitSourceID string `json:"git_source_id,omitempty"`
	Branch      string `json:"branch,omitempty"`
	// RootDir is where the Compose file is, which is what its build contexts
	// are relative to.
	RootDir  string                   `json:"root_dir,omitempty"`
	Services []builder.ComposeService `json:"services"`
	Deploy   bool                     `json:"deploy,omitempty"`
}

// stackNote is something about a service that did not carry over as written.
// A code and a value rather than a sentence, so the interface says it in the
// reader's language.
type stackNote struct {
	Service string `json:"service"`
	Code    string `json:"code"`
	Value   string `json:"value,omitempty"`
}

// plannedApp is one service, worked out before anything is created.
type plannedApp struct {
	service   builder.ComposeService
	app       store.App
	variables map[string]string
	volumes   []store.Volume
}

func (s *Server) handleCreateStack(w http.ResponseWriter, r *http.Request) {
	env, user, err := s.authorizeEnvironment(r, chi.URLParam(r, "envID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req stackRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if len(req.Services) == 0 || len(req.Services) > maxStackServices {
		writeError(w, r, errdoc.StackSize(maxStackServices))
		return
	}
	repoURL := strings.TrimSpace(req.RepoURL)
	if repoURL != "" {
		checked, err := gitsrc.ValidateRepoURL(repoURL)
		if err != nil {
			writeError(w, r, errdoc.BadRequest(capitalise(err.Error())+"."))
			return
		}
		repoURL = checked
	}
	if err := s.checkGitSource(r, req.GitSourceID, env.ID); err != nil {
		writeError(w, r, err)
		return
	}

	// Everything is checked before anything is created, so a problem with the
	// fourth service does not leave three apps behind it.
	var notes []stackNote
	planned := make([]plannedApp, 0, len(req.Services))
	bySlug := map[string]string{}
	for _, service := range req.Services {
		plan, serviceNotes, err := planStackApp(env, req, repoURL, service)
		if err != nil {
			writeError(w, r, err)
			return
		}
		// The same rule a single app is held to: an image this panel built for
		// another environment does not run in this one. A Compose file names
		// its images itself, and was the way round it.
		if plan.app.SourceType == "image" {
			if err := s.checkImageReference(r.Context(), plan.app.Image, env.Namespace); err != nil {
				writeError(w, r, err)
				return
			}
		}
		if other, taken := bySlug[plan.app.Slug]; taken {
			writeError(w, r, errdoc.StackDuplicate(other, service.Name, plan.app.Slug))
			return
		}
		bySlug[plan.app.Slug] = service.Name
		if owner, err := s.db.SlugOwnerInEnvironment(r.Context(), env.ID, plan.app.Slug); err != nil {
			writeError(w, r, err)
			return
		} else if owner != "" {
			writeError(w, r, errdoc.NameTaken(owner, plan.app.Slug))
			return
		}
		notes = append(notes, serviceNotes...)
		planned = append(planned, plan)
	}

	var created []store.App
	undo := func() {
		for _, app := range created {
			_ = s.db.DeleteApp(r.Context(), app.ID)
		}
	}
	for i := range planned {
		plan := &planned[i]
		if err := s.db.CreateApp(r.Context(), &plan.app); err != nil {
			undo()
			writeError(w, r, err)
			return
		}
		created = append(created, plan.app)
		if err := s.setInitialVariables(r, plan.app, plan.variables); err != nil {
			undo()
			writeError(w, r, err)
			return
		}
		for j := range plan.volumes {
			plan.volumes[j].AppID = plan.app.ID
			if err := s.db.CreateVolume(r.Context(), &plan.volumes[j]); err != nil {
				undo()
				writeError(w, r, err)
				return
			}
		}
	}

	teamID, _ := s.db.TeamIDForEnvironment(r.Context(), env.ID)
	names := make([]string, 0, len(created))
	for _, app := range created {
		names = append(names, app.Name)
		s.hub.Publish(events.TeamTopic(teamID), "app.created", app)
	}
	s.audit(r, teamID, "stack.created", "environment", env.ID, strings.Join(names, ", "))

	answer := map[string]any{"apps": created, "notes": notesOrEmpty(notes)}
	// One repository, one webhook: the first app built from it registers it,
	// and a push then reaches every app built from it.
	for _, app := range created {
		if app.SourceType == "git" {
			answer["webhook"] = s.ensureWebhookFor(r, app)
			break
		}
	}
	if req.Deploy && s.deployer != nil {
		var deployments []store.Deployment
		for _, app := range deployOrder(planned) {
			deployment, err := s.deployer.Deploy(r.Context(), DeployRequest{
				AppID: app.ID, Trigger: "create", CreatedBy: user.ID,
			})
			if err != nil {
				s.log.Warn("could not start a stack app's first deploy", "app", app.ID, "error", err)
				continue
			}
			deployments = append(deployments, deployment)
		}
		answer["deployments"] = deployments
	}
	writeJSON(w, http.StatusCreated, answer)
}

// planStackApp works out the app one service becomes.
func planStackApp(env store.Environment, req stackRequest, repoURL string, service builder.ComposeService) (plannedApp, []stackNote, error) {
	name := strings.TrimSpace(service.Name)
	if name == "" {
		return plannedApp{}, nil, errdoc.StackNoSource("?")
	}
	slug := kube.Slugify(name)
	var notes []stackNote
	// The others reach it by its slug, and a Compose name with an underscore
	// or capitals is not one a cluster's DNS will answer to.
	if slug != name {
		notes = append(notes, stackNote{Service: name, Code: "renamed", Value: slug})
	}

	app := store.App{
		EnvironmentID: env.ID, Name: name, Slug: slug,
		Port:         service.ContainerPort(),
		StartCommand: strings.TrimSpace(service.Command),
		Internal:     !service.Public(),
		// The same safe defaults a single app gets.
		Replicas: 1, MinReplicas: 1, MaxReplicas: 3, CPUTarget: 75,
		CPURequestM: 50, CPULimitM: 1000, MemRequestMB: 128, MemLimitMB: 512,
		AutoDeploy: true, Status: "created",
	}
	switch {
	case service.Build != "":
		if repoURL == "" {
			return plannedApp{}, nil, errdoc.StackNeedsRepository(name)
		}
		app.SourceType, app.RepoURL = "git", repoURL
		app.GitSourceID = req.GitSourceID
		app.Branch = strings.TrimSpace(req.Branch)
		app.RootDir = composeRoot(req.RootDir, service.Build)
		// Compose always builds with a Dockerfile, so that is what builds it
		// here too, rather than a guess at what the directory holds.
		app.Builder = "dockerfile"
		app.DockerfilePath = defaultString(strings.TrimSpace(service.Dockerfile), "Dockerfile")
	case service.Image != "":
		app.SourceType, app.Image, app.Builder = "image", strings.TrimSpace(service.Image), "auto"
	default:
		return plannedApp{}, nil, errdoc.StackNoSource(name)
	}
	if app.Port == 0 && !app.Internal {
		app.Internal = true
	}

	variables := map[string]string{}
	for key, value := range service.Environment {
		resolved, unresolved := resolveComposeValue(value)
		if unresolved {
			notes = append(notes, stackNote{Service: name, Code: "interpolation", Value: key})
		}
		variables[key] = resolved
	}

	var volumes []store.Volume
	for _, entry := range service.Volumes {
		source, target, ok := strings.Cut(entry, ":")
		target, _, _ = strings.Cut(target, ":")
		switch {
		case !ok || !strings.HasPrefix(target, "/"):
			// An anonymous volume, "/data" alone: the data is thrown away with
			// the container in Compose too.
			continue
		case strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/") || strings.HasPrefix(source, "~"):
			// A folder on the machine running Compose. There is no such
			// machine here; the files belong in the image or a volume.
			notes = append(notes, stackNote{Service: name, Code: "bind_mount", Value: entry})
			continue
		}
		volumes = append(volumes, store.Volume{
			Name: kube.Slugify(source), MountPath: strings.TrimSuffix(target, "/"), SizeGB: volumeSizeFor(app.Port),
		})
	}
	return plannedApp{service: service, app: app, variables: variables, volumes: volumes}, notes, nil
}

// composeRoot is a build context as a root directory: relative to where the
// Compose file is, without "./", and "" for the repository itself.
func composeRoot(rootDir, context string) string {
	joined := path.Clean(path.Join(strings.Trim(rootDir, "/"), strings.TrimPrefix(context, "./")))
	if joined == "." {
		return ""
	}
	return strings.TrimPrefix(joined, "/")
}

// volumeSizeFor is how big a new volume is. A database's grows, so it starts
// larger than a folder of uploads.
func volumeSizeFor(port int) int {
	switch port {
	case 5432, 3306, 27017, 9200:
		return 5
	}
	return 1
}

var composeDefault = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*:?-(.*)\}$`)

// resolveComposeValue takes a value's default where Compose would, because
// the shell environment Compose reads it from is not here. A reference with
// no default is kept as written and reported.
func resolveComposeValue(value string) (string, bool) {
	if match := composeDefault.FindStringSubmatch(value); match != nil {
		return match[1], false
	}
	return value, strings.Contains(value, "${") || strings.HasPrefix(value, "$")
}

// deployOrder puts a service after the ones it depends on, where it can.
func deployOrder(planned []plannedApp) []store.App {
	byName := map[string]plannedApp{}
	for _, plan := range planned {
		byName[plan.service.Name] = plan
	}
	names := make([]string, 0, len(planned))
	for _, plan := range planned {
		names = append(names, plan.service.Name)
	}
	sort.Strings(names)

	var out []store.App
	done := map[string]bool{}
	visiting := map[string]bool{}
	var visit func(name string)
	visit = func(name string) {
		plan, ok := byName[name]
		if !ok || done[name] || visiting[name] {
			return
		}
		visiting[name] = true
		for _, dependency := range plan.service.DependsOn {
			visit(dependency)
		}
		done[name] = true
		out = append(out, plan.app)
	}
	for _, name := range names {
		visit(name)
	}
	return out
}

func notesOrEmpty(notes []stackNote) []stackNote {
	if notes == nil {
		return []stackNote{}
	}
	return notes
}
