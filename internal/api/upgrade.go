package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"skifity/internal/errdoc"
)

// versionPattern is what a release tag looks like. Anything else is refused
// rather than passed to the cluster, because this value becomes part of an
// image reference.
var versionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.\-]+)?$`)

// upgradePanel rolls the panel forward to another version, and returns the
// image it replaced.
//
// The panel upgrades itself by changing the image on its own Deployment and
// letting Kubernetes roll it out. The apps it manages keep running throughout,
// because nothing about them depends on the panel being up.
//
// The panel itself does not have that luxury. Its Deployment uses the Recreate
// strategy — one copy at a time, because the database is a file on one node's
// disk — so the running panel is stopped before the new one starts, and
// Kubernetes never rolls a Deployment back on its own. If the new image does
// not come up there is no panel left to fix it, which is why the caller is
// handed the previous image and told the command that undoes this.
func (s *Server) upgradePanel(r *http.Request, target string) (upgradeStarted, error) {
	var started upgradeStarted
	if target == "" {
		return started, errdoc.BadRequest("Enter the version to upgrade to, for example v1.2.0.")
	}
	if !versionPattern.MatchString(target) {
		return started, errdoc.BadRequest(fmt.Sprintf("%q is not a version number. Use a release tag such as v1.2.0.", target))
	}
	if s.cluster == nil {
		return started, errdoc.ClusterUnreachable(nil)
	}

	upgrader, ok := s.cluster.(interface {
		UpgradePanel(ctx contextType, version string) (string, error)
	})
	if !ok {
		return started, errdoc.New("upgrade.unsupported", "This panel cannot upgrade itself").
			WithCause("The panel is not running inside a cluster it can update, which is normal in development.").
			WithImpact("Nothing was changed.").
			WithFix("Upgrade by pulling the new image and restarting, or re-run the installer with the version you want.").
			WithStatus(http.StatusBadRequest)
	}

	snapshot, err := s.snapshotBeforeUpgrade(r, target)
	if err != nil {
		return started, err
	}
	started.Snapshot = snapshot
	// And off the server, when there is a bucket: the snapshot is on the
	// disk that an upgrade gone badly wrong may be the reason to leave.
	if s.backups != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		backup, err := s.backups.BackupPanel(ctx, "pre-upgrade")
		cancel()
		var problem *errdoc.Problem
		switch {
		case err == nil:
			started.OffSite = backup.Location
		case errors.As(err, &problem) && problem.Code == errdoc.StorageNotConfigured().Code:
		default:
			s.log.Warn("could not copy the panel off the server before upgrading", "error", err)
		}
	}

	previous, err := upgrader.UpgradePanel(r.Context(), target)
	if err != nil {
		return started, errdoc.New("upgrade.failed", "The upgrade could not be started").
			WithCause("%s", err.Error()).
			WithImpact("The panel is still running the current version.").
			WithFix("Check that the version exists and that the cluster can pull its image.").
			WithStatus(http.StatusBadGateway).Retry()
	}
	started.Previous = previous
	return started, nil
}

// upgradeStarted is what an upgrade leaves behind to go back with.
type upgradeStarted struct {
	// Previous is the image the panel ran before.
	Previous string
	// Snapshot is the copy of the database taken beside it, and OffSite the
	// one in the backup bucket, when there is one.
	Snapshot, OffSite string
}

// keptUpgradeSnapshots is how many copies taken before an upgrade stay beside
// the database. The newest is the one a rollback wants; two more cover an
// upgrade that was followed by another before anybody noticed.
const keptUpgradeSnapshots = 3

// snapshotBeforeUpgrade copies the database beside itself before the image
// changes, and says where.
//
// The new version migrates the database when it starts, and a database a
// later version has migrated is one the earlier version refuses to open. So
// rolling the image back is only half of going back: the other half is this
// copy. Taken with the panel still running, through SQLite, so it is whole.
// An upgrade that cannot take it does not start.
func (s *Server) snapshotBeforeUpgrade(r *http.Request, target string) (string, error) {
	current := s.db.Path()
	if current == "" || current == ":memory:" {
		return "", nil
	}
	// Named for the upgrade and not only for the time, so a copy that
	// `restore-db` kept aside is never one this clears out.
	stamp := time.Now().UTC().Format("20060102-150405.000000")
	path := fmt.Sprintf("%s.before-upgrade-%s-to-%s", current, stamp, target)
	if err := s.db.Snapshot(r.Context(), path); err != nil {
		return "", errdoc.New("upgrade.no_snapshot", "The database could not be copied before upgrading").
			WithCause("%s", err.Error()).
			WithImpact("The upgrade was not started. The panel is still running the current version.").
			WithFix("Make room on the disk the database is on, then upgrade again.").
			WithStatus(http.StatusInsufficientStorage)
	}
	// The timestamp comes first in the name, so sorted by name is oldest
	// first.
	older, _ := filepath.Glob(current + ".before-upgrade-*")
	sort.Strings(older)
	for len(older) > keptUpgradeSnapshots {
		_ = os.Remove(older[0])
		older = older[1:]
	}
	return path, nil
}
