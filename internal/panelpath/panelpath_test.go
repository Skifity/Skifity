package panelpath

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The tab is in the query string, because the panel routes /apps/{id} and
// nothing below it: a path with the tab in it is the not-found page.
func TestATabIsAQueryAndNeverAPath(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{App("app_1", AppDeployments), "/apps/app_1?tab=deployments"},
		{App("app_1", AppOverview), "/apps/app_1"},
		{App("app_1", ""), "/apps/app_1"},
		{AppDeployment("app_1", "dep_2"), "/apps/app_1?tab=deployments&deployment=dep_2"},
		{Database("db_1", DatabaseBackups), "/databases/db_1?tab=backups"},
		{Database("db_1", ""), "/databases/db_1"},
		{DatabaseBackup("db_1", "bak_3"), "/databases/db_1?tab=backups&backup=bak_3"},
		{Server("srv_1"), "/servers/srv_1"},
		{Settings(SettingsCertificates), "/settings?tab=certificates"},
		{Settings(""), "/settings"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

// web is the panel's source, beside this package's.
var web = filepath.Join("..", "..", "web", "src")

func readWeb(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(web, name))
	if err != nil {
		t.Fatalf("read the panel's %s: %v", name, err)
	}
	return string(body)
}

// Every address built here is one the panel serves. A notification, a commit
// status and a pull request comment linked to /apps/{id}/deployments for as
// long as the SPA had no such route, and every one of them opened "Page not
// found" at the moment somebody clicked it because something had broken.
func TestEveryAddressIsARouteThePanelServes(t *testing.T) {
	routes := spaRoutes(t)
	addresses := []string{Server("srv_1"), Settings(""), DatabaseBackup("db_1", "bak_1"), AppDeployment("app_1", "dep_1")}
	for _, tab := range AppTabs {
		addresses = append(addresses, App("app_1", tab))
	}
	for _, tab := range DatabaseTabs {
		addresses = append(addresses, Database("db_1", tab))
	}
	for _, tab := range SettingsTabs {
		addresses = append(addresses, Settings(tab))
	}
	for _, address := range addresses {
		path, _, _ := strings.Cut(address, "?")
		if !servedBy(routes, path) {
			t.Errorf("%s is not a route of the panel's (App.tsx has %v)", address, routes)
		}
	}
}

// Every tab named here is a tab its page has, by the value the page's tabs
// are switched with; a query naming one it does not have opens nothing.
func TestEveryTabIsOneItsPageHas(t *testing.T) {
	for _, page := range []struct {
		file string
		tabs []string
		// How the page declares a tab.
		declared string
	}{
		{"pages/app-detail.tsx", AppTabs, `<TabsTrigger value="%s">`},
		{"pages/database-detail.tsx", DatabaseTabs, `<TabsTrigger value="%s">`},
		{"pages/settings.tsx", SettingsTabs, `value: "%s"`},
	} {
		source := readWeb(t, page.file)
		for _, tab := range page.tabs {
			if !strings.Contains(source, strings.Replace(page.declared, "%s", tab, 1)) {
				t.Errorf("%s has no tab %q, which this package links to", page.file, tab)
			}
		}
	}
}

// The query parameters that pick one thing out of a tab are ones the tab
// reads: a link to a deployment that opened the list with nothing open, or to
// a backup in a list nobody marked, would look like it worked.
func TestThePagesReadTheParametersLinkedTo(t *testing.T) {
	for file, parameter := range map[string]string{
		"components/app/deployments-tab.tsx": "deployment",
		"pages/database-detail.tsx":          "backup",
		"pages/app-detail.tsx":               "tab",
	} {
		if !strings.Contains(readWeb(t, file), `.get("`+parameter+`")`) {
			t.Errorf("%s does not read ?%s=, which this package links to", file, parameter)
		}
	}
}

// Nothing outside this package writes a panel address by hand. That is how
// /apps/{id}/deployments and /apps/{id}/domains were written, in three places,
// and how every producer that links somewhere is now held to the tests above.
//
// The patterns are what a hand-built address looks like: a page and an id
// joined with +, a tab written into a query, or a literal page in a Path
// field. The cloud providers' own APIs have /servers/{id} of their own and are
// not the panel's; anything under /api/ is the panel's API, not a page.
func TestNoAddressIsBuiltByHandElsewhere(t *testing.T) {
	handBuilt := []*regexp.Regexp{
		regexp.MustCompile(`"/(apps|servers|databases|projects|environments)/"\s*\+`),
		regexp.MustCompile(`"[^"]*\?tab=`),
		regexp.MustCompile(`Path:\s*"/(apps|servers|databases|projects|environments|settings)`),
	}
	skip := map[string]bool{
		filepath.Join("..", "panelpath"): true,
		filepath.Join("..", "cloud"):     true,
	}
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skip[path] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for number, line := range strings.Split(string(body), "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "//") || strings.Contains(code, "/api/") {
				continue
			}
			for _, pattern := range handBuilt {
				if pattern.MatchString(code) {
					t.Errorf("%s:%d builds a panel address by hand; use panelpath: %s", path, number+1, code)
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read the Go sources: %v", err)
	}
}

// spaRoutes reads the route patterns App.tsx declares, without the leading
// slash: "apps/:appId", "settings".
func spaRoutes(t *testing.T) []string {
	t.Helper()
	declared := regexp.MustCompile(`path="([^"]+)"`)
	var routes []string
	for _, match := range declared.FindAllStringSubmatch(readWeb(t, "App.tsx"), -1) {
		if match[1] != "*" {
			routes = append(routes, strings.TrimPrefix(match[1], "/"))
		}
	}
	if len(routes) < 5 {
		t.Fatalf("App.tsx declares %v, so this test has stopped reading its routes", routes)
	}
	return routes
}

// servedBy reports whether a path matches one of the routes, a ":name"
// segment matching any one segment.
func servedBy(routes []string, path string) bool {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for _, route := range routes {
		pattern := strings.Split(route, "/")
		if len(pattern) != len(segments) {
			continue
		}
		matched := true
		for i, part := range pattern {
			if !strings.HasPrefix(part, ":") && part != segments[i] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
