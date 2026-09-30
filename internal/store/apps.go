package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const appColumns = `id, environment_id, name, slug, source_type, COALESCE(git_source_id,''), repo_url, branch,
	root_dir, builder, dockerfile_path, build_command, static_dir, image, port, health_path, start_command, release_command, replicas,
	autoscale, min_replicas, max_replicas, cpu_target, memory_target, scale_to_zero,
	cpu_request_m, cpu_limit_m, mem_request_mb, mem_limit_mb, auto_deploy, preview_deploys,
	status, created_at, updated_at`

func scanApp(row interface{ Scan(...any) error }) (App, error) {
	var a App
	var created, updated string
	err := row.Scan(&a.ID, &a.EnvironmentID, &a.Name, &a.Slug, &a.SourceType, &a.GitSourceID, &a.RepoURL,
		&a.Branch, &a.RootDir, &a.Builder, &a.DockerfilePath, &a.BuildCommand, &a.StaticDir, &a.Image, &a.Port, &a.HealthPath,
		&a.StartCommand, &a.ReleaseCommand, &a.Replicas, &a.Autoscale, &a.MinReplicas, &a.MaxReplicas, &a.CPUTarget,
		&a.MemoryTarget, &a.ScaleToZero, &a.CPURequestM, &a.CPULimitM, &a.MemRequestMB, &a.MemLimitMB,
		&a.AutoDeploy, &a.PreviewDeploys, &a.Status, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return a, ErrNotFound
		}
		return a, fmt.Errorf("scan app: %w", err)
	}
	a.CreatedAt, _ = ParseTime(created)
	a.UpdatedAt, _ = ParseTime(updated)
	return a, nil
}

// CreateApp inserts an app.
func (db *DB) CreateApp(ctx context.Context, a *App) error {
	if a.ID == "" {
		a.ID = NewID("app")
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO apps
		(id, environment_id, name, slug, source_type, git_source_id, repo_url, branch, root_dir, builder,
		 dockerfile_path, build_command, static_dir, image, port, health_path, start_command, release_command, replicas, autoscale,
		 min_replicas, max_replicas, cpu_target, memory_target, scale_to_zero, cpu_request_m, cpu_limit_m,
		 mem_request_mb, mem_limit_mb, auto_deploy, preview_deploys, status, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.EnvironmentID, a.Name, a.Slug, defaultStr(a.SourceType, "git"), NullString(a.GitSourceID),
		a.RepoURL, a.Branch, a.RootDir, defaultStr(a.Builder, "auto"), a.DockerfilePath, a.BuildCommand, a.StaticDir, a.Image,
		a.Port, a.HealthPath, a.StartCommand, a.ReleaseCommand, a.Replicas, a.Autoscale, a.MinReplicas, a.MaxReplicas,
		a.CPUTarget, a.MemoryTarget, a.ScaleToZero, a.CPURequestM, a.CPULimitM, a.MemRequestMB,
		a.MemLimitMB, a.AutoDeploy, a.PreviewDeploys, defaultStr(a.Status, "created"), now, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: this environment already has an app named %s", ErrConflict, a.Name)
		}
		return fmt.Errorf("create app: %w", err)
	}
	a.CreatedAt, _ = ParseTime(now)
	a.UpdatedAt = a.CreatedAt
	return nil
}

// GetApp looks an app up by id.
func (db *DB) GetApp(ctx context.Context, id string) (App, error) {
	return scanApp(db.QueryRowContext(ctx, `SELECT `+appColumns+` FROM apps WHERE id = ?`, id))
}

// ListApps returns the apps in an environment.
func (db *DB) ListApps(ctx context.Context, envID string) ([]App, error) {
	return db.queryApps(ctx, `SELECT `+appColumns+` FROM apps WHERE environment_id = ? ORDER BY created_at`, envID)
}

