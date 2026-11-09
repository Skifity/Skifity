// Package panelpath builds the addresses of the panel's own pages.
//
// The panel is a single-page app with a handful of routes — /apps/{id},
// /databases/{id}, /servers/{id}, /settings — and the tab a page opens on is in
// its query string, never in its path: /apps/{id}?tab=deployments is a page,
// /apps/{id}/deployments is "Page not found". Anything the server hands out
// that a person will click — a notification, a commit status, an item on the
// overview — is built here, so the shape is written down once.
package panelpath

import (
	"net/url"
	"strings"
)

// The tabs of an app's page, as its TabsTrigger values name them.
const (
	AppOverview    = "overview"
	AppDeployments = "deployments"
	AppDomains     = "domains"
	AppSecurity    = "security"
	AppStorage     = "storage"
	AppAdvanced    = "advanced"
)

// The tabs of a database's page.
const (
	DatabaseBackups = "backups"
)

// The tabs of Settings.
const (
	SettingsPanel         = "panel"
	SettingsCertificates  = "certificates"
	SettingsNotifications = "notifications"
)

// App is an app's page, open on a tab. The overview is the page's own address.
func App(id, tab string) string {
	if tab == AppOverview {
		tab = ""
	}
	return withQuery("/apps/"+url.PathEscape(id), "tab", tab)
}

// AppDeployment is an app's Deployments tab with one deployment open.
func AppDeployment(id, deploymentID string) string {
	return withQuery("/apps/"+url.PathEscape(id), "tab", AppDeployments, "deployment", deploymentID)
}

// Database is a database's page, open on a tab.
func Database(id, tab string) string {
	return withQuery("/databases/"+url.PathEscape(id), "tab", tab)
}

// Server is a server's page.
func Server(id string) string {
	return "/servers/" + url.PathEscape(id)
}

// Settings is Settings, open on a tab.
func Settings(tab string) string {
	return withQuery("/settings", "tab", tab)
}

// withQuery adds the pairs that have a value, in the order given: the tab
// first, so an address reads the way the page is laid out.
func withQuery(path string, pairs ...string) string {
	var query []string
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			query = append(query, url.QueryEscape(pairs[i])+"="+url.QueryEscape(pairs[i+1]))
		}
	}
	if len(query) == 0 {
		return path
	}
	return path + "?" + strings.Join(query, "&")
}
