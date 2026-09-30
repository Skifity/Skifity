package templates

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"skifity/internal/kube"
)

// The engines Skifity provisions, spelled out here rather than imported.
//
// internal/dbsvc reaches internal/api, which reaches this package, so
// importing it from here would be a cycle. That the two lists agree is checked
// where both are already in scope, in internal/api.
var engines = map[string]bool{"postgres": true, "mysql": true, "redis": true}

// The catalogue is data, and data goes wrong quietly.
//
// A template is installed with one click by somebody who has not read it, so a
// mistake in it is not a compile error or a failing request: it is a database
// that is created and never linked, or an app that comes up as software nobody
// chose. These check the shape of every entry, because the catalogue is the one
// part of this product a user runs without looking at first.

func TestEveryTemplateIsComplete(t *testing.T) {
	seen := map[string]string{}
	for _, tpl := range All() {
		if tpl.ID == "" {
			t.Fatalf("a template has no id: %+v", tpl)
		}
		if other, clash := seen[tpl.ID]; clash {
			t.Errorf("%s and %s share the id %q, so Lookup returns whichever is first", other, tpl.Name, tpl.ID)
		}
		seen[tpl.ID] = tpl.Name

		if kube.Slugify(tpl.ID) != tpl.ID {
			t.Errorf("%s: the id %q is not already a slug, and it ends up in a URL", tpl.Name, tpl.ID)
		}
		for field, value := range map[string]string{
			"name": tpl.Name, "description": tpl.Description,
			"category": tpl.Category, "website": tpl.Website,
		} {
			if strings.TrimSpace(value) == "" {
				t.Errorf("%s has no %s, and the card shows it", tpl.ID, field)
			}
		}
		if !strings.HasPrefix(tpl.Website, "https://") {
			t.Errorf("%s links to %q; a link out of the panel must be https", tpl.ID, tpl.Website)
		}
		if len(tpl.Services) == 0 {
			t.Errorf("%s installs nothing", tpl.ID)
		}
	}
}

func TestEveryServiceCouldRun(t *testing.T) {
	for _, tpl := range All() {
		public := 0
		names := map[string]bool{}
		for _, svc := range tpl.Services {
			if svc.Name == "" || kube.Slugify(svc.Name) != svc.Name {
				t.Errorf("%s: the service name %q is not a slug, and it becomes a Kubernetes object", tpl.ID, svc.Name)
			}
			if names[svc.Name] {
				t.Errorf("%s: two services are called %q, and the second takes the first's Service over", tpl.ID, svc.Name)
			}
			names[svc.Name] = true

			// A worker does not listen, and Skifity runs one as an app with
			// no port: no Service, no probes, no ingress. Inventing a port for
			// a Sidekiq gives it a readiness check against something that
			// never answers, and an app that is "starting" forever.
			if svc.Port == 0 {
				if svc.Public {
					t.Errorf("%s/%s is public and listens on nothing", tpl.ID, svc.Name)
				}
				if svc.HealthPath != "" {
					t.Errorf("%s/%s has a health path and no port to check it on", tpl.ID, svc.Name)
				}
			} else if svc.Port < 1 || svc.Port > 65535 {
				t.Errorf("%s/%s listens on %d", tpl.ID, svc.Name, svc.Port)
			}
			if svc.Public {
				public++
			}
			if svc.HealthPath != "" && !strings.HasPrefix(svc.HealthPath, "/") {
				t.Errorf("%s/%s has the health path %q, which is not a path", tpl.ID, svc.Name, svc.HealthPath)
			}
			if svc.MemLimitMB > 0 && svc.MemRequestMB > svc.MemLimitMB {
				t.Errorf("%s/%s asks for more memory than it is allowed, so it can never be scheduled", tpl.ID, svc.Name)
			}
			if svc.CPULimitM > 0 && svc.CPURequestM > svc.CPULimitM {
				t.Errorf("%s/%s asks for more CPU than it is allowed", tpl.ID, svc.Name)
			}
			for key := range svc.Variables {
				if _, err := kube.SanitiseEnvKey(key); err != nil {
					t.Errorf("%s/%s sets %q, which a container cannot carry: %v", tpl.ID, svc.Name, key, err)
				}
			}
			for _, vol := range svc.Volumes {
				if !strings.HasPrefix(vol.MountPath, "/") {
					t.Errorf("%s/%s mounts %q, which is not an absolute path", tpl.ID, svc.Name, vol.MountPath)
				}
				if vol.SizeGB < 0 {
					t.Errorf("%s/%s asks for %d GB", tpl.ID, svc.Name, vol.SizeGB)
				}
			}
		}
		if public == 0 {
			t.Errorf("%s has no public service, so nothing it installs can be opened", tpl.ID)
		}
	}
}

