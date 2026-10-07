package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

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
	// WithoutGPU names the services that use a GPU when the cluster has one
	// and were installed without, because no server offers one. Each can be
	// given one under its settings once a server does.
	WithoutGPU []string `json:"without_gpu,omitempty"`
}

// installTemplate turns a template into ordinary Skifity resources.
//
// Databases come first so their connection strings exist before the services
// that need them start, which is the difference between a template that works
// on the first try and one that crash-loops until someone redeploys it.
//
// catalogueID is the team catalogue the template came from, "" for the
// built-in one, and is remembered with each app so an update is looked for
// in the same place.
func (s *Server) installTemplate(
	r *http.Request, catalogueID string, tpl templates.Template, env store.Environment,
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
	// What the cluster's servers offer, asked once and only when a service
	// could use a GPU.
	var gpus []gpuVendorView
	gpusRead := false

	for _, svc := range tpl.Services {
		name := svc.Name
		if nameOverride != "" && len(tpl.Services) == 1 {
			name = nameOverride
		}
		// A catalogue a team keeps is its own, and its images are whatever it
		// wrote. One that names an image this panel built for another
		// environment is the same way round the rule a single app is held to.
		if err := s.checkImageReference(r.Context(), svc.Image, env.Namespace); err != nil {
			return result, err
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
			StartCommand:  svc.Command,
			RunAsUser:     svc.RunAsUser,
			// A template says which of its services the public reaches.
			// This was read and never used, so a search index or a worker
			// with a port got a public address like the app in front of it.
			Internal: !svc.Public,
			Status:   "created",
		}
		if err := s.db.CreateApp(r.Context(), &app); err != nil {
			return result, err
		}
		// Remembered, so a newer version of the template can be offered.
		if err := s.db.RecordAppTemplate(r.Context(), store.AppTemplate{
			AppID: app.ID, TemplateID: tpl.ID, CatalogueID: catalogueID, Service: svc.Name, InstalledImage: svc.Image,
		}); err != nil {
			return result, err
		}
		created[svc.Name] = app
		result.Apps = append(result.Apps, app)

		if svc.GPU != nil {
			if !gpusRead {
				gpus, gpusRead = s.clusterGPUsNow(r), true
			}
			vendor, count := svc.GPU.Wants()
			if err := gpuFits(store.AppGPU{Count: count, Vendor: vendor}, gpus); gpus == nil || err != nil {
				result.WithoutGPU = append(result.WithoutGPU, svc.Name)
			} else if err := s.db.SetAppGPU(r.Context(), store.AppGPU{AppID: app.ID, Count: count, Vendor: vendor}); err != nil {
				return result, err
			}
		}

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
		for _, file := range svc.Files {
			if err := s.setTemplateFile(r, app.ID, file); err != nil {
				return result, err
			}
		}
		for _, p := range svc.Ports {
			protocol := p.Protocol
			if protocol == "" {
				protocol = "tcp"
			}
			// The same number outside as inside: a Minecraft client dials
			// 25565 unless told otherwise. A second install finds it taken
			// and says so, rather than opening something else quietly.
			port := store.AppPort{AppID: app.ID, Port: p.Port, Protocol: protocol, PublicPort: p.Port}
			if err := s.db.AddPort(r.Context(), &port); err != nil {
				if errors.Is(err, store.ErrPortTaken) {
					return result, errdoc.PortTaken(p.Port, protocol)
				}
				return result, err
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
		if err := s.setDatabasePieces(r, record.ID, spec, created); err != nil {
			return result, err
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

// setTemplateFile saves one of a template service's files on its app.
func (s *Server) setTemplateFile(r *http.Request, appID string, file templates.FileSpec) error {
	if err := kube.ValidateFilePath(file.Path); err != nil {
		return errdoc.BadRequest(err.Error())
	}
	sealed, err := s.keyring.Seal([]byte(file.Content), store.FileContext(appID, file.Path))
	if err != nil {
		return err
	}
	return s.db.SetFile(r.Context(), &store.AppFile{
		AppID: appID, Path: file.Path, Size: len(file.Content),
		IsSecret: file.Secret, Executable: file.Executable,
	}, sealed)
}

// setDatabasePieces delivers a database's connection in pieces to the
// services it is linked to, for software with no setting for a URL.
//
// They are variables of the app, written once here, where the URL is a link
// the panel keeps: unlinking the database does not remove them. The password
// is a secret like the URL it is part of.
func (s *Server) setDatabasePieces(r *http.Request, databaseID string, spec templates.DatabaseSpec, created map[string]store.App) error {
	pieces := spec.Vars.Pieces()
	if len(pieces) == 0 {
		return nil
	}
	credentials, err := s.databases.Credentials(r.Context(), databaseID)
	if err != nil {
		return err
	}
	values := map[string]string{
		"host": credentials.Host, "port": strconv.Itoa(credentials.Port), "name": credentials.Database,
		"user": credentials.Username, "password": credentials.Password,
	}
	for _, target := range spec.LinkTo {
		app := created[target]
		for name, piece := range pieces {
			if err := s.setTemplateVariable(r, app.ID, name, values[piece], piece == "password"); err != nil {
				return err
			}
		}
	}
	return nil
}

func orDefault(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}
