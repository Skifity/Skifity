package templates

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"

	"skifity/internal/dbsvc/engine"
	"skifity/internal/kube"
)

// What makes a template installable.
//
// These started as the tests in this package, run over the catalogue that
// ships in the binary. A team's own catalogue is read at run time, from a file
// nobody here has seen, and it is installed by the same one click — so the
// same checks decide whether one of its templates can be installed at all.
// They live here, in the package rather than beside its tests, so the tests
// and the loader for a private catalogue run the same code: a check tightened
// for one is tightened for the other, and neither can drift.
//
// Each check answers sentences rather than a boolean, because a template that
// fails is listed with why, and the person who wrote it needs the why.

// Engines are the managed databases Skifity provisions, and so the only ones a
// template may ask for. They come from the catalogue in internal/dbsvc/engine,
// which imports nothing of the panel's; internal/dbsvc itself reaches
// internal/api, which reaches this package, and could not be imported here.
var Engines = func() map[string]bool {
	out := map[string]bool{}
	for _, name := range engine.Names() {
		out[name] = true
	}
	return out
}()

// Parse reads one template, as a file in the catalogue holds it.
//
// Strictly: a field this version does not know is refused rather than
// dropped. `mount_pth` read loosely is a volume mounted nowhere and an app that
// loses its data on the first restart, and nothing would say why.
func Parse(body []byte) (Template, error) {
	var template Template
	if err := yaml.UnmarshalStrict(body, &template); err != nil {
		return Template{}, err
	}
	normalise(&template)
	return template, nil
}

// normalise gives a template's lists a value when the file left them out.
//
// A nil slice marshals as `null`, and the API says these are arrays. 157 of
// the templates in the built-in catalogue have no database, and every one of
// them answered `"databases": null` — which the panel iterated, which threw,
// which meant the Templates page rendered an error boundary instead of the
// catalogue on every install since it grew past the hand-written eight.
// Nothing caught it: the structural tests check the Go value, not the JSON,
// and nothing ever opened the page.
func normalise(template *Template) {
	if template.Databases == nil {
		template.Databases = []DatabaseSpec{}
	}
	if template.Inputs == nil {
		template.Inputs = []Input{}
	}
	if template.Services == nil {
		template.Services = []Service{}
	}
}

// Check is one of the things a template has to get right.
type Check func(Template) []string

// Checks are every check Validate runs, in the order their answers are read.
var Checks = []Check{
	CheckComplete,
	CheckServices,
	CheckWorkers,
	CheckDatabases,
	CheckVersions,
	CheckInputs,
	CheckWiring,
	CheckFiles,
	CheckDatabasePieces,
}

// Validate returns every reason a template cannot be installed, and nothing
// when it can.
func Validate(template Template) []string {
	var problems []string
	for _, check := range Checks {
		problems = append(problems, check(template)...)
	}
	return problems
}

// DuplicateIDs names the ids more than one template in a catalogue answers
// to, each with the names that share it: Lookup returns whichever is first,
// so the others could never be installed.
func DuplicateIDs(catalogue []Template) []string {
	seen := map[string]string{}
	var problems []string
	for _, template := range catalogue {
		if other, clash := seen[template.ID]; clash {
			problems = append(problems, fmt.Sprintf(
				"%s and %s share the id %q, so Lookup returns whichever is first", other, template.Name, template.ID))
			continue
		}
		seen[template.ID] = template.Name
	}
	return problems
}

// CheckComplete asks for an id that is a slug, the words on its card, a link
// out that is https, and something to install.
func CheckComplete(template Template) []string {
	var problems []string
	if template.ID == "" {
		problems = append(problems, fmt.Sprintf("a template has no id: %s", describe(template)))
	} else if kube.Slugify(template.ID) != template.ID {
		problems = append(problems, fmt.Sprintf(
			"%s: the id %q is not already a slug, and it ends up in a URL", template.Name, template.ID))
	}
	for _, field := range []struct{ name, value string }{
		{"name", template.Name}, {"description", template.Description},
		{"category", template.Category}, {"website", template.Website},
	} {
		if strings.TrimSpace(field.value) == "" {
			problems = append(problems, fmt.Sprintf("%s has no %s, and the card shows it", template.ID, field.name))
		}
	}
	if !strings.HasPrefix(template.Website, "https://") {
		problems = append(problems, fmt.Sprintf(
			"%s links to %q; a link out of the panel must be https", template.ID, template.Website))
	}
	if len(template.Services) == 0 {
		problems = append(problems, fmt.Sprintf("%s installs nothing", template.ID))
	}
	return problems
}

