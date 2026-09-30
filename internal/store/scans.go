package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Scanning an app's image for known vulnerabilities. What runs the scan is
// internal/vulnscan and internal/deploy; this is what is kept of it.

// Scan states. A scan is queued until the one scanner the panel runs at a time
// is free, then running, then one of the two ends.
const (
	ScanQueued    = "queued"
	ScanRunning   = "running"
	ScanSucceeded = "succeeded"
	ScanFailed    = "failed"
)

// What started a scan. A token the interface translates, never a sentence.
const (
	// ScanTriggerDeploy is a deployment's image, scanned when it was built or
	// deployed.
	ScanTriggerDeploy = "deploy"
	// ScanTriggerSchedule is the daily rescan of what every app runs.
	ScanTriggerSchedule = "schedule"
	// ScanTriggerManual is somebody pressing Scan now, or `skifity scan --now`.
	ScanTriggerManual = "manual"
)

// ScansKeptPerApp is how many finished scans an app keeps. A scan runs at
// least daily, so this is about three weeks of history, which is what the
// question "since when has this been there" needs and not much more.
const ScansKeptPerApp = 20

// ScanCounts is how many vulnerabilities of each severity a scan found.
type ScanCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Unknown  int `json:"unknown"`
}

// Total is every vulnerability counted, whatever its severity.
func (c ScanCounts) Total() int { return c.Critical + c.High + c.Medium + c.Low + c.Unknown }

// ScanFinding is one known vulnerability in one package of an image.
type ScanFinding struct {
	// ID is the advisory's identifier: a CVE, a GHSA, a distribution's own.
	ID        string `json:"id"`
	Package   string `json:"package"`
	Installed string `json:"installed"`
	// FixedIn is the version, or versions, that fix it. Empty when nobody has
	// published a fix yet, which is the difference between "update this" and
	// "know about this".
	FixedIn  string `json:"fixed_in,omitempty"`
	Severity string `json:"severity"`
	Title    string `json:"title,omitempty"`
	// URL is the advisory's page. Only ever an https address.
	URL string `json:"url,omitempty"`
	// Target is where in the image the package is: the operating system, or
	// the lockfile or directory a language package was found in.
	Target string `json:"target,omitempty"`
}

// ScanResult is what a finished scan found.
type ScanResult struct {
	// Digest is the image's manifest digest, sha256:..., when the report
	// named one: a tag can move, and this says exactly what was looked at.
	Digest          string
	Counts          ScanCounts
	Fixable         int
	FixableCritical int
	// Findings are the most severe first, at most MaxFindings of them.
	Findings []ScanFinding
	// Omitted is how many more there were than Findings holds.
	Omitted        int
	ScannerVersion string
	OS             string
}

// ImageScan is one scan of an app's image.
type ImageScan struct {
	ID           string `json:"id"`
	AppID        string `json:"app_id"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Image        string `json:"image"`
	Digest       string `json:"digest,omitempty"`
	Trigger      string `json:"trigger"`
	Status       string `json:"status"`

	Counts          ScanCounts    `json:"counts"`
	Fixable         int           `json:"fixable"`
	FixableCritical int           `json:"fixable_critical"`
	Findings        []ScanFinding `json:"findings"`
	Omitted         int           `json:"omitted"`
	ScannerVersion  string        `json:"scanner_version,omitempty"`
	OS              string        `json:"os,omitempty"`

	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	ErrorHint    string `json:"error_hint,omitempty"`

	RequestedBy string    `json:"requested_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	StartedAt   time.Time `json:"started_at,omitzero"`
	FinishedAt  time.Time `json:"finished_at,omitzero"`
}

// Finished reports whether a scan has stopped, one way or the other.
func (s ImageScan) Finished() bool { return s.Status == ScanSucceeded || s.Status == ScanFailed }

const imageScanColumns = `id, app_id, COALESCE(deployment_id,''), image, digest, trigger, status,
	critical, high, medium, low, unknown, fixable, fixable_critical, findings, omitted,
	scanner_version, os, error_code, error_message, error_hint, requested_by,
	created_at, started_at, finished_at`

