package api

import (
	"slices"
	"testing"

	"skifity/internal/store"
)

// A database's status line is stored in English and read back by key, so the
// panel can say it in five languages. A sentence the database service writes
// that the view cannot name would be English on every page.
func TestADatabasesStatusLineIsNamedForTheInterface(t *testing.T) {
	cases := []struct {
		detail string
		key    string
		args   []string
	}{
		{NoClusterDetail, "noCluster", nil},
		{DatabaseDetailObjectGone, "objectGone", nil},
		{DatabaseDetailWaiting, "waiting", nil},
		{DatabaseDetailStartingAgain, "startingAgain", nil},
		{DatabaseDetailDegraded(2, 3), "degraded", []string{"2", "3"}},
		{DatabaseDetailDegraded(1, 3), "degradedLastOne", []string{"1", "3"}},
		{"", "", nil},
		// Whatever the cluster said is shown as it came.
		{"pods \"db-0\" is forbidden", "", nil},
	}
	for _, c := range cases {
		view := viewDatabase(store.Database{StatusDetail: c.detail})
		if view.StatusKey != c.key || !slices.Equal(view.StatusArgs, c.args) {
			t.Errorf("%q: key %q args %v, want %q %v", c.detail, view.StatusKey, view.StatusArgs, c.key, c.args)
		}
	}
}
