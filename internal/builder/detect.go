// Package builder turns a repository into a container image.
package builder

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// Builder names the strategy used to produce an image.
type Builder string

const (
	// BuilderDockerfile uses a Dockerfile from the repository.
	BuilderDockerfile Builder = "dockerfile"
	// BuilderRailpack is the zero-config builder, Railway's BuildKit-native
	// successor to Nixpacks.
	BuilderRailpack Builder = "railpack"
	// BuilderNixpacks is kept as a fallback because Railpack is younger.
	BuilderNixpacks Builder = "nixpacks"
	// BuilderCompose converts a Docker Compose file.
	BuilderCompose Builder = "compose"
	// BuilderStatic serves a directory of files behind a tiny web server.
	BuilderStatic Builder = "static"
	// BuilderImage skips building: the app runs a prebuilt image.
	BuilderImage Builder = "image"
)

// Detection is what the panel worked out about a repository.
type Detection struct {
	Builder Builder `json:"builder"`
	// Language is shown in the UI: "Node.js", "Python", "Go".
	Language string `json:"language"`
	// Framework is shown when it changes what happens: "Next.js", "Django".
	Framework string `json:"framework"`
	// Port is the port the app is expected to listen on.
	Port int `json:"port"`
	// StartCommand is filled in only when the repository does not say itself.
	StartCommand string `json:"start_command,omitempty"`
	// HealthPath is a path known to exist for this framework.
	HealthPath string `json:"health_path,omitempty"`
	// DockerfilePath is where the Dockerfile was found.
	DockerfilePath string `json:"dockerfile_path,omitempty"`
	// StaticDir is the directory to serve for a static site.
	StaticDir string `json:"static_dir,omitempty"`
	// BuildCommand produces that directory, for a front end that has to be
	// built before there is anything to serve. Empty when the repository
	// already holds its HTML.
	BuildCommand string `json:"build_command,omitempty"`
	// Confidence is high when a marker file is unambiguous, low when the guess
	// is based on a file extension. The UI shows low-confidence guesses as a
	// question rather than a statement.
	Confidence string `json:"confidence"`
	// Notes explain the decision, which is what makes the guess reviewable.
	Notes []string `json:"notes,omitempty"`
	// Compose are the services read from a Compose file, when the repository
	// has one. A Compose file describes several services and an app runs one,
	// so this is a list to choose from rather than something to build: the form
	// offers each service and fills itself in from the one that is picked.
	Compose []ComposeService `json:"compose,omitempty"`
	// ComposeWarnings are the parts of the Compose file that did not carry
	// over, named rather than dropped quietly.
	ComposeWarnings []string `json:"compose_warnings,omitempty"`
	// Needs are what the app will reach for once it is running: a database,
	// the settings its .env.example lists, and data kept in a file that the
	// next deploy erases. See needs.go.
	Needs []Need `json:"needs,omitempty"`
}

// Tree is the subset of a repository the detector needs: the list of paths, and
// the contents of the few files that matter.
type Tree struct {
	// Files are repository-relative paths.
	Files []string
	// Contents holds the files the detector reads, keyed by path.
	Contents map[string]string
}

// Has reports whether a path exists in the tree.
func (t Tree) Has(name string) bool {
	for _, f := range t.Files {
		if f == name {
			return true
		}
	}
	return false
}

// HasAny reports whether any of the paths exists.
func (t Tree) HasAny(names ...string) bool {
	for _, name := range names {
		if t.Has(name) {
			return true
		}
	}
	return false
}

// HasExtension reports whether any file has the given extension, ignoring
// vendored and dependency directories.
func (t Tree) HasExtension(ext string) bool {
	for _, f := range t.Files {
		if skipPath(f) {
			continue
		}
		if strings.HasSuffix(f, ext) {
			return true
		}
	}
	return false
}