func scanImageScan(row interface{ Scan(...any) error }) (ImageScan, error) {
	var s ImageScan
	var findings, created string
	var started, finished sql.NullString
	err := row.Scan(&s.ID, &s.AppID, &s.DeploymentID, &s.Image, &s.Digest, &s.Trigger, &s.Status,
		&s.Counts.Critical, &s.Counts.High, &s.Counts.Medium, &s.Counts.Low, &s.Counts.Unknown,
		&s.Fixable, &s.FixableCritical, &findings, &s.Omitted,
		&s.ScannerVersion, &s.OS, &s.ErrorCode, &s.ErrorMessage, &s.ErrorHint, &s.RequestedBy,
		&created, &started, &finished)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s, ErrNotFound
		}
		return s, fmt.Errorf("scan image scan: %w", err)
	}
	s.Findings = []ScanFinding{}
	if findings != "" {
		if err := json.Unmarshal([]byte(findings), &s.Findings); err != nil {
			return s, fmt.Errorf("read the findings of scan %s: %w", s.ID, err)
		}
	}
	s.CreatedAt, _ = ParseTime(created)
	s.StartedAt = scanTime(started)
	s.FinishedAt = scanTime(finished)
	return s, nil
}

// CreateImageScan records a scan waiting to run, whatever else the app has
// queued: a deploy that waits for its own image to be scanned needs a scan of
// that image, and not the nightly one of the image before it.
func (db *DB) CreateImageScan(ctx context.Context, scan *ImageScan) error {
	if scan.ID == "" {
		scan.ID = NewID("scan")
	}
	now := Now()
	if _, err := db.Exec(ctx, `INSERT INTO image_scans
		(id, app_id, deployment_id, image, trigger, status, requested_by, created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		scan.ID, scan.AppID, NullString(scan.DeploymentID), scan.Image, scan.Trigger, ScanQueued,
		scan.RequestedBy, now); err != nil {
		return fmt.Errorf("record an image scan: %w", err)
	}
	scan.Status = ScanQueued
	scan.Findings = []ScanFinding{}
	scan.CreatedAt, _ = ParseTime(now)
	return nil
}

// QueueImageScan records a scan waiting to run, unless the app already has one
// queued or running: then that one is put in scan and nothing is added, so
// pressing Scan now twice, or a deploy landing during the nightly rescan, is
// one scan and not two in a row of the same image. created says which.
func (db *DB) QueueImageScan(ctx context.Context, scan *ImageScan) (created bool, err error) {
	if scan.ID == "" {
		scan.ID = NewID("scan")
	}
	now := Now()
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		existing, err := scanImageScan(tx.QueryRowContext(ctx, `SELECT `+imageScanColumns+` FROM image_scans
			WHERE app_id = ? AND status IN ('queued','running')
			ORDER BY created_at DESC, id DESC LIMIT 1`, scan.AppID))
		if err == nil {
			*scan = existing
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO image_scans
			(id, app_id, deployment_id, image, trigger, status, requested_by, created_at)
			VALUES (?,?,?,?,?,?,?,?)`,
			scan.ID, scan.AppID, NullString(scan.DeploymentID), scan.Image, scan.Trigger, ScanQueued,
			scan.RequestedBy, now); err != nil {
			return fmt.Errorf("queue an image scan: %w", err)
		}
		scan.Status = ScanQueued
		scan.Findings = []ScanFinding{}
		scan.CreatedAt, _ = ParseTime(now)
		created = true
		return nil
	})
	return created, mapError(err)
}

// StartImageScan marks a queued scan as running.
func (db *DB) StartImageScan(ctx context.Context, id string) error {
	if _, err := db.Exec(ctx, `UPDATE image_scans SET status = 'running', started_at = ?
		WHERE id = ? AND status = 'queued'`, Now(), id); err != nil {
		return fmt.Errorf("start an image scan: %w", err)
	}
	return nil
}

