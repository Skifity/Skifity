package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// What the panel last recorded about the state of a team's things, read for
// the overview's list of what needs attention.
//
// Nothing here asks the cluster. The watcher, the deployer, the backup
// manager and the scanner already write down what they found — an app's
// status, its firing alerts, its drift, a backup's outcome — and these read
// that back across the whole team in a few queries, so the list costs the
// same with the cluster down as up. Previews are left out: a pull request's
// build is reported on the pull request, and a preview comes and goes with it.

// AppHealth is one app's recorded state.
type AppHealth struct {
	AppID           string
	AppName         string
	Status          string
	ProjectID       string
	ProjectName     string
	EnvironmentID   string
	EnvironmentName string

	// Deploying is a deployment of it under way.
	Deploying bool
	// Finished is its newest deployment that succeeded or failed; a
	// cancelled or superseded one is not an outcome. Empty ID for none.
	Finished AppHealthDeployment

	// Firing is which of its usage alerts are going off.
	Firing []string
	// Drift is the watcher's last comparison with the cluster, and DriftSince
	// when it stopped matching.
	Drift      string
	DriftSince time.Time

	// Critical is the critical vulnerabilities the newest scan of the image
	// it runs found, and ScannedAt when that scan finished. Accepted is the
	// deployment that put that image out having gone ahead past them.
	Critical  int
	ScannedAt time.Time
	Accepted  bool
}

// AppHealthDeployment is a deployment's number and outcome.
type AppHealthDeployment struct {
	ID         string
	Number     int
	Status     DeploymentStatus
	ErrorCode  string
	FinishedAt time.Time
}