// describe is a template that has no id yet, told apart some other way.
func describe(template Template) string {
	if template.Name != "" {
		return template.Name
	}
	return fmt.Sprintf("%d service(s), %d database(s)", len(template.Services), len(template.Databases))
}

// CheckServices asks that every service could run, and that something can be
// opened.
func CheckServices(template Template) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	public := 0
	names := map[string]bool{}
	for _, svc := range template.Services {
		if svc.Name == "" || kube.Slugify(svc.Name) != svc.Name {
			add("%s: the service name %q is not a slug, and it becomes a Kubernetes object", template.ID, svc.Name)
		}
		if names[svc.Name] {
			add("%s: two services are called %q, and the second takes the first's Service over", template.ID, svc.Name)
		}
		names[svc.Name] = true

		// A worker does not listen, and Skifity runs one as an app with no
		// port: no Service, no probes, no ingress. Inventing a port for a
		// Sidekiq gives it a readiness check against something that never
		// answers, and an app that is "starting" forever.
		if svc.Port == 0 {
			if svc.Public {
				add("%s/%s is public and listens on nothing", template.ID, svc.Name)
			}
			if svc.HealthPath != "" {
				add("%s/%s has a health path and no port to check it on", template.ID, svc.Name)
			}
		} else if svc.Port < 1 || svc.Port > 65535 {
			add("%s/%s listens on %d", template.ID, svc.Name, svc.Port)
		}
		// A port that is not HTTP is as much a way in: a game server has
		// nothing else.
		if svc.Public || len(svc.Ports) > 0 {
			public++
		}
		for _, p := range svc.Ports {
			if err := kube.ValidatePublicPort(p.Port, protocolOr(p.Protocol)); err != nil {
				add("%s/%s: %v", template.ID, svc.Name, err)
			}
		}
		// Root is never pinned: 0 is "the image decides", and a uid is only
		// given so the kubelet can see a named user is not root.
		if svc.RunAsUser < 0 || svc.RunAsUser > 1<<31-1 {
			add("%s/%s runs as %d, which is not a uid", template.ID, svc.Name, svc.RunAsUser)
		}
		if svc.GPU != nil {
			vendor, count := svc.GPU.Wants()
			if err := kube.ValidateGPURequest(kube.GPURequest{Count: count, Vendor: vendor}); err != nil || count < 1 {
				add("%s/%s asks for %d GPUs of %q, which is not something to ask for: %v", template.ID, svc.Name, count, vendor, err)
			}
		}
		if svc.HealthPath != "" && !strings.HasPrefix(svc.HealthPath, "/") {
			add("%s/%s has the health path %q, which is not a path", template.ID, svc.Name, svc.HealthPath)
		}
		if svc.MemLimitMB > 0 && svc.MemRequestMB > svc.MemLimitMB {
			add("%s/%s asks for more memory than it is allowed, so it can never be scheduled", template.ID, svc.Name)
		}
		if svc.CPULimitM > 0 && svc.CPURequestM > svc.CPULimitM {
			add("%s/%s asks for more CPU than it is allowed", template.ID, svc.Name)
		}
		for _, key := range sortedKeys(svc.Variables) {
			if _, err := kube.SanitiseEnvKey(key); err != nil {
				add("%s/%s sets %q, which a container cannot carry: %v", template.ID, svc.Name, key, err)
			}
		}
		for _, vol := range svc.Volumes {
			if !strings.HasPrefix(vol.MountPath, "/") {
				add("%s/%s mounts %q, which is not an absolute path", template.ID, svc.Name, vol.MountPath)
			}
			if vol.SizeGB < 0 {
				add("%s/%s asks for %d GB", template.ID, svc.Name, vol.SizeGB)
			}
		}
	}
	if public == 0 {
		add("%s has no public service, so nothing it installs can be opened", template.ID)
	}
	return problems
}

