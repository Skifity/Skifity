package api

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/store"
	"skifity/internal/version"
)

// Taking everything out.
//
// A self-hosted product that cannot be left is not self-hosted, it is hosted
// somewhere inconvenient. This writes down everything the panel knows about a
// team in a form somebody can read without Skifity, and — for every app — the
// Kubernetes objects it would apply, so the same apps can be run on any cluster
// with kubectl and nothing of ours.
//
// What is deliberately not here: the value of anything marked secret. The
// promise that a stored secret is never shown again is worth more than the
// convenience, and it costs nothing, because those values are already in your
// own cluster as ordinary Kubernetes Secrets that you have root over. The
// export says which keys exist and how to read them out.

// exportDocument is the whole of a team, as JSON.
type exportDocument struct {
	Product    string          `json:"product"`
	Version    string          `json:"version"`
	ExportedAt time.Time       `json:"exported_at"`
	Note       string          `json:"note"`
	Secrets    string          `json:"secrets"`
	Team       store.Team      `json:"team"`
	Servers    []store.Server  `json:"servers"`
	Projects   []exportProject `json:"projects"`
}

type exportProject struct {
	store.Project
	SharedVariables []store.SharedVariable `json:"shared_variables"`
	Environments    []exportEnvironment    `json:"environments"`
}

type exportEnvironment struct {
	store.Environment
	Apps      []exportApp      `json:"apps"`
	Databases []store.Database `json:"databases"`
}

type exportApp struct {
	store.App
	Domains   []store.Domain   `json:"domains"`
	Variables []store.Variable `json:"variables"`
	Volumes   []store.Volume   `json:"volumes"`
	Schedules []store.AppJob   `json:"schedules"`
	// Manifests is the app as Kubernetes YAML: apply it anywhere.
	Manifests string `json:"manifests,omitempty"`
	// ManifestError says why the objects could not be rendered, rather than
	// leaving an empty field that reads like "this app has none".
	ManifestError string `json:"manifest_error,omitempty"`
}

const exportNote = "Everything Skifity knows about this team. The manifests under each app are " +
	"plain Kubernetes objects: `kubectl apply -f` them on any cluster and the app runs there " +
	"without Skifity."

const exportSecretsNote = "The value of a variable marked secret is not in this file, and never " +
	"leaves the panel once it is stored. Those values are in your own cluster already: " +
	"`kubectl -n <namespace> get secret <app>-env -o jsonpath='{.data}'` reads them, " +
	"base64 encoded, with the access you already have to your own servers."

func (s *Server) handleExportTeam(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	// Admin rather than member: this is every app, every domain and every
	// database in one answer, which is a different thing from being able to
	// look at them one at a time.
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	ctx := r.Context()

	team, err := s.db.GetTeam(ctx, teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	servers, err := s.db.ListServers(ctx, teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	projects, err := s.db.ListProjects(ctx, teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	out := exportDocument{
		Product:    version.Name,
		Version:    version.Version,
		ExportedAt: time.Now().UTC(),
		Note:       exportNote,
		Secrets:    exportSecretsNote,
		Team:       team,
		Servers:    servers,
		Projects:   make([]exportProject, 0, len(projects)),
	}

	for _, project := range projects {
		entry := exportProject{Project: project}

		shared, err := s.db.ListSharedVariables(ctx, project.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, row := range shared {
			v := row.SharedVariable
			if v.IsSecret {
				v.Value = ""
			} else if plaintext, err := s.keyring.Open(row.Sealed, sharedVariableContext(project.ID, v.Key)); err == nil {
				v.Value = string(plaintext)
			}
			entry.SharedVariables = append(entry.SharedVariables, v)
		}

		environments, err := s.db.ListEnvironments(ctx, project.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, env := range environments {
			section := exportEnvironment{Environment: env}

			databases, err := s.db.ListDatabases(ctx, env.ID)
			if err != nil {
				writeError(w, r, err)
				return
			}
			section.Databases = databases

			apps, err := s.db.ListApps(ctx, env.ID)
			if err != nil {
				writeError(w, r, err)
				return
			}
			for _, app := range apps {
				one, err := s.exportApp(r, app, env)
				if err != nil {
					writeError(w, r, err)
					return
				}
				section.Apps = append(section.Apps, one)
			}
			entry.Environments = append(entry.Environments, section)
		}
		out.Projects = append(out.Projects, entry)
	}

	s.audit(r, teamID, "team.exported", "team", teamID, team.Name)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) exportApp(r *http.Request, app store.App, env store.Environment) (exportApp, error) {
	ctx := r.Context()
	out := exportApp{App: app}

	domains, err := s.db.ListDomains(ctx, app.ID)
	if err != nil {
		return exportApp{}, err
	}
	out.Domains = domains

	rows, err := s.db.ListVariables(ctx, app.ID)
	if err != nil {
		return exportApp{}, err
	}
	for _, row := range rows {
		out.Variables = append(out.Variables,
			s.exportVariable(row.Variable, row.Sealed, variableContext(app.ID, row.Key)))
	}

	volumes, err := s.db.ListVolumes(ctx, app.ID)
	if err != nil {
		return exportApp{}, err
	}
	out.Volumes = volumes

	jobs, err := s.db.ListAppJobs(ctx, app.ID)
	if err != nil {
		return exportApp{}, err
	}
	out.Schedules = jobs

	out.Manifests, out.ManifestError = s.renderManifests(ctx, app, env)
	return out, nil
}

// renderManifests returns an app's Kubernetes objects, or the reason there are
// none — as a string rather than an error, because neither answer should stop
// the rest of the export.
//
// An export is worth having on a panel whose cluster is unreachable. That is
// arguably when somebody wants one most.
func (s *Server) renderManifests(ctx context.Context, app store.App, env store.Environment) (string, string) {
	if s.cluster == nil {
		return "", "This panel has no cluster configured, so the Kubernetes objects could not be rendered."
	}
	manifests, err := s.cluster.Manifests(ctx, app, env)
	if err != nil {
		return "", err.Error()
	}
	return manifests, ""
}

// exportVariable copies a variable, opening it only when it is not a secret.
func (s *Server) exportVariable(v store.Variable, sealed, context string) store.Variable {
	if v.IsSecret {
		v.Value = ""
		return v
	}
	if plaintext, err := s.keyring.Open(sealed, context); err == nil {
		v.Value = string(plaintext)
	}
	return v
}
