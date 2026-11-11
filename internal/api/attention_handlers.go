package api

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/cron"
	"skifity/internal/panelpath"
	"skifity/internal/store"
	"skifity/internal/tlscert"
)

// What needs somebody's attention, across a team.
//
// The overview used to answer "is anything wrong" with the cluster's
// reachability and the recovery key, and nothing else: a crashing app, a
// failed deploy, an alert going off, a backup that failed or never ran, a
// certificate that could not be issued or is about to run out — each was on
// its own page, and a team with no notification channel learned of it only by
// opening the right one. This is that list, in one answer, for the overview,
// the header's bell, `skifity status --all` and an assistant's get_attention.
//
// It is built from what the panel has already written down (see
// store/attention.go), so it asks the cluster nothing and answers the same
// with the cluster down. Each item is something a person can act on, with the
// page to do it on. It says what is wrong now, not what went wrong once: an
// alert that stopped, a deploy that failed and was followed by one that
// worked, a backup that failed and then ran — none of those is listed.

// Attention severities. An error is something broken or not happening — an
// app down, a deploy that failed, a backup that did not run; a warning is
// something heading that way.
const (
	attentionError   = "error"
	attentionWarning = "warning"
)

// Attention kinds: what kind of thing is wrong, in the order the list puts
// them within a severity.
const (
	attentionServerNotReady      = "server_not_ready"
	attentionDeployFailed        = "deploy_failed"
	attentionAppUnhealthy        = "app_unhealthy"
	attentionDatabaseUnwell      = "database_unwell"
	attentionBackupFailed        = "backup_failed"
	attentionBackupMissed        = "backup_missed"
	attentionPanelBackupFailed   = "panel_backup_failed"
	attentionDomainCertFailed    = "domain_cert_failed"
	attentionCertificateExpiring = "certificate_expiring"
	attentionAppAlert            = "app_alert"
	attentionServerAlert         = "server_alert"
	attentionAppVulnerable       = "app_vulnerable"
	attentionAppDrifted          = "app_drifted"
	attentionNoChannel           = "no_channel"
)

var attentionOrder = []string{
	attentionServerNotReady, attentionDeployFailed, attentionAppUnhealthy, attentionDatabaseUnwell,
	attentionBackupFailed, attentionBackupMissed, attentionPanelBackupFailed, attentionDomainCertFailed,
	attentionCertificateExpiring, attentionAppAlert, attentionServerAlert, attentionAppVulnerable,
	attentionAppDrifted, attentionNoChannel,
}

// backupGrace is how long after a backup was due it is called missed. The
// minute tick starts it at once and records it before it uploads anything,
// so a few minutes is only room for a tick that ran late.
const backupGrace = 10 * time.Minute

// attentionItem is one thing that needs attention.
type attentionItem struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	// Code is the sentence: attention.<code> in the panel's languages, with
	// Params filled in. Message is the same sentence in English, for the CLI
	// and an assistant.
	Code    string         `json:"code"`
	Params  map[string]any `json:"params"`
	Message string         `json:"message"`

	// The thing it is about: an app, a domain, a database, a volume, a
	// server, a certificate, the team or the panel.
	TargetType  string `json:"target_type"`
	TargetID    string `json:"target_id"`
	TargetLabel string `json:"target_label"`

	ProjectID       string `json:"project_id,omitempty"`
	ProjectName     string `json:"project_name,omitempty"`
	EnvironmentID   string `json:"environment_id,omitempty"`
	EnvironmentName string `json:"environment_name,omitempty"`

	// Since is when it started, where the panel knows: when the deploy or
	// the backup failed, when a backup was due, when the drift was found.
	// For a certificate it is when it runs out, which may be ahead.
	Since time.Time `json:"since,omitzero"`
	// Path is the panel page to deal with it on.
	Path string `json:"path"`
}

// attentionAudience is what one person may be told about.
type attentionAudience struct {
	membership store.Membership
	// teamWide is a member not limited to some projects: servers are theirs
	// to hear about.
	teamWide bool
	// settings is somebody who can open the team's settings, where its
	// certificates and notification channels are.
	settings bool
	// panelAdmin is the panel's administrator, whose the panel's own backup
	// is.
	panelAdmin bool
}