// workerName is a service named for a worker: a queue consumer, a Sidekiq, a
// scheduler — nothing about it answers HTTP.
var workerName = regexp.MustCompile(
	`(^|[-_])(workers?|sidekiq|celery|beat|scheduler|cron|queue|consumer|` +
		`runners?|supervisor|jobs?)([-_]|$)`)

// CheckWorkers refuses a public worker. Giving one a domain produces a
// certificate, an ingress rule and a readiness probe pointed at a port that
// will never open, and the app stays "starting" until somebody reads the
// events. The converter that built the built-in catalogue marked one worker
// public twice before this existed.
func CheckWorkers(template Template) []string {
	var problems []string
	for _, svc := range template.Services {
		if workerName.MatchString(svc.Name) && svc.Public {
			problems = append(problems, fmt.Sprintf(
				"%s/%s is named for a worker and is public; a queue consumer has no page to open",
				template.ID, svc.Name))
		}
	}
	return problems
}

// CheckDatabases holds each database to what it is for. A LinkTo that names
// no service is the worst kind of mistake here, because everything appears to
// work. The database is created, the link is skipped, and the app starts
// without the one variable it cannot run without — and crash-loops with
// nothing on screen saying why.
func CheckDatabases(template Template) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	services := map[string]bool{}
	for _, svc := range template.Services {
		services[svc.Name] = true
	}
	for _, db := range template.Databases {
		if db.Name == "" || kube.Slugify(db.Name) != db.Name {
			add("%s: the database name %q is not a slug", template.ID, db.Name)
		}
		if !Engines[db.Engine] {
			add("%s: %q is not an engine Skifity runs", template.ID, db.Engine)
		}
		if db.StorageGB <= 0 {
			add("%s/%s asks for %d GB of storage", template.ID, db.Name, db.StorageGB)
		}
		if len(db.LinkTo) == 0 {
			add("%s: the database %s is created and linked to nothing", template.ID, db.Name)
		}
		for _, target := range db.LinkTo {
			if !services[target] {
				add("%s: the database %s links to %q, which is not a service in this template",
					template.ID, db.Name, target)
			}
		}
		if db.VarName != "" {
			if _, err := kube.SanitiseEnvKey(db.VarName); err != nil {
				add("%s/%s arrives as %q, which a container cannot carry: %v", template.ID, db.Name, db.VarName, err)
			}
		}
	}
	return problems
}

// floatingTag matches a tag that means "whatever is newest".
//
// `latest` is only the most honest spelling of it. `main`, `main-stable`,
// `16-master`, `release` and `postgresql-edge` all move under the app, and the
// first version of this caught none of them: litellm reached the catalogue on
// `main-stable`, which is a branch with a nicer name.
var floatingTag = regexp.MustCompile(`(^|[-_.])(latest|main|master|stable|edge|nightly|release|dev)$`)

// majorOnlyTag matches a tag that names a major version and nothing else: `1`,
// `v2`, `15`, `5-alpine`, `3-management`.
//
// That is whatever is newest with a fence around it. Upstream moves it on every
// minor and patch release, so two installs a week apart run different software
// and a rollback restores the tag rather than the image that worked, exactly as
// with `latest`; all the fence promises is that the next image is not a new
// major. The catalogue's README once recommended these, and thirty-seven
// services ran on one.
//
// Four digits or more is not a major version. `260919` is a date, and a
// project that numbers its builds that way means each number to name one image.
var majorOnlyTag = regexp.MustCompile(`^v?[0-9]{1,3}(-[a-z][a-z0-9]*)*$`)