// ListAppHealth returns every app in a team's standard environments.
func (db *DB) ListAppHealth(ctx context.Context, teamID string) ([]AppHealth, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.id, a.name, a.status, p.id, p.name, e.id, e.name,
			EXISTS (SELECT 1 FROM deployments WHERE app_id = a.id AND status IN ('queued','building','deploying')),
			COALESCE(fin.id, ''), COALESCE(fin.number, 0), COALESCE(fin.status, ''),
			COALESCE(fin.error_code, ''), COALESCE(fin.finished_at, ''),
			COALESCE(al.firing, ''), COALESCE(dr.status, ''), COALESCE(dr.since, ''),
			COALESCE(sc.critical, 0), COALESCE(sc.finished_at, ''), COALESCE(live.accepted_vulnerabilities, 0)
		FROM apps a
		JOIN environments e ON e.id = a.environment_id
		JOIN projects p ON p.id = e.project_id
		LEFT JOIN deployments fin ON fin.id = (
			SELECT id FROM deployments WHERE app_id = a.id AND status IN ('succeeded','failed')
			ORDER BY number DESC LIMIT 1)
		LEFT JOIN deployments live ON live.id = (
			SELECT id FROM deployments WHERE app_id = a.id AND status = 'succeeded' AND image != ''
			ORDER BY number DESC LIMIT 1)
		LEFT JOIN image_scans sc ON sc.id = (
			SELECT id FROM image_scans
			WHERE app_id = a.id AND status = 'succeeded' AND image = live.image
			ORDER BY created_at DESC, id DESC LIMIT 1)
		LEFT JOIN app_alerts al ON al.app_id = a.id
		LEFT JOIN app_drift dr ON dr.app_id = a.id
		WHERE p.team_id = ? AND e.kind = 'standard'
		ORDER BY p.name COLLATE NOCASE, e.name COLLATE NOCASE, a.name COLLATE NOCASE, a.id`, teamID)
	if err != nil {
		return nil, fmt.Errorf("read the team's apps' state: %w", err)
	}
	defer rows.Close()
	out := []AppHealth{}
	for rows.Next() {
		var h AppHealth
		var finishedStatus, finishedAt, firing, driftSince, scannedAt string
		if err := rows.Scan(&h.AppID, &h.AppName, &h.Status, &h.ProjectID, &h.ProjectName,
			&h.EnvironmentID, &h.EnvironmentName, &h.Deploying,
			&h.Finished.ID, &h.Finished.Number, &finishedStatus, &h.Finished.ErrorCode, &finishedAt,
			&firing, &h.Drift, &driftSince, &h.Critical, &scannedAt, &h.Accepted); err != nil {
			return nil, fmt.Errorf("scan an app's state: %w", err)
		}
		h.Finished.Status = DeploymentStatus(finishedStatus)
		h.Finished.FinishedAt, _ = ParseTime(finishedAt)
		h.DriftSince, _ = ParseTime(driftSince)
		h.ScannedAt, _ = ParseTime(scannedAt)
		if firing != "" {
			h.Firing = strings.Split(firing, ",")
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// FailedDomain is a domain whose certificate could not be issued.
type FailedDomain struct {
	ID              string
	Hostname        string
	Detail          string
	AppID           string
	AppName         string
	ProjectID       string
	ProjectName     string
	EnvironmentID   string
	EnvironmentName string
}

// ListFailedDomains returns the domains in a team's standard environments
// whose certificate the watcher found had failed.
func (db *DB) ListFailedDomains(ctx context.Context, teamID string) ([]FailedDomain, error) {
	rows, err := db.QueryContext(ctx, `SELECT d.id, d.hostname, d.status_detail, a.id, a.name,
			p.id, p.name, e.id, e.name
		FROM domains d
		JOIN apps a ON a.id = d.app_id
		JOIN environments e ON e.id = a.environment_id
		JOIN projects p ON p.id = e.project_id
		WHERE p.team_id = ? AND e.kind = 'standard' AND d.status = 'failed'
		ORDER BY d.hostname`, teamID)
	if err != nil {
		return nil, fmt.Errorf("read the team's failed domains: %w", err)
	}
	defer rows.Close()
	out := []FailedDomain{}
	for rows.Next() {
		var d FailedDomain
		if err := rows.Scan(&d.ID, &d.Hostname, &d.Detail, &d.AppID, &d.AppName,
			&d.ProjectID, &d.ProjectName, &d.EnvironmentID, &d.EnvironmentName); err != nil {
			return nil, fmt.Errorf("scan a failed domain: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DatabaseHealth is a database whose recorded status is not one it is meant
// to be in.
type DatabaseHealth struct {
	ID              string
	Name            string
	Status          string
	Detail          string
	UpdatedAt       time.Time
	ProjectID       string
	ProjectName     string
	EnvironmentID   string
	EnvironmentName string
}

// ListUnwellDatabases returns the databases in a team's standard
// environments that failed or are recorded as not healthy. Creating,
// starting, running and stopped are each where somebody put it.
func (db *DB) ListUnwellDatabases(ctx context.Context, teamID string) ([]DatabaseHealth, error) {
	rows, err := db.QueryContext(ctx, `SELECT d.id, d.name, d.status, d.status_detail, d.updated_at,
			p.id, p.name, e.id, e.name
		FROM databases d
		JOIN environments e ON e.id = d.environment_id
		JOIN projects p ON p.id = e.project_id
		WHERE p.team_id = ? AND e.kind = 'standard'
			AND d.status IN ('failed','degraded','missing','unhealthy')
		ORDER BY d.name COLLATE NOCASE, d.id`, teamID)
	if err != nil {
		return nil, fmt.Errorf("read the team's databases' state: %w", err)
	}
	defer rows.Close()
	out := []DatabaseHealth{}
	for rows.Next() {
		var d DatabaseHealth
		var updated string
		if err := rows.Scan(&d.ID, &d.Name, &d.Status, &d.Detail, &updated,
			&d.ProjectID, &d.ProjectName, &d.EnvironmentID, &d.EnvironmentName); err != nil {
			return nil, fmt.Errorf("scan a database's state: %w", err)
		}
		d.UpdatedAt, _ = ParseTime(updated)
		out = append(out, d)
	}
	return out, rows.Err()
}

// ScheduledBackup is an enabled backup policy, with what it backs up, where
// that lives, and the newest backup of it.
type ScheduledBackup struct {
	Policy BackupPolicy
	// TargetName is the database's name, or the volume's.
	TargetName string
	// AppID and AppName are the app a volume belongs to; empty for a
	// database.
	AppID           string
	AppName         string
	ProjectID       string
	ProjectName     string
	EnvironmentID   string
	EnvironmentName string
	// Latest is the newest backup of the target, of any kind; empty ID for
	// none yet.
	Latest Backup
}

// ListScheduledBackups returns every enabled backup policy of a team's
// databases and volumes in its standard environments.
func (db *DB) ListScheduledBackups(ctx context.Context, teamID string) ([]ScheduledBackup, error) {
	const latest = `LEFT JOIN backups b ON b.id = (
			SELECT id FROM backups WHERE target_type = bp.target_type AND target_id = bp.target_id
			ORDER BY created_at DESC LIMIT 1)`
	const columns = `bp.id, bp.target_type, bp.target_id, bp.schedule, bp.updated_at,
			COALESCE(b.id, ''), COALESCE(b.status, ''), COALESCE(b.kind, ''), COALESCE(b.error_message, ''),
			COALESCE(b.created_at, ''), COALESCE(b.finished_at, ''),
			p.id, p.name, e.id, e.name`
	rows, err := db.QueryContext(ctx, `SELECT `+columns+`, d.name, '', ''
		FROM backup_policies bp
		JOIN databases d ON d.id = bp.target_id
		JOIN environments e ON e.id = d.environment_id
		JOIN projects p ON p.id = e.project_id
		`+latest+`
		WHERE bp.target_type = 'database' AND bp.enabled = 1 AND p.team_id = ? AND e.kind = 'standard'
		UNION ALL
		SELECT `+columns+`, v.name, a.id, a.name
		FROM backup_policies bp
		JOIN volumes v ON v.id = bp.target_id
		JOIN apps a ON a.id = v.app_id
		JOIN environments e ON e.id = a.environment_id
		JOIN projects p ON p.id = e.project_id
		`+latest+`
		WHERE bp.target_type = 'volume' AND bp.enabled = 1 AND p.team_id = ? AND e.kind = 'standard'`,
		teamID, teamID)
	if err != nil {
		return nil, fmt.Errorf("read the team's backup schedules: %w", err)
	}
	defer rows.Close()
	out := []ScheduledBackup{}
	for rows.Next() {
		var s ScheduledBackup
		var updated, created, finished string
		if err := rows.Scan(&s.Policy.ID, &s.Policy.TargetType, &s.Policy.TargetID, &s.Policy.Schedule, &updated,
			&s.Latest.ID, &s.Latest.Status, &s.Latest.Kind, &s.Latest.ErrorMessage, &created, &finished,
			&s.ProjectID, &s.ProjectName, &s.EnvironmentID, &s.EnvironmentName,
			&s.TargetName, &s.AppID, &s.AppName); err != nil {
			return nil, fmt.Errorf("scan a backup schedule: %w", err)
		}
		s.Policy.Enabled = true
		s.Policy.UpdatedAt, _ = ParseTime(updated)
		s.Latest.CreatedAt, _ = ParseTime(created)
		s.Latest.FinishedAt, _ = ParseTime(finished)
		if s.Latest.ID != "" {
			s.Latest.TargetType, s.Latest.TargetID = s.Policy.TargetType, s.Policy.TargetID
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ServerHealth is a server's status and which of its usage alerts are going
// off.
type ServerHealth struct {
	ID     string
	Name   string
	Status ServerStatus
	Detail string
	Firing []string
}

// ListServerHealth returns every server in a team.
func (db *DB) ListServerHealth(ctx context.Context, teamID string) ([]ServerHealth, error) {
	rows, err := db.QueryContext(ctx, `SELECT s.id, s.name, s.status, s.status_detail, COALESCE(al.firing, '')
		FROM servers s
		LEFT JOIN server_alerts al ON al.server_id = s.id
		WHERE s.team_id = ?
		ORDER BY s.name COLLATE NOCASE, s.id`, teamID)
	if err != nil {
		return nil, fmt.Errorf("read the team's servers' state: %w", err)
	}
	defer rows.Close()
	out := []ServerHealth{}
	for rows.Next() {
		var s ServerHealth
		var firing string
		if err := rows.Scan(&s.ID, &s.Name, &s.Status, &s.Detail, &firing); err != nil {
			return nil, fmt.Errorf("scan a server's state: %w", err)
		}
		if firing != "" {
			s.Firing = strings.Split(firing, ",")
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