// FinishImageScan records what a scan found, and lets go of the oldest scans
// the app no longer needs.
func (db *DB) FinishImageScan(ctx context.Context, id string, result ScanResult) error {
	findings := result.Findings
	if findings == nil {
		findings = []ScanFinding{}
	}
	encoded, err := json.Marshal(findings)
	if err != nil {
		return fmt.Errorf("record the findings: %w", err)
	}
	return db.Tx(ctx, func(tx *sql.Tx) error {
		var appID string
		if err := tx.QueryRowContext(ctx, `SELECT app_id FROM image_scans WHERE id = ?`, id).Scan(&appID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// The app was deleted while its image was being scanned.
				return ErrNotFound
			}
			return fmt.Errorf("find the image scan: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE image_scans SET status = 'succeeded', digest = ?,
			critical = ?, high = ?, medium = ?, low = ?, unknown = ?, fixable = ?, fixable_critical = ?,
			findings = ?, omitted = ?, scanner_version = ?, os = ?,
			error_code = '', error_message = '', error_hint = '',
			started_at = COALESCE(started_at, ?), finished_at = ?
			WHERE id = ?`,
			result.Digest, result.Counts.Critical, result.Counts.High, result.Counts.Medium,
			result.Counts.Low, result.Counts.Unknown, result.Fixable, result.FixableCritical,
			string(encoded), result.Omitted, result.ScannerVersion, result.OS, Now(), Now(), id); err != nil {
			return fmt.Errorf("record the image scan: %w", err)
		}
		return pruneImageScans(ctx, tx, appID)
	})
}

// FailImageScan records a scan that did not produce a report, and why.
func (db *DB) FailImageScan(ctx context.Context, id, code, message, hint string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		var appID string
		if err := tx.QueryRowContext(ctx, `SELECT app_id FROM image_scans WHERE id = ?`, id).Scan(&appID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("find the image scan: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE image_scans SET status = 'failed',
			error_code = ?, error_message = ?, error_hint = ?, finished_at = ?
			WHERE id = ? AND status IN ('queued','running')`,
			code, message, hint, Now(), id); err != nil {
			return fmt.Errorf("record the failed image scan: %w", err)
		}
		return pruneImageScans(ctx, tx, appID)
	})
}

// pruneImageScans keeps an app's newest finished scans and removes the rest,
// except the newest one its team was told about, which the next is compared
// with: twenty stopped deploys in a row must not make an old critical news.
func pruneImageScans(ctx context.Context, tx *sql.Tx, appID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM image_scans
		WHERE app_id = ? AND status IN ('succeeded','failed') AND id NOT IN (
			SELECT id FROM image_scans WHERE app_id = ? AND status IN ('succeeded','failed')
			ORDER BY created_at DESC, id DESC LIMIT ?)
		AND id NOT IN (
			SELECT id FROM image_scans WHERE app_id = ? AND status = 'succeeded' AND announced = 1
			ORDER BY created_at DESC, id DESC LIMIT 1)`,
		appID, appID, ScansKeptPerApp, appID); err != nil {
		return fmt.Errorf("prune image scans: %w", err)
	}
	return nil
}

// GetImageScan looks a scan up by id.
func (db *DB) GetImageScan(ctx context.Context, id string) (ImageScan, error) {
	return scanImageScan(db.QueryRowContext(ctx, `SELECT `+imageScanColumns+` FROM image_scans WHERE id = ?`, id))
}

// LatestImageScan is an app's newest scan, whatever state it is in.
func (db *DB) LatestImageScan(ctx context.Context, appID string) (ImageScan, error) {
	return scanImageScan(db.QueryRowContext(ctx, `SELECT `+imageScanColumns+` FROM image_scans
		WHERE app_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, appID))
}

// LatestSucceededImageScan is an app's newest scan that produced a report.
func (db *DB) LatestSucceededImageScan(ctx context.Context, appID string) (ImageScan, error) {
	return scanImageScan(db.QueryRowContext(ctx, `SELECT `+imageScanColumns+` FROM image_scans
		WHERE app_id = ? AND status = 'succeeded' ORDER BY created_at DESC, id DESC LIMIT 1`, appID))
}

// SucceededImageScanSince is the newest report on one image of an app that
// finished at or after a time, so a deploy of an image scanned an hour ago
// reads that answer rather than asking again.
func (db *DB) SucceededImageScanSince(ctx context.Context, appID, image string, since time.Time) (ImageScan, error) {
	return scanImageScan(db.QueryRowContext(ctx, `SELECT `+imageScanColumns+` FROM image_scans
		WHERE app_id = ? AND image = ? AND status = 'succeeded' AND finished_at >= ?
		ORDER BY finished_at DESC, id DESC LIMIT 1`, appID, image, FormatTime(since)))
}

// LatestAnnouncedImageScan is the newest report on an app that its team has
// been told about: what the next report's criticals are new against.
func (db *DB) LatestAnnouncedImageScan(ctx context.Context, appID string) (ImageScan, error) {
	return scanImageScan(db.QueryRowContext(ctx, `SELECT `+imageScanColumns+` FROM image_scans
		WHERE app_id = ? AND status = 'succeeded' AND announced = 1
		ORDER BY created_at DESC, id DESC LIMIT 1`, appID))
}

