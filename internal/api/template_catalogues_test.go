package api

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"skifity/internal/cron"
	"skifity/internal/netguard"
	"skifity/internal/store"
	"skifity/internal/templates"
	"skifity/internal/templates/remote"
)

// A team's own template catalogues, against the real router: who may add one,
// who sees its templates, that one team can never install from another's,
// that the header a private host wants is sealed and never answered, and that
// a refresh which fails keeps the copy the team had.

// catalogueHost is an https server standing in for a Git host's raw files.
// What it answers can be changed while a test runs, and it remembers the
// header every request brought.
type catalogueHost struct {
	server *httptest.Server
	mu     sync.Mutex
	files  map[string]string
	status map[string]int
	// token, when set, is the PRIVATE-TOKEN every request must carry.
	token string
	seen  []string
}

func newCatalogueHost(t *testing.T, files map[string]string) *catalogueHost {
	t.Helper()
	host := &catalogueHost{files: files, status: map[string]int{}}
	host.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host.mu.Lock()
		defer host.mu.Unlock()
		host.seen = append(host.seen, r.URL.Path+" "+r.Header.Get("PRIVATE-TOKEN"))
		if host.token != "" && r.Header.Get("PRIVATE-TOKEN") != host.token {
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
			return
		}
		if status, ok := host.status[r.URL.Path]; ok {
			http.Error(w, "down for maintenance", status)
			return
		}
		body, ok := host.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(host.server.Close)
	return host
}

func (c *catalogueHost) set(path, body string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.files[path] = body
	delete(c.status, path)
}

func (c *catalogueHost) fail(path string, status int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status[path] = status
}

func (c *catalogueHost) requests() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.seen...)
}

// reach lets the panel's downloads reach the test host, which listens on
// loopback — the one kind of address netguard refuses that a test server has.
func (h *harness) reach(host *catalogueHost) {
	pool := x509.NewCertPool()
	pool.AddCert(host.server.Certificate())
	h.api.catalogueFetcher = &remote.Fetcher{
		Allowed: func(ip net.IP) bool { return ip.IsLoopback() || netguard.Allowed(ip) },
		RootCAs: pool, Timeout: 5 * time.Second,
	}
}

// catalogueTemplate is a template every check accepts.
func catalogueTemplate(id, name, image string) string {
	return fmt.Sprintf(`- id: %s
  name: %s
  description: One of our own.
  category: developer
  website: https://tools.example.org/%s
  services:
  - name: %s
    image: %s
    port: 8080
    public: true
`, id, name, id, id, image)
}

// acmeIndex is a catalogue with an internal wiki, a template whose id is also
// a built-in one's, and one that fails the checks.
func acmeIndex(wikiImage string) string {
	return "templates:\n" +
		catalogueTemplate("wiki", "Acme Wiki", wikiImage) +
		catalogueTemplate("adminer", "Acme Adminer", "registry.acme.example/adminer:5.0.1") +
		catalogueTemplate("floating", "Floating", "registry.acme.example/floating:latest")
}

// addCatalogue adds a catalogue as a tenant and returns its id.
func (h *harness) addCatalogue(as tenant, body map[string]any) (string, int, string) {
	h.t.Helper()
	status, answer := h.do(as, http.MethodPost, "/api/teams/"+as.team.ID+"/template-catalogues", body)
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(answer), &created)
	return created.ID, status, answer
}

