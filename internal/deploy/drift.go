package deploy

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/logging"
	"skifity/internal/store"
)

// CheckDrift compares an app's objects in the cluster with what an apply of
// its current configuration would write — the same rendering, see render —
// and reports what somebody changed or deleted outside the panel.
//
// It reads the cluster and writes nothing. The rules for what counts are in
// kube.CompareObject; the one decided here is that an autoscaler's replica
// count is its own, whatever managedFields says about it.
func (d *Deployer) CheckDrift(ctx context.Context, appID string) (api.DriftReport, error) {
	report := api.DriftReport{Status: store.DriftInSync, Items: []api.DriftItem{}, CheckedAt: time.Now().UTC()}
	app, err := d.db.GetApp(ctx, appID)
	if err != nil {
		return report, err
	}
	target, deployed, err := d.syncTarget(ctx, appID)
	if err != nil {
		return report, err
	}
	if !deployed {
		report.Status = store.DriftNotDeployed
		return report, nil
	}
	// While the panel is changing the app, what the cluster holds is half the
	// old and half the new, and an object it is about to create is not there
	// yet. Nothing read then is anybody else's doing.
	applying, err := d.applying(ctx, appID)
	if err != nil {
		return report, err
	}
	if applying {
		report.Status = store.DriftApplying
		return report, nil
	}
	if d.cluster == nil {
		return report, errdoc.ClusterUnreachable(nil)
	}
	env, err := d.db.GetEnvironment(ctx, app.EnvironmentID)
	if err != nil {
		return report, err
	}
	out, err := d.render(ctx, target, app, env)
	if err != nil {
		return report, err
	}
	stored, err := d.db.GetAppDrift(ctx, appID)
	if err != nil {
		return report, err
	}

	applier := d.cluster.Client().Applier()
	var changes []kube.DriftChange
	for _, desired := range append(out.objects, out.processObjects...) {
		live, err := applier.Get(ctx, desired.GetAPIVersion(), desired.GetKind(), desired.GetNamespace(), desired.GetName())
		switch {
		case err == nil:
			changes = append(changes, kube.CompareObject(desired, live, ignoredOn(out.spec, desired)...)...)
		case kube.IsNotFound(err):
			// Deleted by somebody only if the panel had written it. One it
			// never wrote is one it has yet to create: a domain added while
			// the cluster could not be reached, whose Ingress the next apply
			// makes. An app last applied before this was recorded is taken at
			// its word that everything it would apply was applied.
			if _, written := stored.Applied[desired.GetKind()+"/"+desired.GetName()]; stored.AppliedKnown && !written {
				continue
			}
			changes = append(changes, kube.DeletedObject(desired))
		case meta.IsNoMatchError(err):
			// The kind is not installed — a Traefik middleware on a cluster
			// with another ingress controller. The apply that wrote the app
			// would have said so; there is nothing here to compare.
		case kube.IsUnreachable(err):
			return report, errdoc.ClusterUnreachable(err)
		default:
			return report, errdoc.DriftCheckFailed(desired.GetKind()+"/"+desired.GetName(), err.Error(), err)
		}
	}

	secrets := d.cluster.SecretValues(ctx, app, env)
	for _, change := range changes {
		report.Items = append(report.Items, api.DriftItem{
			Kind: change.Kind, Name: change.Name, Path: change.Path, Change: change.Change,
			Panel:     logging.RedactValues(change.Panel, secrets),
			Live:      logging.RedactValues(change.Live, secrets),
			Hidden:    change.Hidden,
			ChangedBy: change.Manager, ChangedAt: change.ChangedAt,
		})
		switch {
		case change.Change == kube.DriftDeleted:
			report.Status = store.DriftMissing
		case report.Status == store.DriftInSync:
			report.Status = store.DriftDrifted
		}
	}
	return report, nil
}

// applying reports whether the panel is changing an app right now: a sync in
// this process, or a deployment or rollback that has not finished.
func (d *Deployer) applying(ctx context.Context, appID string) (bool, error) {
	if d.Busy(appID) {
		return true, nil
	}
	unfinished, err := d.db.ListUnfinishedDeployments(ctx)
	if err != nil {
		return false, err
	}
	for _, deployment := range unfinished {
		if deployment.AppID == appID {
			return true, nil
		}
	}
	return false, nil
}

// RepairDrift puts an app's objects back: the apply a change to a variable
// makes, which builds nothing and records no deployment.
func (d *Deployer) RepairDrift(ctx context.Context, appID string) error {
	return d.Sync(ctx, appID)
}

// ignoredOn is what the comparison leaves alone on one object.
//
// The app's replica count, when an autoscaler or scale to zero owns it: the
// panel does not write the field then, so it is not compared anyway, and this
// says so in the one place a reader would look.
func ignoredOn(spec kube.AppSpec, desired *unstructured.Unstructured) []string {
	if desired.GetKind() == "Deployment" && desired.GetName() == spec.Name && spec.ReplicasAreSomebodyElses() {
		return []string{"spec.replicas"}
	}
	return nil
}