// TestNoWorkerIsTreatedAsAWebApp: a queue consumer, a Sidekiq, a scheduler —
// nothing about it answers HTTP. Giving one a domain produces a certificate, an
// ingress rule and a readiness probe pointed at a port that will never open,
// and the app stays "starting" until somebody reads the events. The converter
// that built this catalogue marked one worker public twice before this existed.
func TestNoWorkerIsTreatedAsAWebApp(t *testing.T) {
	worker := regexp.MustCompile(
		`(^|[-_])(workers?|sidekiq|celery|beat|scheduler|cron|queue|consumer|` +
			`runners?|supervisor|jobs?)([-_]|$)`)
	for _, tpl := range All() {
		for _, svc := range tpl.Services {
			if worker.MatchString(svc.Name) && svc.Public {
				t.Errorf("%s/%s is named for a worker and is public; a queue consumer has no page to open",
					tpl.ID, svc.Name)
			}
		}
	}
}

// TestEveryDatabaseReachesTheServiceItIsFor: a LinkTo that names no service is
// the worst kind of mistake here, because everything appears to work. The
// database is created, the link is skipped, and the app starts without the one
// variable it cannot run without — and crash-loops with nothing on screen
// saying why.
func TestEveryDatabaseReachesTheServiceItIsFor(t *testing.T) {
	for _, tpl := range All() {
		services := map[string]bool{}
		for _, svc := range tpl.Services {
			services[svc.Name] = true
		}
		for _, db := range tpl.Databases {
			if db.Name == "" || kube.Slugify(db.Name) != db.Name {
				t.Errorf("%s: the database name %q is not a slug", tpl.ID, db.Name)
			}
			if !engines[db.Engine] {
				t.Errorf("%s: %q is not an engine Skifity runs", tpl.ID, db.Engine)
			}
			if db.StorageGB <= 0 {
				t.Errorf("%s/%s asks for %d GB of storage", tpl.ID, db.Name, db.StorageGB)
			}
			if len(db.LinkTo) == 0 {
				t.Errorf("%s: the database %s is created and linked to nothing", tpl.ID, db.Name)
			}
			for _, target := range db.LinkTo {
				if !services[target] {
					t.Errorf("%s: the database %s links to %q, which is not a service in this template",
						tpl.ID, db.Name, target)
				}
			}
			if db.VarName != "" {
				if _, err := kube.SanitiseEnvKey(db.VarName); err != nil {
					t.Errorf("%s/%s arrives as %q, which a container cannot carry: %v",
						tpl.ID, db.Name, db.VarName, err)
				}
			}
		}
	}
}

// floatingTag matches a tag that means "whatever is newest".
//
// `latest` is only the most honest spelling of it. `main`, `main-stable`,
// `16-master`, `release` and `postgresql-edge` all move under the app, and the
// first version of this caught none of them: litellm reached the catalogue on
// `main-stable`, which is a branch with a nicer name.
var floatingTag = regexp.MustCompile(`(^|[-_.])(latest|main|master|stable|edge|nightly|release|dev)$`)

// TestNoTemplateRunsWhateverIsNewest: an image on a floating tag is not a
// version. Two deploys of the same app run different software, a rollback
// restores a tag rather than the thing that worked, and an upstream release
// arrives on a restart nobody asked for. The product promises rollback, so the
// catalogue has to name what it runs.
func TestNoTemplateRunsWhateverIsNewest(t *testing.T) {
	for _, tpl := range All() {
		for _, svc := range tpl.Services {
			// A registry host may carry a port, so the tag is after the last
			// colon and only if there is no slash after it.
			colon := strings.LastIndex(svc.Image, ":")
			if colon < 0 || strings.Contains(svc.Image[colon:], "/") {
				t.Errorf("%s/%s runs %q with no tag, which means latest", tpl.ID, svc.Name, svc.Image)
				continue
			}
			if tag := svc.Image[colon+1:]; floatingTag.MatchString(tag) {
				t.Errorf("%s/%s runs %q: %q is whatever is newest, so this app cannot be rolled back",
					tpl.ID, svc.Name, svc.Image, tag)
			}
		}
	}
}

// An input that is neither required, nor generated, nor defaulted is a field
// the installer asks for and then does nothing about when it is empty.
func TestEveryInputIsAskedForOrFilledIn(t *testing.T) {
	for _, tpl := range All() {
		for _, input := range tpl.Inputs {
			if input.Key == "" || input.Label == "" {
				t.Errorf("%s has an input with no key or no label: %+v", tpl.ID, input)
			}
			if _, err := kube.SanitiseEnvKey(input.Key); err != nil {
				t.Errorf("%s asks for %q, which a container cannot carry: %v", tpl.ID, input.Key, err)
			}
			if !input.Required && !input.Generate && input.Default == "" {
				t.Errorf("%s asks for %s and does nothing when it is left empty", tpl.ID, input.Key)
			}
			if input.Generate && input.Default != "" {
				t.Errorf("%s/%s is both generated and defaulted; the default wins and the generator never runs",
					tpl.ID, input.Key)
			}
		}
	}
}

