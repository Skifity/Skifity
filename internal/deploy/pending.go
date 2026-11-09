package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"skifity/internal/store"
)

// Saved changes that are waiting for a deploy.
//
// Saving a new branch, build command or output folder rolls out nothing:
// those go into the image, and the next deploy builds with them. Each
// deployment records what it was built from (store.BuildSpec), and the app's
// page compares that with the app's settings now, so "Saved" is followed by
// what is waiting rather than by an app that silently runs the old build.

// buildSpec is what a deploy of the app would build from now. A build-time
// variable is there by key and digest: of its value, or of where a secret
// manager keeps it — reading a manager on every look at an app's page would
// hammer it, and a value that changed there is refresh_variables' to find.
func (d *Deployer) buildSpec(ctx context.Context, app store.App) (store.BuildSpec, error) {
	rows, err := d.db.ListVariables(ctx, app.ID)
	if err != nil {
		return store.BuildSpec{}, err
	}
	digests := map[string]string{}
	for _, row := range rows {
		if !row.BuildTime {
			continue
		}
		if row.Reference != nil {
			digests[row.Key] = digest("from:" + row.Reference.ConnectionID + ":" + row.Reference.Path + "#" + row.Reference.Key)
			continue
		}
		plaintext, err := d.keyring.Open(row.Sealed, "variable:"+app.ID+":"+row.Key)
		if err != nil {
			return store.BuildSpec{}, fmt.Errorf("read the build variable %s: %w", row.Key, err)
		}
		digests[row.Key] = digest("value:" + string(plaintext))
	}
	return store.BuildSpecOf(app, digests), nil
}

// sealedBuildSpec is the spec a new deployment of the app records. A spec
// that cannot be made is logged and left out: it is bookkeeping, and no
// deploy fails for want of it; that deployment just says nothing is waiting.
func (d *Deployer) sealedBuildSpec(ctx context.Context, app store.App) string {
	spec, err := d.buildSpec(ctx, app)
	if err == nil {
		var encoded []byte
		if encoded, err = json.Marshal(spec); err == nil {
			var sealed string
			if sealed, err = d.keyring.Seal(encoded, store.BuildSpecContext(app.ID)); err == nil {
				return sealed
			}
		}
	}
	d.log.Warn("could not record what a deployment is built from", "app", app.ID, "error", err)
	return ""
}

// PendingChanges are the saved settings and build-time variables the app's
// version was not built with: the version being deployed now, or the one
// running when none is. Empty for an app never deployed, and for a version
// deployed before what it was built from was recorded.
func (d *Deployer) PendingChanges(ctx context.Context, appID string) ([]store.BuildChange, error) {
	none := []store.BuildChange{}
	target, err := d.db.LatestDeployment(ctx, appID)
	if err == nil && target.Status.Terminal() {
		// A version that failed or was cancelled never ran, so what it was
		// built from says nothing about what is running.
		target, err = d.db.LatestSuccessfulDeployment(ctx, appID)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return none, nil
	case err != nil:
		return nil, err
	case target.BuildSpec == "":
		return none, nil
	}
	plaintext, err := d.keyring.Open(target.BuildSpec, store.BuildSpecContext(appID))
	if err != nil {
		return nil, fmt.Errorf("open what deployment #%d was built from: %w", target.Number, err)
	}
	var live store.BuildSpec
	if err := json.Unmarshal(plaintext, &live); err != nil {
		return nil, fmt.Errorf("read what deployment #%d was built from: %w", target.Number, err)
	}
	app, err := d.db.GetApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	current, err := d.buildSpec(ctx, app)
	if err != nil {
		return nil, err
	}
	return store.BuildChanges(live, current), nil
}