func skipPath(p string) bool {
	for _, prefix := range []string{"node_modules/", "vendor/", ".git/", "dist/", "build/", ".venv/", "venv/"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// Read returns a file's contents, or "" when it was not fetched.
func (t Tree) Read(name string) string { return t.Contents[name] }

// Detect works out how to build a repository, and what it will need once it is
// running.
//
// The second half applies whatever the first decides. A repository with its own
// Dockerfile still reads DATABASE_URL, and the Dockerfile says nothing about it.
func Detect(tree Tree) Detection {
	d := detectBuild(tree)
	d.Needs = DetectNeeds(tree)
	return d
}

// detectBuild works out how to build a repository.
//
// The order is deliberate. An explicit Dockerfile is a decision the repository's
// author already made and must not be overridden. Compose is next, because a
// repository with a Compose file is describing several services. Only then does
// the language detection run.
func detectBuild(tree Tree) Detection {
	if dockerfile := findDockerfile(tree); dockerfile != "" {
		d := Detection{
			Builder: BuilderDockerfile, DockerfilePath: dockerfile,
			Confidence: "high",
			Notes:      []string{"Found " + dockerfile + ", so it is used as-is."},
		}
		d.Port = portFromDockerfile(tree.Read(dockerfile))
		d.Language = "Dockerfile"
		return d
	}

	if compose := findCompose(tree); compose != "" {
		d := Detection{
			Builder: BuilderCompose, Confidence: "high", Language: "Docker Compose",
			DockerfilePath: compose,
			Notes:          []string{"Found " + compose + "."},
		}
		services, warnings, err := ParseCompose(tree.Read(compose))
		switch {
		case err != nil:
			d.Notes = append(d.Notes, "It could not be read: "+err.Error()+".")
		case len(services) == 0:
			d.Notes = append(d.Notes, "No services were found in it.")
		default:
			d.Compose, d.ComposeWarnings = services, warnings
			d.Notes = append(d.Notes,
				"Skifity runs one service per app, so pick the service this app is. "+
					"Create the others the same way, in the same environment, where they "+
					"reach each other by name.")
		}
		return d
	}

	if d, ok := detectNode(tree); ok {
		return d
	}
	if d, ok := detectPython(tree); ok {
		return d
	}
	if d, ok := detectGo(tree); ok {
		return d
	}
	if d, ok := detectPHP(tree); ok {
		return d
	}
	if d, ok := detectRuby(tree); ok {
		return d
	}
	if d, ok := detectRust(tree); ok {
		return d
	}
	if d, ok := detectJava(tree); ok {
		return d
	}
	if d, ok := detectStatic(tree); ok {
		return d
	}

	return Detection{
		Builder: BuilderRailpack, Confidence: "low", Language: "unknown",
		Notes: []string{
			"Skifity could not tell what this repository is written in.",
			"It will try the zero-config builder, which may still work. If it does not, add a Dockerfile.",
		},
	}
}

func findDockerfile(tree Tree) string {
	for _, candidate := range []string{"Dockerfile", "dockerfile", "docker/Dockerfile", "build/Dockerfile"} {
		if tree.Has(candidate) {
			return candidate
		}
	}
	return ""
}

func findCompose(tree Tree) string {
	for _, candidate := range []string{
		"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml",
	} {
		if tree.Has(candidate) {
			return candidate
		}
	}
	return ""
}

// packageJSON is the part of package.json the detector reads.
type packageJSON struct {
	Name            string            `json:"name"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Engines         map[string]string `json:"engines"`
}

func detectNode(tree Tree) (Detection, bool) {
	if !tree.Has("package.json") {
		return Detection{}, false
	}
	d := Detection{Builder: BuilderRailpack, Language: "Node.js", Confidence: "high", Port: 3000}

	var pkg packageJSON
	if raw := tree.Read("package.json"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &pkg); err != nil {
			d.Confidence = "medium"
			d.Notes = append(d.Notes, "package.json could not be parsed, so the defaults are used.")
		}
	}

	deps := map[string]bool{}
	for name := range pkg.Dependencies {
		deps[name] = true
	}
	for name := range pkg.DevDependencies {
		deps[name] = true
	}

	switch {
	case deps["next"]:
		d.Framework, d.Port = "Next.js", 3000
		d.Notes = append(d.Notes, "Next.js listens on port 3000 by default.")
	case deps["nuxt"], deps["nuxt3"]:
		d.Framework, d.Port = "Nuxt", 3000
	case deps["@nestjs/core"]:
		d.Framework, d.Port = "NestJS", 3000
	case deps["@remix-run/node"]:
		d.Framework, d.Port = "Remix", 3000
	case deps["@sveltejs/kit"]:
		d.Framework, d.Port = "SvelteKit", 3000
	case deps["astro"]:
		d.Framework, d.Port = "Astro", 4321
	case deps["fastify"]:
		d.Framework, d.Port = "Fastify", 3000
	case deps["koa"]:
		d.Framework, d.Port = "Koa", 3000
	case deps["express"]:
		d.Framework, d.Port = "Express", 3000
	case deps["vite"] && pkg.Scripts["build"] != "" && !deps["express"]:
		// A Vite app with a build script and no server is a static site, and
		// serving it as one avoids running a dev server in production.
		//
		// It still has to be built. `dist` does not exist in the repository —
		// that is the whole point of a build step — so the build command comes
		// with the directory, and the image is built from what it produces.
		d.Builder, d.Framework, d.Port = BuilderStatic, "Vite", 80
		d.StaticDir, d.BuildCommand = "dist", "npm run build"
		d.Notes = append(d.Notes,
			"This looks like a front-end build: `npm run build` runs and the files it writes to `dist` are served directly, with no Node process in the image.")
	case deps["react-scripts"] && pkg.Scripts["build"] != "":
		// Create React App writes to build/ rather than dist/, and a wrong
		// directory here is an image with nothing in it.
		d.Builder, d.Framework, d.Port = BuilderStatic, "Create React App", 80
		d.StaticDir, d.BuildCommand = "build", "npm run build"
		d.Notes = append(d.Notes,
			"Create React App writes to `build`, which is served directly.")
	case deps["@angular/core"] && pkg.Scripts["build"] != "":
		d.Builder, d.Framework, d.Port = BuilderStatic, "Angular", 80
		d.StaticDir, d.BuildCommand = "dist", "npm run build"
		d.Notes = append(d.Notes,
			"Angular writes to `dist/<project>`; check the directory under Source if the site comes up empty.")
	}

	if start := pkg.Scripts["start"]; start != "" {
		d.Notes = append(d.Notes, "The `start` script from package.json is used to run the app.")
	} else if d.Builder != BuilderStatic {
		d.Confidence = "medium"
		d.Notes = append(d.Notes,
			"package.json has no `start` script, so the builder has to guess how to run this. Adding one makes the deploy predictable.")
	}
	if version := pkg.Engines["node"]; version != "" {
		d.Notes = append(d.Notes, "Node version "+version+" is requested by package.json.")
	}
	return d, true
}

func detectPython(tree Tree) (Detection, bool) {
	if !tree.HasAny("requirements.txt", "pyproject.toml", "Pipfile", "setup.py", "poetry.lock", "uv.lock") {
		return Detection{}, false
	}
	d := Detection{Builder: BuilderRailpack, Language: "Python", Confidence: "high", Port: 8000}

	requirements := strings.ToLower(tree.Read("requirements.txt") + tree.Read("pyproject.toml") + tree.Read("Pipfile"))
	switch {
	case strings.Contains(requirements, "django"):
		d.Framework, d.Port = "Django", 8000
		d.Notes = append(d.Notes,
			"Django needs DJANGO_SETTINGS_MODULE and ALLOWED_HOSTS set, and collectstatic run during the build.")
	case strings.Contains(requirements, "fastapi"):
		d.Framework, d.Port = "FastAPI", 8000
		d.HealthPath = "/docs"
	case strings.Contains(requirements, "flask"):
		d.Framework, d.Port = "Flask", 8000
		d.Notes = append(d.Notes,
			"Flask's built-in server is not meant for production; the builder runs it behind gunicorn.")
	case strings.Contains(requirements, "streamlit"):
		d.Framework, d.Port = "Streamlit", 8501
	}

	if tree.Has("Procfile") {
		d.Notes = append(d.Notes, "The Procfile is used to start the app.")
	}
	return d, true
}

func detectGo(tree Tree) (Detection, bool) {
	if !tree.Has("go.mod") {
		return Detection{}, false
	}
	d := Detection{Builder: BuilderRailpack, Language: "Go", Confidence: "high", Port: 8080}
	d.Notes = append(d.Notes,
		"Go builds to a single binary, so the image will be small.",
		"Read the port from the PORT environment variable rather than hardcoding one.")
	return d, true
}

func detectPHP(tree Tree) (Detection, bool) {
	if !tree.Has("composer.json") && !tree.HasExtension(".php") {
		return Detection{}, false
	}
	d := Detection{Builder: BuilderRailpack, Language: "PHP", Confidence: "high", Port: 80}
	composer := strings.ToLower(tree.Read("composer.json"))
	switch {
	case strings.Contains(composer, "laravel/framework"):
		d.Framework = "Laravel"
		d.StaticDir = "public"
		d.Notes = append(d.Notes,
			"Laravel needs APP_KEY set. Generate one with `php artisan key:generate --show` and add it as a secret.")
	case strings.Contains(composer, "symfony/"):
		d.Framework = "Symfony"
		d.StaticDir = "public"
	case tree.Has("wp-config.php") || tree.Has("wp-load.php"):
		d.Framework = "WordPress"
		d.Notes = append(d.Notes,
			"WordPress writes uploads to disk, so add a volume for wp-content/uploads before scaling past one instance.")
	}
	if !tree.Has("composer.json") {
		d.Confidence = "medium"
		d.Notes = append(d.Notes, "There is no composer.json, so this was detected from the .php files alone.")
	}
	return d, true
}

func detectRuby(tree Tree) (Detection, bool) {
	if !tree.HasAny("Gemfile", "Gemfile.lock") {
		return Detection{}, false
	}
	d := Detection{Builder: BuilderRailpack, Language: "Ruby", Confidence: "high", Port: 3000}
	if strings.Contains(strings.ToLower(tree.Read("Gemfile")), "rails") {
		d.Framework = "Ruby on Rails"
		d.Notes = append(d.Notes,
			"Rails needs SECRET_KEY_BASE set, and RAILS_SERVE_STATIC_FILES=true unless something else serves the assets.")
	}
	return d, true
}

func detectRust(tree Tree) (Detection, bool) {
	if !tree.Has("Cargo.toml") {
		return Detection{}, false
	}
	return Detection{
		Builder: BuilderRailpack, Language: "Rust", Confidence: "high", Port: 8080,
		Notes: []string{"Rust builds take a while the first time; later builds reuse the cache."},
	}, true
}

func detectJava(tree Tree) (Detection, bool) {
	if !tree.HasAny("pom.xml", "build.gradle", "build.gradle.kts") {
		return Detection{}, false
	}
	d := Detection{Builder: BuilderRailpack, Language: "Java", Confidence: "high", Port: 8080}
	if strings.Contains(tree.Read("pom.xml")+tree.Read("build.gradle"), "spring-boot") {
		d.Framework = "Spring Boot"
		d.HealthPath = "/actuator/health"
		d.Notes = append(d.Notes,
			"The JVM needs a memory limit it knows about; the builder sets -XX:MaxRAMPercentage so it respects the container's limit.")
	}
	return d, true
}

func detectStatic(tree Tree) (Detection, bool) {
	if !tree.HasAny("index.html", "public/index.html", "dist/index.html", "site/index.html") {
		return Detection{}, false
	}
	dir := "."
	for _, candidate := range []string{"dist/index.html", "public/index.html", "site/index.html"} {
		if tree.Has(candidate) {
			dir = path.Dir(candidate)
			break
		}
	}
	return Detection{
		Builder: BuilderStatic, Language: "Static site", Confidence: "high",
		Port: 80, StaticDir: dir,
		Notes: []string{"The files are served by a small web server; no application process runs."},
	}, true
}

// portFromDockerfile reads the first EXPOSE line, which is the closest thing a
// Dockerfile has to declaring its port.
func portFromDockerfile(content string) int {
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(strings.ToUpper(line), "EXPOSE ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// EXPOSE 8080/tcp is valid.
		portText, _, _ := strings.Cut(fields[1], "/")
		if port, err := strconv.Atoi(portText); err == nil && port > 0 && port < 65536 {
			return port
		}
	}
	return 0
}

// ComposeService is one service read from a Compose file.
type ComposeService struct {
	Name        string            `json:"name"`
	Image       string            `json:"image,omitempty"`
	Build       string            `json:"build,omitempty"`
	Ports       []int             `json:"ports,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Volumes     []string          `json:"volumes,omitempty"`
	DependsOn   []string          `json:"depends_on,omitempty"`
	// Unsupported lists Compose features that do not carry over, so the
	// conversion is honest about what it dropped.
	Unsupported []string `json:"unsupported,omitempty"`
}

// ParseCompose reads a Compose file and converts it.
//
// Compose YAML is read through the same YAML-to-JSON path Kubernetes manifests
// use, so numbers arrive as float64 and the conversion below sees one shape
// whichever way a port or a variable was written.
func ParseCompose(source string) ([]ComposeService, []string, error) {
	if strings.TrimSpace(source) == "" {
		return nil, nil, errors.New("the file is empty")
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(source), &raw); err != nil {
		return nil, nil, fmt.Errorf("this is not valid YAML: %w", err)
	}
	services, warnings := ConvertCompose(raw)
	return services, warnings, nil
}

// ConvertCompose turns a parsed Compose file into services Skifity can run.
//
// The conversion is deliberately conservative: anything that cannot be
// represented is reported rather than silently dropped, because a Compose file
// that half-works is worse than one that clearly needs attention.
func ConvertCompose(raw map[string]any) ([]ComposeService, []string) {
	var warnings []string

	servicesRaw, ok := raw["services"].(map[string]any)
	if !ok {
		return nil, []string{"This Compose file has no services section."}
	}

	names := make([]string, 0, len(servicesRaw))
	for name := range servicesRaw {
		names = append(names, name)
	}
	sort.Strings(names)

	var services []ComposeService
	for _, name := range names {
		body, ok := servicesRaw[name].(map[string]any)
		if !ok {
			warnings = append(warnings, "The service "+name+" could not be read and was skipped.")
			continue
		}
		service := ComposeService{Name: name, Environment: map[string]string{}}

		if image, ok := body["image"].(string); ok {
			service.Image = image
		}
		if build, ok := body["build"].(string); ok {
			service.Build = build
		} else if buildMap, ok := body["build"].(map[string]any); ok {
			if context, ok := buildMap["context"].(string); ok {
				service.Build = context
			}
		}

		for _, entry := range toSlice(body["ports"]) {
			if port := parseComposePort(entry); port > 0 {
				service.Ports = append(service.Ports, port)
			}
		}
		for key, value := range toStringMap(body["environment"]) {
			service.Environment[key] = value
		}
		for _, entry := range toSlice(body["volumes"]) {
			if text, ok := entry.(string); ok {
				service.Volumes = append(service.Volumes, text)
			}
		}
		for _, entry := range toSlice(body["depends_on"]) {
			if text, ok := entry.(string); ok {
				service.DependsOn = append(service.DependsOn, text)
			}
		}

		// Features with no equivalent. Naming them is the point.
		for key, explanation := range map[string]string{
			"network_mode": "network_mode is ignored: every service gets a cluster address instead.",
			"privileged":   "privileged is not allowed: containers run unprivileged.",
			"cap_add":      "cap_add is not allowed: all capabilities are dropped.",
			"devices":      "devices are not available to containers here.",
			"pid":          "pid namespace sharing is not supported.",
			"extends":      "extends is not resolved; copy the settings into the service.",
		} {
			if _, present := body[key]; present {
				service.Unsupported = append(service.Unsupported, explanation)
				warnings = append(warnings, name+": "+explanation)
			}
		}
		if _, present := body["restart"]; present {
			// Not a warning: Kubernetes restarts everything by default, so the
			// behaviour a user wants is what they get.
			service.Unsupported = append(service.Unsupported,
				"restart is not needed: instances are restarted automatically.")
		}
		sort.Strings(service.Unsupported)
		services = append(services, service)
	}
	return services, warnings
}

func toSlice(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}
	return nil
}

func toStringMap(v any) map[string]string {
	out := map[string]string{}
	switch value := v.(type) {
	case map[string]any:
		for k, item := range value {
			out[k] = toString(item)
		}
	case []any:
		// Compose also allows a list of KEY=value strings.
		for _, item := range value {
			if text, ok := item.(string); ok {
				if key, val, found := strings.Cut(text, "="); found {
					out[key] = val
				}
			}
		}
	}
	return out
}

func toString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case bool:
		return strconv.FormatBool(value)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case int:
		return strconv.Itoa(value)
	default:
		return ""
	}
}

// parseComposePort reads the container port from a Compose ports entry.
//
// The host side is deliberately discarded: in a cluster the ingress decides what
// is published, and honouring a host port would mean opening a port on a node.
func parseComposePort(entry any) int {
	var text string
	switch value := entry.(type) {
	case string:
		text = value
	case float64:
		return int(value)
	case int:
		return value
	case map[string]any:
		if target, ok := value["target"].(float64); ok {
			return int(target)
		}
		return 0
	default:
		return 0
	}

	// Forms: "8080", "8080:80", "127.0.0.1:8080:80", "8080:80/tcp".
	text, _, _ = strings.Cut(text, "/")
	parts := strings.Split(text, ":")
	container := parts[len(parts)-1]
	if port, err := strconv.Atoi(container); err == nil {
		return port
	}
	return 0
}
