package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/secretmgr"
	"skifity/internal/store"
)

// Variables whose values live in a secret manager (see internal/secretmgr).
//
// They are read at the same moments as every other variable: the runtime
// ones when a deployment or a sync writes the app's Secret, and the
// build-time ones when the build fingerprint is worked out and when the image
// is built. The build-time ones are therefore part of the fingerprint like
// any other build argument (ADR-0007): a new value there is a new image, and a
// new runtime value is a rollout that reuses the one the app has.
//
// One that cannot be read stops whatever was reading it before anything
// reaches the cluster. The version running keeps running with what it had,
// and the problem names the variable, the connection and why. Nothing is ever
// deployed with such a variable empty.

// resolved is an app's variables with their values, and which of them were
// read from a secret manager.
type resolved struct {
	values     map[string]string
	references map[string]bool
}

// referenceReader reads the values of the variables that are references.
// readReferences asks their secret managers; the drift check reads what the
// app's Secret holds instead (see appliedReferences).
type referenceReader func(ctx context.Context, app store.App, wanted map[string]secretmgr.Wanted) (map[string]string, error)

// resolveVariables collects everything that becomes an environment variable:
// the project's shared variables, then the app's own. Later sources win, so an
// app can override a shared value. A reference is read from its manager.
func (d *Deployer) resolveVariables(ctx context.Context, app store.App, env store.Environment) (resolved, error) {
	return d.resolveVariablesWith(ctx, app, env, d.readReferences)
}

// resolveVariablesWith is resolveVariables with the references read by read.
func (d *Deployer) resolveVariablesWith(ctx context.Context, app store.App, env store.Environment, read referenceReader) (resolved, error) {
	out := resolved{values: map[string]string{}, references: map[string]bool{}}
	wanted := map[string]secretmgr.Wanted{}

	shared, err := d.db.ListSharedVariables(ctx, env.ProjectID)
	if err != nil {
		return out, err
	}
	for _, row := range shared {
		// A preview of a pull request from a fork runs code anybody could
		// have written, and its first commit could print the environment. It
		// gets the project's plain settings and none of its secrets — the same
		// line GitHub Actions draws, and the one the preview's own copy of the
		// app's variables already drew. A variable read from a secret manager
		// is a secret whatever it is marked.
		if env.FromFork && (row.IsSecret || row.Reference != nil) {
			continue
		}
		if row.Reference != nil {
			wanted[row.Key] = secretmgr.Wanted{Variable: row.Key, Reference: *row.Reference}
			continue
		}
		plaintext, err := d.keyring.Open(row.Sealed, "shared_variable:"+env.ProjectID+":"+row.Key)
		if err != nil {
			return out, fmt.Errorf("read the shared variable %s: %w", row.Key, err)
		}
		out.values[row.Key] = string(plaintext)
	}

	own, err := d.db.ListVariables(ctx, app.ID)
	if err != nil {
		return out, err
	}
	for _, row := range own {
		if row.Reference != nil {
			if env.FromFork {
				continue
			}
			delete(out.values, row.Key)
			wanted[row.Key] = secretmgr.Wanted{Variable: row.Key, Reference: *row.Reference}
			continue
		}
		plaintext, err := d.keyring.Open(row.Sealed, "variable:"+app.ID+":"+row.Key)
		if err != nil {
			return out, fmt.Errorf("read the variable %s: %w", row.Key, err)
		}
		delete(wanted, row.Key)
		out.values[row.Key] = string(plaintext)
	}

	values, err := read(ctx, app, wanted)
	if err != nil {
		return out, err
	}
	for key, value := range values {
		out.values[key] = value
		out.references[key] = true
	}
	return out, nil
}

// runtimeVariables is what becomes the app's environment, by name.
func (d *Deployer) runtimeVariables(ctx context.Context, app store.App, env store.Environment) (map[string]string, error) {
	r, err := d.resolveVariables(ctx, app, env)
	return r.values, err
}