func (s *Server) handleTeamAttention(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	user, membership, err := s.authorizeTeamMember(r, teamID, store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	audience := attentionAudience{
		membership: membership,
		teamWide:   !membership.Scoped,
		// The same people the Settings page shows the team's tabs to.
		settings:   !membership.Scoped && (membership.Role.AtLeast(store.RoleAdmin) || user.IsAdmin),
		panelAdmin: user.IsAdmin,
	}
	items, err := s.attention(r.Context(), teamID, audience, time.Now())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, items)
}

// attention builds a team's list as one person may see it.
func (s *Server) attention(ctx context.Context, teamID string, who attentionAudience, now time.Time) ([]attentionItem, error) {
	var items []attentionItem
	add := func(item attentionItem) {
		if item.Params == nil {
			item.Params = map[string]any{}
		}
		items = append(items, item)
	}

	apps, err := s.db.ListAppHealth(ctx, teamID)
	if err != nil {
		return nil, err
	}
	for _, app := range apps {
		if who.membership.Reaches(app.ProjectID) {
			appAttention(app, add)
		}
	}

	domains, err := s.db.ListFailedDomains(ctx, teamID)
	if err != nil {
		return nil, err
	}
	for _, domain := range domains {
		if !who.membership.Reaches(domain.ProjectID) {
			continue
		}
		add(attentionItem{
			Kind: attentionDomainCertFailed, Severity: attentionError, Code: attentionDomainCertFailed,
			Params:     map[string]any{"hostname": domain.Hostname, "app": domain.AppName},
			Message:    fmt.Sprintf("%s, a domain of %s, has no certificate: issuing one failed.", domain.Hostname, domain.AppName),
			TargetType: "domain", TargetID: domain.ID, TargetLabel: domain.Hostname,
			ProjectID: domain.ProjectID, ProjectName: domain.ProjectName,
			EnvironmentID: domain.EnvironmentID, EnvironmentName: domain.EnvironmentName,
			Path: panelpath.App(domain.AppID, panelpath.AppDomains),
		})
	}

	databases, err := s.db.ListUnwellDatabases(ctx, teamID)
	if err != nil {
		return nil, err
	}
	for _, database := range databases {
		if !who.membership.Reaches(database.ProjectID) {
			continue
		}
		item := attentionItem{
			Kind: attentionDatabaseUnwell, Severity: attentionError, Code: "database_unhealthy",
			Params:     map[string]any{"database": database.Name},
			Message:    fmt.Sprintf("The database %s is not healthy.", database.Name),
			TargetType: "database", TargetID: database.ID, TargetLabel: database.Name,
			ProjectID: database.ProjectID, ProjectName: database.ProjectName,
			EnvironmentID: database.EnvironmentID, EnvironmentName: database.EnvironmentName,
			Path: panelpath.Database(database.ID, ""),
		}
		if database.Status == "failed" {
			item.Code = "database_failed"
			item.Message = fmt.Sprintf("The database %s failed.", database.Name)
		}
		add(item)
	}

	backups, err := s.db.ListScheduledBackups(ctx, teamID)
	if err != nil {
		return nil, err
	}
	for _, scheduled := range backups {
		if who.membership.Reaches(scheduled.ProjectID) {
			backupAttention(scheduled, now, add)
		}
	}

	if who.teamWide {
		servers, err := s.db.ListServerHealth(ctx, teamID)
		if err != nil {
			return nil, err
		}
		for _, server := range servers {
			serverAttention(server, add)
		}
	}

	if who.settings {
		certificates, err := s.db.ListCertificates(ctx, teamID)
		if err != nil {
			return nil, err
		}
		for _, certificate := range certificates {
			certificateAttention(certificate, now, add)
		}

		channels, err := s.db.ListNotificationChannels(ctx, teamID)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(channels, func(c store.NotificationChannel) bool { return c.Enabled }) {
			add(attentionItem{
				Kind: attentionNoChannel, Severity: attentionWarning, Code: attentionNoChannel,
				Message:    "Nobody is told when something breaks: the team has no notification channel.",
				TargetType: "team", TargetID: teamID,
				Path: panelpath.Settings(panelpath.SettingsNotifications),
			})
		}
	}

	// The panel's own copy of its database, which is the administrator's
	// whichever team they are looking at. Recorded under the target "panel"
	// (backup.PanelTarget, which this package cannot import).
	if who.panelAdmin {
		latest, err := s.db.ListBackups(ctx, "panel", "panel", 1)
		if err != nil {
			return nil, err
		}
		if len(latest) == 1 && latest[0].Status == "failed" {
			add(attentionItem{
				Kind: attentionPanelBackupFailed, Severity: attentionError, Code: attentionPanelBackupFailed,
				Message:    "The panel's last backup of itself failed, so the copy in storage is older than it should be.",
				TargetType: "panel", TargetID: latest[0].ID,
				Since: firstSet(latest[0].FinishedAt, latest[0].CreatedAt),
				Path:  panelpath.Settings(panelpath.SettingsPanel),
			})
		}
	}

	sortAttention(items)
	return items, nil
}