// CheckVersions asks every image for a version, and a floating tag is not
// one. Two deploys of the same app run different software, a rollback
// restores a tag rather than the thing that worked, and an upstream release
// arrives on a restart nobody asked for. The product promises rollback, so a
// template has to name what it runs.
func CheckVersions(template Template) []string {
	var problems []string
	for _, svc := range template.Services {
		// A registry host may carry a port, so the tag is after the last
		// colon and only if there is no slash after it.
		colon := strings.LastIndex(svc.Image, ":")
		if colon < 0 || strings.Contains(svc.Image[colon:], "/") {
			problems = append(problems, fmt.Sprintf(
				"%s/%s runs %q with no tag, which means latest", template.ID, svc.Name, svc.Image))
			continue
		}
		tag := svc.Image[colon+1:]
		if floatingTag.MatchString(tag) {
			problems = append(problems, fmt.Sprintf(
				"%s/%s runs %q: %q is whatever is newest, so this app cannot be rolled back",
				template.ID, svc.Name, svc.Image, tag))
		}
		if majorOnlyTag.MatchString(tag) {
			problems = append(problems, fmt.Sprintf(
				"%s/%s runs %q: %q is whatever is newest in that major version; "+
					"name the release inside it that it runs", template.ID, svc.Name, svc.Image, tag))
		}
	}
	return problems
}

// CheckInputs refuses an input nothing fills in. An input that is neither
// required, nor generated, nor defaulted is a field the installer asks for and
// then does nothing about when it is empty.
func CheckInputs(template Template) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	for _, input := range template.Inputs {
		if input.Key == "" || input.Label == "" {
			add("%s has an input with no key or no label: %+v", template.ID, input)
		}
		if _, err := kube.SanitiseEnvKey(input.Key); err != nil {
			add("%s asks for %q, which a container cannot carry: %v", template.ID, input.Key, err)
		}
		if !input.Required && !input.Generate && input.Default == "" {
			add("%s asks for %s and does nothing when it is left empty", template.ID, input.Key)
		}
		if input.Generate && input.Default != "" {
			add("%s/%s is both generated and defaulted; the default wins and the generator never runs",
				template.ID, input.Key)
		}
	}
	return problems
}

var (
	// datastore and address together are a variable that wires a datastore
	// by hand: DB_HOST, REDIS_PORT, MB_DB_URL.
	datastore = regexp.MustCompile(
		`(^|_)(DB|DATABASE|POSTGRES|POSTGRESQL|PG|MYSQL|MARIADB|REDIS|VALKEY|KEYDB|MONGO|MONGODB|` +
			`CACHE|QUEUE|BROKER|AMQP|RABBITMQ|ELASTIC|ELASTICSEARCH|MEILI|CLICKHOUSE)($|_)`)
	address = regexp.MustCompile(`(^|_)(HOST|HOSTNAME|PORT|SERVER|ADDR|ADDRESS|URL|URI|DSN|CONNECTION|CONNECTIONSTRING)$`)

	// urlHost is the host a URL names, whatever the variable that holds it
	// is called.
	urlHost = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://(?:[^@/?#\s]*@)?([^:/?#\s\[\]]*)`)
)

