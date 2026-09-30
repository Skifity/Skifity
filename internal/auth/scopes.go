package auth

import (
	"fmt"
	"net/http"
	"strings"
)

// What a token is allowed to touch.
//
// # Why this is not just read and write
//
// It was, and for a token somebody makes for their own script that is enough.
// It is not enough the moment a token is handed to something else. A plugin
// that backs up databases and a plugin that provisions servers would carry the
// same "write" token, so installing the first would be granting the second's
// powers to it — and "this plugin may only read your apps" would be a sentence
// on a screen that nothing anywhere enforced.
//
// So a scope names a resource as well as a direction: `apps:read`,
// `backups:write`. The check stays in one middleware rather than in each
// handler, because a scope enforced per handler is a scope that stops being
// enforced the day somebody adds a route and does not think about it.
//
// # The three things that are deliberately not scopes
//
// There is no scope that grants the master key, because there is no route that
// exposes it. There is no scope for another team's data, because the team check
// is separate and runs anyway — a scope widens nothing. And there is no
// `*:write` wildcard: a token that should be able to do everything is a token
// with no scopes at all, which is the older, plainer way of saying it.

// Actions a scope may grant.
const (
	// ScopeRead allows requests that only read, whatever the resource.
	ScopeRead = "read"
	// ScopeWrite allows everything else, whatever the resource.
	ScopeWrite = "write"
)

// Resources a scope may name. The list is closed: a scope naming something
// that is not here is refused when the token is made, so a token is never
// narrower than its owner believes and never wider.
const (
	ResourceAccount     = "account"
	ResourceTeams       = "teams"
	ResourceServers     = "servers"
	ResourceProjects    = "projects"
	ResourceApps        = "apps"
	ResourceDatabases   = "databases"
	ResourceBackups     = "backups"
	ResourceDeployments = "deployments"
	ResourceOperations  = "operations"
	ResourceTemplates   = "templates"
	ResourceEvents      = "events"
	ResourceSettings    = "settings"
)

// Resources is every resource a scope may name, in the order the panel lists
// them when somebody is choosing.
var Resources = []string{
	ResourceApps, ResourceDeployments, ResourceDatabases, ResourceBackups,
	ResourceProjects, ResourceServers, ResourceTemplates, ResourceOperations,
	ResourceEvents, ResourceTeams, ResourceSettings, ResourceAccount,
}

// routeResources maps a path to the resource it belongs to.
//
// Ordered, longest first, and matched by prefix: a nested collection has to win
// over the route it hangs off, or `/api/environments/{id}/apps` would be judged
// as a project and a token that may only read apps could not list them.
//
// Anything not matched here has no resource, and a scoped token cannot reach
// it at all. That is the fail-closed direction on purpose: a route added later
// and not thought about is refused to scoped tokens rather than quietly
// falling into whichever scope happened to be nearest.
var routeResources = []struct{ prefix, resource string }{
	{"/api/environments/", ""}, // decided below by what comes after the id
	{"/api/me", ResourceAccount},
	{"/api/teams", ResourceTeams},
	{"/api/servers", ResourceServers},
	{"/api/projects", ResourceProjects},
	{"/api/apps", ResourceApps},
	{"/api/databases", ResourceDatabases},
	{"/api/operations", ResourceOperations},
	{"/api/templates", ResourceTemplates},
	{"/api/events", ResourceEvents},
	{"/api/settings", ResourceSettings},
	{"/api/components", ResourceSettings},
	{"/api/audit", ResourceTeams},
}

// nestedResources are the collections that hang off another route and belong to
// themselves rather than to their parent.
var nestedResources = map[string]string{
	"apps":        ResourceApps,
	"databases":   ResourceDatabases,
	"deployments": ResourceDeployments,
	"backups":     ResourceBackups,
}

// ResourceFor returns the resource a request path belongs to, or "" when the
// path is one no scope covers.
func ResourceFor(path string) string {
	path = "/" + strings.Trim(path, "/")

	// A nested collection wins over its parent: /apps/{id}/deployments is about
	// deployments, and a token that may read deployments and not apps should
	// still be refused the app itself.
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segments) >= 4 {
		if resource, ok := nestedResources[segments[3]]; ok {
			return resource
		}
	}

	for _, entry := range routeResources {
		if entry.resource == "" {
			continue
		}
		if path == strings.TrimSuffix(entry.prefix, "/") || strings.HasPrefix(path, entry.prefix+"/") ||
			strings.HasPrefix(path, entry.prefix) && isBoundary(path, entry.prefix) {
			return entry.resource
		}
	}
	// An environment is part of a project, once its nested collections have
	// been taken out above.
	if strings.HasPrefix(path, "/api/environments") {
		return ResourceProjects
	}
	return ""
}

// isBoundary stops "/api/apps" matching "/api/appstore".
func isBoundary(path, prefix string) bool {
	if len(path) == len(prefix) {
		return true
	}
	return path[len(prefix)] == '/'
}

// TokenAllows reports whether a token's scopes permit a request.
//
// An empty scope list is full access, which is what every token issued before
// scopes existed carries and what somebody who wants a token for themselves
// still gets.
func TokenAllows(scopes, method, path string) bool {
	scopes = strings.TrimSpace(scopes)
	if scopes == "" {
		return true
	}
	readOnly := method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
	resource := ResourceFor(path)

	for _, scope := range strings.Split(scopes, ",") {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		named, action, scoped := strings.Cut(scope, ":")
		if !scoped {
			// The older, unscoped form: read or write, anywhere.
			if scope == ScopeWrite || (scope == ScopeRead && readOnly) {
				return true
			}
			continue
		}
		if resource == "" || named != resource {
			continue
		}
		if action == ScopeWrite || (action == ScopeRead && readOnly) {
			return true
		}
	}
	return false
}

// ValidateScopes rejects a scope the panel would not enforce, so a token is
// never narrower than its owner believes.
func ValidateScopes(scopes string) error {
	if strings.TrimSpace(scopes) == "" {
		return nil
	}
	for _, scope := range strings.Split(scopes, ",") {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if scope == ScopeRead || scope == ScopeWrite {
			continue
		}
		named, action, scoped := strings.Cut(scope, ":")
		if !scoped {
			return fmt.Errorf(
				"%q is not a scope; it is read, write, or a resource and an action such as apps:read", scope)
		}
		if action != ScopeRead && action != ScopeWrite {
			return fmt.Errorf("%q is not an action; it is %q or %q", action, ScopeRead, ScopeWrite)
		}
		if !KnownResource(named) {
			return fmt.Errorf("%q is not something a scope can name; it is one of %s",
				named, strings.Join(Resources, ", "))
		}
	}
	return nil
}

// KnownResource reports whether a scope may name a resource.
func KnownResource(name string) bool {
	for _, resource := range Resources {
		if resource == name {
			return true
		}
	}
	return false
}