func TestSearchAndLookupFindWhatIsThere(t *testing.T) {
	for _, tpl := range All() {
		found, ok := Lookup(tpl.ID)
		if !ok || found.ID != tpl.ID {
			t.Errorf("Lookup(%q) did not find it", tpl.ID)
		}
		if len(Search(tpl.Name)) == 0 {
			t.Errorf("searching for %q finds nothing, and it is the name on the card", tpl.Name)
		}
	}
	if len(Search("")) != len(All()) {
		t.Error("an empty search hides templates")
	}
	if len(Search("nothing-is-called-this")) != 0 {
		t.Error("a search that matches nothing returned something")
	}
	if len(Categories()) == 0 {
		t.Error("there are no categories, and the page groups by them")
	}
}

// TestNoTemplatePointsAtAContainerThatIsNotThere: a Compose file wires services
// together by service name — DB_HOST=mariadb, REDIS_HOST=redis — and a template
// converted from one carries those over unless something stops it. In Skifity
// there is no sibling container to point at: the database is a managed one and
// arrives as a URL through the link. An app given the old variables starts,
// fails to resolve a hostname nobody recognises, and crash-loops.
//
// This caught bookstack, glpi, metabase, redmine and keycloak on the first
// import, which is a fifth of the templates that bring a database.
func TestNoTemplatePointsAtAContainerThatIsNotThere(t *testing.T) {
	datastore := regexp.MustCompile(
		`(^|_)(DB|DATABASE|POSTGRES|POSTGRESQL|PG|MYSQL|MARIADB|REDIS|VALKEY|KEYDB|MONGO|MONGODB|` +
			`CACHE|QUEUE|BROKER|AMQP|RABBITMQ|ELASTIC|ELASTICSEARCH|MEILI|CLICKHOUSE)($|_)`)
	address := regexp.MustCompile(`(^|_)(HOST|HOSTNAME|PORT|SERVER|ADDR|ADDRESS|URL|URI|DSN|CONNECTION|CONNECTIONSTRING)$`)

	for _, tpl := range All() {
		for _, svc := range tpl.Services {
			for key, value := range svc.Variables {
				upper := strings.ToUpper(key)
				if datastore.MatchString(upper) && address.MatchString(upper) {
					t.Errorf("%s/%s sets %s=%q; Skifity injects a connection string instead, "+
						"and this points at a container that does not exist",
						tpl.ID, svc.Name, key, value)
				}
				// A value the source file expected a shell to expand is not a
				// value; it reaches the container as the literal text.
				if strings.Contains(value, "$") {
					t.Errorf("%s/%s sets %s=%q, which was never expanded", tpl.ID, svc.Name, key, value)
				}
			}
		}
	}
}

// The catalogue is read from files at startup. A file that does not parse is a
// build-time mistake and has to fail here rather than at run time, where it
// would be an empty Templates page and no reason for it.
func TestTheCatalogueLoads(t *testing.T) {
	if err := Err(); err != nil {
		t.Fatalf("the catalogue could not be read: %v", err)
	}
	if len(All()) < 100 {
		t.Fatalf("the catalogue has %d templates; the files are not being embedded", len(All()))
	}
}

// TestWhatWeSayWeShipIsWhatWeShip: the README and docs/templates.md both put a
// number on this catalogue, and a number in prose is the first thing to go
// stale. It said 219 while the directory held 279, and nothing anywhere failed.
// Every count this repository states about the catalogue is checked here
// against the catalogue.
func TestWhatWeSayWeShipIsWhatWeShip(t *testing.T) {
	total := len(All())
	multi := 0
	for _, tpl := range All() {
		if len(tpl.Services) > 1 {
			multi++
		}
	}

	for _, page := range []string{"../../README.md", "../../docs/templates.md"} {
		body, err := os.ReadFile(page)
		if err != nil {
			t.Fatalf("read %s: %v", page, err)
		}
		text := string(body)
		// Each page states the total once, in digits, and says how many
		// templates install more than one app — in digits or in words.
		if !strings.Contains(text, strconv.Itoa(total)) {
			t.Errorf("%s never says %d, and this catalogue holds %d templates", page, total, total)
		}
		if !strings.Contains(text, strconv.Itoa(multi)) &&
			!strings.Contains(strings.ToLower(text), spell(multi)) {
			t.Errorf("%s never says %d (or %q), and %d templates install more than one app",
				page, multi, spell(multi), multi)
		}
	}
}

