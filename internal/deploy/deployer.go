// Package deploy turns a source commit into running instances.
package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"skifity/internal/api"
	"skifity/internal/cluster"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/gitsrc"
	"skifity/internal/kube"
	"skifity/internal/metrics"
	"skifity/internal/notify"
	"skifity/internal/plugins"
	"skifity/internal/registry"
	"skifity/internal/runsafe"
	"skifity/internal/secretmgr"
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
	// PanelURL is the panel's public address, for the link a Git host shows
	// beside a commit's status. Nil or empty means the status carries none.
	PanelURL func(context.Context) string
	// Secrets reads the variables whose values live in a secret manager. New
	// gives every Deployer one that dials through internal/netguard.
	Secrets *secretmgr.Resolver
	gitHost gitHost
	log     *slog.Logger

	mu      sync.Mutex
	running map[string]context.CancelFunc
	// syncing counts the applies in flight per app that are not a
	// deployment's; see Busy.
	syncing map[string]int

	// slots queues builds past the panel's limit. See buildslots.go.
	slots *buildSlots
	// scans queues image scans, which run one at a time. See scan.go.
	scans *buildSlots
	// scanJob runs one scan. Nil is the Trivy Job in the cluster; a test puts
	// a scanner of its own here.
	scanJob func(context.Context, store.ImageScan) (store.ScanResult, error)
}

// New builds a Deployer. notifier may be nil, and then nothing is sent.
func New(db *store.DB, keyring *crypto.Keyring, hub *events.Hub, c *cluster.Cluster, notifier notify.Notifier, log *slog.Logger) *Deployer {
	return &Deployer{
		db: db, keyring: keyring, hub: hub, cluster: c, notifier: notifier, log: log,
		Secrets: secretmgr.New(db, keyring),
		running: map[string]context.CancelFunc{},
		syncing: map[string]int{},
		slots:   newBuildSlots(),
		scans:   newBuildSlots(),
	}
}

// maxDeployDuration bounds a whole deploy, build included.
//
// Forty-five minutes, plus however much longer than the default an app may
// be allowed to take to start: a slow build followed by an app with half an
// hour to warm up is slow, not stuck, and a bound that cut it off would fail a
// deployment that was doing what it had been told it could.
const maxDeployDuration = 45*time.Minute +
	(kube.MaxHealthStartSeconds-kube.DefaultHealthStartSeconds)*time.Second

// rolloutWait is how long a deployment waits for an app's new instances to
// be ready: ten minutes, or the app's start budget with room for pulling and
// scheduling on top, whichever is longer. It has to be at least the budget,
// or a deployment gives up on an instance that is still within the time the
// app's settings allow it.
func rolloutWait(spec kube.AppSpec) time.Duration {
	return max(10*time.Minute, time.Duration(spec.HealthStart()+kube.RolloutMargin)*time.Second)
}

