package builder

import (
	"strings"
	"testing"
)

func tree(files map[string]string) Tree {
	t := Tree{Contents: map[string]string{}}
	for name, content := range files {
		t.Files = append(t.Files, name)
		if content != "" {
			t.Contents[name] = content
		}
	}
	return t
}

func TestDockerfileWins(t *testing.T) {
	// A repository that ships a Dockerfile has already made this decision.
	// Overriding it is the fastest way to break someone's deploy.
	d := Detect(tree(map[string]string{
		"Dockerfile":   "FROM node:22\nEXPOSE 4000\nCMD [\"node\", \"server.js\"]",
		"package.json": `{"dependencies":{"next":"15.0.0"}}`,
	}))
	if d.Builder != BuilderDockerfile {
		t.Fatalf("builder is %q, want dockerfile even though package.json is present", d.Builder)
	}
	if d.Port != 4000 {
		t.Fatalf("the port from EXPOSE was not used: %d", d.Port)
	}
	if d.Confidence != "high" {
		t.Fatalf("confidence is %q", d.Confidence)
	}
}

func TestDockerfileExposeParsing(t *testing.T) {
	cases := map[string]int{
		"FROM alpine\nEXPOSE 8080":       8080,
		"FROM alpine\nEXPOSE 8080/tcp":   8080,
		"FROM alpine\nexpose 3000":       3000,
		"FROM alpine\n  EXPOSE   9000  ": 9000,
		"FROM alpine\nEXPOSE 8080 8081":  8080,
		"FROM alpine":                    0,
		"FROM alpine\nEXPOSE notaport":   0,
		"FROM alpine\nEXPOSE 99999":      0,
		"FROM alpine\n# EXPOSE 1234":     0,
	}
	for content, want := range cases {
		if got := portFromDockerfile(content); got != want {
			t.Errorf("portFromDockerfile(%q) = %d, want %d", content, got, want)
		}
	}
}

func TestComposeBeatsLanguageDetection(t *testing.T) {
	d := Detect(tree(map[string]string{
		"compose.yaml": "services:\n  web:\n    build: .",
		"package.json": `{}`,
	}))
	if d.Builder != BuilderCompose {
		t.Fatalf("builder is %q, want compose", d.Builder)
	}
}

// TestComposeDetectionCarriesTheServices: the detector said "Docker Compose"
// and stopped, while the converter that turns the file into services was
// written, tested and called by nothing. What the panel showed was a label; the
// services the person actually has to create were never read.
func TestComposeDetectionCarriesTheServices(t *testing.T) {
	d := Detect(tree(map[string]string{
		"docker-compose.yml": `services:
  web:
    build: .
    ports: ["8080:3000"]
    environment:
      NODE_ENV: production
    depends_on: [db]
  db:
    image: postgres:16
    privileged: true
`,
	}))
	if len(d.Compose) != 2 {
		t.Fatalf("the detector found %d services, want 2 (web and db)", len(d.Compose))
	}
	web := d.Compose[1]
	if web.Name != "web" {
		t.Fatalf("services are not in a stable order: %q came second", web.Name)
	}
	if len(web.Ports) != 1 || web.Ports[0] != 3000 {
		t.Errorf("web listens on %v, want the container side 3000", web.Ports)
	}
	if web.Environment["NODE_ENV"] != "production" {
		t.Errorf("web lost its environment: %v", web.Environment)
	}
	if len(web.DependsOn) != 1 || web.DependsOn[0] != "db" {
		t.Errorf("web lost what it depends on: %v", web.DependsOn)
	}
	if d.Compose[0].Image != "postgres:16" {
		t.Errorf("db runs %q, want postgres:16", d.Compose[0].Image)
	}
	// What cannot be carried over has to be said, not dropped.
	if len(d.ComposeWarnings) == 0 {
		t.Error("privileged: true was dropped without a word")
	}
	// And the note must not promise that any of this happens by itself.
	for _, note := range d.Notes {
		if strings.Contains(note, "become separate apps") {
			t.Errorf("the note still promises an import that does not exist: %q", note)
		}
	}
}

