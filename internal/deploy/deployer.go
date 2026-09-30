// Package deploy turns a source commit into running instances.
package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"skifity/internal/api"
	"skifity/internal/cluster"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/kube"
	"skifity/internal/metrics"
	"skifity/internal/notify"
	"skifity/internal/plugins"
	"skifity/internal/registry"
	"skifity/internal/runsafe"
	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/internal/upload"
)

// Deployer implements api.Deployer.
type Deployer struct {
	db      *store.DB
	keyring *crypto.Keyring
	hub     *events.Hub
	cluster *cluster.Cluster
	// Metrics is set by the server command. Nil is fine: every method on the
	// registry checks for it, because a deployer in a test has no monitoring.
	Metrics  *metrics.Registry
	notifier notify.Notifier
	// Plugins is how installed plugins hear about a deploy and, for the one
	// blocking hook, get to stop it. The zero value sends nothing and refuses
	// nothing, which is what a deployer in a test wants.
	Plugins plugins.Dispatcher
	// Uploads is where the code of an app with no repository is kept. Nil
	// means this panel does not deploy uploaded folders.
	Uploads *upload.Store
	log     *slog.Logger

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// New builds a Deployer. notifier may be nil, and then nothing is sent.
func New(db *store.DB, keyring *crypto.Keyring, hub *events.Hub, c *cluster.Cluster, notifier notify.Notifier, log *slog.Logger) *Deployer {
	return &Deployer{
		db: db, keyring: keyring, hub: hub, cluster: c, notifier: notifier, log: log,
		running: map[string]context.CancelFunc{},
	}
}

// maxDeployDuration bounds a whole deploy, build included.
const maxDeployDuration = 45 * time.Minute

// Deploy queues a deployment and starts it.
//
// The record is created synchronously so the UI can open the log view straight
// away; the build and rollout run in the background.
func (d *Deployer) Deploy(ctx context.Context, req api.DeployRequest) (store.Deployment, error) {
	app, err := d.db.GetApp(ctx, req.AppID)
	if err != nil {
		return store.Deployment{}, err
	}
	env, err := d.db.GetEnvironment(ctx, app.EnvironmentID)
	if err != nil {
		return store.Deployment{}, err
	}

	// Empty means the build clones the branch tip, and the image is tagged with
	// the deployment number instead of a commit.
	commit := req.CommitSHA
	if app.SourceType == "upload" {
		// An upload's hash stands where a commit would, so the fingerprint
		// changes exactly when the code does.
		if commit, err = d.uploadToDeploy(app, req.CommitSHA); err != nil {
			return store.Deployment{}, err
		}
	}

	buildArgs, err := d.buildTimeVariables(ctx, app)
	if err != nil {
		return store.Deployment{}, err
	}
	fingerprint := kube.BuildFingerprint(app.RepoURL, commit, app.Builder, app.DockerfilePath, app.RootDir, buildArgs)
	if app.SourceType == "image" {
		// A prebuilt image is its own fingerprint: nothing is built.
		fingerprint = "image:" + app.Image
	}

	runtimeSpec, err := d.runtimeSpec(ctx, app, env)
	if err != nil {
		return store.Deployment{}, err
	}

	deployment := store.Deployment{
		AppID:            app.ID,
		Status:           store.DeployQueued,
		Trigger:          req.Trigger,
		CommitSHA:        commit,
		BuildFingerprint: fingerprint,
		RuntimeSpec:      runtimeSpec,
		CreatedBy:        req.CreatedBy,
	}

	// The heart of ADR-0007: when the build inputs have not changed, the
	// existing image is reused and this is a rollout, not a build. Changing an
	// environment variable or a replica count never rebuilds.
	if !req.Force && app.SourceType != "image" {
		if previous, err := d.db.FindDeploymentByFingerprint(ctx, app.ID, fingerprint); err == nil {
			deployment.Image = previous.Image
			deployment.CommitSHA = previous.CommitSHA
			deployment.CommitMessage = previous.CommitMessage
			deployment.CommitAuthor = previous.CommitAuthor
		}
	}
	if app.SourceType == "image" {
		deployment.Image = app.Image
	}

	if err := d.db.CreateDeployment(ctx, &deployment); err != nil {
		return store.Deployment{}, err
	}
	superseded, err := d.db.SupersedeRunningDeployments(ctx, app.ID, deployment.ID)
	if err != nil {
		d.log.Warn("could not supersede earlier deployments", "app", app.ID, "error", err)
	}
	// Marking the row is not enough: the build it belongs to is still running,
	// and would go on to roll out an older version after this one.
	for _, id := range superseded {
		d.log.Info("stopping a deployment that a newer one replaced",
			"app", app.ID, "deployment", id, "replaced_by", deployment.ID)
		d.stop(id)
	}

	d.start(deployment.ID, func(runCtx context.Context) {
		d.run(runCtx, deployment.ID)
	})
	return deployment, nil
}

// uploadToDeploy decides which upload a deploy builds: the one asked for, or
// the newest one there is.
func (d *Deployer) uploadToDeploy(app store.App, requested string) (string, error) {
	if d.Uploads == nil {
		return "", errdoc.NotConfigured("Deploying uploaded code", "the panel's data directory")
	}
	if requested == "" {
		latest, err := d.Uploads.Latest(app.ID)
		if err != nil {
			return "", errdoc.NoUpload(app.Name)
		}
		return latest, nil
	}
	// Checked here as well as by the store, because it is about to be a tag,
	// a fingerprint and a file name.
	if !upload.ValidSHA(requested) || !d.Uploads.Has(app.ID, requested) {
		return "", errdoc.UploadNotFound(requested)
	}
	return requested, nil
}

// run executes a deployment from start to finish.
func (d *Deployer) run(ctx context.Context, deploymentID string) {
	defer d.finish(deploymentID)

	deployment, err := d.db.GetDeployment(ctx, deploymentID)
	if err != nil {
		d.log.Error("could not load the deployment to run", "deployment", deploymentID, "error", err)
		return
	}
	app, err := d.db.GetApp(ctx, deployment.AppID)
	if err != nil {
		d.fail(ctx, deployment, errdoc.From(err))
		return
	}
	env, err := d.db.GetEnvironment(ctx, app.EnvironmentID)
	if err != nil {
		d.fail(ctx, deployment, errdoc.From(err))
		return
	}

	if deployment.Image == "" {
		d.setStatus(ctx, &deployment, store.DeployBuilding)
		image, err := d.build(ctx, &deployment, app, env)
		if err != nil {
			d.fail(ctx, deployment, errdoc.From(err))
			return
		}
		deployment.Image = image
		if err := d.db.SetDeploymentImage(ctx, deployment.ID, image); err != nil {
			d.log.Warn("could not record the built image", "deployment", deployment.ID, "error", err)
		}
	} else {
		d.appendLog(ctx, deployment.ID,
			"Nothing to build: this version was already built, so the existing image is reused.")
	}

	d.setStatus(ctx, &deployment, store.DeployDeploying)

	// Which team this belongs to, because a plugin only hears about the teams
	// whoever installed it can already see. A team that cannot be read means no
	// plugin is told, which is the safe direction: silence rather than an event
	// crossing a boundary it should not.
	teamID, err := d.db.TeamIDForApp(ctx, app.ID)
	if err != nil {
		d.log.Warn("could not work out which team this deployment belongs to",
			"app", app.ID, "error", err)
	}

	// Plugins get their say after the image exists and before anything reaches
	// the cluster: the point of a blocking hook is to stop a deploy, and one
	// that ran after the rollout would be a hook that watched it happen.
	//
	// Every plugin that subscribed is asked and all of them have to agree. One
	// that is down has not refused — a plugin that stops every deploy the
	// moment it is upgraded is a plugin nobody installs twice.
	verdict, refusedBy := d.Plugins.Ask(ctx, plugins.EventDeployBefore, teamID, map[string]any{
		"app_id": app.ID, "app": app.Slug, "environment": env.Slug,
		"deployment_id": deployment.ID, "image": deployment.Image,
		"commit": deployment.CommitSHA,
	})
	if !verdict.Allow {
		d.fail(ctx, deployment, errdoc.New("deploy.refused_by_plugin", "A plugin stopped this deployment").
			WithCause("%s refused it: %s", refusedBy, verdict.Reason).
			WithImpact("Nothing was changed. The version that was serving is still serving.").
			WithFix("Take up whatever it is asking for, or switch that plugin off under Plugins."))
		return
	}

	// The release command runs after the image exists and before anything is
	// applied, so a migration that fails stops the deployment rather than
	// leaving the new code talking to the old schema. The version that was
	// serving before is still serving while it runs.
	if err := d.runRelease(ctx, deployment, app, env, deployment.Image); err != nil {
		d.fail(ctx, deployment, errdoc.From(err))
		return
	}

	if err := d.apply(ctx, deployment, app, env); err != nil {
		d.fail(ctx, deployment, errdoc.From(err))
		return
	}

	d.appendLog(ctx, deployment.ID, "Deployed.")
	_ = d.db.UpdateDeploymentStatus(ctx, deployment.ID, store.DeploySucceeded, "", "", "")
	d.Metrics.Inc("skifity_deployments_total", "result", "succeeded")
	_ = d.db.SetAppStatus(ctx, app.ID, "running")
	d.publish(ctx, deployment.ID)
	d.notify(ctx, app, deployment, notify.EventDeploySucceeded, notify.Message{
		Title: app.Name + " is live",
		Body:  "The deployment finished and the new version is serving traffic.",
		Level: "success",
	})
	// Told, not asked: this already happened, and a plugin that is down must
	// not turn a deploy that succeeded into a failure on somebody's page.
	d.Plugins.Notify(ctx, plugins.EventDeploySucceeded, teamID, map[string]any{
		"app_id": app.ID, "app": app.Slug, "environment": env.Slug,
		"deployment_id": deployment.ID, "image": deployment.Image,
		"commit": deployment.CommitSHA,
	})

	// Build logs are by far the largest thing stored; keep the last few.
	if err := d.db.PruneBuildLogs(ctx, app.ID, 20); err != nil {
		d.log.Warn("could not prune build logs", "app", app.ID, "error", err)
	}
}

// apply renders and applies the app's Kubernetes objects and waits for the
// rollout.
func (d *Deployer) apply(ctx context.Context, deployment store.Deployment, app store.App, env store.Environment) error {
	if d.cluster == nil {
		return errdoc.ClusterUnreachable(nil)
	}

	// Every app gets a working address, and it is worked out here rather than
	// at creation because the settings it depends on — a wildcard domain, the
	// cluster's public IP — are often filled in after the app already exists.
	teamID, err := d.db.TeamIDForApp(ctx, app.ID)
	if err != nil {
		return err
	}
	// Before the objects that reference it: a Deployment naming a pull secret
	// that is not there yet is an ImagePullBackOff nobody can explain.
	if err := d.ensureRegistryAuth(ctx, env.Namespace); err != nil {
		return err
	}
	if err := d.cluster.EnsureAutoDomain(ctx, app, env, teamID); err != nil {
		// An app that cannot be given a free URL can still be deployed on a
		// domain of its own, so this is a warning and not a failure.
		//
		// It goes in the deployment log as well as the panel's, because the
		// consequence is an app with no address, and a person looking at that
		// app has no reason to read the panel's log or any way to reach it.
		d.log.Warn("could not give the app an automatic domain", "app", app.ID, "error", err)
		d.appendLog(ctx, deployment.ID,
			"This app has no automatic address: "+errdoc.From(err).Cause)
	}

	spec, err := d.cluster.SpecFor(ctx, app, env, deployment.Image)
	if err != nil {
		return err
	}
	spec.DeploymentID = deployment.ID

	variables, err := d.runtimeVariables(ctx, app, env)
	if err != nil {
		return err
	}
	// The pod template carries a hash of the configuration, so a variable
	// change actually restarts the pods. Kubernetes does not watch a Secret's
	// contents, so without this the new value would only appear at the next
	// unrelated restart.
	spec.Revision = kube.EnvHash(variables) + ":" + deployment.ID

	if err := spec.Validate(); err != nil {
		return errdoc.BadRequest(err.Error())
	}

	// Make sure the namespace and its guards exist: an app can be created
	// before the cluster was reachable.
	project, err := d.db.GetProject(ctx, env.ProjectID)
	if err != nil {
		return err
	}
	if err := d.cluster.EnsureNamespace(ctx, env, project.TeamID, project.ID); err != nil {
		return err
	}

	objects := []any{
		kube.BuildEnvSecret(spec, variables),
	}
	for _, claim := range kube.BuildPVCs(spec) {
		objects = append(objects, claim)
	}
	objects = append(objects,
		kube.BuildDeployment(spec),
		kube.BuildService(spec),
		kube.BuildIngress(spec),
		kube.BuildHPA(spec),
		kube.BuildPDB(spec),
		kube.BuildInterceptorService(spec),
		kube.BuildHTTPScaledObject(spec),
	)

	d.appendLog(ctx, deployment.ID, "Applying the configuration to the cluster.")
	if err := d.cluster.Client().Applier().ApplyAll(ctx, objects...); err != nil {
		return err
	}

	// Scheduled commands run the version that is deployed, so they are applied
	// with it rather than when somebody writes the schedule.
	if err := d.applyScheduledJobs(ctx, deployment.ID, spec, app); err != nil {
		return err
	}

	// Objects that are no longer wanted have to be removed explicitly: server-
	// side apply removes fields, not whole objects.
	d.removeUnwanted(ctx, spec, app)

	d.appendLog(ctx, deployment.ID, "Waiting for the new instances to become ready.")
	if err := d.cluster.Client().WaitForRollout(ctx, env.Namespace, app.Slug, 10*time.Minute); err != nil {
		status, statusErr := d.cluster.AppStatus(ctx, env.Namespace, app.Slug)
		ready, wanted := 0, int(spec.DesiredReplicas())
		reason := err.Error()
		if statusErr == nil {
			ready = status.ReadyReplicas
			// The live number, not the configured one: an autoscaled app is
			// waiting for however many instances the autoscaler has asked for,
			// and saying "2 of 1 ready" helps nobody.
			if status.DesiredReplicas > 0 {
				wanted = status.DesiredReplicas
			}
			if status.Detail != "" {
				reason = status.Detail
			}
		}
		return errdoc.RolloutTimedOut(app.Name, ready, wanted, reason)
	}
	return nil
}

// removeUnwanted deletes objects the app no longer needs.
func (d *Deployer) removeUnwanted(ctx context.Context, spec kube.AppSpec, app store.App) {
	applier := d.cluster.Client().Applier()

	if len(spec.Domains) == 0 {
		if err := applier.Delete(ctx, "networking.k8s.io/v1", "Ingress", spec.Namespace, spec.Name); err != nil {
			d.log.Warn("could not remove the ingress", "app", app.ID, "error", err)
		}
	}
	// Not "autoscaling was switched off": an app that turns scale to zero on
	// keeps autoscaling on and stops having an HorizontalPodAutoscaler, because
	// KEDA brings its own. Asking the builder is the only way this stays true
	// when the rule changes again.
	if kube.BuildHPA(spec) == nil {
		if err := applier.Delete(ctx, "autoscaling/v2", "HorizontalPodAutoscaler",
			spec.Namespace, kube.ResourceName(spec.Name, "hpa")); err != nil {
			d.log.Warn("could not remove the autoscaler", "app", app.ID, "error", err)
		}
	}
	if kube.BuildPDB(spec) == nil {
		if err := applier.Delete(ctx, "policy/v1", "PodDisruptionBudget",
			spec.Namespace, kube.ResourceName(spec.Name, "pdb")); err != nil {
			d.log.Warn("could not remove the disruption budget", "app", app.ID, "error", err)
		}
	}
	if !kube.ScaleToZeroEnabled(spec) {
		// Left behind, these would keep routing traffic through an interceptor
		// for an app that no longer sleeps.
		if err := applier.Delete(ctx, "http.keda.sh/v1alpha1", "HTTPScaledObject",
			spec.Namespace, spec.Name); err != nil {
			d.log.Warn("could not remove the scale-to-zero object", "app", app.ID, "error", err)
		}
		if err := applier.Delete(ctx, "v1", "Service",
			spec.Namespace, kube.InterceptorServiceName(spec.Name)); err != nil {
			d.log.Warn("could not remove the wake service", "app", app.ID, "error", err)
		}
	}
}

// Sync applies an app's current configuration without building.
//
// This is what a change to a variable, a domain or a replica count does, and it
// is why those changes never trigger a build.
func (d *Deployer) Sync(ctx context.Context, appID string) error {
	app, err := d.db.GetApp(ctx, appID)
	if err != nil {
		return err
	}
	last, err := d.db.LatestSuccessfulDeployment(ctx, appID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Nothing has been deployed yet, so there is nothing to update.
			return nil
		}
		return err
	}
	env, err := d.db.GetEnvironment(ctx, app.EnvironmentID)
	if err != nil {
		return err
	}
	if d.cluster == nil {
		return nil
	}
	return d.apply(ctx, last, app, env)
}

