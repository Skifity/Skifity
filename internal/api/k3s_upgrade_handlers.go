package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/settings"
)

// Upgrading what the installer installed. See internal/cluster/upgrades.go
// for the work and internal/kube/k3supgrade.go for the plan.

// upgradeBackupAge is how recent a backup of the panel's database has to be
// before k3s is upgraded: the thing to restore from if the upgrade takes the
// panel's own server with it.
const upgradeBackupAge = 24 * time.Hour

func (s *Server) handleUpgradeComponent(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	def, ok := settings.LookupComponent(name)
	if !ok || def.External {
		writeError(w, r, errdoc.NotFound("component", name))
		return
	}
	if s.cluster == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	before, _ := s.cluster.ComponentStatus(r.Context(), name)
	if err := s.cluster.UpgradeInstalledComponent(r.Context(), name); err != nil {
		writeError(w, r, err)
		return
	}
	after, err := s.cluster.ComponentStatus(context.WithoutCancel(r.Context()), name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if after.Version != before.Version {
		s.audit(r, "", "component.upgraded", "component", name, name+" "+before.Version+" → "+after.Version)
	}
	writeJSON(w, http.StatusOK, after)
}

type k3sUpgradeAnswer struct {
	Nodes    []kube.UpgradeNode `json:"nodes"`
	Releases []K3sRelease       `json:"releases"`
	// ReleasesError says why the releases could not be listed; a version can
	// still be typed.
	ReleasesError string               `json:"releases_error,omitempty"`
	Plan          *kube.K3sUpgradePlan `json:"plan,omitempty"`
}

func (s *Server) handleK3sUpgradePlan(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	nodes, err := s.cluster.K3sUpgradeNodes(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	answer := k3sUpgradeAnswer{Nodes: nodes, Releases: []K3sRelease{}}
	if releases, err := s.cluster.K3sReleases(r.Context()); err != nil {
		answer.ReleasesError = err.Error()
	} else {
		answer.Releases = releases
	}
	if target := strings.TrimSpace(r.URL.Query().Get("version")); target != "" {
		plan := s.k3sPlan(r, nodes, target)
		answer.Plan = &plan
	}
	writeJSON(w, http.StatusOK, answer)
}

type startK3sUpgradeRequest struct {
	Version string `json:"version"`
}

func (s *Server) handleStartK3sUpgrade(w http.ResponseWriter, r *http.Request) {
	var req startK3sUpgradeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	nodes, err := s.cluster.K3sUpgradeNodes(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	// The plan again, not the one the form showed: a server that went down
	// since, or a backup that aged past a day, is a reason now.
	plan := s.k3sPlan(r, nodes, req.Version)
	if len(plan.Blockers) > 0 {
		writeError(w, r, errdoc.K3sUpgradeBlocked(strings.Join(kube.Texts(plan.Blockers), "; ")))
		return
	}
	if plan.Nothing {
		writeJSON(w, http.StatusOK, plan)
		return
	}
	target, _ := kube.ParseK3sVersion(plan.Target)
	if err := s.cluster.StartK3sUpgrade(r.Context(), target); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "cluster.k3s_upgrade_started", "cluster", "k3s", plan.Target)
	writeJSON(w, http.StatusAccepted, plan)
}

// k3sPlan is the planner's answer with the panel's own condition added: a
// recent backup of its database.
func (s *Server) k3sPlan(r *http.Request, nodes []kube.UpgradeNode, target string) kube.K3sUpgradePlan {
	plan := kube.PlanK3sUpgrade(nodes, target)
	if plan.Nothing {
		return plan
	}
	backups, err := s.db.ListBackups(r.Context(), panelBackupTarget, panelBackupTarget, 20)
	recent := false
	if err == nil {
		for _, backup := range backups {
			if backup.Status == "succeeded" && time.Since(backup.CreatedAt) < upgradeBackupAge {
				recent = true
				break
			}
		}
	}
	if !recent {
		plan.Blockers = append(plan.Blockers, kube.UpgradeReason{Code: "no_backup",
			Text: "there is no backup of the panel's own database from the last day; take one under Backups of this panel in Settings, " +
				"since it is what the panel is restored from if an upgrade takes its server with it"})
	}
	// Plans somebody applied by hand, as the k3s documentation shows, would
	// upgrade the same servers beside the panel's: two at once, or to two
	// versions. Not being able to tell is a reason not to start, too.
	foreign, err := s.cluster.ForeignUpgradePlans(r.Context())
	switch {
	case err != nil:
		plan.Blockers = append(plan.Blockers, kube.UpgradeReason{Code: "plans_unreadable",
			Params: map[string]string{"error": err.Error()},
			Text:   "the upgrade plans already in the cluster could not be read: " + err.Error()})
	case len(foreign) > 0:
		names := strings.Join(foreign, " ")
		plan.Blockers = append(plan.Blockers, kube.UpgradeReason{Code: "other_plans",
			Params: map[string]string{"plans": names},
			Text: "upgrade plans the panel did not write are already in the cluster (" + names + "); two sets would upgrade " +
				"servers side by side. Remove them first: kubectl -n system-upgrade delete plans.upgrade.cattle.io " + names})
	}
	return plan
}