// ListAppsForProject returns every app across a project's environments.
func (db *DB) ListAppsForProject(ctx context.Context, projectID string) ([]App, error) {
	return db.queryApps(ctx, `SELECT `+prefixColumns("a", appColumns)+` FROM apps a
		JOIN environments e ON e.id = a.environment_id
		WHERE e.project_id = ? ORDER BY a.created_at`, projectID)
}

// SlugOwnerInEnvironment reports what already answers to a name in an
// environment: "app", "database", or "" when the name is free.
//
// Apps and databases are unique among themselves and share a namespace, so
// nothing stopped an app and a database in one environment from having the same
// slug — and they do not merely sit next to each other, they collide. Both
// render a Service under that name, and the panel applies with force, so
// creating a Redis called "web" next to an app called "web" took the app's
// Service over: its Ingress kept pointing at the name and the name now meant
// Redis. Deleting either one then deleted the other's Service as well.
func (db *DB) SlugOwnerInEnvironment(ctx context.Context, envID, slug string) (string, error) {
	var kind string
	err := db.QueryRowContext(ctx, `
		SELECT 'app'      FROM apps      WHERE environment_id = ? AND slug = ?
		UNION ALL
		SELECT 'database' FROM databases WHERE environment_id = ? AND slug = ?
		LIMIT 1`, envID, slug, envID, slug).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("check whether %q is already taken: %w", slug, err)
	}
	return kind, nil
}

