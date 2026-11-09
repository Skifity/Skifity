package api

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"skifity/internal/crypto"
	"skifity/internal/dbsvc/engine"
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

// What installing a template makes is worked out before any of it is.
//
// An install used to make each service's app and then each database, and stop
// at the first error with whatever it had made left behind. Installing
// WordPress a second time, named "shop", made the app shop and then failed on
// the database name the template fixes, with an error whose impact said
// nothing was created — beside an app with no database. A public port taken by
// a later service left the earlier apps the same way, and every app had been
// announced as created before the install could still fail.
//
// So a plan comes first: every name the install takes, every port it opens,
// every input it needs, and everything that stands in the way, with nothing
// written. A plan with a problem is answered with it. A dry run answers the
// plan itself, which is how the install dialog says a name is taken before
// Install is pressed. What can only fail while writing — the cluster refusing
// a database — removes what the install made, in reverse, and only an install
// that made everything is announced and deployed.

// templatePlan is one install, worked out.
type templatePlan struct {
	Apps      []plannedTemplateApp      `json:"apps"`
	Databases []plannedTemplateDatabase `json:"databases"`
	Volumes   []plannedTemplateVolume   `json:"volumes"`
	// Problems are everything that stops the install, each as the install
	// would answer it. An install with any writes nothing.
	Problems []*errdoc.Problem `json:"problems"`

	env         store.Environment
	tpl         templates.Template
	catalogueID string
	// values are the inputs with the generated and defaulted ones filled in.
	// Never answered: a generated secret is set on the apps and shown nowhere.
	values map[string]string
}

