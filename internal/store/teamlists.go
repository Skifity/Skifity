package store

import (
	"context"
	"fmt"
	"time"
)

// Lists that cover a whole team rather than one environment.
//
// Apps live under environments, and every list of them used to be one
// environment's: a page that wanted the team's apps asked for its projects,
// then each project's environments, then each environment's apps. These answer
// in one query, with where each thing lives, so nothing has to walk the tree.

// TeamApp is an app with the project and environment it lives in and how its
// newest deploy went.
type TeamApp struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Slug            string          `json:"slug"`
	SourceType      string          `json:"source_type"`
	Status          string          `json:"status"`
	ProjectID       string          `json:"project_id"`
	ProjectName     string          `json:"project_name"`
	EnvironmentID   string          `json:"environment_id"`
	EnvironmentName string          `json:"environment_name"`
	EnvironmentKind EnvironmentKind `json:"environment_kind"`
	// URL is the address a person opens: a domain of the team's own when
	// there is one, otherwise the one the panel gave it. Empty for an
	// internal app, which has no public address, and for one with no domain.
	URL string `json:"url,omitempty"`
	// LastDeployment is the newest deployment, whatever became of it; absent
	// for an app never deployed.
	LastDeployment *TeamAppDeployment `json:"last_deployment,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
}

// TeamAppDeployment is the part of a deployment a list has room for.
type TeamAppDeployment struct {
	ID        string           `json:"id"`
	Number    int              `json:"number"`
	Status    DeploymentStatus `json:"status"`
	CommitSHA string           `json:"commit_sha,omitempty"`
	// At is when it was asked for; FinishedAt when it ended, if it has.
	At         time.Time `json:"at"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
}

// ListTeamApps returns every app in a team, previews included, by project,
// then environment, then name.
//
// The address is chosen the way a commit status's link is: the first domain
// of the team's own, else the panel's own; a domain that only redirects
// elsewhere is not where the app is, so it comes last.
func (db *DB) ListTeamApps(ctx context.Context, teamID string) ([]TeamApp, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.id, a.name, a.slug, a.source_type, a.status, a.created_at,
			p.id, p.name, e.id, e.name, e.kind,
			CASE WHEN a.internal THEN '' ELSE COALESCE((
				SELECT CASE WHEN d.tls THEN 'https://' ELSE 'http://' END || d.hostname || RTRIM(d.path, '/')
				FROM domains d WHERE d.app_id = a.id
				ORDER BY d.redirect_to <> '', d.auto, d.hostname LIMIT 1), '') END,
			COALESCE(dep.id, ''), COALESCE(dep.number, 0), COALESCE(dep.status, ''),
			COALESCE(dep.commit_sha, ''), COALESCE(dep.created_at, ''), COALESCE(dep.finished_at, '')
		FROM apps a
		JOIN environments e ON e.id = a.environment_id
		JOIN projects p ON p.id = e.project_id
		LEFT JOIN deployments dep ON dep.id = (
			SELECT id FROM deployments WHERE app_id = a.id ORDER BY number DESC LIMIT 1)
		WHERE p.team_id = ?
		ORDER BY p.name COLLATE NOCASE, p.id, e.kind <> 'standard', e.name COLLATE NOCASE, e.id,
			a.name COLLATE NOCASE, a.id`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list the team's apps: %w", err)
	}
	defer rows.Close()
	out := []TeamApp{}
	for rows.Next() {
		var a TeamApp
		var created, depID, depStatus, depCommit, depAt, depFinished string
		var depNumber int
		if err := rows.Scan(&a.ID, &a.Name, &a.Slug, &a.SourceType, &a.Status, &created,
			&a.ProjectID, &a.ProjectName, &a.EnvironmentID, &a.EnvironmentName, &a.EnvironmentKind,
			&a.URL, &depID, &depNumber, &depStatus, &depCommit, &depAt, &depFinished); err != nil {
			return nil, fmt.Errorf("scan a team's app: %w", err)
		}
		a.CreatedAt, _ = ParseTime(created)
		if depID != "" {
			deployment := TeamAppDeployment{ID: depID, Number: depNumber,
				Status: DeploymentStatus(depStatus), CommitSHA: depCommit}
			deployment.At, _ = ParseTime(depAt)
			deployment.FinishedAt, _ = ParseTime(depFinished)
			a.LastDeployment = &deployment
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