// Deploy queues a deployment and starts it.
//
// The record is created synchronously so the UI can open the log view straight
// away; the build and rollout run in the background.
func (d *Deployer) Deploy(ctx context.Context, req api.DeployRequest) (store.Deployment, error) {
	app, err := d.db.GetApp(ctx, req.AppID)
	if err != nil {
		return store.Deployment{}, err
	}
	if err := d.checkUnlocked(ctx, app.ID); err != nil {
		return store.Deployment{}, err
	}
	env, err := d.db.GetEnvironment(ctx, app.EnvironmentID)
	if err != nil {
		return store.Deployment{}, err
	}

	// Empty means the build clones the branch tip, and the image is tagged with
	// the deployment number instead of a commit.
	commit := req.CommitSHA
	promoting := req.Image != ""
	if app.SourceType == "upload" && !promoting {
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
	fingerprint := kube.BuildFingerprint(app.RepoURL, commit, app.Builder, app.DockerfilePath, app.RootDir,
		app.BuildCommand, app.StaticDir, buildArgs)
	if app.SourceType == "image" {
		// A prebuilt image is its own fingerprint: nothing is built.
		fingerprint = "image:" + app.Image
	}
	// A promoted image was built with the other environment's build settings
	// and build-time variables. When this app's would give a different build
	// — a NEXT_PUBLIC_API_URL for staging baked into production — running it
	// is running code built for somewhere else, and that has to be asked for.
	if promoting && app.SourceType != "image" && fingerprint != req.Fingerprint && !req.Force {
		return store.Deployment{}, errdoc.PromotionBuiltDifferently(app.Name)
	}

	runtimeSpec, err := d.runtimeSpec(ctx, app, env)
	if err != nil {
		return store.Deployment{}, err
	}

	deployment := store.Deployment{
		AppID:                   app.ID,
		Status:                  store.DeployQueued,
		Trigger:                 req.Trigger,
		CommitSHA:               commit,
		BuildFingerprint:        fingerprint,
		RuntimeSpec:             runtimeSpec,
		CreatedBy:               req.CreatedBy,
		AcceptedVulnerabilities: req.AcceptVulnerabilities,
	}

	// The heart of ADR-0007: when the build inputs have not changed, the
	// existing image is reused and this is a rollout, not a build. Changing an
	// environment variable or a replica count never rebuilds.
	if promoting {
		deployment.Image = req.Image
		deployment.CommitMessage = req.CommitMessage
		deployment.CommitAuthor = req.CommitAuthor
		deployment.BuildFingerprint = req.Fingerprint
	} else if !req.Force && app.SourceType != "image" && (commit != "" || app.SourceType != "git") {
		// Not for a Git deploy that names no commit — the Deploy button, a
		// CLI deploy with no --commit: it builds whatever the branch holds
		// now, which nobody knows until it is cloned. Its fingerprint said
		// "no commit" every time, so the second one reused the first one's
		// image and put the app back on that code, whatever had been pushed
		// and deployed in between.
		if previous, err := d.db.FindDeploymentByFingerprint(ctx, app.ID, fingerprint, registry.KeptPerApp); err == nil {
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
	d.supersede(ctx, app.ID, deployment.ID)

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

	d.reportToGit(ctx, deployment, gitsrc.StatePending, "")

	// Every variable read from a secret manager is read before anything is
	// built or applied: one that cannot be read fails the deployment now,
	// with the version that was serving still serving, rather than after a
	// ten-minute build. What was read is kept for the rest of this deploy, so
	// the build and the rollout ask each manager once between them.
	ctx = secretmgr.WithCache(ctx)
	if _, err := d.resolveVariables(ctx, app, env); err != nil {
		d.fail(ctx, deployment, errdoc.From(err))
		return
	}

	if deployment.Image == "" {
		// Queued until a build slot is free, and said so: a deployment that
		// sits at "queued" with nothing in its log looks stuck.
		release, err := d.slots.acquire(ctx, func() int { return d.buildLimit(ctx) }, func(running, ahead int) {
			d.appendLog(ctx, deployment.ID, fmt.Sprintf(
				"Waiting to build: %d builds are running, as many as this panel runs at once, and %d %s ahead of this one.",
				running, ahead, pluralIs(ahead)))
		})
		if err != nil {
			d.fail(ctx, deployment, errdoc.From(err))
			return
		}
		d.setStatus(ctx, &deployment, store.DeployBuilding)
		_, err = d.build(ctx, &deployment, app, env)
		release()
		if err != nil {
			d.fail(ctx, deployment, errdoc.From(err))
			return
		}
	} else {
		d.appendLog(ctx, deployment.ID,
			"Nothing to build: this version was already built, so the existing image is reused.")
	}

	// Before anything reaches the cluster, like the plugins below: a deploy
	// stopped for a vulnerability with a fix changes nothing that is running.
	if err := d.checkImage(ctx, deployment, app); err != nil {
		d.fail(ctx, deployment, errdoc.From(err))
		return
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

	processes, err := d.apply(ctx, deployment, app, env, true)
	if err != nil {
		d.fail(ctx, deployment, errdoc.From(err))
		return
	}
	// The app is serving this version from here on, so it is recorded as the
	// one that succeeded now — on a context of its own. Written after the
	// processes and the seed, a cancel or the deploy's deadline landing in
	// between left the row cancelled or "deploying" with the new version
	// live, and the next variable change synced the app back to the version
	// before it.
	finished := context.WithoutCancel(ctx)
	_ = d.db.UpdateDeploymentStatus(finished, deployment.ID, store.DeploySucceeded, "", "", "")
	d.watchProcesses(ctx, deployment, app, env, processes)
	d.runSeed(ctx, deployment, app, env)
	ctx = finished

	d.appendLog(ctx, deployment.ID, "Deployed.")
	d.Metrics.Inc("skifity_deployments_total", "result", "succeeded")
	_ = d.db.SetAppStatus(ctx, app.ID, "running")
	d.publish(ctx, deployment.ID)
	d.reportToGit(ctx, deployment, gitsrc.StateSuccess, "")
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
// app's own rollout, and answers with the processes it applied.
//
// A new version reaches the processes only once the app itself is serving it:
// a worker running this week's code while the web rollout fails and the web
// stays on last week's is the skew that one build for both is meant to rule
// out. A sync changes no image, so it applies everything at once — waiting
// for the web first would mean a process edit never lands while the web is
// unwell, which is when somebody is most likely to be making one.
func (d *Deployer) apply(ctx context.Context, deployment store.Deployment, app store.App, env store.Environment, newVersion bool) ([]store.AppProcess, error) {
	if d.cluster == nil {
		return nil, errdoc.ClusterUnreachable(nil)
	}

	// Every app gets a working address, and it is worked out here rather than
	// at creation because the settings it depends on — a wildcard domain, the
	// cluster's public IP — are often filled in after the app already exists.
	teamID, err := d.db.TeamIDForApp(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	// Before the objects that reference it: a Deployment naming a pull secret
	// that is not there yet is an ImagePullBackOff nobody can explain.
	if err := d.ensureRegistryAuth(ctx, env.Namespace); err != nil {
		return nil, err
	}
	if err := d.ensureTeamRegistries(ctx, env.Namespace, teamID); err != nil {
		return nil, err
	}
	// An internal app is reached by name from its environment and has no
	// address to give.
	if !app.Internal {
		if err := d.cluster.EnsureAutoDomain(ctx, app, env, teamID); err != nil {
			// An app that cannot be given a free URL can still be deployed on a
			// domain of its own, so this is a warning and not a failure.
			//
			// It goes in the deployment log as well as the panel's, because the
			// consequence is an app with no address, and a person looking at
			// that app has no reason to read the panel's log or any way to
			// reach it.
			d.log.Warn("could not give the app an automatic domain", "app", app.ID, "error", err)
			d.appendLog(ctx, deployment.ID,
				"This app has no automatic address: "+errdoc.From(err).Cause)
		}
	}

	out, err := d.render(ctx, deployment, app, env)
	if err != nil {
		return nil, err
	}
	spec, processes := out.spec, out.processes
	objects, processObjects := asAny(out.objects), asAny(out.processObjects)

	// Make sure the namespace and its guards exist: an app can be created
	// before the cluster was reachable.
	project, err := d.db.GetProject(ctx, env.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := d.cluster.EnsureNamespace(ctx, env, project.TeamID, project.ID); err != nil {
		return nil, err
	}

	if !newVersion {
		objects = append(objects, processObjects...)
	}

	d.appendLog(ctx, deployment.ID, "Applying the configuration to the cluster.")
	if err := d.cluster.Client().Applier().ApplyAll(ctx, objects...); err != nil {
		return nil, err
	}
	// A sync applies everything the app has; a new version, everything but
	// its processes, which keep their place in the list until they follow.
	d.recordApplied(ctx, app.ID, objects, !newVersion)
	// What the app's Secret now holds for each variable read from a secret
	// manager, so the next refresh can say which of them changed.
	d.recordReferences(ctx, app.ID, out.variables)

	// Scheduled commands run the version that is deployed, so they are applied
	// with it rather than when somebody writes the schedule — and a new
	// version's only once the app serves it, below, like its processes: a
	// nightly job on this week's code while the web stays on last week's is
	// the same skew.
	if !newVersion {
		if err := d.applyScheduledJobs(ctx, deployment.ID, spec, app); err != nil {
			return nil, err
		}
	}

	// Objects that are no longer wanted have to be removed explicitly: server-
	// side apply removes fields, not whole objects.
	d.removeUnwanted(ctx, spec, app)
	keep := make(map[string]bool, len(processes))
	for _, process := range processes {
		keep[process.Name] = true
	}
	if err := d.cluster.Client().PruneProcesses(ctx, env.Namespace, app.Slug, keep); err != nil {
		d.log.Warn("could not remove a process that is no longer wanted", "app", app.ID, "error", err)
	}

	d.appendLog(ctx, deployment.ID, "Waiting for the new instances to become ready.")
	if err := d.cluster.Client().WaitForRollout(ctx, env.Namespace, app.Slug, rolloutWait(spec)); err != nil {
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
		return nil, errdoc.RolloutTimedOut(app.Name, ready, wanted, reason)
	}
	if !newVersion {
		return processes, nil
	}
	// From here the app is serving this version, so nothing below fails the
	// deployment: one marked failed with the new version live is one the next
	// variable change would sync the app back from. What did not happen is
	// said in the log, and the next deploy or sync applies it again.
	if err := d.applyScheduledJobs(ctx, deployment.ID, spec, app); err != nil {
		d.appendLog(ctx, deployment.ID, "The scheduled commands could not be moved to this version, and still run the previous one: "+
			errdoc.From(err).Cause)
	}
	if len(processObjects) > 0 {
		d.appendLog(ctx, deployment.ID, "Starting the new version of the app's other processes.")
		if err := d.cluster.Client().Applier().ApplyAll(ctx, processObjects...); err != nil {
			d.appendLog(ctx, deployment.ID, "The app's other processes could not be moved to this version, and still run the previous one: "+
				errdoc.From(err).Cause)
			return nil, nil
		}
		d.recordApplied(ctx, app.ID, processObjects, false)
	}
	return processes, nil
}

// processWait is how long a deployment waits for the app's other processes,
// all of them at once, before saying which are not ready and finishing.
const processWait = 5 * time.Minute

// watchProcesses waits for a new version's processes and says in the
// deployment's log which did not come up. It does not fail the deployment:
// the app is already serving the new version by then, and a deployment
// marked failed is one the next variable change would quietly roll the app
// back from. A worker that crashes is the worker's state, shown beside it
// under Processes, the way Heroku shows a crashed dyno after a release.
func (d *Deployer) watchProcesses(ctx context.Context, deployment store.Deployment, app store.App, env store.Environment, processes []store.AppProcess) {
	if len(processes) == 0 {
		return
	}
	d.appendLog(ctx, deployment.ID, "Waiting for the app's other processes.")
	var wg sync.WaitGroup
	notReady := make([]string, len(processes))
	for i, process := range processes {
		if process.Instances == 0 {
			continue
		}
		wg.Add(1)
		go func() {
			defer runsafe.Recover(d.log, "wait for a process", nil)
			defer wg.Done()
			name := kube.ProcessDeploymentName(app.Slug, process.Name)
			err := d.cluster.Client().WaitForRollout(ctx, env.Namespace, name, processWait)
			if err == nil {
				return
			}
			reason := err.Error()
			if status, statusErr := d.cluster.AppStatus(ctx, env.Namespace, name); statusErr == nil && status.Detail != "" {
				reason = status.Detail
			}
			notReady[i] = process.Name + ": " + reason
		}()
	}
	wg.Wait()
	for _, line := range notReady {
		if line != "" {
			d.appendLog(ctx, deployment.ID, "Not ready yet, and still trying — "+line+
				". The app itself is serving this version; the process's state is under Processes.")
		}
	}
}

// removeUnwanted deletes objects the app no longer needs.
func (d *Deployer) removeUnwanted(ctx context.Context, spec kube.AppSpec, app store.App) {
	applier := d.cluster.Client().Applier()

	// Asking the builder rather than counting domains: an app whose every
	// hostname is on a certificate of the team's own has domains and no
	// first Ingress, and one left behind would keep sending those hostnames
	// to cert-manager.
	if kube.BuildIngress(spec) == nil {
		if err := applier.Delete(ctx, "networking.k8s.io/v1", "Ingress", spec.Namespace, spec.Name); err != nil {
			d.log.Warn("could not remove the ingress", "app", app.ID, "error", err)
		}
	}
	// The second Ingress when no hostname is on a certificate of the team's
	// own any more, and the Secret of each certificate the app stopped using:
	// removed from the panel, or no longer covering any of its hostnames.
	if err := d.cluster.Client().PruneCertificates(ctx, spec); err != nil {
		d.log.Warn("could not remove a certificate the app no longer uses", "app", app.ID, "error", err)
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
	if len(spec.PublicPorts) == 0 {
		// A port somebody closed is closed on every server, not left open
		// until the next thing that happens to tidy up.
		if err := applier.Delete(ctx, "v1", "Service", spec.Namespace, kube.PortsServiceName(spec.Name)); err != nil {
			d.log.Warn("could not close the app's public ports", "app", app.ID, "error", err)
		}
		if err := applier.Delete(ctx, "networking.k8s.io/v1", "NetworkPolicy",
			spec.Namespace, kube.PortsPolicyName(spec.Name)); err != nil {
			d.log.Warn("could not remove the app's public ports policy", "app", app.ID, "error", err)
		}
	}
	if len(spec.Files) == 0 {
		// Configuration somebody removed is not left in the cluster, where
		// anybody who can read the namespace's Secrets still reads it.
		if err := applier.Delete(ctx, "v1", "Secret", spec.Namespace, kube.FilesSecretName(spec.Name)); err != nil {
			d.log.Warn("could not remove the files secret", "app", app.ID, "error", err)
		}
	}
	if spec.PasswordUsers == "" {
		// The Ingress no longer names the middleware once the password is
		// gone, so these are only tidying — but a Secret holding a hash of a
		// password somebody took off is not something to leave lying around.
		if err := applier.Delete(ctx, "traefik.io/v1alpha1", "Middleware",
			spec.Namespace, kube.PasswordMiddlewareName(spec.Name)); err != nil {
			d.log.Warn("could not remove the password middleware", "app", app.ID, "error", err)
		}
		if err := applier.Delete(ctx, "v1", "Secret",
			spec.Namespace, kube.PasswordSecretName(spec.Name)); err != nil {
			d.log.Warn("could not remove the password secret", "app", app.ID, "error", err)
		}
	}
	// A redirect somebody took off, or whose hostname went, is not left to
	// answer for a name the Ingress no longer routes.
	wanted := map[string]bool{}
	for _, redirect := range kube.BuildHostRedirects(spec) {
		wanted[redirect.GetName()] = true
	}
	if middlewares, err := applier.List(ctx, "traefik.io/v1alpha1", "Middleware", spec.Namespace); err == nil {
		for _, middleware := range middlewares {
			if kube.IsHostRedirectOf(middleware, spec.Name) && !wanted[middleware.GetName()] {
				if err := applier.Delete(ctx, "traefik.io/v1alpha1", "Middleware",
					spec.Namespace, middleware.GetName()); err != nil {
					d.log.Warn("could not remove a redirect", "app", app.ID, "error", err)
				}
			}
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
	// Said while it runs, so the drift check does not read the app halfway
	// through being applied and take the panel's own apply for somebody
	// else's change.
	d.beginSync(appID)
	defer d.endSync(appID)

	app, err := d.db.GetApp(ctx, appID)
	if err != nil {
		return err
	}
	last, deployed, err := d.syncTarget(ctx, appID)
	if err != nil {
		return err
	}
	if !deployed {
		// Nothing has been deployed yet, so there is nothing to update.
		return nil
	}
	env, err := d.db.GetEnvironment(ctx, app.EnvironmentID)
	if err != nil {
		return err
	}
	// A variable read from a secret manager that cannot be read stops the
	// sync before anything reaches the cluster, whether or not there is one:
	// the app keeps the configuration it has, and the problem, naming the
	// variable, goes back to the caller.
	ctx = secretmgr.WithCache(ctx)
	if _, err := d.resolveVariables(ctx, app, env); err != nil {
		return err
	}
	if d.cluster == nil {
		return nil
	}
	_, err = d.apply(ctx, last, app, env, false)
	return err
}

// syncTarget is the deployment an apply of the app's configuration applies:
// the last that succeeded, or a newer one being rolled out.
//
// A version being rolled out is the one to apply the change to. The last that
// succeeded is the one before it, and re-applying that in the middle of a
// rollout — somebody adding the variable the new version crashes without,
// which is when it happens — put the app back on the old image, and the
// rollout then reported success for pods running it.
func (d *Deployer) syncTarget(ctx context.Context, appID string) (store.Deployment, bool, error) {
	last, err := d.db.LatestSuccessfulDeployment(ctx, appID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.Deployment{}, false, err
	}
	if rolling, rollingErr := d.db.DeploymentRollingOut(ctx, appID); rollingErr == nil &&
		(errors.Is(err, store.ErrNotFound) || rolling.Number > last.Number) {
		return rolling, true, nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return store.Deployment{}, false, nil
	}
	return last, true, nil
}

func (d *Deployer) beginSync(appID string) {
	d.mu.Lock()
	if d.syncing == nil {
		d.syncing = map[string]int{}
	}
	d.syncing[appID]++
	d.mu.Unlock()
}

func (d *Deployer) endSync(appID string) {
	d.mu.Lock()
	if d.syncing[appID]--; d.syncing[appID] <= 0 {
		delete(d.syncing, appID)
	}
	d.mu.Unlock()
}

// Busy reports whether the panel is applying an app's objects right now.
func (d *Deployer) Busy(appID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.syncing[appID] > 0
}

// Rollback re-applies a previous deployment's image and runtime configuration.
func (d *Deployer) Rollback(ctx context.Context, appID, deploymentID, actorID string) (store.Deployment, error) {
	if err := d.checkUnlocked(ctx, appID); err != nil {
		return store.Deployment{}, err
	}
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
	// A rollback is the version that should be running from now on, as much
	// as a deploy is: a build still in flight rolled out over it, and a stuck
	// rollout being rolled back saw the rollback finish and took the credit.
	d.supersede(ctx, appID, deployment.ID)

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

// supersede marks the deployments before this one that are still running as
// superseded, and stops them: marking the row is not enough, since the build
// it belongs to would go on to roll out an older version after this one.
func (d *Deployer) supersede(ctx context.Context, appID, deploymentID string) {
	superseded, err := d.db.SupersedeRunningDeployments(ctx, appID, deploymentID)
	if err != nil {
		d.log.Warn("could not supersede earlier deployments", "app", appID, "error", err)
	}
	for _, id := range superseded {
		d.log.Info("stopping a deployment that a newer one replaced",
			"app", appID, "deployment", id, "replaced_by", deploymentID)
		d.stop(id)
	}
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
	// Written on a context of its own: a deploy that ran into its deadline
	// failed because the context ran out, and every write on that context
	// failed with it — the row stayed "building" for good.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
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
	d.reportToGit(ctx, deployment, gitsrc.StateFailure, problem.Error())

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

// notify fills in what every deployment notification carries and sends it:
// the team to tell, and the project the app is in, so a channel limited to
// other projects is left out.
//
// A failure to work out the team is not worth failing a deployment over, so it
// is logged and the notification is dropped.
func (d *Deployer) notify(ctx context.Context, app store.App, deployment store.Deployment, event string, msg notify.Message) {
	if d.notifier == nil {
		return
	}
	teamID, projectID, err := d.db.ProjectOfApp(ctx, app.ID)
	if err != nil {
		d.log.Warn("could not work out which team to notify", "app", app.ID, "error", err)
		return
	}
	msg.ProjectID = projectID
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

// certificatePairs opens the team's own certificates the app's hostnames
// are served with, keyed by id, for their Secrets.
func (d *Deployer) certificatePairs(ctx context.Context, spec kube.AppSpec) (map[string]kube.CertificatePair, error) {
	used := kube.CertificatesUsed(spec)
	out := make(map[string]kube.CertificatePair, len(used))
	for _, id := range used {
		chain, sealed, err := d.db.CertificateMaterial(ctx, spec.TeamID, id)
		if err != nil {
			return nil, fmt.Errorf("read the certificate %s: %w", id, err)
		}
		key, err := d.keyring.Open(sealed, store.CertificateContext(spec.TeamID, id))
		if err != nil {
			return nil, fmt.Errorf("open the certificate %s: %w", id, err)
		}
		out[id] = kube.CertificatePair{Chain: []byte(chain), Key: key}
	}
	return out, nil
}

// fileContents opens an app's files, keyed as their Secret holds them.
func (d *Deployer) fileContents(ctx context.Context, app store.App) (map[string][]byte, error) {
	rows, err := d.db.ListFiles(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(rows))
	for _, row := range rows {
		content, err := d.keyring.Open(row.Sealed, store.FileContext(app.ID, row.Path))
		if err != nil {
			return nil, fmt.Errorf("read the file %s: %w", row.Path, err)
		}
		out[kube.FileKey(row.Path)] = content
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
		// How the version's instances were checked. A health check changed
		// to one the app cannot pass is a bad configuration change like any
		// other, and a rollback is how it is undone.
		"health_check":           app.HealthCheck,
		"health_start_seconds":   app.HealthStartSeconds,
		"health_timeout_seconds": app.HealthTimeoutSeconds,
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("record the runtime settings: %w", err)
	}
	return string(encoded), nil
}

// checkUnlocked refuses a deploy or a rollback of an app whose deploys are
// locked. Here, where every one of them passes — the panel, the CLI, an
// assistant, a webhook — rather than in each of those.
func (d *Deployer) checkUnlocked(ctx context.Context, appID string) error {
	lock, err := d.db.GetDeployLock(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		// A lock that cannot be read is not one to assume is absent.
		return err
	}
	return errdoc.DeployLocked(lock.LockedBy, lock.Reason)
}

// restoreRuntimeSpec puts back the settings a previous deployment ran with.
func (d *Deployer) restoreRuntimeSpec(ctx context.Context, appID, encoded string) error {
	if encoded == "" || encoded == "{}" {
		return nil
	}
	var spec store.RecordedSpec
	if err := json.Unmarshal([]byte(encoded), &spec); err != nil {
		return fmt.Errorf("read the recorded settings: %w", err)
	}
	spec = spec.WithHealthDefaults()

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
	app.HealthCheck = spec.HealthCheck
	app.HealthStartSeconds = spec.HealthStartSeconds
	app.HealthTimeoutSeconds = spec.HealthTimeoutSeconds
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

// teamLogins opens a team's registry credentials.
func (d *Deployer) teamLogins(ctx context.Context, teamID string) ([]kube.RegistryLogin, error) {
	rows, err := d.db.ListRegistryCredentials(ctx, teamID)
	if err != nil {
		return nil, err
	}
	logins := make([]kube.RegistryLogin, 0, len(rows))
	for _, row := range rows {
		password, err := d.keyring.Open(row.SealedPassword, store.RegistryContext(teamID, row.Host))
		if err != nil {
			return nil, fmt.Errorf("read the credentials for %s: %w", row.Host, err)
		}
		logins = append(logins, kube.RegistryLogin{Host: row.Host, Username: row.Username, Password: string(password)})
	}
	return logins, nil
}

// ensureTeamRegistries writes the team's registry credentials into an app's
// namespace, or removes them when the team has none left: a credential
// somebody took away is not left where a pod could still pull with it.
func (d *Deployer) ensureTeamRegistries(ctx context.Context, namespace, teamID string) error {
	logins, err := d.teamLogins(ctx, teamID)
	if err != nil {
		return err
	}
	applier := d.cluster.Client().Applier()
	if len(logins) == 0 {
		return applier.Delete(ctx, "v1", "Secret", namespace, kube.TeamRegistriesSecretName)
	}
	secret, err := kube.PullSecret(kube.TeamRegistriesSecretName, namespace, logins)
	if err != nil {
		return err
	}
	if err := applier.Apply(ctx, secret); err != nil {
		return fmt.Errorf("place the team's registry credentials in %s: %w", namespace, err)
	}
	return nil
}

// buildLimit is how many builds may run at once: the setting, or two.
func (d *Deployer) buildLimit(ctx context.Context) int {
	value, _, err := d.db.GetSetting(ctx, settings.KeyBuildConcurrency)
	if err != nil {
		return settings.DefaultBuildConcurrency
	}
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 1 {
		return settings.DefaultBuildConcurrency
	}
	return n
}

func pluralIs(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
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