// A Compose file the panel cannot read is still a Compose repository, and
// saying so beats guessing the language from whatever else is lying around.
func TestAnUnreadableComposeFileSaysSo(t *testing.T) {
	d := Detect(tree(map[string]string{"compose.yaml": "services:\n  web:\n   - this: [is not"}))
	if d.Builder != BuilderCompose {
		t.Fatalf("builder is %q, want compose", d.Builder)
	}
	if len(d.Compose) != 0 {
		t.Fatal("services were invented from a file that does not parse")
	}
	joined := strings.Join(d.Notes, " ")
	if !strings.Contains(joined, "could not be read") {
		t.Errorf("nothing says the file could not be read: %q", joined)
	}
}

// A Compose file that the tree lists but never fetched used to reach the
// converter as an empty string. Detection has to depend on the contents being
// there, so a reader that stops fetching them fails here rather than in the UI.
func TestComposeNeedsTheFileContents(t *testing.T) {
	d := Detect(Tree{Files: []string{"compose.yaml"}, Contents: map[string]string{}})
	if d.Builder != BuilderCompose {
		t.Fatalf("builder is %q, want compose", d.Builder)
	}
	if len(d.Compose) != 0 {
		t.Fatal("services appeared from a file with no contents")
	}
}

func TestNodeFrameworks(t *testing.T) {
	cases := []struct {
		name      string
		pkg       string
		framework string
		port      int
		builder   Builder
	}{
		{"next", `{"dependencies":{"next":"15"},"scripts":{"start":"next start"}}`, "Next.js", 3000, BuilderRailpack},
		{"express", `{"dependencies":{"express":"4"},"scripts":{"start":"node index.js"}}`, "Express", 3000, BuilderRailpack},
		{"nest", `{"dependencies":{"@nestjs/core":"10"},"scripts":{"start":"nest start"}}`, "NestJS", 3000, BuilderRailpack},
		{"astro", `{"dependencies":{"astro":"4"},"scripts":{"start":"astro dev"}}`, "Astro", 4321, BuilderRailpack},
		{"vite spa", `{"devDependencies":{"vite":"5"},"scripts":{"build":"vite build"}}`, "Vite", 80, BuilderStatic},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Detect(tree(map[string]string{"package.json": tc.pkg}))
			if d.Language != "Node.js" {
				t.Fatalf("language is %q", d.Language)
			}
			if d.Framework != tc.framework {
				t.Fatalf("framework is %q, want %q", d.Framework, tc.framework)
			}
			if d.Port != tc.port {
				t.Fatalf("port is %d, want %d", d.Port, tc.port)
			}
			if d.Builder != tc.builder {
				t.Fatalf("builder is %q, want %q", d.Builder, tc.builder)
			}
		})
	}
}

func TestNodeWithoutStartScriptIsFlagged(t *testing.T) {
	// A repository with no start script is where a zero-config build most often
	// goes wrong, so saying so up front is worth more than a confident guess.
	d := Detect(tree(map[string]string{"package.json": `{"dependencies":{"express":"4"}}`}))
	if d.Confidence != "medium" {
		t.Fatalf("confidence is %q, want medium without a start script", d.Confidence)
	}
	joined := strings.Join(d.Notes, " ")
	if !strings.Contains(joined, "start") {
		t.Fatalf("the missing start script is not mentioned: %v", d.Notes)
	}
}

func TestMalformedPackageJSONDoesNotPanic(t *testing.T) {
	d := Detect(tree(map[string]string{"package.json": `{"dependencies": [broken`}))
	if d.Language != "Node.js" {
		t.Fatalf("a broken package.json stopped detection: %q", d.Language)
	}
	if d.Confidence == "high" {
		t.Fatal("a broken package.json was treated as a confident detection")
	}
}