// listed is the catalogue as a team sees it.
func (h *harness) listed(as tenant) []listedTemplate {
	h.t.Helper()
	status, body := h.do(as, http.MethodGet, "/api/teams/"+as.team.ID+"/templates", nil)
	if status != http.StatusOK {
		h.t.Fatalf("listing %s's templates answered %d: %s", as.team.Name, status, body)
	}
	var answer struct {
		Items []listedTemplate `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		h.t.Fatal(err)
	}
	return answer.Items
}

func find(items []listedTemplate, id, catalogue string) (listedTemplate, bool) {
	for _, item := range items {
		inCatalogue := ""
		if item.Catalogue != nil {
			inCatalogue = item.Catalogue.ID
		}
		if item.ID == id && inCatalogue == catalogue {
			return item, true
		}
	}
	return listedTemplate{}, false
}

func TestATeamsCatalogueIsItsOwn(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	host := newCatalogueHost(t, map[string]string{"/acme/index.yaml": acmeIndex("registry.acme.example/wiki:3.2.1")})
	h.reach(host)

	id, status, body := h.addCatalogue(acme, map[string]any{"name": "Acme", "url": host.server.URL + "/acme/index.yaml"})
	if status != http.StatusCreated {
		t.Fatalf("adding a catalogue answered %d: %s", status, body)
	}
	var view catalogueView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	// Two can be installed; the third is listed with why, and the rest of
	// the catalogue still loaded.
	if view.Templates != 2 || len(view.Problems) != 1 || view.Problems[0].ID != "floating" ||
		!strings.Contains(strings.Join(view.Problems[0].Errors, " "), "whatever is newest") {
		t.Fatalf("the catalogue reads as %+v", view)
	}
	if view.FetchedAt.IsZero() || view.LastError != "" {
		t.Errorf("a catalogue that was just fetched says %+v", view.TemplateCatalogue)
	}

	mine := h.listed(acme)
	wiki, ok := find(mine, "wiki", id)
	if !ok || wiki.Catalogue.Name != "Acme" || wiki.Services[0].Image != "registry.acme.example/wiki:3.2.1" {
		t.Fatalf("acme does not see its wiki under its catalogue: %+v", wiki)
	}
	// The same id in two catalogues is two templates, each under its own.
	ours, ok := find(mine, "adminer", id)
	builtIn, builtInOK := find(mine, "adminer", "")
	if !ok || !builtInOK || ours.Name != "Acme Adminer" || builtIn.Name == "Acme Adminer" {
		t.Fatalf("the catalogue's adminer and the built-in one are not both there: %+v / %+v", ours, builtIn)
	}
	if _, ok := find(mine, "floating", id); ok {
		t.Error("a template that fails the checks is offered for installing")
	}
	if len(mine) != len(templates.All())+2 {
		t.Errorf("acme sees %d templates; want the %d built in and its own two", len(mine), len(templates.All()))
	}

	// Nobody else sees any of it.
	theirs := h.listed(globex)
	if len(theirs) != len(templates.All()) {
		t.Errorf("globex sees %d templates, which is more than the built-in catalogue", len(theirs))
	}
	for _, item := range theirs {
		if item.Catalogue != nil {
			t.Errorf("globex is shown %s from %s", item.ID, item.Catalogue.Name)
		}
	}
	for _, path := range []string{
		"/api/teams/" + acme.team.ID + "/templates",
		"/api/teams/" + acme.team.ID + "/template-catalogues",
	} {
		if status, _ := h.do(globex, http.MethodGet, path, nil); status != http.StatusNotFound {
			t.Errorf("globex asked for %s and was answered %d", path, status)
		}
	}
	// The logo is asked for by the catalogue alone, and answers another
	// team as it answers a catalogue that does not exist.
	for _, catalogue := range []string{id, "tcat_does_not_exist"} {
		if status, body := h.do(globex, http.MethodGet, "/api/templates/wiki/icon?catalogue="+catalogue, nil); status != http.StatusNotFound || strings.Contains(body, acme.team.ID) {
			t.Errorf("globex asked for acme's logo and was answered %d: %s", status, body)
		}
	}
	for _, method := range []string{http.MethodDelete, http.MethodPost} {
		path := "/api/teams/" + globex.team.ID + "/template-catalogues/" + id
		if method == http.MethodPost {
			path += "/refresh"
		}
		if status, _ := h.do(globex, method, path, nil); status != http.StatusNotFound {
			t.Errorf("globex %s %s: %d, want 404 — acme's catalogue is not in globex", method, path, status)
		}
	}
	// And the panel's own catalogue is still the panel's own.
	if _, body := h.do(acme, http.MethodGet, "/api/templates", nil); strings.Contains(body, "Acme Wiki") {
		t.Error("a team's template is in the catalogue every team sees")
	}
}

func TestATeamCannotInstallFromAnotherTeamsCatalogue(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	host := newCatalogueHost(t, map[string]string{"/acme/index.yaml": acmeIndex("registry.acme.example/wiki:3.2.1")})
	h.reach(host)
	id, status, body := h.addCatalogue(acme, map[string]any{"name": "Acme", "url": host.server.URL + "/acme/index.yaml"})
	if status != http.StatusCreated {
		t.Fatalf("adding answered %d: %s", status, body)
	}

	install := func(as tenant, env store.Environment, template, catalogue string) (int, string) {
		request := map[string]any{"environment_id": env.ID}
		if catalogue != "" {
			request["catalogue_id"] = catalogue
		}
		return h.do(as, http.MethodPost, "/api/templates/"+template+"/install", request)
	}
	appsIn := func(env store.Environment) int {
		apps, err := h.db.ListApps(t.Context(), env.ID)
		if err != nil {
			t.Fatal(err)
		}
		return len(apps)
	}

	// Globex, into its own environment, naming acme's catalogue: the
	// catalogue is found by globex's team, so it is not found at all.
	if status, body := install(globex, globex.env, "wiki", id); status != http.StatusNotFound || !strings.Contains(body, "template catalogue") {
		t.Errorf("globex installed from acme's catalogue: %d %s", status, body)
	}
	// Acme's own member, into globex's environment: the environment is not
	// theirs.
	if status, _ := install(acme, globex.env, "wiki", id); status != http.StatusNotFound {
		t.Errorf("acme installed into globex: %d", status)
	}
	if appsIn(globex.env) != 0 || appsIn(acme.env) != 0 {
		t.Fatal("a refused install created apps")
	}

	// An id alone is the built-in catalogue, which has no wiki.
	if status, _ := install(acme, acme.env, "wiki", ""); status != http.StatusNotFound {
		t.Errorf("wiki was found with no catalogue named: %d", status)
	}
	// A template that fails the checks says why it cannot be installed.
	if status, body := install(acme, acme.env, "floating", id); status != http.StatusUnprocessableEntity ||
		!strings.Contains(body, "template.not_installable") || !strings.Contains(body, "whatever is newest") {
		t.Errorf("a template that fails the checks answered %d: %s", status, body)
	}

	// Named properly, it installs, through the same path as a built-in one,
	// and the app remembers the catalogue.
	status, body = install(acme, acme.env, "wiki", id)
	if status != http.StatusCreated {
		t.Fatalf("acme could not install its own template: %d %s", status, body)
	}
	var created installedTemplate
	if err := json.Unmarshal([]byte(body), &created); err != nil || len(created.Apps) != 1 {
		t.Fatalf("installed %s (%v)", body, err)
	}
	if created.Apps[0].Image != "registry.acme.example/wiki:3.2.1" {
		t.Errorf("the app runs %s", created.Apps[0].Image)
	}
	record, err := h.db.GetAppTemplate(t.Context(), created.Apps[0].ID)
	if err != nil || record.CatalogueID != id || record.TemplateID != "wiki" {
		t.Errorf("the app remembers %+v (%v)", record, err)
	}

	// The same id, from each catalogue, is each catalogue's.
	status, body = install(acme, acme.env, "adminer", id)
	if status != http.StatusCreated || !strings.Contains(body, "registry.acme.example/adminer:5.0.1") {
		t.Errorf("acme's adminer installed as %d %s", status, body)
	}
	builtIn, _ := templates.Lookup("adminer")
	status, body = h.do(acme, http.MethodPost, "/api/templates/adminer/install",
		map[string]any{"environment_id": acme.env.ID, "name": "adminer-builtin"})
	if status != http.StatusCreated || !strings.Contains(body, builtIn.Services[0].Image) {
		t.Errorf("the built-in adminer installed as %d %s", status, body)
	}
}

// The token a private host wants is sealed under the catalogue and its
// address, used on every download, and never part of anything any API
// answers.
func TestTheHeaderIsSealedUsedAndNeverAnswered(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	const token = "glpat-planted-catalogue-token"
	host := newCatalogueHost(t, map[string]string{"/acme/index.yaml": acmeIndex("registry.acme.example/wiki:3.2.1")})
	host.token = token
	h.reach(host)

	id, status, body := h.addCatalogue(acme, map[string]any{
		"name": "Acme", "url": host.server.URL + "/acme/index.yaml",
		"auth_header_name": "PRIVATE-TOKEN", "auth_header_value": token,
	})
	if status != http.StatusCreated {
		t.Fatalf("adding with the header answered %d: %s", status, body)
	}
	answers := []string{body}

	row, err := h.db.GetTemplateCatalogue(t.Context(), acme.team.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.SealedAuth == "" || strings.Contains(row.SealedAuth, token) {
		t.Fatalf("the header is stored as %q", row.SealedAuth)
	}
	if opened, err := h.keyring.Open(row.SealedAuth, store.TemplateCatalogueContext(acme.team.ID, id, row.URL)); err != nil || string(opened) != token {
		t.Fatalf("the sealed header does not open under its context: %q %v", opened, err)
	}
	// Sealed to the address: a row whose address is changed by hand does
	// not send the token to the new one.
	if _, err := h.keyring.Open(row.SealedAuth, store.TemplateCatalogueContext(acme.team.ID, id, "https://elsewhere.example/index.yaml")); err == nil {
		t.Error("the header opens for another address")
	}

	// The refresh sends it again, opened from the seal.
	status, body = h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/template-catalogues/"+id+"/refresh", nil)
	if status != http.StatusOK {
		t.Fatalf("refreshing answered %d: %s", status, body)
	}
	answers = append(answers, body)
	requests := host.requests()
	if len(requests) < 2 || requests[len(requests)-1] != "/acme/index.yaml "+token {
		t.Errorf("the host saw %q", requests)
	}

	for _, path := range []string{
		"/api/teams/" + acme.team.ID + "/template-catalogues",
		"/api/teams/" + acme.team.ID + "/templates",
		"/api/teams/" + acme.team.ID + "/audit",
	} {
		_, body := h.do(acme, http.MethodGet, path, nil)
		answers = append(answers, body)
	}
	for _, answer := range answers {
		if strings.Contains(answer, token) {
			t.Errorf("an answer carries the header's value: %s", answer)
		}
	}
	if !strings.Contains(answers[2], `"auth_header_name":"PRIVATE-TOKEN"`) {
		t.Errorf("the list does not say which header the catalogue is fetched with: %s", answers[2])
	}
}

func TestTheLastGoodCopyIsKeptWhenARefreshFails(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	host := newCatalogueHost(t, map[string]string{"/acme/index.yaml": acmeIndex("registry.acme.example/wiki:3.2.1")})
	h.reach(host)
	id, status, body := h.addCatalogue(acme, map[string]any{"name": "Acme", "url": host.server.URL + "/acme/index.yaml"})
	if status != http.StatusCreated {
		t.Fatalf("adding answered %d: %s", status, body)
	}
	refresh := "/api/teams/" + acme.team.ID + "/template-catalogues/" + id + "/refresh"
	wikiImage := func() string {
		wiki, ok := find(h.listed(acme), "wiki", id)
		if !ok {
			return ""
		}
		return wiki.Services[0].Image
	}

	for name, breakIt := range map[string]func(){
		"the host is down":        func() { host.fail("/acme/index.yaml", http.StatusBadGateway) },
		"it answers a login page": func() { host.set("/acme/index.yaml", "<!doctype html><html>Sign in</html>") },
		"it answers something else": func() {
			host.set("/acme/index.yaml", "apps:\n- wiki\n")
		},
	} {
		breakIt()
		status, body := h.do(acme, http.MethodPost, refresh, nil)
		if status != http.StatusBadGateway || !strings.Contains(body, "template.catalogue_refresh_failed") {
			t.Errorf("%s: the refresh answered %d: %s", name, status, body)
		}
		if image := wikiImage(); image != "registry.acme.example/wiki:3.2.1" {
			t.Errorf("%s: the wiki is now %q; the last good copy was not kept", name, image)
		}
		row, _ := h.db.GetTemplateCatalogue(t.Context(), acme.team.ID, id)
		if row.LastError == "" || row.AttemptedAt.Before(row.FetchedAt) {
			t.Errorf("%s: the failure is not on the catalogue: %+v", name, row.TemplateCatalogue)
		}
	}

	// And it is still what a template installs from.
	if status, body := h.do(acme, http.MethodPost, "/api/templates/wiki/install",
		map[string]any{"environment_id": acme.env.ID, "catalogue_id": id}); status != http.StatusCreated {
		t.Fatalf("installing from the kept copy answered %d: %s", status, body)
	}

	// A refresh that works replaces it, and clears the error.
	host.set("/acme/index.yaml", acmeIndex("registry.acme.example/wiki:3.3.0"))
	if status, body := h.do(acme, http.MethodPost, refresh, nil); status != http.StatusOK {
		t.Fatalf("a good refresh answered %d: %s", status, body)
	}
	if image := wikiImage(); image != "registry.acme.example/wiki:3.3.0" {
		t.Errorf("after a good refresh the wiki is %q", image)
	}
	row, _ := h.db.GetTemplateCatalogue(t.Context(), acme.team.ID, id)
	if row.LastError != "" {
		t.Errorf("a good refresh left the old error: %q", row.LastError)
	}

	// The app installed from the old copy is offered the new version,
	// looked for in the catalogue it came from.
	apps, _ := h.db.ListApps(t.Context(), acme.env.ID)
	status, body = h.do(acme, http.MethodGet, "/api/apps/"+apps[0].ID+"/template", nil)
	if status != http.StatusOK || !strings.Contains(body, `"update_available":true`) ||
		!strings.Contains(body, `"latest_image":"registry.acme.example/wiki:3.3.0"`) {
		t.Errorf("the app's template answers %d: %s", status, body)
	}
	// Removing the catalogue leaves the app, and no update to offer.
	if status, _ := h.do(acme, http.MethodDelete, "/api/teams/"+acme.team.ID+"/template-catalogues/"+id, nil); status != http.StatusOK {
		t.Fatalf("removing the catalogue answered %d", status)
	}
	if _, body := h.do(acme, http.MethodGet, "/api/apps/"+apps[0].ID+"/template", nil); strings.Contains(body, `"update_available":true`) {
		t.Errorf("an update is offered from a catalogue that is gone: %s", body)
	}
}

// The panel's own client refuses what netguard refuses, whoever typed the
// address, and nothing is kept.
func TestACatalogueThePanelMustNotReachIsRefused(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	host := newCatalogueHost(t, map[string]string{"/index.yaml": acmeIndex("registry.acme.example/wiki:3.2.1")})
	// Not reached: the fetcher is the one the panel runs with.

	for address, says := range map[string]string{
		host.server.URL + "/index.yaml":            "refusing to connect",
		"https://169.254.169.254/latest/meta-data": "metadata",
	} {
		_, status, body := h.addCatalogue(acme, map[string]any{"name": "Nope", "url": address})
		if status != http.StatusBadGateway || !strings.Contains(body, "template.catalogue_fetch_failed") || !strings.Contains(body, says) {
			t.Errorf("%s answered %d: %s", address, status, body)
		}
	}
	for address, code := range map[string]string{
		"http://catalogue.example/index.yaml":                  "template.catalogue_address",
		"https://catalogue.example/index.yaml?private_token=x": "template.catalogue_address",
		"https://bot:hunter2@catalogue.example/index.yaml":     "template.catalogue_address",
	} {
		_, status, body := h.addCatalogue(acme, map[string]any{"name": "Nope", "url": address})
		if status != http.StatusBadRequest || !strings.Contains(body, code) {
			t.Errorf("%s answered %d: %s", address, status, body)
		}
	}
	_, status, body := h.addCatalogue(acme, map[string]any{
		"name": "Nope", "url": "https://catalogue.example/index.yaml",
		"auth_header_name": "Host", "auth_header_value": "elsewhere.example",
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "template.catalogue_header") {
		t.Errorf("setting Host answered %d: %s", status, body)
	}
	if rows, _ := h.db.ListTemplateCatalogues(t.Context(), acme.team.ID); len(rows) != 0 {
		t.Errorf("refused catalogues were kept: %+v", rows)
	}
	if len(host.requests()) != 0 {
		t.Errorf("the panel reached the loopback host: %q", host.requests())
	}
}

func TestOnlyAnAdministratorManagesTheTeamsCatalogues(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	host := newCatalogueHost(t, map[string]string{"/index.yaml": acmeIndex("registry.acme.example/wiki:3.2.1")})
	h.reach(host)
	member := h.newMember(acme, "dev", store.RoleMember)
	viewer := h.newMember(acme, "auditor", store.RoleViewer)
	add := map[string]any{"name": "Acme", "url": host.server.URL + "/index.yaml"}

	if _, status, _ := h.addCatalogue(member, add); status != http.StatusForbidden {
		t.Errorf("a member added a catalogue: %d", status)
	}
	id, status, body := h.addCatalogue(acme, add)
	if status != http.StatusCreated {
		t.Fatalf("the owner could not add one: %d %s", status, body)
	}
	if _, status, body := h.addCatalogue(acme, add); status != http.StatusConflict {
		t.Errorf("the same catalogue twice answered %d: %s", status, body)
	}
	base := "/api/teams/" + acme.team.ID + "/template-catalogues/" + id
	for _, as := range []tenant{member, viewer} {
		if status, _ := h.do(as, http.MethodPost, base+"/refresh", nil); status != http.StatusForbidden {
			t.Errorf("%s refreshed it: %d", as.user.Name, status)
		}
		if status, _ := h.do(as, http.MethodDelete, base, nil); status != http.StatusForbidden {
			t.Errorf("%s removed it: %d", as.user.Name, status)
		}
		if status, body := h.do(as, http.MethodGet, "/api/teams/"+acme.team.ID+"/template-catalogues", nil); status != http.StatusOK ||
			!strings.Contains(body, `"name":"Acme"`) {
			t.Errorf("%s cannot see which catalogues the team has: %d %s", as.user.Name, status, body)
		}
		if _, ok := find(h.listed(as), "wiki", id); !ok {
			t.Errorf("%s does not see the team's templates", as.user.Name)
		}
	}
	// A member installs, into an environment they may change.
	if status, body := h.do(member, http.MethodPost, "/api/templates/wiki/install",
		map[string]any{"environment_id": acme.env.ID, "catalogue_id": id}); status != http.StatusCreated {
		t.Errorf("a member could not install the team's template: %d %s", status, body)
	}
	if status, _ := h.do(viewer, http.MethodPost, "/api/templates/wiki/install",
		map[string]any{"environment_id": acme.env.ID, "catalogue_id": id, "name": "wiki-2"}); status != http.StatusForbidden {
		t.Errorf("a viewer installed a template: %d", status)
	}
}

// A logo is fetched by the panel, from the catalogue's own host, when the
// catalogue is; the browser is sent nowhere but the panel.
func TestALogoIsFetchedFromTheCataloguesHostAndServedByThePanel(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	const logo = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><rect width="1" height="1"/></svg>`
	index := "templates:\n" +
		catalogueTemplate("wiki", "Acme Wiki", "registry.acme.example/wiki:3.2.1") + "  icon: icons/wiki.svg\n" +
		catalogueTemplate("tracker", "Tracked", "registry.acme.example/tracker:1.0.1") + "  icon: https://tracker.example/pixel.png\n" +
		catalogueTemplate("page", "Page", "registry.acme.example/page:1.0.1") + "  icon: icons/page.svg\n"
	host := newCatalogueHost(t, map[string]string{
		"/acme/index.yaml":     index,
		"/acme/icons/wiki.svg": logo,
		"/acme/icons/page.svg": "<!doctype html><html><body>a page, not a picture</body></html>",
	})
	h.reach(host)
	id, status, body := h.addCatalogue(acme, map[string]any{"name": "Acme", "url": host.server.URL + "/acme/index.yaml"})
	if status != http.StatusCreated {
		t.Fatalf("adding answered %d: %s", status, body)
	}

	listed := h.listed(acme)
	if wiki, _ := find(listed, "wiki", id); wiki.Icon != "wiki.svg" {
		t.Errorf("the wiki's logo is %q", wiki.Icon)
	}
	for _, other := range []string{"tracker", "page"} {
		if item, _ := find(listed, other, id); item.Icon != "" {
			t.Errorf("%s has the logo %q", other, item.Icon)
		}
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		h.server.URL+"/api/templates/wiki/icon?catalogue="+id, nil)
	req.Header.Set("Authorization", "Bearer "+acme.token)
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/svg+xml" ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("the logo is served as %d %q %q", resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Security-Policy"))
	}
	for _, other := range []string{"tracker", "page"} {
		if status, _ := h.do(acme, http.MethodGet, "/api/templates/"+other+"/icon?catalogue="+id, nil); status != http.StatusNotFound {
			t.Errorf("%s's logo answered %d", other, status)
		}
	}
	for _, request := range host.requests() {
		if strings.Contains(request, "pixel") {
			t.Errorf("the panel fetched a logo from another host: %s", request)
		}
	}
}