// appAttention adds what is wrong with one app.
func appAttention(app store.AppHealth, add func(attentionItem)) {
	base := func(kind, severity, code, message string) attentionItem {
		return attentionItem{
			Kind: kind, Severity: severity, Code: code, Message: message,
			Params:     map[string]any{"app": app.AppName},
			TargetType: "app", TargetID: app.AppID, TargetLabel: app.AppName,
			ProjectID: app.ProjectID, ProjectName: app.ProjectName,
			EnvironmentID: app.EnvironmentID, EnvironmentName: app.EnvironmentName,
			Path: panelpath.App(app.AppID, ""),
		}
	}

	// A deploy under way is somebody dealing with it, and the watcher leaves
	// an app alone while one runs; so does this.
	if !app.Deploying {
		switch {
		case app.Finished.Status == store.DeployFailed:
			item := base(attentionDeployFailed, attentionError, attentionDeployFailed,
				fmt.Sprintf("Deployment #%d of %s failed.", app.Finished.Number, app.AppName))
			item.Params["number"] = app.Finished.Number
			item.Since = app.Finished.FinishedAt
			item.Path = panelpath.AppDeployment(app.AppID, app.Finished.ID)
			add(item)
		case app.Status == "unhealthy":
			add(base(attentionAppUnhealthy, attentionError, attentionAppUnhealthy,
				app.AppName+" has no instance running."))
		}
	}

	if len(app.Firing) > 0 {
		item := base(attentionAppAlert, attentionWarning, attentionAppAlert,
			fmt.Sprintf("%s is over its alert threshold for %s.", app.AppName, alertWords(app.Firing)))
		item.Params["alerts"] = strings.Join(app.Firing, ",")
		add(item)
	}

	if app.Critical > 0 && !app.Accepted {
		item := base(attentionAppVulnerable, attentionWarning, attentionAppVulnerable,
			fmt.Sprintf("%s runs an image with %d critical %s.", app.AppName, app.Critical,
				plural(app.Critical, "vulnerability", "vulnerabilities")))
		item.Params["count"] = app.Critical
		item.Since = app.ScannedAt
		item.Path = panelpath.App(app.AppID, panelpath.AppSecurity)
		add(item)
	}

	if app.Drift == store.DriftDrifted || app.Drift == store.DriftMissing {
		item := base(attentionAppDrifted, attentionWarning, attentionAppDrifted,
			app.AppName+" was changed outside the panel, and no longer matches what it applied.")
		item.Since = app.DriftSince
		item.Path = panelpath.App(app.AppID, panelpath.AppAdvanced)
		add(item)
	}
}

// backupAttention adds what is wrong with one target's scheduled backups:
// one that was due and not taken, or else the newest having failed. A
// backup started after it was due, whichever way it ended, was not missed;
// one still running has not failed yet; and one skipped because the database
// was stopped is where somebody put it.
func backupAttention(scheduled store.ScheduledBackup, now time.Time, add func(attentionItem)) {
	target := scheduled.TargetName
	item := attentionItem{
		Params:     map[string]any{"target": target},
		TargetType: scheduled.Policy.TargetType, TargetID: scheduled.Policy.TargetID, TargetLabel: target,
		ProjectID: scheduled.ProjectID, ProjectName: scheduled.ProjectName,
		EnvironmentID: scheduled.EnvironmentID, EnvironmentName: scheduled.EnvironmentName,
		Path: panelpath.Database(scheduled.Policy.TargetID, panelpath.DatabaseBackups),
	}
	if scheduled.Policy.TargetType == "volume" {
		target = scheduled.AppName + " / " + scheduled.TargetName
		item.Params["target"], item.TargetLabel = target, target
		item.Path = panelpath.App(scheduled.AppID, panelpath.AppStorage)
	}

	latest := scheduled.Latest
	due, ok := cron.LastDue(scheduled.Policy.Schedule, scheduled.Policy.UpdatedAt, now.Add(-backupGrace))
	switch {
	case ok && (latest.ID == "" || latest.CreatedAt.Before(due)):
		item.Kind, item.Severity, item.Code = attentionBackupMissed, attentionError, attentionBackupMissed
		item.Message = fmt.Sprintf("%s was not backed up when it was due, at %s.", target, due.Format("15:04 MST on 2 January"))
		item.Since = due
	case latest.Status == "failed":
		item.Kind, item.Severity, item.Code = attentionBackupFailed, attentionError, attentionBackupFailed
		item.Message = "The last backup of " + target + " failed."
		item.Since = firstSet(latest.FinishedAt, latest.CreatedAt)
	default:
		return
	}
	add(item)
}

