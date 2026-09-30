package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"skifity/internal/tlscert"
)

// Certificate is one a team brought for its own hostnames. Its private key is
// never part of it: the only query that reads the key is CertificateMaterial,
// which the deployer calls to write it into the cluster.
type Certificate struct {
	ID     string `json:"id"`
	TeamID string `json:"team_id"`
	Name   string `json:"name"`
	// Hostnames are the names the certificate is for, wildcards as written.
	Hostnames   []string  `json:"hostnames"`
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	Fingerprint string    `json:"fingerprint"`
	KeyType     string    `json:"key_type"`
	SelfSigned  bool      `json:"self_signed"`
	ChainLength int       `json:"chain_length"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// ChainPEM is public — every visitor is sent it — and long, and nothing
	// that lists certificates needs it. It is read by CertificateMaterial and
	// written by SaveCertificate.
	ChainPEM string `json:"-"`
	// ExpiryNotified is the smallest threshold, in days before expiry, a
	// warning has been sent for; 0 when none has. See tlscert.DueThreshold.
	ExpiryNotified int `json:"-"`
}

// Candidate is the certificate as matching sees it.
func (c Certificate) Candidate() tlscert.Candidate {
	return tlscert.Candidate{ID: c.ID, Hostnames: c.Hostnames, NotAfter: c.NotAfter}
}

// CertificateFor picks, from a team's certificates, the one a hostname is
// served with. See tlscert.Best for which one that is.
func CertificateFor(certificates []Certificate, hostname string) (Certificate, bool) {
	candidates := make([]tlscert.Candidate, len(certificates))
	for i, certificate := range certificates {
		candidates[i] = certificate.Candidate()
	}
	if i := tlscert.Best(hostname, candidates); i >= 0 {
		return certificates[i], true
	}
	return Certificate{}, false
}

// CertificateContext is what a certificate's private key is sealed under: the
// team and the certificate, so a key copied to another row does not open.
func CertificateContext(teamID, id string) string { return "certificate:" + teamID + ":" + id }

const certificateColumns = `id, team_id, name, hostnames, subject, issuer, not_before, not_after,
	fingerprint, key_type, self_signed, chain_length, expiry_notified, created_at, updated_at`

func scanCertificate(row interface{ Scan(...any) error }) (Certificate, error) {
	var c Certificate
	var hostnames, notBefore, notAfter, created, updated string
	if err := row.Scan(&c.ID, &c.TeamID, &c.Name, &hostnames, &c.Subject, &c.Issuer, &notBefore, &notAfter,
		&c.Fingerprint, &c.KeyType, &c.SelfSigned, &c.ChainLength, &c.ExpiryNotified, &created, &updated); err != nil {
		return Certificate{}, err
	}
	if err := json.Unmarshal([]byte(hostnames), &c.Hostnames); err != nil {
		return Certificate{}, fmt.Errorf("read the hostnames of certificate %s: %w", c.ID, err)
	}
	c.NotBefore, _ = ParseTime(notBefore)
	c.NotAfter, _ = ParseTime(notAfter)
	c.CreatedAt, _ = ParseTime(created)
	c.UpdatedAt, _ = ParseTime(updated)
	return c, nil
}

// SaveCertificate stores a certificate, or replaces the one of the same name
// in the team, keeping its id. sealedKey is the private key sealed under
// CertificateContext with the id c carries — which, for a replacement, has to
// be the id the stored one already has; CertificateByName is how the caller
// finds it. A replacement puts the expiry warnings back to none sent.
//
// It reports whether a certificate was replaced. Two uploads of the same name
// racing each other are told apart by the id: the second one's id does not
// match and it answers ErrConflict, rather than storing a key sealed under a
// context its row will not have.
func (db *DB) SaveCertificate(ctx context.Context, c *Certificate, sealedKey string) (bool, error) {
	if c.ID == "" || sealedKey == "" || c.ChainPEM == "" {
		return false, errors.New("a certificate is saved with its id, its chain and its sealed key")
	}
	hostnames, err := json.Marshal(c.Hostnames)
	if err != nil {
		return false, err
	}
	now := Now()
	replaced := false
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		var existing string
		switch err := tx.QueryRowContext(ctx, `SELECT id FROM certificates WHERE team_id = ? AND name = ?`,
			c.TeamID, c.Name).Scan(&existing); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("look for the certificate %q: %w", c.Name, err)
		case existing != c.ID:
			return fmt.Errorf("%w: another upload of %q was saved first", ErrConflict, c.Name)
		default:
			replaced = true
		}
		if replaced {
			_, err := tx.ExecContext(ctx, `UPDATE certificates SET chain_pem = ?, key_enc = ?, hostnames = ?,
				subject = ?, issuer = ?, not_before = ?, not_after = ?, fingerprint = ?, key_type = ?,
				self_signed = ?, chain_length = ?, expiry_notified = 0, updated_at = ?
				WHERE id = ? AND team_id = ?`,
				c.ChainPEM, sealedKey, string(hostnames), c.Subject, c.Issuer, FormatTime(c.NotBefore),
				FormatTime(c.NotAfter), c.Fingerprint, c.KeyType, c.SelfSigned, c.ChainLength, now, c.ID, c.TeamID)
			return mapError(err)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO certificates (id, team_id, name, chain_pem, key_enc, hostnames,
			subject, issuer, not_before, not_after, fingerprint, key_type, self_signed, chain_length,
			expiry_notified, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?,?)`,
			c.ID, c.TeamID, c.Name, c.ChainPEM, sealedKey, string(hostnames), c.Subject, c.Issuer,
			FormatTime(c.NotBefore), FormatTime(c.NotAfter), c.Fingerprint, c.KeyType, c.SelfSigned,
			c.ChainLength, now, now)
		return mapError(err)
	})
	if err != nil {
		return false, fmt.Errorf("save the certificate %q: %w", c.Name, err)
	}
	c.ExpiryNotified = 0
	c.UpdatedAt, _ = ParseTime(now)
	if !replaced {
		c.CreatedAt = c.UpdatedAt
	}
	return replaced, nil
}