// readReferences reads the values of the variables that are references, from
// the connections of the team the app belongs to.
func (d *Deployer) readReferences(ctx context.Context, app store.App, wanted map[string]secretmgr.Wanted) (map[string]string, error) {
	if len(wanted) == 0 {
		return nil, nil
	}
	teamID, err := d.db.TeamIDForApp(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	list := make([]secretmgr.Wanted, 0, len(wanted))
	for _, w := range wanted {
		list = append(list, w)
	}
	return d.Secrets.Resolve(ctx, teamID, list)
}

// buildTimeVariables are the subset that affects what the image contains, and
// therefore the build fingerprint. One read from a secret manager is read now.
func (d *Deployer) buildTimeVariables(ctx context.Context, app store.App) (map[string]string, error) {
	rows, err := d.db.ListVariables(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	fromFork := false
	if env, err := d.db.GetEnvironment(ctx, app.EnvironmentID); err == nil {
		fromFork = env.FromFork
	}
	out := map[string]string{}
	wanted := map[string]secretmgr.Wanted{}
	for _, row := range rows {
		if !row.BuildTime {
			continue
		}
		if row.Reference != nil {
			if !fromFork {
				wanted[row.Key] = secretmgr.Wanted{Variable: row.Key, Reference: *row.Reference}
			}
			continue
		}
		plaintext, err := d.keyring.Open(row.Sealed, "variable:"+app.ID+":"+row.Key)
		if err != nil {
			return nil, fmt.Errorf("read the build variable %s: %w", row.Key, err)
		}
		out[row.Key] = string(plaintext)
	}
	values, err := d.readReferences(ctx, app, wanted)
	if err != nil {
		return nil, err
	}
	for key, value := range values {
		out[key] = value
	}
	return out, nil
}

// secretBuildTimeVariables names the build-time variables marked secret, which
// reach the build as BuildKit secrets and never as build arguments. One read
// from a secret manager is always one of them.
func (d *Deployer) secretBuildTimeVariables(ctx context.Context, app store.App) (map[string]bool, error) {
	rows, err := d.db.ListVariables(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, row := range rows {
		if row.BuildTime && (row.IsSecret || row.Reference != nil) {
			out[row.Key] = true
		}
	}
	return out, nil
}

// digest is what is kept of a value: enough to tell that it changed.
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// recordReferences keeps, sealed, a digest of each value that was just
// written to the app's Secret from a secret manager. A failure is logged and
// costs nothing but a refresh that reports a change that was not one.
func (d *Deployer) recordReferences(ctx context.Context, appID string, r resolved) {
	digests := make(map[string]string, len(r.references))
	for key := range r.references {
		sealed, err := d.keyring.Seal([]byte(digest(r.values[key])), store.ReferenceDigestContext(appID, key))
		if err != nil {
			d.log.Warn("could not record what a referenced variable held", "app", appID, "variable", key, "error", err)
			return
		}
		digests[key] = sealed
	}
	if err := d.db.SetReferenceDigests(context.WithoutCancel(ctx), appID, digests); err != nil {
		d.log.Warn("could not record what the referenced variables held", "app", appID, "error", err)
	}
}

// RefreshReferences reads an app's variables from their secret managers
// again and, when any of them changed, rolls the app out with the new
// values: a rollout when only runtime ones changed, and a build of the
// version that is running when one the build reads did.
//
// The answer says which changed and what was done. A variable that cannot be
// read fails the refresh with the problem naming it, and nothing is changed.
func (d *Deployer) RefreshReferences(ctx context.Context, appID, actorID string) (api.ReferenceRefresh, error) {
	out := api.ReferenceRefresh{Changed: []string{}, BuildTimeChanged: []string{}}
	ctx = secretmgr.WithCache(ctx)
	app, err := d.db.GetApp(ctx, appID)
	if err != nil {
		return out, err
	}
	env, err := d.db.GetEnvironment(ctx, app.EnvironmentID)
	if err != nil {
		return out, err
	}
	current, err := d.resolveVariables(ctx, app, env)
	if err != nil {
		return out, err
	}
	out.References = len(current.references)
	if out.References == 0 {
		return out, nil
	}

	stored, err := d.db.ReferenceDigests(ctx, app.ID)
	if err != nil {
		return out, err
	}
	rows, err := d.db.ListVariables(ctx, app.ID)
	if err != nil {
		return out, err
	}
	buildTime := map[string]bool{}
	for _, row := range rows {
		if row.BuildTime && row.Reference != nil {
			buildTime[row.Key] = true
		}
	}
	for key := range current.references {
		was, err := d.keyring.Open(stored[key], store.ReferenceDigestContext(app.ID, key))
		if err == nil && string(was) == digest(current.values[key]) {
			continue
		}
		out.Changed = append(out.Changed, key)
		if buildTime[key] {
			out.BuildTimeChanged = append(out.BuildTimeChanged, key)
		}
	}
	slices.Sort(out.Changed)
	slices.Sort(out.BuildTimeChanged)
	if len(out.Changed) == 0 {
		return out, nil
	}

	last, err := d.db.LatestSuccessfulDeployment(ctx, app.ID)
	if errors.Is(err, store.ErrNotFound) {
		// Nothing is running, so there is nothing to roll out: the first
		// deploy reads every value anyway.
		out.NotDeployed = true
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if len(out.BuildTimeChanged) > 0 {
		// The same code, built again with the new value: a refresh is not a
		// way to ship whatever the branch holds now.
		deployment, err := d.Deploy(ctx, api.DeployRequest{
			AppID: app.ID, Trigger: "refresh", CommitSHA: last.CommitSHA, CreatedBy: actorID,
		})
		if err != nil {
			return out, err
		}
		out.Deployment = &deployment
		return out, nil
	}
	if err := d.Sync(ctx, app.ID); err != nil {
		return out, err
	}
	out.RolledOut = true
	return out, nil
}

// --- the periodic refresh ---

const (
	// refreshConnectionsPerTick bounds how many connections one minute
	// refreshes, and refreshAppsPerConnection how many apps each.
	refreshConnectionsPerTick = 5
	refreshAppsPerConnection  = 50
	// refreshRunLimit bounds one minute's refresh as a whole.
	refreshRunLimit = 5 * time.Minute
	// maxRefreshBackoff is the longest a failing connection waits.
	maxRefreshBackoff = 24 * time.Hour
)

// refreshing is held while a periodic refresh runs, so a slow one is not
// joined by the next minute's.
var refreshing sync.Mutex

// RefreshDue refreshes the apps that read each connection whose periodic
// refresh is due, and says when each is due next.
//
// It is bounded three ways, so a manager is never hammered: a few
// connections a minute, a few dozen apps each, and one fetch per secret for
// the whole run however many apps read it. A connection whose refresh fails
// waits twice as long each time, up to a day, and says why on its settings.
func (d *Deployer) RefreshDue(ctx context.Context, now time.Time) {
	if !refreshing.TryLock() {
		return
	}
	defer refreshing.Unlock()
	ctx, cancel := context.WithTimeout(secretmgr.WithCache(ctx), refreshRunLimit)
	defer cancel()

	connections, err := d.db.SecretConnectionsWithRefresh(ctx)
	if err != nil {
		d.log.Warn("could not list the secret managers to refresh", "error", err)
		return
	}
	done := 0
	for _, connection := range connections {
		if done == refreshConnectionsPerTick {
			return
		}
		if !connection.NextRefreshAt.IsZero() && connection.NextRefreshAt.After(now) {
			continue
		}
		done++
		interval := time.Duration(connection.RefreshMinutes) * time.Minute
		// Claimed before the work, so a run cut short by the deadline or a
		// restart is not started again by the very next minute.
		if err := d.db.ClaimSecretRefresh(ctx, connection.ID, now.Add(interval)); err != nil {
			d.log.Warn("could not claim a secret manager's refresh", "connection", connection.ID, "error", err)
			continue
		}
		failures, lastError := d.refreshConnection(ctx, connection)
		next := now.Add(interval)
		if failures > 0 {
			failures = connection.RefreshFailures + 1
			next = now.Add(backoff(interval, failures))
		}
		if err := d.db.RecordSecretRefresh(context.WithoutCancel(ctx), connection.ID, now, next, failures, lastError); err != nil {
			d.log.Warn("could not record a secret manager's refresh", "connection", connection.ID, "error", err)
		}
	}
}

// refreshConnection refreshes every app that reads a connection, and answers
// how many failed and the first reason.
func (d *Deployer) refreshConnection(ctx context.Context, connection store.SecretConnectionRow) (int, string) {
	apps, err := d.db.AppsReadingSecretConnection(ctx, connection.ID, refreshAppsPerConnection)
	if err != nil {
		return 1, err.Error()
	}
	failures, first := 0, ""
	for _, appID := range apps {
		if ctx.Err() != nil {
			return failures + 1, "the refresh ran out of time"
		}
		result, err := d.RefreshReferences(ctx, appID, "schedule")
		if err != nil {
			failures++
			if first == "" {
				first = errdoc.From(err).Error()
			}
			continue
		}
		if len(result.Changed) > 0 {
			d.log.Info("refreshed an app's variables from its secret managers", "app", appID,
				"changed", strings.Join(result.Changed, ","), "rebuilt", len(result.BuildTimeChanged) > 0)
		}
	}
	return failures, truncate(first, 500)
}

// backoff is how long a connection whose refresh failed n times in a row
// waits: the interval, doubled for each failure, up to a day.
func backoff(interval time.Duration, failures int) time.Duration {
	wait := interval
	for range failures {
		wait *= 2
		if wait >= maxRefreshBackoff {
			return maxRefreshBackoff
		}
	}
	return wait
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
