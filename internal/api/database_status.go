package api

import (
	"fmt"
	"regexp"

	"skifity/internal/store"
)

// The sentences a database's status line carries that never vary. They are
// written here, once, so the view below can name them: the database service
// writes them, and the panel reads them back by key, as it does a server's.
const (
	DatabaseDetailObjectGone    = "The database's Kubernetes object is gone."
	DatabaseDetailWaiting       = "Waiting for the database to accept connections."
	DatabaseDetailStartingAgain = "Starting again on the disk it kept."
)

// NoClusterDetail is what a database's line, an app's status and the cluster
// summary say on a panel with no cluster connection at all: one sentence, keyed
// "noCluster" wherever it is shown.
const NoClusterDetail = "The panel is not connected to a cluster."

// DatabaseDetailDegraded is the line of a database some of whose instances
// are not ready. Down to one, it says what that one failing would mean.
func DatabaseDetailDegraded(ready, wanted int64) string {
	if ready == 1 {
		return fmt.Sprintf("%d of %d instances are ready. The database is answering, but a failure of the one that is left is an outage.", ready, wanted)
	}
	return fmt.Sprintf("%d of %d instances are ready.", ready, wanted)
}

var degradedDetail = regexp.MustCompile(`^(\d+) of (\d+) instances are ready\.( The database is answering)?`)

// databaseView is a database as the API shows it: the row, and the key its
// status line is translated by. The line itself stays the English sentence the
// CLI and an assistant read; a line the cluster wrote has no key and is shown
// as it came.
type databaseView struct {
	store.Database
	StatusKey  string   `json:"status_key,omitempty"`
	StatusArgs []string `json:"status_args,omitempty"`
}

func viewDatabase(record store.Database) databaseView {
	view := databaseView{Database: record}
	switch record.StatusDetail {
	case "":
	case NoClusterDetail:
		view.StatusKey = "noCluster"
	case DatabaseDetailObjectGone:
		view.StatusKey = "objectGone"
	case DatabaseDetailWaiting:
		view.StatusKey = "waiting"
	case DatabaseDetailStartingAgain:
		view.StatusKey = "startingAgain"
	default:
		if match := degradedDetail.FindStringSubmatch(record.StatusDetail); match != nil {
			view.StatusKey = "degraded"
			if match[3] != "" {
				view.StatusKey = "degradedLastOne"
			}
			view.StatusArgs = []string{match[1], match[2]}
		}
	}
	return view
}