func TestPythonFrameworks(t *testing.T) {
	cases := []struct {
		name         string
		requirements string
		framework    string
		port         int
	}{
		{"django", "Django==5.0\npsycopg2", "Django", 8000},
		{"fastapi", "fastapi==0.115\nuvicorn", "FastAPI", 8000},
		{"flask", "Flask==3.0", "Flask", 8000},
		{"streamlit", "streamlit==1.38", "Streamlit", 8501},
		{"plain", "requests==2.32", "", 8000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Detect(tree(map[string]string{"requirements.txt": tc.requirements}))
			if d.Language != "Python" {
				t.Fatalf("language is %q", d.Language)
			}
			if d.Framework != tc.framework {
				t.Fatalf("framework is %q, want %q", d.Framework, tc.framework)
			}
			if d.Port != tc.port {
				t.Fatalf("port is %d, want %d", d.Port, tc.port)
			}
		})
	}
}

func TestDjangoMentionsItsRequirements(t *testing.T) {
	d := Detect(tree(map[string]string{"requirements.txt": "Django==5.0"}))
	joined := strings.Join(d.Notes, " ")
	// These are the three things that trip up every first Django deploy.
	for _, needed := range []string{"ALLOWED_HOSTS", "collectstatic"} {
		if !strings.Contains(joined, needed) {
			t.Errorf("the notes do not mention %s: %v", needed, d.Notes)
		}
	}
}

func TestOtherLanguages(t *testing.T) {
	cases := []struct {
		files    map[string]string
		language string
		port     int
	}{
		{map[string]string{"go.mod": "module example.com/app"}, "Go", 8080},
		{map[string]string{"Cargo.toml": "[package]"}, "Rust", 8080},
		{map[string]string{"Gemfile": "gem 'rails'"}, "Ruby", 3000},
		{map[string]string{"composer.json": `{"require":{"laravel/framework":"11"}}`}, "PHP", 80},
		{map[string]string{"pom.xml": "<artifactId>spring-boot-starter</artifactId>"}, "Java", 8080},
	}
	for _, tc := range cases {
		d := Detect(tree(tc.files))
		if d.Language != tc.language {
			t.Errorf("%v detected as %q, want %q", tc.files, d.Language, tc.language)
		}
		if d.Port != tc.port {
			t.Errorf("%s port is %d, want %d", tc.language, d.Port, tc.port)
		}
	}
}

func TestSpringBootGetsItsHealthPath(t *testing.T) {
	d := Detect(tree(map[string]string{"pom.xml": "<artifactId>spring-boot-starter-web</artifactId>"}))
	if d.HealthPath != "/actuator/health" {
		t.Fatalf("health path is %q", d.HealthPath)
	}
	if !strings.Contains(strings.Join(d.Notes, " "), "MaxRAMPercentage") {
		t.Fatal("the JVM memory note is missing; without it the JVM ignores the container limit and gets killed")
	}
}

func TestLaravelMentionsAppKey(t *testing.T) {
	d := Detect(tree(map[string]string{"composer.json": `{"require":{"laravel/framework":"11.0"}}`}))
	if d.Framework != "Laravel" {
		t.Fatalf("framework is %q", d.Framework)
	}
	if !strings.Contains(strings.Join(d.Notes, " "), "APP_KEY") {
		t.Fatal("APP_KEY is not mentioned, and Laravel will not boot without it")
	}
}

func TestWordPressWarnsAboutState(t *testing.T) {
	d := Detect(tree(map[string]string{"wp-config.php": "<?php", "index.php": "<?php"}))
	if d.Framework != "WordPress" {
		t.Fatalf("framework is %q", d.Framework)
	}
	if !strings.Contains(strings.Join(d.Notes, " "), "volume") {
		t.Fatal("the uploads-on-disk problem is not mentioned")
	}
}

func TestStaticSite(t *testing.T) {
	d := Detect(tree(map[string]string{"index.html": "<html>", "style.css": ""}))
	if d.Builder != BuilderStatic {
		t.Fatalf("builder is %q, want static", d.Builder)
	}
	if d.StaticDir != "." {
		t.Fatalf("static directory is %q", d.StaticDir)
	}

	nested := Detect(tree(map[string]string{"public/index.html": "<html>"}))
	if nested.StaticDir != "public" {
		t.Fatalf("static directory is %q, want public", nested.StaticDir)
	}
}