// Rollback re-applies a previous deployment's image and runtime configuration.
func (d *Deployer) Rollback(ctx context.Context, appID, deploymentID, actorID string) (store.Deployment, error) {
	previous, err := d.db.GetDeployment(ctx, deploymentID)
	if err != nil {
		return store.Deployment{}, err
	}
	if previous.AppID != appID {
		return store.Deployment{}, errdoc.NotFound("deployment", deploymentID)
	}
	if previous.Image == "" {
		return store.Deployment{}, errdoc.BadRequest("That version has no image to roll back to.")
	}
	// The registry keeps the last few images per app and collects the rest, so
	// a record older than that window is still worth reading and is no longer
	// something to go back to. Saying so here is the difference between a clear
	// refusal and a rollout that sits in ImagePullBackOff.
	within, err := d.db.WithinRollbackWindow(ctx, appID, deploymentID, registry.KeptPerApp)
	if err != nil {
		return store.Deployment{}, err
	}
	if !within {
		return store.Deployment{}, errdoc.ImageCollected(previous.Number, registry.KeptPerApp)
	}

	// A rollback is a new deployment carrying the old image, so the history
	// stays a straight line and rolling forward again is just another rollback.
	deployment := store.Deployment{
		AppID:            appID,
		Status:           store.DeployQueued,
		Trigger:          "rollback",
		RollbackOf:       previous.Number,
		CommitSHA:        previous.CommitSHA,
		CommitMessage:    previous.CommitMessage,
		CommitAuthor:     previous.CommitAuthor,
		Image:            previous.Image,
		BuildFingerprint: previous.BuildFingerprint,
		RuntimeSpec:      previous.RuntimeSpec,
		CreatedBy:        actorID,
	}
	if err := d.db.CreateDeployment(ctx, &deployment); err != nil {
		return store.Deployment{}, err
	}

	// Restore the runtime settings the old version ran with, so a rollback
	// undoes a bad configuration change and not only a bad build.
	if err := d.restoreRuntimeSpec(ctx, appID, previous.RuntimeSpec); err != nil {
		d.log.Warn("could not restore the previous settings", "app", appID, "error", err)
	}

	d.start(deployment.ID, func(runCtx context.Context) {
		d.run(runCtx, deployment.ID)
	})
	return deployment, nil
}