// ListCertificates returns a team's certificates by name, without their keys.
func (db *DB) ListCertificates(ctx context.Context, teamID string) ([]Certificate, error) {
	return db.queryCertificates(ctx, `WHERE team_id = ? ORDER BY name`, teamID)
}

// ListAllCertificates returns every team's certificates, for the expiry check.
func (db *DB) ListAllCertificates(ctx context.Context) ([]Certificate, error) {
	return db.queryCertificates(ctx, `ORDER BY team_id, name`)
}

// OtherTeamsCertificates returns the certificates every other team has, for
// the check that no two teams' certificates name the same hostname.
func (db *DB) OtherTeamsCertificates(ctx context.Context, teamID string) ([]Certificate, error) {
	return db.queryCertificates(ctx, `WHERE team_id <> ? ORDER BY team_id, name`, teamID)
}

func (db *DB) queryCertificates(ctx context.Context, where string, args ...any) ([]Certificate, error) {
	// The where clause is one of the fixed strings above, never input.
	rows, err := db.QueryContext(ctx, `SELECT `+certificateColumns+` FROM certificates `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list certificates: %w", err)
	}
	defer rows.Close()
	out := []Certificate{}
	for rows.Next() {
		c, err := scanCertificate(rows)
		if err != nil {
			return nil, fmt.Errorf("read a certificate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCertificate returns one of a team's certificates, without its key.
func (db *DB) GetCertificate(ctx context.Context, teamID, id string) (Certificate, error) {
	row := db.QueryRowContext(ctx, `SELECT `+certificateColumns+` FROM certificates WHERE team_id = ? AND id = ?`,
		teamID, id)
	c, err := scanCertificate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Certificate{}, ErrNotFound
	}
	return c, err
}

// CertificateByName returns a team's certificate by its name.
func (db *DB) CertificateByName(ctx context.Context, teamID, name string) (Certificate, error) {
	row := db.QueryRowContext(ctx, `SELECT `+certificateColumns+` FROM certificates WHERE team_id = ? AND name = ?`,
		teamID, name)
	c, err := scanCertificate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Certificate{}, ErrNotFound
	}
	return c, err
}

// CertificateMaterial returns a certificate's chain and its sealed private
// key, for writing into the cluster and for nothing else.
func (db *DB) CertificateMaterial(ctx context.Context, teamID, id string) (chainPEM, sealedKey string, err error) {
	err = db.QueryRowContext(ctx, `SELECT chain_pem, key_enc FROM certificates WHERE team_id = ? AND id = ?`,
		teamID, id).Scan(&chainPEM, &sealedKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("read the certificate %s: %w", id, err)
	}
	return chainPEM, sealedKey, nil
}

// DeleteCertificate removes one of a team's certificates.
func (db *DB) DeleteCertificate(ctx context.Context, teamID, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM certificates WHERE team_id = ? AND id = ?`, teamID, id)
	if err != nil {
		return fmt.Errorf("delete a certificate: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCertificateExpiryNotified records the warning just sent for a
// certificate. It changes nothing when the certificate was replaced in the
// meantime — the new one has its own warnings to be sent — which is what the
// date it was sent about checks.
func (db *DB) SetCertificateExpiryNotified(ctx context.Context, id string, notAfter time.Time, days int) error {
	if _, err := db.Exec(ctx, `UPDATE certificates SET expiry_notified = ? WHERE id = ? AND not_after = ?`,
		days, id, FormatTime(notAfter)); err != nil {
		return fmt.Errorf("record a certificate's expiry warning: %w", err)
	}
	return nil
}

// TeamDomain is a domain with the app it belongs to, for the list of which
// domains a team's certificates are used by.
type TeamDomain struct {
	Domain
	AppName string `json:"app_name"`
	// Internal apps have no Ingress, so their domains are not served at all.
	Internal bool `json:"-"`
}

// ListTeamDomains returns every domain of every app in a team.
func (db *DB) ListTeamDomains(ctx context.Context, teamID string) ([]TeamDomain, error) {
	rows, err := db.QueryContext(ctx, `SELECT d.id, d.app_id, d.hostname, d.path, d.tls, d.auto, d.status,
			d.status_detail, d.redirect_to, d.created_at, a.name, a.internal
		FROM domains d
		JOIN apps a ON a.id = d.app_id
		JOIN environments e ON e.id = a.environment_id
		JOIN projects p ON p.id = e.project_id
		WHERE p.team_id = ?
		ORDER BY d.hostname`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list the team's domains: %w", err)
	}
	defer rows.Close()
	out := []TeamDomain{}
	for rows.Next() {
		var d TeamDomain
		var created string
		if err := rows.Scan(&d.ID, &d.AppID, &d.Hostname, &d.Path, &d.TLS, &d.Auto, &d.Status, &d.StatusDetail,
			&d.RedirectTo, &created, &d.AppName, &d.Internal); err != nil {
			return nil, fmt.Errorf("scan a team's domain: %w", err)
		}
		d.CreatedAt, _ = ParseTime(created)
		out = append(out, d)
	}
	return out, rows.Err()
}

// HostnamesOfOtherTeams returns which of these hostnames are domains of apps
// in teams other than this one.
func (db *DB) HostnamesOfOtherTeams(ctx context.Context, teamID string, hostnames []string) ([]string, error) {
	if len(hostnames) == 0 {
		return nil, nil
	}
	args := []any{teamID}
	for _, hostname := range hostnames {
		args = append(args, hostname)
	}
	rows, err := db.QueryContext(ctx, `SELECT d.hostname FROM domains d
		JOIN apps a ON a.id = d.app_id
		JOIN environments e ON e.id = a.environment_id
		JOIN projects p ON p.id = e.project_id
		WHERE p.team_id <> ? AND d.hostname IN (?`+strings.Repeat(",?", len(hostnames)-1)+`)
		ORDER BY d.hostname`, args...)
	if err != nil {
		return nil, fmt.Errorf("look for hostnames other teams use: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var hostname string
		if err := rows.Scan(&hostname); err != nil {
			return nil, err
		}
		out = append(out, hostname)
	}
	return out, rows.Err()
}