// serverAttention adds what is wrong with one server. One being added or
// removed is somebody's operation under way, with its own page.
func serverAttention(server store.ServerHealth, add func(attentionItem)) {
	base := func(kind, severity, code, message string) attentionItem {
		return attentionItem{
			Kind: kind, Severity: severity, Code: code, Message: message,
			Params:     map[string]any{"server": server.Name},
			TargetType: "server", TargetID: server.ID, TargetLabel: server.Name,
			Path: panelpath.Server(server.ID),
		}
	}
	switch server.Status {
	case store.ServerNotReady:
		add(base(attentionServerNotReady, attentionError, "server_not_ready",
			"The server "+server.Name+" is not ready: the cluster has stopped hearing from it."))
	case store.ServerFailed:
		add(base(attentionServerNotReady, attentionError, "server_failed",
			"Adding the server "+server.Name+" failed."))
	}
	if len(server.Firing) > 0 {
		item := base(attentionServerAlert, attentionWarning, attentionServerAlert,
			fmt.Sprintf("The server %s is over its alert threshold for %s.", server.Name, alertWords(server.Firing)))
		item.Params["alerts"] = strings.Join(server.Firing, ",")
		add(item)
	}
}

// certificateAttention adds a certificate the team brought that runs out
// soon, or has. It counts the way the warnings sent about it count, so the
// overview and the message in somebody's inbox say the same number of days.
func certificateAttention(certificate store.Certificate, now time.Time, add func(attentionItem)) {
	state := tlscert.State(certificate.NotAfter, now)
	if state == tlscert.StateValid {
		return
	}
	left := certificate.NotAfter.Sub(now)
	days := int((left + 24*time.Hour - 1) / (24 * time.Hour))
	item := attentionItem{
		Kind: attentionCertificateExpiring, Severity: attentionWarning, Code: "certificate_expiring",
		Params:     map[string]any{"certificate": certificate.Name, "count": days},
		Message:    fmt.Sprintf("The certificate %s runs out in %d %s.", certificate.Name, days, plural(days, "day", "days")),
		TargetType: "certificate", TargetID: certificate.ID, TargetLabel: certificate.Name,
		Since: certificate.NotAfter,
		Path:  panelpath.Settings(panelpath.SettingsCertificates),
	}
	switch {
	case state == tlscert.StateExpired:
		item.Severity, item.Code = attentionError, "certificate_expired"
		item.Params = map[string]any{"certificate": certificate.Name}
		item.Message = "The certificate " + certificate.Name + " has run out."
	case left <= 24*time.Hour:
		item.Severity = attentionError
	}
	add(item)
}

// sortAttention puts errors first, then each kind together in a fixed order,
// then by project and by name, so the list does not reshuffle from one look
// to the next.
func sortAttention(items []attentionItem) {
	rank := func(kind string) int {
		if i := slices.Index(attentionOrder, kind); i >= 0 {
			return i
		}
		return len(attentionOrder)
	}
	slices.SortStableFunc(items, func(a, b attentionItem) int {
		return cmp.Or(
			cmp.Compare(severityRank(a.Severity), severityRank(b.Severity)),
			cmp.Compare(rank(a.Kind), rank(b.Kind)),
			strings.Compare(strings.ToLower(a.ProjectName), strings.ToLower(b.ProjectName)),
			strings.Compare(strings.ToLower(a.TargetLabel), strings.ToLower(b.TargetLabel)),
			strings.Compare(a.Code, b.Code),
		)
	})
}

func severityRank(severity string) int {
	if severity == attentionError {
		return 0
	}
	return 1
}

// alertWords names alerts going off the way the usage cards do.
func alertWords(names []string) string {
	words := map[string]string{"errors": "server errors", "cpu": "CPU"}
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, cmp.Or(words[name], name))
	}
	return strings.Join(out, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// firstSet is the first time that is not zero.
func firstSet(times ...time.Time) time.Time {
	for _, t := range times {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}
