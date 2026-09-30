package deploy

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// rendered is everything an apply writes for one app, in the order it writes
// it.
type rendered struct {
	spec kube.AppSpec
	// objects are the app's own, and processObjects its other processes',
	// which a new version applies only once the app is serving it.
	objects        []*unstructured.Unstructured
	processes      []store.AppProcess
	processObjects []*unstructured.Unstructured
	// variables are the values the app's Secret is rendered with, and which
	// of them were read from a secret manager.
	variables resolved
}

// render builds what an apply of this deployment writes, and changes nothing.
//
// The apply and the drift check both call it, so what the check compares the
// cluster with is what the last apply wrote, object for object and field for
// field — including the fingerprint each object carries, which is how the
// check tells a field somebody removed from one the panel has yet to add.
func (d *Deployer) render(ctx context.Context, deployment store.Deployment, app store.App, env store.Environment) (rendered, error) {
	return d.renderWith(ctx, deployment, app, env, d.readReferences)
}

// renderWith is render with the variables read from secret managers read by
// read.
func (d *Deployer) renderWith(ctx context.Context, deployment store.Deployment, app store.App, env store.Environment, read referenceReader) (rendered, error) {
	spec, err := d.cluster.SpecFor(ctx, app, env, deployment.Image)
	if err != nil {
		return rendered{}, err
	}
	// Only a commit: an uploaded folder's deployment carries the upload's hash
	// in the same field, and calling that a commit would be a lie an app might
	// act on.
	if app.SourceType == "git" {
		spec.CommitSHA = deployment.CommitSHA
	}
	spec.DeploymentID = deployment.ID

	resolved, err := d.resolveVariablesWith(ctx, app, env, read)
	if err != nil {
		return rendered{}, err
	}
	variables := resolved.values
	files, err := d.fileContents(ctx, app)
	if err != nil {
		return rendered{}, err
	}
	// The pod template carries a hash of the configuration, so a variable
	// change actually restarts the pods. Kubernetes does not watch a Secret's
	// contents, so without this the new value would only appear at the next
	// unrelated restart. The files' hash only when there are files, so an
	// app without any is not restarted by the upgrade that added them.
	spec.Revision = kube.EnvHash(variables) + ":" + deployment.ID
	if len(spec.Files) > 0 {
		spec.Revision += ":" + kube.FilesHash(spec.Files, files)
	}

	if err := spec.Validate(); err != nil {
		return rendered{}, errdoc.BadRequest(err.Error())
	}

	// The team's own certificates the app's hostnames are served with, opened
	// here and nowhere else: the Secret is written, and the key is not kept.
	certificates, err := d.certificatePairs(ctx, spec)
	if err != nil {
		return rendered{}, err
	}
	certificateSecrets, err := kube.BuildCertificateSecrets(spec, certificates)
	if err != nil {
		return rendered{}, err
	}

	objects := []any{
		kube.BuildEnvSecret(spec, variables),
		// Before the Deployment that mounts it, for the same reason as the
		// variables: a pod naming a Secret that is not there does not start.
		kube.BuildFilesSecret(spec, files),
		// Before the Ingress that names them: a middleware that is missing
		// when Traefik reads the Ingress is a route Traefik refuses to serve.
		kube.BuildPasswordSecret(spec),
		kube.BuildPasswordMiddleware(spec),
	}
	for _, redirect := range kube.BuildHostRedirects(spec) {
		objects = append(objects, redirect)
	}
	// Before the Ingress that names them, too: one whose certificate is not
	// there yet is served with Traefik's own, which every browser refuses.
	for _, secret := range certificateSecrets {
		objects = append(objects, secret)
	}
	for _, claim := range kube.BuildPVCs(spec) {
		objects = append(objects, claim)
	}
	objects = append(objects,
		kube.BuildDeployment(spec),
		kube.BuildService(spec),
		// The second Ingress before the first: a hostname moving onto a
		// certificate of the team's own is in both for a moment, both routing
		// to the same place, rather than in neither.
		kube.BuildOwnCertIngress(spec),
		kube.BuildIngress(spec),
		kube.BuildHPA(spec),
		kube.BuildPDB(spec),
		kube.BuildInterceptorService(spec),
		kube.BuildHTTPScaledObject(spec),
		// The policy first: a port opened before connections may reach it
		// is one that refuses everybody for a moment.
		kube.BuildPortsPolicy(spec),
		kube.BuildPortsService(spec),
	)
	// The app's other processes go out with it, on the same image.
	processes, err := d.db.ListProcesses(ctx, app.ID)
	if err != nil {
		return rendered{}, err
	}
	processObjects := make([]any, 0, len(processes))
	for _, process := range processes {
		processObjects = append(processObjects, kube.BuildProcessDeployment(spec, process.Name, process.Command, process.Instances))
	}

	out := rendered{spec: spec, processes: processes, variables: resolved}
	if out.objects, err = kube.Prepare(objects...); err != nil {
		return rendered{}, err
	}
	if out.processObjects, err = kube.Prepare(processObjects...); err != nil {
		return rendered{}, err
	}
	return out, nil
}

// recordApplied notes what an apply wrote, so the drift check can tell an
// object somebody deleted from one the panel has yet to create. whole is an
// apply of everything the app has, which replaces the list; anything less adds
// to it. A failure to record is only logged: the apply happened either way,
// and the cost is a deleted object the check does not call deleted.
func (d *Deployer) recordApplied(ctx context.Context, appID string, objects []any, whole bool) {
	written := make(map[string]string, len(objects))
	for _, obj := range objects {
		if u, ok := obj.(*unstructured.Unstructured); ok && u != nil {
			written[u.GetKind()+"/"+u.GetName()] = u.GetAnnotations()[kube.AppliedAnnotation]
		}
	}
	if err := d.db.RecordApplied(context.WithoutCancel(ctx), appID, written, whole); err != nil {
		d.log.Warn("could not record what was applied", "app", appID, "error", err)
	}
}

// asAny is the list the applier takes.
func asAny(objects []*unstructured.Unstructured) []any {
	out := make([]any, len(objects))
	for i, obj := range objects {
		out[i] = obj
	}
	return out
}