// Cancel stops an in-flight build or rollout.
func (d *Deployer) Cancel(ctx context.Context, deploymentID string) error {
	d.mu.Lock()
	cancel, running := d.running[deploymentID]
	d.mu.Unlock()
	if !running {
		return errdoc.BadRequest("That deployment is not running.")
	}
	cancel()
	if err := d.db.UpdateDeploymentStatus(ctx, deploymentID, store.DeployCancelled, "cancelled", "Cancelled by a user.", ""); err != nil {
		return err
	}
	d.publish(ctx, deploymentID)
	return nil
}

// stop cancels a running deployment's work without touching its status, which
// the caller has already decided.
func (d *Deployer) stop(deploymentID string) {
	d.mu.Lock()
	cancel, running := d.running[deploymentID]
	d.mu.Unlock()
	if running {
		cancel()
	}
}

func (d *Deployer) start(deploymentID string, work func(context.Context)) {
	ctx, cancel := context.WithTimeout(context.Background(), maxDeployDuration)

	d.mu.Lock()
	if _, already := d.running[deploymentID]; already {
		d.mu.Unlock()
		cancel()
		return
	}
	d.running[deploymentID] = cancel
	d.mu.Unlock()

	// Every deployment runs through here, so this is the one place a panic in
	// one of them has to be stopped. Without it a nil pointer in a build or a
	// rollout takes the whole panel down, and the panel is often the only way
	// to reach the cluster and find out why.
	go func() {
		defer cancel()
		defer runsafe.Recover(d.log, "deployment "+deploymentID, func(err error) {
			d.finish(deploymentID)
			d.failPanicked(ctx, deploymentID, err)
		})
		work(ctx)
	}()
}