// Once a day each, at a minute of the night of its own, and caught up after a
// panel that was not running then.
func TestACatalogueIsRefreshedOnceADay(t *testing.T) {
	row := store.TemplateCatalogueRow{TemplateCatalogue: store.TemplateCatalogue{ID: "tcat_example"}}
	schedule := catalogueRefreshSchedule(row.ID)
	next, err := cron.NextRun(schedule, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("the schedule %q does not parse: %v", schedule, err)
	}
	if next.Hour() < 2 || next.Hour() > 5 {
		t.Errorf("%q fires at %s, not in the night", schedule, next)
	}

	row.AttemptedAt = next.Add(-24 * time.Hour)
	if !catalogueDue(row, next) {
		t.Error("a catalogue is not refreshed at its minute")
	}
	if catalogueDue(row, next.Add(time.Minute)) {
		t.Error("a catalogue is refreshed at a minute that is not its own")
	}
	row.AttemptedAt = next.Add(-10 * time.Minute)
	if catalogueDue(row, next) {
		t.Error("a catalogue refreshed by hand ten minutes ago is refreshed again")
	}
	row.AttemptedAt = next.Add(-26 * time.Hour)
	if !catalogueDue(row, next.Add(3*time.Hour)) {
		t.Error("a catalogue a day and more behind is not caught up")
	}
}