// ImagesWorthKeeping returns the images the panel must not delete, keyed by the
// registry repository they live in.
//
// An image is worth keeping when something could still need it: the one an app
// is running now, and the ones behind the deployments it could be rolled back
// to. perApp bounds the second part, and should match the Deployment's
// revision history, because a rollback further back than Kubernetes remembers
// is not offered anyway.
//
// A repository with nothing in the result is an app that no longer exists, and
// the sweep takes all of it.
func (db *DB) ImagesWorthKeeping(ctx context.Context, perApp int) ([]string, error) {
	if perApp < 1 {
		perApp = 1
	}
	rows, err := db.QueryContext(ctx, `
		SELECT image FROM (
			SELECT d.image AS image,
			       ROW_NUMBER() OVER (PARTITION BY d.app_id ORDER BY d.number DESC) AS rn
			FROM deployments d
			WHERE d.image <> ''
		) WHERE rn <= ?
		UNION
		SELECT image FROM apps WHERE image <> ''`, perApp)
	if err != nil {
		return nil, fmt.Errorf("list the images worth keeping: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var image string
		if err := rows.Scan(&image); err != nil {
			return nil, err
		}
		out = append(out, image)
	}
	return out, rows.Err()
}

// ListAppsByRepo finds every app built from a repository, which is how a webhook
// knows what to deploy.
func (db *DB) ListAppsByRepo(ctx context.Context, repoURL string) ([]App, error) {
	return db.queryApps(ctx, `SELECT `+appColumns+` FROM apps WHERE repo_url = ? ORDER BY created_at`, repoURL)
}

func (db *DB) queryApps(ctx context.Context, query string, args ...any) ([]App, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateApp writes an app's mutable fields.
func (db *DB) UpdateApp(ctx context.Context, a *App) error {
	now := Now()
	res, err := db.Exec(ctx, `UPDATE apps SET
		name=?, slug=?, source_type=?, git_source_id=?, repo_url=?, branch=?, root_dir=?, builder=?,
		dockerfile_path=?, build_command=?, static_dir=?, image=?, port=?, health_path=?, start_command=?, release_command=?, replicas=?, autoscale=?,
		min_replicas=?, max_replicas=?, cpu_target=?, memory_target=?, scale_to_zero=?, cpu_request_m=?,
		cpu_limit_m=?, mem_request_mb=?, mem_limit_mb=?, auto_deploy=?, preview_deploys=?, status=?, updated_at=?
		WHERE id=?`,
		a.Name, a.Slug, a.SourceType, NullString(a.GitSourceID), a.RepoURL, a.Branch, a.RootDir, a.Builder,
		a.DockerfilePath, a.BuildCommand, a.StaticDir, a.Image, a.Port, a.HealthPath, a.StartCommand, a.ReleaseCommand, a.Replicas, a.Autoscale,
		a.MinReplicas, a.MaxReplicas, a.CPUTarget, a.MemoryTarget, a.ScaleToZero, a.CPURequestM,
		a.CPULimitM, a.MemRequestMB, a.MemLimitMB, a.AutoDeploy, a.PreviewDeploys, a.Status, now, a.ID)
	if err != nil {
		return fmt.Errorf("update app: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	a.UpdatedAt, _ = ParseTime(now)
	return nil
}

// SetAppStatus updates just the status.
func (db *DB) SetAppStatus(ctx context.Context, id, status string) error {
	_, err := db.Exec(ctx, `UPDATE apps SET status = ?, updated_at = ? WHERE id = ?`, status, Now(), id)
	if err != nil {
		return fmt.Errorf("set app status: %w", err)
	}
	return nil
}

// DeleteApp removes an app and everything attached to it.
func (db *DB) DeleteApp(ctx context.Context, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM apps WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete app: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- variables ---

// SetVariable creates or replaces one variable. value must already be sealed.
func (db *DB) SetVariable(ctx context.Context, v *Variable, sealed string) error {
	if v.ID == "" {
		v.ID = NewID("var")
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO app_variables (id, app_id, key, value_enc, is_secret, build_time, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT (app_id, key) DO UPDATE SET value_enc = excluded.value_enc,
			is_secret = excluded.is_secret, build_time = excluded.build_time, updated_at = excluded.updated_at`,
		v.ID, v.AppID, v.Key, sealed, v.IsSecret, v.BuildTime, now, now)
	if err != nil {
		return fmt.Errorf("set variable: %w", err)
	}
	v.UpdatedAt, _ = ParseTime(now)
	return nil
}

// variableRow carries the sealed value alongside the model, because callers
// decide whether to decrypt.
type variableRow struct {
	Variable
	Sealed string
}

// ListVariables returns an app's variables with their sealed values.
func (db *DB) ListVariables(ctx context.Context, appID string) ([]variableRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, app_id, key, value_enc, is_secret, build_time, created_at, updated_at
		FROM app_variables WHERE app_id = ? ORDER BY key`, appID)
	if err != nil {
		return nil, fmt.Errorf("list variables: %w", err)
	}
	defer rows.Close()
	out := []variableRow{}
	for rows.Next() {
		var r variableRow
		var created, updated string
		if err := rows.Scan(&r.ID, &r.AppID, &r.Key, &r.Sealed, &r.IsSecret, &r.BuildTime, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan variable: %w", err)
		}
		r.CreatedAt, _ = ParseTime(created)
		r.UpdatedAt, _ = ParseTime(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteVariable removes one variable.
func (db *DB) DeleteVariable(ctx context.Context, appID, key string) error {
	res, err := db.Exec(ctx, `DELETE FROM app_variables WHERE app_id = ? AND key = ?`, appID, key)
	if err != nil {
		return fmt.Errorf("delete variable: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSharedVariable creates or replaces a project-wide variable.
func (db *DB) SetSharedVariable(ctx context.Context, v *SharedVariable, sealed string) error {
	if v.ID == "" {
		v.ID = NewID("svar")
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO shared_variables (id, project_id, key, value_enc, is_secret, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT (project_id, key) DO UPDATE SET value_enc = excluded.value_enc,
			is_secret = excluded.is_secret, updated_at = excluded.updated_at`,
		v.ID, v.ProjectID, v.Key, sealed, v.IsSecret, now, now)
	if err != nil {
		return fmt.Errorf("set shared variable: %w", err)
	}
	return nil
}

type sharedVariableRow struct {
	SharedVariable
	Sealed string
}

// ListSharedVariables returns a project's shared variables.
func (db *DB) ListSharedVariables(ctx context.Context, projectID string) ([]sharedVariableRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, project_id, key, value_enc, is_secret, created_at, updated_at
		FROM shared_variables WHERE project_id = ? ORDER BY key`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list shared variables: %w", err)
	}
	defer rows.Close()
	out := []sharedVariableRow{}
	for rows.Next() {
		var r sharedVariableRow
		var created, updated string
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Key, &r.Sealed, &r.IsSecret, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan shared variable: %w", err)
		}
		r.CreatedAt, _ = ParseTime(created)
		r.UpdatedAt, _ = ParseTime(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteSharedVariable removes one project-wide variable.
func (db *DB) DeleteSharedVariable(ctx context.Context, projectID, key string) error {
	res, err := db.Exec(ctx, `DELETE FROM shared_variables WHERE project_id = ? AND key = ?`, projectID, key)
	if err != nil {
		return fmt.Errorf("delete shared variable: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// sealedSource is one column that holds envelopes.
//
// The whole list is here so that rotation, and the test that checks nothing is
// missing from it, read the same thing. A column that holds a sealed value and
// is not on this list is a secret wrapped in a key that rotation is about to
// drop — and nothing says so until something tries to read it back.
type sealedSource struct {
	table, valueCol, ctxPrefix string
	// keyCol defaults to "id"; keyCol2 is for a row identified by two columns.
	keyCol, keyCol2 string
	// where narrows the rows, for a table whose column holds a sealed value
	// only when a flag beside it says so.
	where string
}

var sealedSources = []sealedSource{
	{table: "app_variables", valueCol: "value_enc", ctxPrefix: "variable"},
	{table: "shared_variables", valueCol: "value_enc", ctxPrefix: "shared_variable"},
	{table: "servers", valueCol: "ssh_key_enc", ctxPrefix: "server_key"},
	{table: "databases", valueCol: "credentials_enc", ctxPrefix: "database_credentials"},
	{table: "git_sources", valueCol: "config_enc", ctxPrefix: "git_source"},
	{table: "notification_channels", valueCol: "config_enc", ctxPrefix: "notification_channel"},
	{table: "users", valueCol: "totp_secret_enc", ctxPrefix: "totp"},
	{table: "plugins", valueCol: "hmac_sealed", ctxPrefix: "plugin"},
	{table: "settings", valueCol: "value", keyCol: "key", ctxPrefix: "setting", where: "encrypted = 1"},
	{table: "plugin_settings", valueCol: "value", keyCol: "plugin_id", keyCol2: "key",
		ctxPrefix: "plugin-setting", where: "encrypted = 1"},
}

// ListSealedSecrets returns every sealed value in the database with the context
// it was sealed under. Master key rotation walks this list.
func (db *DB) ListSealedSecrets(ctx context.Context) ([]SealedRef, error) {
	out := []SealedRef{}
	for _, source := range sealedSources {
		keyCol := source.keyCol
		if keyCol == "" {
			keyCol = "id"
		}
		columns := keyCol
		if source.keyCol2 != "" {
			columns += ", " + source.keyCol2
		}
		where := source.valueCol + " != ''"
		if source.where != "" {
			where = source.where + " AND " + where
		}
		// Table and column names come from this fixed list, never from user
		// input, so interpolating them is safe.
		query := fmt.Sprintf(`SELECT %s, %s FROM %s WHERE %s`,
			columns, source.valueCol, source.table, where)

		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("list sealed values in %s: %w", source.table, err)
		}
		for rows.Next() {
			ref := SealedRef{
				Table: source.table, Column: source.valueCol,
				KeyColumn: keyCol, KeyColumn2: source.keyCol2,
				ContextPrefix: source.ctxPrefix,
			}
			var err error
			if source.keyCol2 != "" {
				err = rows.Scan(&ref.ID, &ref.ID2, &ref.Sealed)
			} else {
				err = rows.Scan(&ref.ID, &ref.Sealed)
			}
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan sealed value in %s: %w", source.table, err)
			}
			out = append(out, ref)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SealedRef points at one encrypted column value, for rotation.
//
// A row this misses is a secret that stays wrapped in a key the rotation is
// about to drop, and nothing notices until something tries to read it back —
// which for a plugin's signing secret is the next event nobody receives.
// TestEverySealedColumnIsRotated walks the schema so that a new one cannot be
// added without being listed here.
type SealedRef struct {
	Table     string
	Column    string
	KeyColumn string // defaults to "id"
	ID        string
	// KeyColumn2 and ID2 are the second half of a composite key, for a table
	// whose row is identified by two columns rather than one.
	KeyColumn2    string
	ID2           string
	Sealed        string
	ContextPrefix string
}

// UpdateSealed writes a rewrapped envelope back where it came from.
func (db *DB) UpdateSealed(ctx context.Context, ref SealedRef, sealed string) error {
	keyCol := ref.KeyColumn
	if keyCol == "" {
		keyCol = "id"
	}
	// Table and column names come from the fixed list in ListSealedSecrets, never
	// from user input, so interpolating them is safe here.
	query := fmt.Sprintf(`UPDATE %s SET %s = ? WHERE %s = ?`, ref.Table, ref.Column, keyCol)
	args := []any{sealed, ref.ID}
	if ref.KeyColumn2 != "" {
		query += fmt.Sprintf(` AND %s = ?`, ref.KeyColumn2)
		args = append(args, ref.ID2)
	}
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("rewrap %s.%s: %w", ref.Table, ref.Column, err)
	}
	return nil
}

// --- domains ---

// CreateDomain attaches a hostname to an app.
func (db *DB) CreateDomain(ctx context.Context, d *Domain) error {
	if d.ID == "" {
		d.ID = NewID("dom")
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO domains (id, app_id, hostname, path, tls, auto, status, status_detail, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		d.ID, d.AppID, d.Hostname, defaultStr(d.Path, "/"), d.TLS, d.Auto,
		defaultStr(d.Status, "pending"), d.StatusDetail, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: %s is already used by another app", ErrConflict, d.Hostname)
		}
		return fmt.Errorf("create domain: %w", err)
	}
	d.CreatedAt, _ = ParseTime(now)
	return nil
}

// DomainOwner returns the id of the app holding a hostname, or "" when it is
// free.
//
// The panel gives every app an address of its own, worked out from its name.
// Two projects both calling an app "web" therefore want the same address, and
// the second one used to lose: the unique constraint refused the row, the
// deployer logged a warning, and that app simply had no address, with nothing
// on the page saying why. Asking first is what lets it be given a different one
// instead.
func (db *DB) DomainOwner(ctx context.Context, hostname string) (string, error) {
	var appID string
	err := db.QueryRowContext(ctx,
		`SELECT app_id FROM domains WHERE hostname = ?`, hostname).Scan(&appID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("check who holds %q: %w", hostname, err)
	}
	return appID, nil
}

// ListDomains returns an app's domains.
func (db *DB) ListDomains(ctx context.Context, appID string) ([]Domain, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, app_id, hostname, path, tls, auto, status, status_detail, created_at
		FROM domains WHERE app_id = ? ORDER BY auto DESC, hostname`, appID)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		var d Domain
		var created string
		if err := rows.Scan(&d.ID, &d.AppID, &d.Hostname, &d.Path, &d.TLS, &d.Auto, &d.Status, &d.StatusDetail, &created); err != nil {
			return nil, fmt.Errorf("scan domain: %w", err)
		}
		d.CreatedAt, _ = ParseTime(created)
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetAutoDomain moves an app's automatic hostname, which happens when the
// operator configures a wildcard domain after the app was already deployed.
//
// It touches only the automatic domain: one somebody typed in themselves is
// theirs, and a settings change must never rewrite it.
func (db *DB) SetAutoDomain(ctx context.Context, id, hostname string, tls bool) error {
	_, err := db.Exec(ctx,
		`UPDATE domains SET hostname = ?, tls = ?, status = 'pending', status_detail = '' WHERE id = ? AND auto = 1`,
		hostname, tls, id)
	if err != nil {
		return fmt.Errorf("move the automatic domain: %w", err)
	}
	return nil
}

// SetDomainStatus records certificate and routing progress.
func (db *DB) SetDomainStatus(ctx context.Context, id, status, detail string) error {
	_, err := db.Exec(ctx, `UPDATE domains SET status = ?, status_detail = ? WHERE id = ?`, status, detail, id)
	if err != nil {
		return fmt.Errorf("set domain status: %w", err)
	}
	return nil
}

// DeleteDomain detaches a hostname.
func (db *DB) DeleteDomain(ctx context.Context, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM domains WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete domain: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- volumes ---

// CreateVolume attaches persistent storage to an app.
func (db *DB) CreateVolume(ctx context.Context, v *Volume) error {
	if v.ID == "" {
		v.ID = NewID("vol")
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO volumes (id, app_id, name, mount_path, size_gb, storage_class, created_at)
		VALUES (?,?,?,?,?,?,?)`, v.ID, v.AppID, v.Name, v.MountPath, v.SizeGB, v.StorageClass, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: this app already has a volume named %s", ErrConflict, v.Name)
		}
		return fmt.Errorf("create volume: %w", err)
	}
	v.CreatedAt, _ = ParseTime(now)
	return nil
}

// ListVolumes returns an app's volumes.
func (db *DB) ListVolumes(ctx context.Context, appID string) ([]Volume, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, app_id, name, mount_path, size_gb, storage_class, created_at
		FROM volumes WHERE app_id = ? ORDER BY name`, appID)
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	defer rows.Close()
	out := []Volume{}
	for rows.Next() {
		var v Volume
		var created string
		if err := rows.Scan(&v.ID, &v.AppID, &v.Name, &v.MountPath, &v.SizeGB, &v.StorageClass, &created); err != nil {
			return nil, fmt.Errorf("scan volume: %w", err)
		}
		v.CreatedAt, _ = ParseTime(created)
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetVolume looks one volume up by id.
func (db *DB) GetVolume(ctx context.Context, id string) (Volume, error) {
	var v Volume
	var created string
	err := db.QueryRowContext(ctx, `SELECT id, app_id, name, mount_path, size_gb, storage_class, created_at
		FROM volumes WHERE id = ?`, id).
		Scan(&v.ID, &v.AppID, &v.Name, &v.MountPath, &v.SizeGB, &v.StorageClass, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Volume{}, ErrNotFound
	}
	if err != nil {
		return Volume{}, fmt.Errorf("read volume: %w", err)
	}
	v.CreatedAt, _ = ParseTime(created)
	return v, nil
}

// DeleteVolume detaches a volume record. The PVC is removed separately so the
// data can be kept deliberately.
func (db *DB) DeleteVolume(ctx context.Context, appID, id string) error {
	// The app is part of the condition rather than something the caller is
	// trusted to have checked: a volume id from another team must not delete
	// anything, however the handler above got here.
	res, err := db.Exec(ctx, `DELETE FROM volumes WHERE id = ? AND app_id = ?`, id, appID)
	if err != nil {
		return fmt.Errorf("delete volume: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeployedApp is an app together with where it runs and who owns it, which is
// what a watcher needs to compare the panel's picture with the cluster's.
type DeployedApp struct {
	App
	TeamID    string
	Namespace string
}

// ListDeployedApps returns every app that has been deployed at least once,
// across every team.
//
// An app that was created and never deployed has nothing in the cluster to
// compare against, so including it would only produce false alarms.
func (db *DB) ListDeployedApps(ctx context.Context) ([]DeployedApp, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+prefixColumns("a", appColumns)+`, p.team_id, e.namespace
		FROM apps a
		JOIN environments e ON e.id = a.environment_id
		JOIN projects p ON p.id = e.project_id
		WHERE a.status <> 'created'
		ORDER BY a.created_at`)
	if err != nil {
		return nil, fmt.Errorf("list deployed apps: %w", err)
	}
	defer rows.Close()

	out := []DeployedApp{}
	for rows.Next() {
		var a App
		var created, updated string
		var item DeployedApp
		if err := rows.Scan(&a.ID, &a.EnvironmentID, &a.Name, &a.Slug, &a.SourceType, &a.GitSourceID,
			&a.RepoURL, &a.Branch, &a.RootDir, &a.Builder, &a.DockerfilePath, &a.Image, &a.Port,
			&a.HealthPath, &a.StartCommand, &a.ReleaseCommand, &a.Replicas, &a.Autoscale, &a.MinReplicas, &a.MaxReplicas,
			&a.CPUTarget, &a.MemoryTarget, &a.ScaleToZero, &a.CPURequestM, &a.CPULimitM,
			&a.MemRequestMB, &a.MemLimitMB, &a.AutoDeploy, &a.PreviewDeploys, &a.Status,
			&created, &updated, &item.TeamID, &item.Namespace); err != nil {
			return nil, fmt.Errorf("scan deployed app: %w", err)
		}
		a.CreatedAt, _ = ParseTime(created)
		a.UpdatedAt, _ = ParseTime(updated)
		item.App = a
		out = append(out, item)
	}
	return out, rows.Err()
}

// --- scheduled commands ---

// AppJob is a command that runs on a schedule, in the app's own image.
type AppJob struct {
	ID        string    `json:"id"`
	AppID     string    `json:"app_id"`
	Name      string    `json:"name"`
	Schedule  string    `json:"schedule"`
	Command   string    `json:"command"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateAppJob adds a scheduled command.
func (db *DB) CreateAppJob(ctx context.Context, j *AppJob) error {
	if j.ID == "" {
		j.ID = NewID("job")
	}
	now := Now()
	_, err := db.Exec(ctx,
		`INSERT INTO app_jobs (id, app_id, name, schedule, command, enabled, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		j.ID, j.AppID, j.Name, j.Schedule, j.Command, j.Enabled, now, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: this app already has a scheduled command called %s", ErrConflict, j.Name)
		}
		return fmt.Errorf("create scheduled command: %w", err)
	}
	j.CreatedAt, _ = ParseTime(now)
	j.UpdatedAt = j.CreatedAt
	return nil
}

// ListAppJobs returns an app's scheduled commands.
func (db *DB) ListAppJobs(ctx context.Context, appID string) ([]AppJob, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, app_id, name, schedule, command, enabled, created_at, updated_at
		 FROM app_jobs WHERE app_id = ? ORDER BY name`, appID)
	if err != nil {
		return nil, fmt.Errorf("list scheduled commands: %w", err)
	}
	defer rows.Close()
	out := []AppJob{}
	for rows.Next() {
		var j AppJob
		var created, updated string
		if err := rows.Scan(&j.ID, &j.AppID, &j.Name, &j.Schedule, &j.Command,
			&j.Enabled, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan scheduled command: %w", err)
		}
		j.CreatedAt, _ = ParseTime(created)
		j.UpdatedAt, _ = ParseTime(updated)
		out = append(out, j)
	}
	return out, rows.Err()
}

// UpdateAppJob changes a scheduled command. The app is part of the condition,
// so an id from another app changes nothing.
func (db *DB) UpdateAppJob(ctx context.Context, j *AppJob) error {
	res, err := db.Exec(ctx,
		`UPDATE app_jobs SET name = ?, schedule = ?, command = ?, enabled = ?, updated_at = ?
		 WHERE id = ? AND app_id = ?`,
		j.Name, j.Schedule, j.Command, j.Enabled, Now(), j.ID, j.AppID)
	if err != nil {
		return fmt.Errorf("update scheduled command: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAppJob removes a scheduled command.
func (db *DB) DeleteAppJob(ctx context.Context, appID, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM app_jobs WHERE id = ? AND app_id = ?`, id, appID)
	if err != nil {
		return fmt.Errorf("delete scheduled command: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