// failPanicked marks a deployment failed after the goroutine running it
// panicked, so what the user sees is a deployment that failed rather than one
// that is still going and never will be.
func (d *Deployer) failPanicked(ctx context.Context, deploymentID string, err error) {
	problem := errdoc.New("internal", "Something went wrong").
		WithCause("The panel hit an unexpected error while deploying.").
		WithImpact("The deployment stopped where it was. Whatever had already been applied is still applied.").
		WithFix("Deploy again. If it happens every time, copy this error and open an issue: it is a bug in Skifity, not in your app.")

	_ = d.db.UpdateDeploymentStatus(ctx, deploymentID, store.DeployFailed,
		problem.Code, err.Error(), problem.Fix)
	if deployment, getErr := d.db.GetDeployment(ctx, deploymentID); getErr == nil {
		_ = d.db.SetAppStatus(ctx, deployment.AppID, "failed")
	}
	d.hub.Publish(events.DeploymentTopic(deploymentID), "failed", problem)
}

func (d *Deployer) finish(deploymentID string) {
	d.mu.Lock()
	delete(d.running, deploymentID)
	d.mu.Unlock()
}

func (d *Deployer) setStatus(ctx context.Context, deployment *store.Deployment, status store.DeploymentStatus) {
	deployment.Status = status
	if err := d.db.UpdateDeploymentStatus(ctx, deployment.ID, status, "", "", ""); err != nil {
		d.log.Warn("could not update the deployment status", "deployment", deployment.ID, "error", err)
	}
	d.publish(ctx, deployment.ID)
}