func TestTheDailyPassRefreshesWhatIsDueAndNothingElse(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	host := newCatalogueHost(t, map[string]string{
		"/due.yaml":   acmeIndex("registry.acme.example/wiki:3.2.1"),
		"/fresh.yaml": acmeIndex("registry.acme.example/wiki:3.2.1"),
	})
	h.reach(host)
	due, _, _ := h.addCatalogue(acme, map[string]any{"name": "Due", "url": host.server.URL + "/due.yaml"})
	fresh, _, _ := h.addCatalogue(acme, map[string]any{"name": "Fresh", "url": host.server.URL + "/fresh.yaml"})
	if due == "" || fresh == "" {
		t.Fatal("the catalogues were not added")
	}
	longAgo := store.FormatTime(time.Now().Add(-48 * time.Hour))
	if _, err := h.db.Exec(t.Context(), `UPDATE template_catalogues SET attempted_at = ?, fetched_at = ? WHERE id = ?`,
		longAgo, longAgo, due); err != nil {
		t.Fatal(err)
	}
	host.set("/due.yaml", acmeIndex("registry.acme.example/wiki:4.0.0"))
	host.set("/fresh.yaml", acmeIndex("registry.acme.example/wiki:4.0.0"))
	before := len(host.requests())

	h.api.RefreshTemplateCatalogues(t.Context(), time.Now())

	requests := host.requests()[before:]
	if len(requests) != 1 || !strings.HasPrefix(requests[0], "/due.yaml") {
		t.Fatalf("the pass fetched %q; want only the catalogue that was due", requests)
	}
	if wiki, _ := find(h.listed(acme), "wiki", due); wiki.Services[0].Image != "registry.acme.example/wiki:4.0.0" {
		t.Errorf("the due catalogue was not refreshed: %s", wiki.Services[0].Image)
	}
}