// plannedTemplateApp is an app an install would make, one per service.
type plannedTemplateApp struct {
	// Service is the template's own name for it, which its databases link by.
	Service string `json:"service"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Image   string `json:"image"`
	Public  bool   `json:"public"`
}

// plannedTemplateDatabase is a database an install would make.
type plannedTemplateDatabase struct {
	Name   string `json:"name"`
	Engine string `json:"engine"`
	// StorageGB is the disk it would get, which is what the engine asks for
	// at least and nothing for a cache.
	StorageGB int `json:"storage_gb"`
	// LinkTo names the apps it would be linked to, as planned.
	LinkTo []string `json:"link_to"`
}

// plannedTemplateVolume is a disk an install would give one of its apps.
type plannedTemplateVolume struct {
	App       string `json:"app"`
	Name      string `json:"name"`
	MountPath string `json:"mount_path"`
	SizeGB    int    `json:"size_gb"`
}

// planTemplateInstall works out what installing tpl into env would make, and
// what stops it. It writes nothing; it does read, so a name or a port taken
// since a dry run is still found by the install itself.
//
// catalogueID is the team catalogue the template came from, "" for the
// built-in one, and is remembered with each app so an update is looked for in
// the same place.
func (s *Server) planTemplateInstall(ctx context.Context, env store.Environment, tpl templates.Template,
	catalogueID, nameOverride string, values map[string]string) (templatePlan, error) {
	plan := templatePlan{
		Apps: []plannedTemplateApp{}, Databases: []plannedTemplateDatabase{}, Volumes: []plannedTemplateVolume{},
		Problems: []*errdoc.Problem{}, env: env, tpl: tpl, catalogueID: catalogueID, values: map[string]string{},
	}
	// A refusal is a problem of the plan; anything else is the panel's own
	// failure and stops planning.
	refuse := func(err error) error {
		var problem *errdoc.Problem
		if errors.As(err, &problem) {
			plan.Problems = append(plan.Problems, problem)
			return nil
		}
		return err
	}

	maps.Copy(plan.values, values)
	for _, input := range tpl.Inputs {
		if plan.values[input.Key] != "" {
			continue
		}
		switch {
		case input.Generate:
			generated, err := crypto.RandomToken(24)
			if err != nil {
				return plan, err
			}
			plan.values[input.Key] = generated
		case input.Default != "":
			plan.values[input.Key] = input.Default
		case input.Required:
			plan.Problems = append(plan.Problems, errdoc.TemplateInputRequired(input.Label, tpl.Name))
		}
	}

	// An app and a database in one environment share a namespace, so every
	// name the install takes is held to the same rule, and to each other.
	claimed := map[string]bool{}
	claim := func(slug string) error {
		if claimed[slug] {
			// Validate refuses a database named like a service, and names
			// one install derives are distinct by construction.
			return fmt.Errorf("the %s template would make two things called %s", tpl.ID, slug)
		}
		claimed[slug] = true
		owner, err := s.db.SlugOwnerInEnvironment(ctx, env.ID, slug)
		if err != nil {
			return err
		}
		if owner != "" {
			plan.Problems = append(plan.Problems, errdoc.NameTaken(owner, slug))
		}
		return nil
	}

	nameOverride = strings.TrimSpace(nameOverride)
	opened := map[string]bool{}
	for _, svc := range tpl.Services {
		name := svc.Name
		if nameOverride != "" && len(tpl.Services) == 1 {
			name = nameOverride
		}
		app := plannedTemplateApp{Service: svc.Name, Name: name, Slug: kube.Slugify(name), Image: svc.Image, Public: svc.Public}
		if err := claim(app.Slug); err != nil {
			return plan, err
		}
		// A catalogue a team keeps is its own, and its images are whatever it
		// wrote. One that names an image this panel built for another
		// environment is held to the rule a single app is held to.
		if err := refuse(s.checkImageReference(ctx, svc.Image, env.Namespace)); err != nil {
			return plan, err
		}
		// The same number outside as inside: a Minecraft client dials 25565
		// unless told otherwise. A second install finds it taken and says so,
		// rather than opening something else quietly.
		for _, p := range svc.Ports {
			protocol := protocolOr(p.Protocol)
			key := strconv.Itoa(p.Port) + "/" + protocol
			if opened[key] {
				plan.Problems = append(plan.Problems, errdoc.PortTaken(p.Port, protocol))
				continue
			}
			opened[key] = true
			if _, err := s.db.PortHolder(ctx, p.Port, protocol); err == nil {
				plan.Problems = append(plan.Problems, errdoc.PortTaken(p.Port, protocol))
			} else if !errors.Is(err, store.ErrNotFound) {
				return plan, err
			}
		}
		for _, vol := range svc.Volumes {
			plan.Volumes = append(plan.Volumes, plannedTemplateVolume{
				App: app.Name, Name: kube.Slugify(vol.Name), MountPath: vol.MountPath, SizeGB: orDefault(vol.SizeGB, 1),
			})
		}
		plan.Apps = append(plan.Apps, app)
	}

	if len(tpl.Databases) > 0 && s.databases == nil {
		plan.Problems = append(plan.Problems, errdoc.NotConfigured("Managed databases", "the panel's cluster connection"))
	}
	// A template of one app installed under a name of its own names its
	// databases after that app. The template fixes the names of its
	// databases, so a second copy beside the first had a name of its own for
	// the app and the first copy's name for its database, and failed on it.
	renamed := ""
	if len(plan.Apps) == 1 && plan.Apps[0].Slug != tpl.Services[0].Name {
		renamed = plan.Apps[0].Slug
	}
	for _, spec := range tpl.Databases {
		database := plannedTemplateDatabase{
			Name: spec.Name, Engine: spec.Engine, StorageGB: plannedStorage(spec), LinkTo: []string{},
		}
		if renamed != "" {
			database.Name = renamedDatabase(renamed, spec.Name, tpl.Databases)
		}
		if err := claim(kube.Slugify(database.Name)); err != nil {
			return plan, err
		}
		// A link that names no service is the one mistake in a template that
		// looks like success: the database is created, the link is skipped,
		// and the app comes up without the variable it cannot run without.
		// Validate refuses one; this is the install not taking that on trust.
		if len(spec.LinkTo) == 0 {
			return plan, fmt.Errorf("the %s template creates the database %s and links it to nothing", tpl.ID, spec.Name)
		}
		for _, target := range spec.LinkTo {
			index := slices.IndexFunc(plan.Apps, func(app plannedTemplateApp) bool { return app.Service == target })
			if index < 0 {
				return plan, fmt.Errorf("the %s template links the database %s to a service called %q, which it does not have",
					tpl.ID, spec.Name, target)
			}
			database.LinkTo = append(database.LinkTo, plan.Apps[index].Name)
		}
		plan.Databases = append(plan.Databases, database)
	}
	return plan, nil
}

// renamedDatabase is what one of a template's databases is called when its one
// app is installed as app: the app's name and the last word of the database's
// own — shop-db for wordpress-db, shop-redis for outline-redis — or the app's
// name and the whole of it, when another database of the template ends in the
// same word.
func renamedDatabase(app, name string, all []templates.DatabaseSpec) string {
	word := name[strings.LastIndex(name, "-")+1:]
	for _, other := range all {
		if other.Name != name && other.Name[strings.LastIndex(other.Name, "-")+1:] == word {
			return app + "-" + name
		}
	}
	return app + "-" + word
}

// plannedStorage is the disk a template's database would get: what the
// manager gives it, which is at least what its engine asks for, and nothing
// for a cache.
func plannedStorage(spec templates.DatabaseSpec) int {
	requested := orDefault(spec.StorageGB, 5)
	kind, known := engine.Lookup(spec.Engine)
	if !known {
		return requested
	}
	if !kind.Storage {
		return 0
	}
	return max(requested, kind.StorageGB, 5)
}

// installTemplate makes what a plan says, and answers its first problem
// instead when it has one.
//
// Apps come first, with their variables, files, ports and disks, then the
// databases, linked to them. Only once all of it exists are the apps
// announced, the install recorded and the deploys started — the deploy is
// what the databases come first for, so the connection string exists by the
// time the app looks for it. A failure on the way removes what was made.
func (s *Server) installTemplate(r *http.Request, plan templatePlan, user store.User) (installedTemplate, error) {
	if len(plan.Problems) > 0 {
		return installedTemplate{}, plan.Problems[0]
	}
	made := installedTemplate{Notes: plan.tpl.Notes, Apps: []store.App{}, Databases: []store.Database{}}
	if err := s.makeTemplate(r, plan, &made); err != nil {
		return installedTemplate{}, s.undoTemplate(context.WithoutCancel(r.Context()), plan.tpl, made, err)
	}

	// As one made by hand or from a Compose file is: the activity log names
	// it, plugins hear of it, and a page open elsewhere refreshes.
	teamID, _ := s.db.TeamIDForEnvironment(r.Context(), plan.env.ID)
	for _, app := range made.Apps {
		s.announceAppCreated(r, teamID, app)
	}
	if s.deployer != nil {
		for _, app := range made.Apps {
			if _, err := s.deployer.Deploy(r.Context(), DeployRequest{
				AppID: app.ID, Trigger: "template", CreatedBy: user.ID,
			}); err != nil {
				s.log.Warn("could not deploy a template service", "app", app.ID, "error", err)
			}
		}
	}
	return made, nil
}

// makeTemplate writes what a plan says. made holds what exists so far, so a
// failure can be undone.
func (s *Server) makeTemplate(r *http.Request, plan templatePlan, made *installedTemplate) error {
	ctx, tpl, env := r.Context(), plan.tpl, plan.env
	// Map service name to the created app, so links can be made afterwards.
	created := map[string]store.App{}
	// What the cluster's servers offer, asked once and only when a service
	// could use a GPU.
	var gpus []gpuVendorView
	gpusRead := false

	for i, svc := range tpl.Services {
		planned := plan.Apps[i]
		app := store.App{
			EnvironmentID: env.ID,
			Name:          planned.Name,
			Slug:          planned.Slug,
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
		if err := s.db.CreateApp(ctx, &app); err != nil {
			return err
		}
		made.Apps = append(made.Apps, app)
		created[svc.Name] = app
		// Remembered, so a newer version of the template can be offered.
		if err := s.db.RecordAppTemplate(ctx, store.AppTemplate{
			AppID: app.ID, TemplateID: tpl.ID, CatalogueID: plan.catalogueID, Service: svc.Name, InstalledImage: svc.Image,
		}); err != nil {
			return err
		}

		if svc.GPU != nil {
			if !gpusRead {
				gpus, gpusRead = s.clusterGPUsNow(r), true
			}
			vendor, count := svc.GPU.Wants()
			if err := gpuFits(store.AppGPU{Count: count, Vendor: vendor}, gpus); gpus == nil || err != nil {
				made.WithoutGPU = append(made.WithoutGPU, svc.Name)
			} else if err := s.db.SetAppGPU(ctx, store.AppGPU{AppID: app.ID, Count: count, Vendor: vendor}); err != nil {
				return err
			}
		}

		for key, value := range svc.Variables {
			if err := s.setTemplateVariable(r, app.ID, key, value, false); err != nil {
				return err
			}
		}
		// Template inputs go to every service, because a template's services
		// share the secrets that tie them together.
		for _, input := range tpl.Inputs {
			if value := plan.values[input.Key]; value != "" {
				if err := s.setTemplateVariable(r, app.ID, input.Key, value, input.Secret); err != nil {
					return err
				}
			}
		}
		for _, file := range svc.Files {
			if err := s.setTemplateFile(r, app.ID, file); err != nil {
				return err
			}
		}
		for _, p := range svc.Ports {
			protocol := protocolOr(p.Protocol)
			port := store.AppPort{AppID: app.ID, Port: p.Port, Protocol: protocol, PublicPort: p.Port}
			if err := s.db.AddPort(ctx, &port); err != nil {
				// Free when planned, and taken since.
				if errors.Is(err, store.ErrPortTaken) {
					return errdoc.PortTaken(p.Port, protocol)
				}
				return err
			}
		}
		for _, vol := range svc.Volumes {
			volume := store.Volume{
				AppID: app.ID, Name: kube.Slugify(vol.Name),
				MountPath: vol.MountPath, SizeGB: orDefault(vol.SizeGB, 1),
			}
			if err := s.db.CreateVolume(ctx, &volume); err != nil {
				return err
			}
		}
	}

	for i, spec := range tpl.Databases {
		record, err := s.databases.Create(ctx, env, CreateDatabaseRequest{
			Name: plan.Databases[i].Name, Engine: spec.Engine, StorageGB: orDefault(spec.StorageGB, 5), Instances: 1,
		})
		if err != nil {
			return err
		}
		made.Databases = append(made.Databases, record)

		varName := spec.VarName
		if varName == "" {
			varName = defaultVarNameFor(spec.Engine)
		}
		for _, target := range spec.LinkTo {
			if err := s.databases.Link(ctx, record.ID, created[target].ID, varName); err != nil {
				return err
			}
		}
		if err := s.setDatabasePieces(r, record.ID, spec, created); err != nil {
			return err
		}
	}
	return nil
}

// undoTemplate removes what a failed install made — the databases first,
// because they were made last — and says what became of it. Deleting an app
// takes its variables, files, ports, disks and the record of its template
// with it; deleting a database takes its links.
//
// Nothing it removes was deployed, so the panel's records and the databases'
// objects in the cluster are all there is.
func (s *Server) undoTemplate(ctx context.Context, tpl templates.Template, made installedTemplate, cause error) error {
	if len(made.Apps) == 0 && len(made.Databases) == 0 {
		// Nothing was made, so the failure is the whole answer.
		return cause
	}
	var removed, remains []string
	for i := len(made.Databases) - 1; i >= 0; i-- {
		database := made.Databases[i]
		if err := s.databases.Delete(ctx, database.ID); err != nil {
			s.log.Warn("could not remove a database a failed install made", "database", database.ID, "error", err)
			remains = append(remains, database.Name)
			continue
		}
		removed = append(removed, database.Name)
	}
	for i := len(made.Apps) - 1; i >= 0; i-- {
		app := made.Apps[i]
		if err := s.db.DeleteApp(ctx, app.ID); err != nil {
			s.log.Warn("could not remove an app a failed install made", "app", app.ID, "error", err)
			remains = append(remains, app.Name)
			continue
		}
		removed = append(removed, app.Name)
	}
	// Named in the order they were made, which is the order the install
	// dialog listed them in.
	slices.Reverse(removed)
	slices.Reverse(remains)
	if len(remains) > 0 {
		return errdoc.TemplateInstallLeftOver(tpl.Name, cause, remains)
	}
	return errdoc.TemplateInstallRolledBack(tpl.Name, cause, removed)
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

// protocolOr is a template port's protocol, tcp when it names none.
func protocolOr(protocol string) string {
	if protocol == "" {
		return "tcp"
	}
	return protocol
}

func orDefault(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}