func (d *Deployer) fail(ctx context.Context, deployment store.Deployment, problem *errdoc.Problem) {
	// A cancelled deploy is not a failure to shout about.
	if errors.Is(problem, context.Canceled) {
		_ = d.db.UpdateDeploymentStatus(ctx, deployment.ID, store.DeployCancelled, "cancelled", "Cancelled.", "")
		d.publish(ctx, deployment.ID)
		return
	}
	d.log.Error("deployment failed",
		"deployment", deployment.ID, "app", deployment.AppID, "code", problem.Code, "error", problem.Error())

	d.Metrics.Inc("skifity_deployments_total", "result", "failed")
	_ = d.db.UpdateDeploymentStatus(ctx, deployment.ID, store.DeployFailed,
		problem.Code, problem.Error(), problem.Fix)
	_ = d.db.SetAppStatus(ctx, deployment.AppID, "failed")

	d.appendLog(ctx, deployment.ID, "")
	for _, line := range strings.Split(problem.Text(), "\n") {
		d.appendLog(ctx, deployment.ID, line)
	}
	d.hub.Publish(events.DeploymentTopic(deployment.ID), "failed", problem)
	d.publish(ctx, deployment.ID)

	app, err := d.db.GetApp(ctx, deployment.AppID)
	if err != nil {
		return
	}
	d.notify(ctx, app, deployment, notify.EventDeployFailed, notify.Message{
		Title:  "Deploying " + app.Name + " failed",
		Body:   problem.Error() + "\n\n" + problem.Fix,
		Level:  "error",
		Fields: map[string]string{"Reason": problem.Code},
	})
	// A plugin that subscribed to deploy.failed was never told, because only
	// the success path sent anything. A rollback plugin, or one that opens a
	// ticket, heard about every deploy that worked and none that did not.
	teamID, err := d.db.TeamIDForApp(ctx, app.ID)
	if err != nil {
		return
	}
	d.Plugins.Notify(ctx, plugins.EventDeployFailed, teamID, map[string]any{
		"app_id": app.ID, "app": app.Slug,
		"deployment_id": deployment.ID, "commit": deployment.CommitSHA,
		"reason": problem.Code, "error": problem.Error(),
	})
}

