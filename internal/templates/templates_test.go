package templates

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The catalogue is data, and data goes wrong quietly.
//
// A template is installed with one click by somebody who has not read it, so a
// mistake in it is not a compile error or a failing request: it is a database
// that is created and never linked, or an app that comes up as software nobody
// chose. These check the shape of every entry, because the catalogue is the one
// part of this product a user runs without looking at first.
//
// Each check is a function in validate.go rather than a loop in here, because a
// team's own catalogue is held to exactly the same rules when it is loaded: a
// template of theirs that fails one is listed with why and cannot be installed.
// The tests below run those same functions over the catalogue in the binary,
// and TestEveryCheckRefusesWhatItIsFor makes sure none of them has quietly
// become a check that passes everything.

// report fails the test once for every problem a check found in the built-in
// catalogue.
func report(t *testing.T, check Check) {
	t.Helper()
	for _, tpl := range All() {
		for _, problem := range check(tpl) {
			t.Error(problem)
		}
	}
}

func TestEveryTemplateIsComplete(t *testing.T) {
	for _, tpl := range All() {
		if tpl.ID == "" {
			t.Fatalf("a template has no id: %+v", tpl)
		}
	}
	for _, problem := range DuplicateIDs(All()) {
		t.Error(problem)
	}
	report(t, CheckComplete)
}

func TestEveryServiceCouldRun(t *testing.T) {
	report(t, CheckServices)
}

// TestNoWorkerIsTreatedAsAWebApp: a queue consumer, a Sidekiq, a scheduler —
// nothing about it answers HTTP. Giving one a domain produces a certificate, an
// ingress rule and a readiness probe pointed at a port that will never open,
// and the app stays "starting" until somebody reads the events. The converter
// that built this catalogue marked one worker public twice before this existed.
func TestNoWorkerIsTreatedAsAWebApp(t *testing.T) {
	report(t, CheckWorkers)
}

// TestEveryDatabaseReachesTheServiceItIsFor: a LinkTo that names no service is
// the worst kind of mistake here, because everything appears to work. The
// database is created, the link is skipped, and the app starts without the one
// variable it cannot run without — and crash-loops with nothing on screen
// saying why.
func TestEveryDatabaseReachesTheServiceItIsFor(t *testing.T) {
	report(t, CheckDatabases)
}

// TestNoTemplateRunsWhateverIsNewest: an image on a floating tag is not a
// version. Two deploys of the same app run different software, a rollback
// restores a tag rather than the thing that worked, and an upstream release
// arrives on a restart nobody asked for. The product promises rollback, so the
// catalogue has to name what it runs.
func TestNoTemplateRunsWhateverIsNewest(t *testing.T) {
	report(t, CheckVersions)
}

// An input that is neither required, nor generated, nor defaulted is a field
// the installer asks for and then does nothing about when it is empty.
func TestEveryInputIsAskedForOrFilledIn(t *testing.T) {
	report(t, CheckInputs)
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
// import, which is a fifth of the templates that bring a database. See
// CheckWiring for the rest of what it reads.
func TestNoTemplatePointsAtAContainerThatIsNotThere(t *testing.T) {
	report(t, CheckWiring)
}

// TestKnownImagesGetTheVariablesTheyRead: a database handed over as a URL
// to an image that reads its connection piece by piece is created and never
// used. WordPress, Ghost, n8n, and every Gitea, Forgejo and Nextcloud with a
// database did that until this ran over the catalogue.
func TestKnownImagesGetTheVariablesTheyRead(t *testing.T) {
	report(t, CheckKnownImages)
}

// TestTheReadmeAccountsForEveryURLOnlyTemplate: 120 templates handed their
// database over as a URL alone, and only some of their images read one. The
// README lists which have been looked up and which have not, and a list in
// prose is the first thing to go stale, so this holds it to the directory.
func TestTheReadmeAccountsForEveryURLOnlyTemplate(t *testing.T) {
	body, err := os.ReadFile("catalogue/README.md")
	if err != nil {
		t.Fatal(err)
	}
	listed := func(marker string) map[string]bool {
		_, rest, ok := strings.Cut(string(body), "<!-- "+marker+" -->")
		section, _, closed := strings.Cut(rest, "<!-- /"+marker+" -->")
		if !ok || !closed {
			t.Fatalf("the README has no %s list", marker)
		}
		out := map[string]bool{}
		for _, match := range regexp.MustCompile("`([a-z0-9-]+)`").FindAllStringSubmatch(section, -1) {
			out[match[1]] = true
		}
		return out
	}
	audited, unaudited := listed("url-only:audited"), listed("url-only:unaudited")
	for _, tpl := range All() {
		urlOnly, lookedUp := URLOnly(tpl)
		switch {
		case lookedUp && !audited[tpl.ID]:
			t.Errorf("%s hands over a URL its image is known to read; list it as looked up in the README", tpl.ID)
		case urlOnly && !lookedUp && !unaudited[tpl.ID]:
			t.Errorf("%s hands over only a URL, and nobody has looked up whether its image reads one; "+
				"list it in the README, or give its database the vars its image reads", tpl.ID)
		case !urlOnly && (audited[tpl.ID] || unaudited[tpl.ID]):
			t.Errorf("%s is listed in the README as handing over only a URL, and does not", tpl.ID)
		}
		delete(audited, tpl.ID)
		if !lookedUp {
			delete(unaudited, tpl.ID)
		}
	}
	for id := range audited {
		t.Errorf("the README lists %s as looked up, and it is not a URL-only template its image is known to read", id)
	}
	for id := range unaudited {
		t.Errorf("the README lists %s, which is not a template here or has been looked up", id)
	}
}

// TestNotesAndNeedsRootAgree: a template whose image starts as root says so
// twice — needs_root, which the install acts on, and its notes, which are read
// on the page the install lands on. The notes were once the only place, in 56
// templates, and 200 more said it nowhere; hack/resolve_tags.py --users is what
// found them. Either without the other is one of them being wrong.
func TestNotesAndNeedsRootAgree(t *testing.T) {
	for _, tpl := range All() {
		needs := false
		for _, svc := range tpl.Services {
			needs = needs || svc.NeedsRoot
		}
		says := strings.Contains(tpl.Notes, "baseline")
		switch {
		case needs && !says:
			t.Errorf("%s needs root and its notes do not say the environment needs the baseline level", tpl.ID)
		case says && !needs:
			t.Errorf("%s says the environment needs the baseline level, and no service has needs_root", tpl.ID)
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
