package panelpath

import "testing"

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
		{Server("srv_1"), "/servers/srv_1"},
		{Settings(SettingsCertificates), "/settings?tab=certificates"},
		{Settings(""), "/settings"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}