// notify fills in what every deployment notification carries and sends it.
//
// A failure to work out the team is not worth failing a deployment over, so it
// is logged and the notification is dropped.
func (d *Deployer) notify(ctx context.Context, app store.App, deployment store.Deployment, event string, msg notify.Message) {
	if d.notifier == nil {
		return
	}
	teamID, err := d.db.TeamIDForApp(ctx, app.ID)
	if err != nil {
		d.log.Warn("could not work out which team to notify", "app", app.ID, "error", err)
		return
	}
	if msg.Fields == nil {
		msg.Fields = map[string]string{}
	}
	msg.Fields["App"] = app.Name
	if deployment.CommitSHA != "" {
		msg.Fields["Commit"] = shortSHA(deployment.CommitSHA)
	}
	msg.Path = "/apps/" + app.ID + "/deployments"
	d.notifier.Notify(ctx, teamID, event, msg)
}

// shortSHA trims a commit to the seven characters people actually read.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func (d *Deployer) publish(ctx context.Context, deploymentID string) {
	deployment, err := d.db.GetDeployment(ctx, deploymentID)
	if err != nil {
		return
	}
	d.hub.Publish(events.DeploymentTopic(deploymentID), "deployment", deployment)
	if teamID, err := d.db.TeamIDForApp(ctx, deployment.AppID); err == nil {
		d.hub.Publish(events.TeamTopic(teamID), "deployment", deployment)
	}
}

