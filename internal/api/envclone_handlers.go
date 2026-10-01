package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// Cloning an environment: staging made from production, or a second customer
// set up like the first. Coolify and Dokploy both do this; here it is the
// same apps, settings, variables, files, processes and schedules, and a new,
// empty database of each kind the original has, linked the same way.
//
// What is not copied, and why:
//   - A database's data. A copy of production's rows in staging is customers'
//     records somewhere nobody meant them to be; take a backup and restore it
//     into the clone if that is what is wanted.
//   - A volume's contents, for the same reason. The volumes are made, empty.
//   - Custom domains, which point at the original. Each app gets the address
//     every new app gets.
//   - Public ports, which are one app's on the whole panel.
//   - Deploying on push. A clone that also deployed every push to the branch
//     production follows would be a second production nobody asked for.

type cloneEnvironmentRequest struct {
	Name string `json:"name"`
	// Deploy starts each app once everything is copied.
	Deploy bool `json:"deploy"`
}

type clonedEnvironment struct {
	Environment store.Environment `json:"environment"`
	Apps        []store.App       `json:"apps"`
	Databases   []store.Database  `json:"databases"`
	// Notes say what was left behind, app by app.
	Notes []cloneNote `json:"notes"`
}

// A cloneNote is one thing the copy does not have, as a code the interface
// translates rather than a sentence.
type cloneNote struct {
	// Name is the app's or the database's.
	Name string `json:"name"`
	// Code is one of volumes_empty, domains_kept, ports_kept, push_deploy_off,
	// database_skipped or deploy_failed.
	Code string `json:"code"`
	// Detail is the reason, for deploy_failed.
	Detail string `json:"detail,omitempty"`
}