// spell writes a small number the way the prose does, in lower case, because
// the caller lowers the page before looking. Only the tens this catalogue is
// likely to reach: past that, the pages use digits.
func spell(n int) string {
	units := []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}
	tens := []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}
	teens := []string{"ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
		"sixteen", "seventeen", "eighteen", "nineteen"}
	switch {
	case n < 10:
		return units[n]
	case n < 20:
		return teens[n-10]
	case n < 100:
		word := tens[n/10]
		if n%10 != 0 {
			word += "-" + units[n%10]
		}
		return word
	}
	return strconv.Itoa(n)
}

// TestEveryListInATemplateIsAnArrayInJSON.
//
// A nil slice in Go marshals as `null`, and the panel's own type says these are
// arrays. 157 of the templates here have no database and every one of them
// answered `"databases": null`; the Templates page iterated it, threw, and
// rendered an error boundary instead of the catalogue — on every install since
// the catalogue grew past the eight hand-written entries, all of which happened
// to have a database.
//
// The tests in this file all read the Go value. This one reads what goes over
// the wire, because that is where the difference was.
func TestEveryListInATemplateIsAnArrayInJSON(t *testing.T) {
	for _, tpl := range All() {
		body, err := json.Marshal(tpl)
		if err != nil {
			t.Fatalf("%s: marshal: %v", tpl.ID, err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatalf("%s: unmarshal: %v", tpl.ID, err)
		}
		for _, field := range []string{"services", "databases", "inputs"} {
			value, present := raw[field]
			if !present {
				// omitempty is fine: a client reading an absent key gets
				// undefined and can default it. `null` is what breaks, because
				// it is present and not iterable.
				continue
			}
			if string(value) == "null" {
				t.Errorf("%s answers %q: null, which a client cannot iterate", tpl.ID, field)
			}
		}
	}
}

// The catalogue's logos.
//
// They are files on disk matched to templates by name, which is the kind of
// arrangement that rots quietly: a template renamed leaves an orphan, and an
// icon named slightly wrong is simply never shown and nobody notices.

func TestEveryIconBelongsToATemplate(t *testing.T) {
	entries, err := iconFiles.ReadDir("icons")
	if err != nil {
		t.Fatalf("read the icons: %v", err)
	}
	known := map[string]bool{}
	for _, template := range All() {
		known[template.ID] = true
	}

	orphans := 0
	for _, entry := range entries {
		name := entry.Name()
		if name == "README.md" {
			continue
		}
		extension := name[strings.LastIndex(name, "."):]
		if _, ok := IconContentTypes[extension]; !ok {
			t.Errorf("%s is not a kind of picture the panel serves; it would be dead weight in the binary", name)
			continue
		}
		id := strings.TrimSuffix(name, extension)
		if !known[id] {
			t.Errorf("%s belongs to no template, so it is bytes nobody will ever see", name)
			orphans++
		}
	}
	if orphans > 0 {
		t.Log("a template renamed without its icon leaves one of these behind")
	}
}

func TestATemplateWithAnIconCanServeIt(t *testing.T) {
	withIcon := 0
	for _, template := range All() {
		if template.Icon == "" {
			continue
		}
		withIcon++
		body, contentType, ok := ReadIcon(template.ID)
		if !ok {
			t.Errorf("%s says it has an icon and the panel cannot read it", template.ID)
			continue
		}
		if len(body) == 0 {
			t.Errorf("%s has an empty icon file", template.ID)
		}
		if contentType == "" {
			t.Errorf("%s would be served with no type, and a browser will not draw that", template.ID)
		}
	}
	// Not a target to chase, a floor to notice falling through: the catalogue
	// was three hundred grey letters and the point of this was that most of
	// them stop being.
	if withIcon < 150 {
		t.Errorf("only %d templates have a logo; hack/fetch_icons.py is the thing that has stopped working", withIcon)
	}
	t.Logf("%d of %d templates have a logo", withIcon, len(All()))
}

func TestATemplateWithNoIconSaysSoRatherThanBreaking(t *testing.T) {
	// The page draws a letter for these. What it must not get is a name it
	// will put in an <img> tag that then 404s on every card.
	for _, template := range All() {
		if template.Icon != "" {
			continue
		}
		if _, _, ok := ReadIcon(template.ID); ok {
			t.Errorf("%s has no icon recorded and one can be read, so the page shows a letter for nothing", template.ID)
		}
	}
	if _, _, ok := ReadIcon("a-template-that-does-not-exist"); ok {
		t.Error("an icon was served for a template that does not exist")
	}
}