// appendLog stores a line and streams it.
func (d *Deployer) appendLog(ctx context.Context, deploymentID, line string) {
	seq, err := d.db.AppendBuildLog(ctx, deploymentID, "stdout", line)
	if err != nil {
		d.log.Warn("could not store a build log line", "deployment", deploymentID, "error", err)
		return
	}
	d.hub.Publish(events.DeploymentTopic(deploymentID), "log",
		map[string]any{"seq": seq, "stream": "stdout", "line": line})
}

// runtimeVariables collects everything that becomes an environment variable:
// the project's shared variables, then the app's own, then the connection
// strings of the databases linked to it.
//
// Later sources win, so an app can override a shared value, and a database link
// cannot be shadowed by accident.
func (d *Deployer) runtimeVariables(ctx context.Context, app store.App, env store.Environment) (map[string]string, error) {
	out := map[string]string{}

	shared, err := d.db.ListSharedVariables(ctx, env.ProjectID)
	if err != nil {
		return nil, err
	}
	for _, row := range shared {
		plaintext, err := d.keyring.Open(row.Sealed, "shared_variable:"+env.ProjectID+":"+row.Key)
		if err != nil {
			return nil, fmt.Errorf("read the shared variable %s: %w", row.Key, err)
		}
		out[row.Key] = string(plaintext)
	}

	own, err := d.db.ListVariables(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	for _, row := range own {
		plaintext, err := d.keyring.Open(row.Sealed, "variable:"+app.ID+":"+row.Key)
		if err != nil {
			return nil, fmt.Errorf("read the variable %s: %w", row.Key, err)
		}
		out[row.Key] = string(plaintext)
	}
	return out, nil
}

// buildTimeVariables are the subset that affects what the image contains, and
// therefore the build fingerprint.
func (d *Deployer) buildTimeVariables(ctx context.Context, app store.App) (map[string]string, error) {
	rows, err := d.db.ListVariables(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, row := range rows {
		if !row.BuildTime {
			continue
		}
		plaintext, err := d.keyring.Open(row.Sealed, "variable:"+app.ID+":"+row.Key)
		if err != nil {
			return nil, fmt.Errorf("read the build variable %s: %w", row.Key, err)
		}
		out[row.Key] = string(plaintext)
	}
	return out, nil
}

// runtimeSpec captures the settings a deployment ran with, so a rollback can
// restore them rather than only the image.
func (d *Deployer) runtimeSpec(ctx context.Context, app store.App, env store.Environment) (string, error) {
	domains, err := d.db.ListDomains(ctx, app.ID)
	if err != nil {
		return "", err
	}
	hostnames := make([]string, 0, len(domains))
	for _, domain := range domains {
		hostnames = append(hostnames, domain.Hostname)
	}

	spec := map[string]any{
		"replicas":       app.Replicas,
		"autoscale":      app.Autoscale,
		"min_replicas":   app.MinReplicas,
		"max_replicas":   app.MaxReplicas,
		"cpu_target":     app.CPUTarget,
		"cpu_request_m":  app.CPURequestM,
		"cpu_limit_m":    app.CPULimitM,
		"mem_request_mb": app.MemRequestMB,
		"mem_limit_mb":   app.MemLimitMB,
		"port":           app.Port,
		"health_path":    app.HealthPath,
		"start_command":  app.StartCommand,
		"domains":        hostnames,
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("record the runtime settings: %w", err)
	}
	return string(encoded), nil
}

// restoreRuntimeSpec puts back the settings a previous deployment ran with.
func (d *Deployer) restoreRuntimeSpec(ctx context.Context, appID, encoded string) error {
	if encoded == "" || encoded == "{}" {
		return nil
	}
	var spec struct {
		Replicas     int    `json:"replicas"`
		Autoscale    bool   `json:"autoscale"`
		MinReplicas  int    `json:"min_replicas"`
		MaxReplicas  int    `json:"max_replicas"`
		CPUTarget    int    `json:"cpu_target"`
		CPURequestM  int    `json:"cpu_request_m"`
		CPULimitM    int    `json:"cpu_limit_m"`
		MemRequestMB int    `json:"mem_request_mb"`
		MemLimitMB   int    `json:"mem_limit_mb"`
		Port         int    `json:"port"`
		HealthPath   string `json:"health_path"`
		StartCommand string `json:"start_command"`
	}
	if err := json.Unmarshal([]byte(encoded), &spec); err != nil {
		return fmt.Errorf("read the recorded settings: %w", err)
	}

	app, err := d.db.GetApp(ctx, appID)
	if err != nil {
		return err
	}
	app.Replicas = spec.Replicas
	app.Autoscale = spec.Autoscale
	app.MinReplicas = spec.MinReplicas
	app.MaxReplicas = spec.MaxReplicas
	app.CPUTarget = spec.CPUTarget
	app.CPURequestM = spec.CPURequestM
	app.CPULimitM = spec.CPULimitM
	app.MemRequestMB = spec.MemRequestMB
	app.MemLimitMB = spec.MemLimitMB
	app.Port = spec.Port
	app.HealthPath = spec.HealthPath
	app.StartCommand = spec.StartCommand
	return d.db.UpdateApp(ctx, &app)
}

// registryAddress is where images are pushed, which is the in-cluster registry
// unless an external one is configured.
func (d *Deployer) registryAddress(ctx context.Context) (address string, insecure bool, secret string, err error) {
	external, _, err := d.db.GetSetting(ctx, settings.KeyRegistryURL)
	if err != nil {
		return "", false, "", err
	}
	if external != "" {
		return strings.TrimSuffix(external, "/"), false, kube.RegistrySecretName, nil
	}
	if d.cluster == nil {
		return "", false, "", errdoc.ClusterUnreachable(nil)
	}
	return d.cluster.RegistryAddress(), true, "", nil
}

// ensureRegistryAuth puts the external registry's credentials where both ends
// of a deploy can read them.
//
// They were stored, sealed and shown on the settings page, and turned into
// nothing: the build Job mounted a Secret nobody created, so the pod could not
// start, and the app had no pull secret either. An external registry broke the
// deploy at both ends and said only that a Secret was missing.
//
// Applied on every deploy rather than when the setting is saved: a namespace
// is created when an environment is, credentials change, and a secret that
// exists only because somebody pressed save in the right order is one that
// goes missing.
func (d *Deployer) ensureRegistryAuth(ctx context.Context, appNamespace string) error {
	address, _, secretName, err := d.registryAddress(ctx)
	if err != nil || secretName == "" {
		// No external registry, or no cluster to tell: either way there is no
		// credential to place.
		return nil //nolint:nilerr // the caller reports an unreachable cluster
	}
	username, err := d.settingValue(ctx, settings.KeyRegistryUser)
	if err != nil {
		return err
	}
	password, err := d.settingValue(ctx, settings.KeyRegistryPassword)
	if err != nil {
		return err
	}
	if username == "" && password == "" {
		// A registry inside a private network needs no credential, and an
		// empty secret would be worse than none.
		return nil
	}

	for _, namespace := range []string{d.cluster.Client().BuildNamespace(), appNamespace} {
		secret, err := kube.RegistrySecret(namespace, registryHost(address), username, password)
		if err != nil {
			return err
		}
		if err := d.cluster.Client().Applier().Apply(ctx, secret); err != nil {
			return fmt.Errorf("place the registry credentials in %s: %w", namespace, err)
		}
	}
	return nil
}

// settingValue reads one setting, decrypting it when it was sealed.
func (d *Deployer) settingValue(ctx context.Context, key string) (string, error) {
	value, encrypted, err := d.db.GetSetting(ctx, key)
	if err != nil || value == "" || !encrypted {
		return value, err
	}
	plaintext, err := d.keyring.Open(value, settings.Context(key))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", key, err)
	}
	return string(plaintext), nil
}

// registryHost is the part of a registry address a docker config is filed
// under: the host, without a scheme and without a path.
func registryHost(address string) string {
	address = strings.TrimPrefix(strings.TrimPrefix(address, "https://"), "http://")
	if idx := strings.IndexByte(address, '/'); idx > 0 {
		address = address[:idx]
	}
	return address
}