func (s *Server) handleCloneEnvironment(w http.ResponseWriter, r *http.Request) {
	source, _, err := s.authorizeEnvironment(r, chi.URLParam(r, "envID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req cloneEnvironmentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, r, errdoc.BadRequest("The copy needs a name, such as staging."))
		return
	}
	if source.Kind == store.EnvPreview {
		writeError(w, r, errdoc.BadRequest("A preview is a copy already, and goes when its pull request closes. Clone the environment it was made from."))
		return
	}
	project, err := s.db.GetProject(r.Context(), source.ProjectID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	team, err := s.db.GetTeam(r.Context(), project.TeamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	user, _ := UserFrom(r.Context())

	env := store.Environment{
		ProjectID: project.ID, Name: name, Slug: kube.Slugify(name), Kind: store.EnvStandard,
		PodSecurity: source.PodSecurity,
	}
	env.Namespace = kube.NamespaceFor(team.Slug, project.Slug, env.Slug)
	if err := s.db.CreateEnvironment(r.Context(), &env); err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster != nil {
		if err := s.cluster.EnsureNamespace(r.Context(), env, team.ID, project.ID); err != nil {
			s.log.Warn("could not create namespace", "namespace", env.Namespace, "error", err)
		}
	}

	result, err := s.cloneInto(r, source, env)
	if err != nil {
		// The half-made copy goes rather than sitting beside the original
		// looking like one; its namespace, and anything made in it, with it.
		if s.cluster != nil {
			if removeErr := s.cluster.DeleteNamespace(r.Context(), env.Namespace); removeErr != nil {
				s.log.Warn("could not remove a half-made clone's namespace", "namespace", env.Namespace, "error", removeErr)
			}
		}
		if removeErr := s.db.DeleteEnvironment(r.Context(), env.ID); removeErr != nil {
			s.log.Warn("could not remove a half-made clone", "environment", env.ID, "error", removeErr)
		}
		writeError(w, r, err)
		return
	}
	result.Environment = env

	if req.Deploy && s.deployer != nil {
		for _, app := range result.Apps {
			if _, err := s.deployer.Deploy(r.Context(), DeployRequest{
				AppID: app.ID, Trigger: "manual", CreatedBy: user.ID,
			}); err != nil {
				result.Notes = append(result.Notes, cloneNote{Name: app.Name, Code: "deploy_failed", Detail: errdoc.From(err).Cause})
			}
		}
	}
	s.audit(r, project.TeamID, "environment.cloned", "environment", env.ID, source.Name+" → "+env.Name)
	writeJSON(w, http.StatusCreated, result)
}

// cloneInto copies an environment's apps and databases into another.
func (s *Server) cloneInto(r *http.Request, source, env store.Environment) (clonedEnvironment, error) {
	ctx := r.Context()
	out := clonedEnvironment{Apps: []store.App{}, Databases: []store.Database{}, Notes: []cloneNote{}}
	apps, err := s.db.ListApps(ctx, source.ID)
	if err != nil {
		return out, err
	}

	copies := map[string]store.App{}
	for _, app := range apps {
		copied := app
		copied.ID, copied.EnvironmentID, copied.Status = "", env.ID, "created"
		copied.AutoDeploy, copied.PreviewDeploys = false, false
		copied.SeededAt = ""
		if err := s.db.CreateApp(ctx, &copied); err != nil {
			return out, err
		}
		copies[app.ID] = copied
		out.Apps = append(out.Apps, copied)
		if err := s.cloneApp(r, app, copied, &out.Notes); err != nil {
			return out, err
		}
	}

	databases, err := s.db.ListDatabases(ctx, source.ID)
	if err != nil {
		return out, err
	}
	for _, database := range databases {
		if s.databases == nil {
			// This panel has no cluster to make one in.
			out.Notes = append(out.Notes, cloneNote{Name: database.Name, Code: "database_skipped"})
			continue
		}
		made, err := s.databases.Create(ctx, env, CreateDatabaseRequest{
			Name: database.Name, Engine: database.Engine, Version: database.EngineVersion,
			StorageGB: database.StorageGB, Instances: database.Instances,
		})
		if err != nil {
			return out, err
		}
		out.Databases = append(out.Databases, made)
		links, err := s.db.ListLinksForDatabase(ctx, database.ID)
		if err != nil {
			return out, err
		}
		for _, link := range links {
			copied, ok := copies[link.AppID]
			if !ok {
				continue
			}
			if err := s.databases.Link(ctx, made.ID, copied.ID, link.VarName); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// cloneApp copies what belongs to one app, resealing every secret under the
// copy: a value sealed for the original does not open anywhere else.
func (s *Server) cloneApp(r *http.Request, from, to store.App, notes *[]cloneNote) error {
	ctx := r.Context()

	// A variable a database link wrote is the original database's address;
	// the clone's own database writes its own when it is linked.
	links, err := s.db.ListLinksForApp(ctx, from.ID)
	if err != nil {
		return err
	}
	linked := map[string]bool{}
	for _, link := range links {
		linked[link.VarName] = true
	}
	variables, err := s.db.ListVariables(ctx, from.ID)
	if err != nil {
		return err
	}
	for _, row := range variables {
		if linked[row.Key] {
			continue
		}
		if row.Reference != nil {
			// The clone reads the same secret from the same manager: there is
			// no value here to copy.
			ref := *row.Reference
			variable := store.Variable{AppID: to.ID, Key: row.Key, IsSecret: true, BuildTime: row.BuildTime,
				Reference: &store.SecretReference{ConnectionID: ref.ConnectionID, Path: ref.Path, Key: ref.Key}}
			if err := s.db.SetVariable(ctx, &variable, ""); err != nil {
				return err
			}
			continue
		}
		plaintext, err := s.keyring.Open(row.Sealed, variableContext(from.ID, row.Key))
		if err != nil {
			return err
		}
		sealed, err := s.keyring.Seal(plaintext, variableContext(to.ID, row.Key))
		if err != nil {
			return err
		}
		variable := store.Variable{AppID: to.ID, Key: row.Key, IsSecret: row.IsSecret, BuildTime: row.BuildTime}
		if err := s.db.SetVariable(ctx, &variable, sealed); err != nil {
			return err
		}
	}

	files, err := s.db.ListFiles(ctx, from.ID)
	if err != nil {
		return err
	}
	for _, row := range files {
		content, err := s.keyring.Open(row.Sealed, store.FileContext(from.ID, row.Path))
		if err != nil {
			return err
		}
		sealed, err := s.keyring.Seal(content, store.FileContext(to.ID, row.Path))
		if err != nil {
			return err
		}
		file := store.AppFile{AppID: to.ID, Path: row.Path, Size: row.Size, IsSecret: row.IsSecret, Executable: row.Executable}
		if err := s.db.SetFile(ctx, &file, sealed); err != nil {
			return err
		}
	}

	processes, err := s.db.ListProcesses(ctx, from.ID)
	if err != nil {
		return err
	}
	for _, process := range processes {
		process.AppID = to.ID
		if err := s.db.SetProcess(ctx, &process); err != nil {
			return err
		}
	}

	jobs, err := s.db.ListAppJobs(ctx, from.ID)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		job.ID, job.AppID = "", to.ID
		if err := s.db.CreateAppJob(ctx, &job); err != nil {
			return err
		}
	}

	volumes, err := s.db.ListVolumes(ctx, from.ID)
	if err != nil {
		return err
	}
	for _, volume := range volumes {
		volume.ID, volume.AppID = "", to.ID
		if err := s.db.CreateVolume(ctx, &volume); err != nil {
			return err
		}
	}
	if len(volumes) > 0 {
		*notes = append(*notes, cloneNote{Name: to.Name, Code: "volumes_empty"})
	}

	// A copy of the environment asks for the same cards. Whether the cluster
	// has them twice over is what the copy's own GPU card says; the copy is
	// not deployed unless that was asked for.
	gpu, err := s.db.GetAppGPU(ctx, from.ID)
	if err != nil {
		return err
	}
	if gpu.Count > 0 {
		gpu.AppID = to.ID
		if err := s.db.SetAppGPU(ctx, gpu); err != nil {
			return err
		}
	}

	if password, ok, err := s.db.GetAppPassword(ctx, from.ID); err != nil {
		return err
	} else if ok {
		password.AppID = to.ID
		user, _ := UserFrom(ctx)
		if err := s.db.SetAppPassword(ctx, password, user.ID); err != nil {
			return err
		}
	}

	// The original's domains stay with it, and the copy has the address every
	// new app gets; a public port is one app's on the whole panel.
	if domains, err := s.db.ListDomains(ctx, from.ID); err == nil && len(domains) > 0 {
		*notes = append(*notes, cloneNote{Name: to.Name, Code: "domains_kept"})
	}
	if ports, err := s.db.ListPorts(ctx, from.ID); err == nil && len(ports) > 0 {
		*notes = append(*notes, cloneNote{Name: to.Name, Code: "ports_kept"})
	}
	if from.AutoDeploy {
		*notes = append(*notes, cloneNote{Name: to.Name, Code: "push_deploy_off"})
	}
	return nil
}
