package api

import (
	"fmt"
	"net/http"

	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/store"
	"skifity/internal/templates"
)

// installedTemplate is what installing a template produced.
type installedTemplate struct {
	Apps      []store.App      `json:"apps"`
	Databases []store.Database `json:"databases"`
	Notes     string           `json:"notes,omitempty"`
}

// installTemplate turns a template into ordinary Skifity resources.
//
// Databases come first so their connection strings exist before the services
// that need them start, which is the difference between a template that works
// on the first try and one that crash-loops until someone redeploys it.
func (s *Server) installTemplate(
	r *http.Request, tpl templates.Template, env store.Environment,
	user store.User, nameOverride string, values map[string]string,
) (installedTemplate, error) {
	result := installedTemplate{Notes: tpl.Notes}

	for _, input := range tpl.Inputs {
		if values[input.Key] != "" {
			continue
		}
		if input.Generate {
			generated, err := crypto.RandomToken(24)
			if err != nil {
				return result, err
			}
			if values == nil {
				values = map[string]string{}
			}
			values[input.Key] = generated
			continue
		}
		if input.Default != "" {
			if values == nil {
				values = map[string]string{}
			}
			values[input.Key] = input.Default
			continue
		}
		if input.Required {
			return result, errdoc.BadRequest(fmt.Sprintf("%s is needed to install %s.", input.Label, tpl.Name))
		}
	}

	// Map service name to the created app, so links can be made afterwards.
	created := map[string]store.App{}

	for _, svc := range tpl.Services {
		name := svc.Name
		if nameOverride != "" && len(tpl.Services) == 1 {
			name = nameOverride
		}
		app := store.App{
			EnvironmentID: env.ID,
			Name:          name,
			Slug:          kube.Slugify(name),
			SourceType:    "image",
			Image:         svc.Image,
			Port:          svc.Port,
			HealthPath:    svc.HealthPath,
			Replicas:      1,
			MinReplicas:   1,
			MaxReplicas:   3,
			CPUTarget:     75,
			CPURequestM:   orDefault(svc.CPURequestM, 50),
			CPULimitM:     orDefault(svc.CPULimitM, 1000),
			MemRequestMB:  orDefault(svc.MemRequestMB, 128),
			MemLimitMB:    orDefault(svc.MemLimitMB, 512),
			AutoDeploy:    false,
			Status:        "created",
		}
		if err := s.db.CreateApp(r.Context(), &app); err != nil {
			return result, err
		}
		created[svc.Name] = app
		result.Apps = append(result.Apps, app)

		for key, value := range svc.Variables {
			if err := s.setTemplateVariable(r, app.ID, key, value, false); err != nil {
				return result, err
			}
		}
		// Template inputs go to every service, because a template's services
		// share the secrets that tie them together.
		for _, input := range tpl.Inputs {
			if value := values[input.Key]; value != "" {
				if err := s.setTemplateVariable(r, app.ID, input.Key, value, input.Secret); err != nil {
					return result, err
				}
			}
		}
		for _, vol := range svc.Volumes {
			volume := store.Volume{
				AppID: app.ID, Name: kube.Slugify(vol.Name),
				MountPath: vol.MountPath, SizeGB: orDefault(vol.SizeGB, 1),
			}
			if err := s.db.CreateVolume(r.Context(), &volume); err != nil {
				return result, err
			}
		}
	}

	for _, spec := range tpl.Databases {
		if s.databases == nil {
			return result, errdoc.NotConfigured("Managed databases", "the panel's cluster connection")
		}
		record, err := s.databases.Create(r.Context(), env, CreateDatabaseRequest{
			Name: spec.Name, Engine: spec.Engine, StorageGB: orDefault(spec.StorageGB, 5), Instances: 1,
		})
		if err != nil {
			return result, err
		}
		result.Databases = append(result.Databases, record)

		varName := spec.VarName
		if varName == "" {
			varName = defaultVarNameFor(spec.Engine)
		}
		// A link that names no service is the one mistake in a template that
		// looks like success: the database is created, the link is skipped,
		// and the app comes up without the variable it cannot run without.
		// What the user sees is a crash loop and no reason for it.
		if len(spec.LinkTo) == 0 {
			return result, fmt.Errorf(
				"the %s template creates the database %s and links it to nothing",
				tpl.ID, spec.Name)
		}
		for _, target := range spec.LinkTo {
			app, ok := created[target]
			if !ok {
				return result, fmt.Errorf(
					"the %s template links the database %s to a service called %q, which it does not have",
					tpl.ID, spec.Name, target)
			}
			if err := s.databases.Link(r.Context(), record.ID, app.ID, varName); err != nil {
				return result, err
			}
		}
	}

	// Deploy only once everything is wired up.
	if s.deployer != nil {
		for _, app := range result.Apps {
			if _, err := s.deployer.Deploy(r.Context(), DeployRequest{
				AppID: app.ID, Trigger: "template", CreatedBy: user.ID,
			}); err != nil {
				s.log.Warn("could not deploy a template service", "app", app.ID, "error", err)
			}
		}
	}
	return result, nil
}

func (s *Server) setTemplateVariable(r *http.Request, appID, key, value string, secret bool) error {
	cleanKey, err := kube.SanitiseEnvKey(key)
	if err != nil {
		return errdoc.BadRequest(err.Error())
	}
	sealed, err := s.keyring.Seal([]byte(value), variableContext(appID, cleanKey))
	if err != nil {
		return err
	}
	variable := store.Variable{AppID: appID, Key: cleanKey, IsSecret: secret}
	return s.db.SetVariable(r.Context(), &variable, sealed)
}

func orDefault(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}