// CheckWiring refuses a database wired by hand. A Compose file wires services
// together by service name — DB_HOST=mariadb, REDIS_HOST=redis — and a
// template converted from one carries those over unless something stops it. In Skifity there is no sibling
// container to point at: the database is a managed one and arrives as a URL
// through the link. An app given the old variables starts, fails to resolve a
// hostname nobody recognises, and crash-loops.
//
// This caught bookstack, glpi, metabase, redmine and keycloak on the first
// import, which is a fifth of the templates that bring a database.
//
// A URL names a host whatever the variable that holds it is called.
// PAPERLESS_REDIS=redis://redis:6379 went past the check on names, because
// PAPERLESS_REDIS ends in neither HOST nor URL, and gave Paperless a Redis the
// template never installed. A host with no dot in it is either the container
// itself (localhost) or something in the same environment, reached by name: a
// service of this template, or one of its managed databases. Anything else —
// including grampsweb_redis, which no resolver would even look up — is a
// container that is not there.
func CheckWiring(template Template) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	reachable := map[string]bool{"localhost": true}
	services := map[string]bool{}
	for _, svc := range template.Services {
		reachable[svc.Name] = true
		services[svc.Name] = true
	}
	// A datastore that is a service of the template — a MongoDB, which
	// Skifity does not manage — is reached by name like any sibling, and the
	// address of one is not wiring a managed database by hand.
	pointsAtService := func(value string) bool {
		for _, match := range urlHost.FindAllStringSubmatch(value, -1) {
			if services[strings.ToLower(match[1])] {
				return true
			}
		}
		host, _, _ := strings.Cut(value, ":")
		return services[host]
	}
	for _, db := range template.Databases {
		reachable[db.Name] = true
	}
	for _, svc := range template.Services {
		for _, key := range sortedKeys(svc.Variables) {
			value := svc.Variables[key]
			for _, match := range urlHost.FindAllStringSubmatch(value, -1) {
				host := strings.ToLower(match[1])
				if host == "" || strings.Contains(host, ".") || reachable[host] {
					continue
				}
				add("%s/%s sets %s=%q; %q is neither a service in this template nor one of its "+
					"databases, so this points at a container that does not exist",
					template.ID, svc.Name, key, value, host)
			}
			upper := strings.ToUpper(key)
			if datastore.MatchString(upper) && address.MatchString(upper) && !pointsAtService(value) {
				add("%s/%s sets %s=%q; Skifity injects a connection string instead, "+
					"and this points at a container that does not exist",
					template.ID, svc.Name, key, value)
			}
			// A value the source file expected a shell to expand is not a
			// value; it reaches the container as the literal text.
			if strings.Contains(value, "$") {
				add("%s/%s sets %s=%q, which was never expanded", template.ID, svc.Name, key, value)
			}
		}
	}
	return problems
}

// CheckFiles asks that every file a service asks for can be mounted where it
// asks.
func CheckFiles(template Template) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	for _, svc := range template.Services {
		total, seen := 0, map[string]bool{}
		for _, file := range svc.Files {
			if err := kube.ValidateFilePath(file.Path); err != nil {
				add("%s/%s: %v", template.ID, svc.Name, err)
			}
			if seen[file.Path] {
				add("%s/%s has two files at %s", template.ID, svc.Name, file.Path)
			}
			seen[file.Path] = true
			for _, v := range svc.Volumes {
				if v.MountPath == file.Path {
					add("%s/%s puts a file where the volume %s is mounted", template.ID, svc.Name, v.Name)
				}
			}
			if len(file.Content) > kube.MaxFileBytes {
				add("%s/%s: %s is over %d bytes", template.ID, svc.Name, file.Path, kube.MaxFileBytes)
			}
			total += len(file.Content)
		}
		if total > kube.MaxAllFilesBytes || len(svc.Files) > kube.MaxFiles {
			add("%s/%s has more files than an app can hold", template.ID, svc.Name)
		}
	}
	return problems
}

// CheckDatabasePieces holds a database's pieces to their names. They arrive
// as variables, so each needs a name a container can carry, and two pieces
// under one name would leave one of them missing.
func CheckDatabasePieces(template Template) []string {
	var problems []string
	for _, db := range template.Databases {
		names := map[string]bool{db.VarName: db.VarName != ""}
		for _, name := range []string{db.Vars.Host, db.Vars.Port, db.Vars.Name, db.Vars.User, db.Vars.Password} {
			if name == "" {
				continue
			}
			if clean, err := kube.SanitiseEnvKey(name); err != nil || clean != name {
				problems = append(problems, fmt.Sprintf(
					"%s/%s: %q is not a variable name a container carries", template.ID, db.Name, name))
			}
			if names[name] {
				problems = append(problems, fmt.Sprintf("%s/%s delivers two things as %s", template.ID, db.Name, name))
			}
			names[name] = true
		}
	}
	return problems
}

func protocolOr(protocol string) string {
	if protocol == "" {
		return "tcp"
	}
	return protocol
}

// sortedKeys walks a map in one order, so a template with two problems lists
// them the same way every time it is read.
func sortedKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}