func TestUnknownRepositoryIsHonest(t *testing.T) {
	d := Detect(tree(map[string]string{"README.md": "# hello", "notes.txt": ""}))
	if d.Confidence != "low" {
		t.Fatalf("confidence is %q for an unrecognised repository", d.Confidence)
	}
	if !strings.Contains(strings.Join(d.Notes, " "), "Dockerfile") {
		t.Fatal("the fallback does not suggest adding a Dockerfile")
	}
}

func TestVendoredFilesAreIgnored(t *testing.T) {
	// A PHP dependency inside vendor/ must not make a Node project look like PHP.
	d := Detect(tree(map[string]string{
		"package.json":               `{"dependencies":{"express":"4"},"scripts":{"start":"node ."}}`,
		"vendor/some/lib/helper.php": "<?php",
		"node_modules/x/index.php":   "<?php",
	}))
	if d.Language != "Node.js" {
		t.Fatalf("a vendored .php file changed the detection to %q", d.Language)
	}
}

func TestConvertCompose(t *testing.T) {
	raw := map[string]any{
		"services": map[string]any{
			"web": map[string]any{
				"build":       ".",
				"ports":       []any{"8080:3000"},
				"environment": map[string]any{"NODE_ENV": "production", "DEBUG": false},
				"depends_on":  []any{"db"},
				"restart":     "always",
			},
			"db": map[string]any{
				"image":   "postgres:16",
				"volumes": []any{"pgdata:/var/lib/postgresql/data"},
			},
			"legacy": map[string]any{
				"image":        "old:1",
				"privileged":   true,
				"network_mode": "host",
			},
		},
	}
	services, warnings := ConvertCompose(raw)
	if len(services) != 3 {
		t.Fatalf("got %d services, want 3", len(services))
	}
	// Sorted, so the conversion is reproducible.
	if services[0].Name != "db" || services[2].Name != "web" {
		t.Fatalf("services are not in a stable order: %s, %s, %s",
			services[0].Name, services[1].Name, services[2].Name)
	}

	var web ComposeService
	for _, s := range services {
		if s.Name == "web" {
			web = s
		}
	}
	// The container port is what matters; the host port would mean opening a
	// port on a node, which the ingress exists to avoid.
	if len(web.Ports) != 1 || web.Ports[0] != 3000 {
		t.Fatalf("web ports are %v, want the container side only", web.Ports)
	}
	if web.Environment["NODE_ENV"] != "production" {
		t.Fatalf("environment was lost: %v", web.Environment)
	}
	if web.Environment["DEBUG"] != "false" {
		t.Fatalf("a boolean environment value became %q", web.Environment["DEBUG"])
	}
	if len(web.DependsOn) != 1 || web.DependsOn[0] != "db" {
		t.Fatalf("depends_on is %v", web.DependsOn)
	}

	// Unsupported features must be reported, not silently dropped.
	if len(warnings) < 2 {
		t.Fatalf("privileged and network_mode produced only %d warnings: %v", len(warnings), warnings)
	}
	joined := strings.Join(warnings, " ")
	if !strings.Contains(joined, "privileged") || !strings.Contains(joined, "network_mode") {
		t.Fatalf("warnings do not name what was dropped: %v", warnings)
	}
}

func TestComposePortForms(t *testing.T) {
	cases := map[any]int{
		"3000":                3000,
		"8080:3000":           3000,
		"127.0.0.1:8080:3000": 3000,
		"8080:3000/tcp":       3000,
		float64(5000):         5000,
		"not-a-port":          0,
	}
	for entry, want := range cases {
		if got := parseComposePort(entry); got != want {
			t.Errorf("parseComposePort(%v) = %d, want %d", entry, got, want)
		}
	}
	// The long form.
	if got := parseComposePort(map[string]any{"target": float64(9000), "published": float64(80)}); got != 9000 {
		t.Errorf("long-form port parsed as %d, want 9000", got)
	}
}

func TestConvertComposeWithoutServices(t *testing.T) {
	_, warnings := ConvertCompose(map[string]any{"version": "3"})
	if len(warnings) == 0 {
		t.Fatal("a Compose file with no services produced no warning")
	}
}