// MarkImageScanAnnounced records that a report has been through the question
// of what is new in it, and reports whether this call was the one that did —
// so a report is announced once, however many deploys of its image read it.
func (db *DB) MarkImageScanAnnounced(ctx context.Context, id string) (bool, error) {
	result, err := db.Exec(ctx, `UPDATE image_scans SET announced = 1
		WHERE id = ? AND status = 'succeeded' AND announced = 0`, id)
	if err != nil {
		return false, fmt.Errorf("mark an image scan as announced: %w", err)
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// ScanCandidate is an app that runs an image, as the daily rescan sees it.
type ScanCandidate struct {
	AppID        string
	AppName      string
	TeamID       string
	ProjectID    string
	DeploymentID string
	// Image is what the app runs: its newest deployment that succeeded.
	Image string
	// LastScanned is when that image last finished a scan, either way; zero
	// when it never has.
	LastScanned time.Time
	// Pending is a scan of the app already queued or running.
	Pending bool
}

// ListScanCandidates returns every app that runs an image, with when that
// image was last looked at.
//
// An app whose newest deployment failed is still here: the version before it
// is the one serving, and it is the one worth scanning.
func (db *DB) ListScanCandidates(ctx context.Context) ([]ScanCandidate, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.id, a.name, p.team_id, p.id, d.id, d.image,
			COALESCE((SELECT MAX(s.finished_at) FROM image_scans s
				WHERE s.app_id = a.id AND s.image = d.image AND s.status IN ('succeeded','failed')), ''),
			EXISTS (SELECT 1 FROM image_scans s WHERE s.app_id = a.id AND s.status IN ('queued','running'))
		FROM apps a
		JOIN environments e ON e.id = a.environment_id
		JOIN projects p ON p.id = e.project_id
		JOIN deployments d ON d.id = (
			SELECT id FROM deployments
			WHERE app_id = a.id AND status = 'succeeded' AND image != ''
			ORDER BY number DESC LIMIT 1)
		ORDER BY a.created_at, a.id`)
	if err != nil {
		return nil, fmt.Errorf("list the apps to scan: %w", err)
	}
	defer rows.Close()
	out := []ScanCandidate{}
	for rows.Next() {
		var c ScanCandidate
		var scanned string
		if err := rows.Scan(&c.AppID, &c.AppName, &c.TeamID, &c.ProjectID, &c.DeploymentID, &c.Image,
			&scanned, &c.Pending); err != nil {
			return nil, fmt.Errorf("scan an app to scan: %w", err)
		}
		c.LastScanned, _ = ParseTime(scanned)
		out = append(out, c)
	}
	return out, rows.Err()
}

// LatestScanCounts is, for each app in an environment that has been scanned,
// what its newest report counted.
func (db *DB) LatestScanCounts(ctx context.Context, environmentID string) (map[string]ScanCounts, error) {
	rows, err := db.QueryContext(ctx, `SELECT s.app_id, s.critical, s.high, s.medium, s.low, s.unknown
		FROM image_scans s
		JOIN apps a ON a.id = s.app_id
		WHERE a.environment_id = ? AND s.status = 'succeeded' AND s.id = (
			SELECT id FROM image_scans
			WHERE app_id = s.app_id AND status = 'succeeded'
			ORDER BY created_at DESC, id DESC LIMIT 1)`, environmentID)
	if err != nil {
		return nil, fmt.Errorf("read the apps' scans: %w", err)
	}
	defer rows.Close()
	out := map[string]ScanCounts{}
	for rows.Next() {
		var appID string
		var c ScanCounts
		if err := rows.Scan(&appID, &c.Critical, &c.High, &c.Medium, &c.Low, &c.Unknown); err != nil {
			return nil, fmt.Errorf("scan an app's scan: %w", err)
		}
		out[appID] = c
	}
	return out, rows.Err()
}

// FailInterruptedImageScans marks the scans a restart caught queued or running
// as failed, because the goroutine that would have finished them is gone.
func (db *DB) FailInterruptedImageScans(ctx context.Context, code, message, hint string) (int64, error) {
	result, err := db.Exec(ctx, `UPDATE image_scans SET status = 'failed',
		error_code = ?, error_message = ?, error_hint = ?, finished_at = ?
		WHERE status IN ('queued','running')`, code, message, hint, Now())
	if err != nil {
		return 0, fmt.Errorf("settle interrupted image scans: %w", err)
	}
	return result.RowsAffected()
}
