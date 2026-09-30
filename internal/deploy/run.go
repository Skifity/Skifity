package deploy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/api"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/logging"
	"skifity/internal/store"
)

// RunOnce starts a command in the app's own image, with the app's own
// variables, and returns the name of the Job doing it.
//
// It returns as soon as the Job exists rather than waiting: a migration takes
// as long as it takes, and the caller follows the log.
func (d *Deployer) RunOnce(ctx context.Context, appID, command string) (api.RunHandle, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return api.RunHandle{}, errdoc.BadRequest("Enter the command to run.")
	}
	if d.cluster == nil {
		return api.RunHandle{}, errdoc.ClusterUnreachable(nil)
	}

	spec, env, err := d.runSpecFor(ctx, appID)
	if err != nil {
		return api.RunHandle{}, err
	}

	// A name, not a token: a Kubernetes object name may not hold the "_" and
	// the uppercase that base64url produces.
	id, err := crypto.RandomName(8)
	if err != nil {
		return api.RunHandle{}, err
	}
	name := kube.RunJobName(spec.Name, kube.RunKindOneOff, id)

	job, err := kube.BuildRunJob(kube.RunSpec{
		App: spec, Name: name, Command: command, Kind: kube.RunKindOneOff,
	})
	if err != nil {
		return api.RunHandle{}, errdoc.BadRequest(err.Error())
	}
	if err := d.cluster.Client().Applier().Apply(ctx, job); err != nil {
		return api.RunHandle{}, err
	}
	d.log.Info("started a one-off command", "app", appID, "run", name)
	return api.RunHandle{Name: name, Namespace: env.Namespace}, nil
}

// runNamespace finds the namespace of one of an app's runs, refusing a run
// that is not the app's.
func (d *Deployer) runNamespace(ctx context.Context, appID, name string) (string, error) {
	if d.cluster == nil {
		return "", errdoc.ClusterUnreachable(nil)
	}
	app, err := d.db.GetApp(ctx, appID)
	if err != nil {
		return "", err
	}
	env, err := d.db.GetEnvironment(ctx, app.EnvironmentID)
	if err != nil {
		return "", err
	}
	// Which app a run belongs to is read off the Job rather than guessed from
	// its name: the name is derived from the app's slug and can be shortened
	// when that slug is long, so a prefix check would refuse a run it made
	// itself. Without this, an id from one app could read another app's output.
	job, err := d.cluster.Client().Clientset().BatchV1().Jobs(env.Namespace).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil || job.Labels["app.kubernetes.io/name"] != app.Slug ||
		job.Labels["app.kubernetes.io/component"] != "run" {
		return "", errdoc.NotFound("run", name)
	}
	return env.Namespace, nil
}

// runResultWait is how long RunResult waits for a run's container to stop
// after its output has: a container that has closed its output is seconds
// from being recorded as terminated, not minutes.
const runResultWait = 30 * time.Second

// RunResult says how a run ended: its container's exit status, once it has
// stopped. A run still going after runResultWait is not finished.
func (d *Deployer) RunResult(ctx context.Context, appID, name string) (api.RunResult, error) {
	namespace, err := d.runNamespace(ctx, appID, name)
	if err != nil {
		return api.RunResult{}, err
	}
	deadline := time.Now().Add(runResultWait)
	for {
		code, finished, err := d.cluster.Client().RunOutcome(ctx, namespace, name)
		if err != nil {
			return api.RunResult{}, err
		}
		if finished || time.Now().After(deadline) {
			return api.RunResult{Finished: finished, ExitCode: code}, nil
		}
		select {
		case <-ctx.Done():
			return api.RunResult{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// RunLogs reads a run's output, following it until the Job ends.
func (d *Deployer) RunLogs(ctx context.Context, appID, name string, follow bool) (io.ReadCloser, error) {
	namespace, err := d.runNamespace(ctx, appID, name)
	if err != nil {
		return nil, err
	}
	env := store.Environment{Namespace: namespace}

	podName, err := d.waitForBuildPod(ctx, env.Namespace, name)
	if err != nil {
		return nil, err
	}
	return d.cluster.Client().StreamClientset().CoreV1().Pods(env.Namespace).
		GetLogs(podName, &corev1.PodLogOptions{Follow: follow}).Stream(ctx)
}

// runSpecFor builds the app spec a run borrows from: the image of the last
// deployment that succeeded, and the variables the app runs with now.
func (d *Deployer) runSpecFor(ctx context.Context, appID string) (kube.AppSpec, store.Environment, error) {
	app, err := d.db.GetApp(ctx, appID)
	if err != nil {
		return kube.AppSpec{}, store.Environment{}, err
	}
	env, err := d.db.GetEnvironment(ctx, app.EnvironmentID)
	if err != nil {
		return kube.AppSpec{}, store.Environment{}, err
	}

	image := app.Image
	if image == "" {
		deployment, err := d.db.LatestSuccessfulDeployment(ctx, app.ID)
		if err != nil {
			return kube.AppSpec{}, env, errdoc.New("run.never_deployed", "This app has not been deployed yet").
				WithCause("A command runs in the app's own image, and there is no image until the app has been deployed once.").
				WithImpact("Nothing was run.").
				WithFix("Deploy the app, then run the command.")
		}
		image = deployment.Image
	}

	spec, err := d.cluster.SpecFor(ctx, app, env, image)
	if err != nil {
		return kube.AppSpec{}, env, err
	}
	return spec, env, nil
}

// runRelease runs an app's release command and waits for it.
//
// This is the point of a release phase: it happens after the image is built and
// before any traffic reaches the new version, so a migration that fails stops
// the deployment instead of leaving the new code talking to the old schema.
func (d *Deployer) runRelease(ctx context.Context, deployment store.Deployment, app store.App, env store.Environment, image string) error {
	command := strings.TrimSpace(app.ReleaseCommand)
	if command == "" {
		return nil
	}

	spec, err := d.cluster.SpecFor(ctx, app, env, image)
	if err != nil {
		return err
	}
	// What the release reads, in place before it runs: it runs before the
	// rollout that would otherwise make them. On a first deploy the pod sat
	// waiting for a variables Secret that did not exist yet, and was reported
	// as a build that never started; on later ones a migration ran with the
	// variables of the version before, and one from an external registry
	// could not be pulled at all.
	if err := d.prepareRuntime(ctx, app, env, spec); err != nil {
		return err
	}
	name := kube.RunJobName(app.Slug, kube.RunKindRelease, shortID(deployment.ID))
	job, err := kube.BuildRunJob(kube.RunSpec{
		App: spec, Name: name, Command: command, Kind: kube.RunKindRelease,
		// A release holds the deployment up, so it is bounded more tightly
		// than a command somebody is watching.
		TimeoutSeconds: 15 * 60,
	})
	if err != nil {
		return errdoc.BadRequest(err.Error())
	}

	// A retry of the same deployment reuses the name, and a finished Job with
	// that name would be rejected.
	_ = d.cluster.Client().Applier().Delete(ctx, "batch/v1", "Job", env.Namespace, name)
	if err := d.cluster.Client().Applier().Apply(ctx, job); err != nil {
		return err
	}

	d.appendLog(ctx, deployment.ID, "Running the release command: "+command)
	if err := d.streamRun(ctx, deployment, env.Namespace, name); err != nil {
		return err
	}
	d.appendLog(ctx, deployment.ID, "The release command finished.")
	return nil
}

// prepareRuntime makes what a pod of the app reads before it can start: the
// namespace and its guards, the registry's pull secret, and the Secrets with
// the app's variables and files as they are now. A release command runs
// before the app's first rollout has written either, and a pod that mounts a
// Secret which is not there never starts.
func (d *Deployer) prepareRuntime(ctx context.Context, app store.App, env store.Environment, spec kube.AppSpec) error {
	project, err := d.db.GetProject(ctx, env.ProjectID)
	if err != nil {
		return err
	}
	if err := d.cluster.EnsureNamespace(ctx, env, project.TeamID, project.ID); err != nil {
		return err
	}
	if err := d.ensureRegistryAuth(ctx, env.Namespace); err != nil {
		return err
	}
	if err := d.ensureTeamRegistries(ctx, env.Namespace, project.TeamID); err != nil {
		return err
	}
	variables, err := d.resolveVariables(ctx, app, env)
	if err != nil {
		return err
	}
	files, err := d.fileContents(ctx, app)
	if err != nil {
		return err
	}
	// Stamped as the deployer stamps them: the same Secrets with the same
	// contents carry the same fingerprint, so a run between two deploys does
	// not make the drift check think the app has something new to apply.
	secrets, err := kube.Prepare(kube.BuildEnvSecret(spec, variables.values), kube.BuildFilesSecret(spec, files))
	if err != nil {
		return err
	}
	if err := d.cluster.Client().Applier().ApplyAll(ctx, asAny(secrets)...); err != nil {
		return err
	}
	d.recordApplied(ctx, app.ID, asAny(secrets), false)
	d.recordReferences(ctx, app.ID, variables)
	return nil
}

// runSeed runs a preview's seed command, once, after its first deploy that
// succeeds: the demo data an empty preview database needs, the way Heroku's
// postdeploy script fills a review app.
//
// After the rollout rather than before it, because a seed is written against
// the schema the release command has just migrated to, and often through the
// app's own code. Once, even when it fails: a seed that half ran and is run
// again is duplicated rows, and the log says how to run it by hand. A failed
// seed does not fail the deploy, which has already happened.
func (d *Deployer) runSeed(ctx context.Context, deployment store.Deployment, app store.App, env store.Environment) {
	command := strings.TrimSpace(app.PreviewSeed)
	if env.Kind != store.EnvPreview || command == "" || app.SeededAt != "" {
		return
	}
	if first, err := d.db.MarkAppSeeded(ctx, app.ID); err != nil || !first {
		return
	}
	again := "Run it again with `skifity run --app " + app.ID + " -- " + command + "`."
	name := kube.RunJobName(app.Slug, kube.RunKindSeed, shortID(deployment.ID))
	if err := d.startSeed(ctx, app, env, deployment.Image, name, command); err != nil {
		d.appendLog(ctx, deployment.ID, "The seed command could not be started: "+err.Error()+". "+again)
		return
	}
	d.appendLog(ctx, deployment.ID, "Seeding this preview, once: "+command)
	if err := d.streamRun(ctx, deployment, env.Namespace, name); err != nil {
		d.appendLog(ctx, deployment.ID, "The seed command did not succeed; its output is above. The preview is up. "+again)
		return
	}
	d.appendLog(ctx, deployment.ID, "The seed command finished.")
}

// startSeed applies the Job a seed command runs in.
func (d *Deployer) startSeed(ctx context.Context, app store.App, env store.Environment, image, name, command string) error {
	spec, err := d.cluster.SpecFor(ctx, app, env, image)
	if err != nil {
		return err
	}
	job, err := kube.BuildRunJob(kube.RunSpec{
		App: spec, Name: name, Command: command, Kind: kube.RunKindSeed, TimeoutSeconds: 15 * 60,
	})
	if err != nil {
		return err
	}
	return d.cluster.Client().Applier().Apply(ctx, job)
}

// streamRun follows a release Job's log into the deployment's own log and
// reports whether it succeeded.
func (d *Deployer) streamRun(ctx context.Context, deployment store.Deployment, namespace, name string) error {
	podName, err := d.waitForBuildPod(ctx, namespace, name)
	if err != nil {
		return err
	}
	stream, err := d.cluster.Client().StreamClientset().CoreV1().Pods(namespace).
		GetLogs(podName, &corev1.PodLogOptions{Follow: true}).Stream(ctx)
	if err == nil {
		scanner := bufio.NewScanner(stream)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			// Scrubbed as build output is: a release command runs with every
			// one of the app's secrets in its environment, and a migration
			// tool that prints its connection string prints the password —
			// into a log every viewer of the team can read.
			d.appendLog(ctx, deployment.ID, logging.Scrub(scanner.Text()))
		}
		stream.Close()
	}

	deadline := time.Now().Add(20 * time.Minute)
	for {
		job, err := d.cluster.Client().Clientset().BatchV1().Jobs(namespace).
			Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("read the release command's result: %w", err)
		}
		if job.Status.Succeeded > 0 {
			return nil
		}
		if job.Status.Failed > 0 {
			return errdoc.New("release.failed", "The release command failed").
				WithCause("The command exited with an error before the new version was rolled out.").
				WithImpact("The deployment was stopped. The version that was running before is still running, and no traffic reached the new one.").
				WithFix("The command's output is in the build log above. Fix it and deploy again; nothing was changed for your users.").
				WithDocs("/docs/concepts#release-command")
		}
		if time.Now().After(deadline) {
			return errdoc.New("release.timed_out", "The release command did not finish").
				WithCause("It was still running after twenty minutes.").
				WithImpact("The deployment was stopped and the previous version is still serving.").
				WithFix("A migration that takes this long usually needs to be run by hand, once, rather than on every deploy.")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// cronApplier is the part of the cluster that a scheduled command needs.
//
// An interface, and the only one in this package, because the thing worth
// proving about scheduled commands is what happens when the cluster refuses
// one — and that cannot be shown against a real cluster here.
type cronApplier interface {
	Apply(ctx context.Context, obj any) error
}

// scheduleFailure is one scheduled command the cluster would not take.
type scheduleFailure struct {
	// Job is the name the person gave it, not the Kubernetes object's.
	Job    string
	Reason string
}

// applyScheduledCommands renders and applies an app's scheduled commands.
//
// It returns the names it applied and the ones that were refused, rather than
// stopping at the first refusal. A person with three nightly jobs and one bad
// schedule should end up with two running jobs and one clear message, not with
// nothing.
func applyScheduledCommands(ctx context.Context, applier cronApplier, spec kube.AppSpec,
	appSlug string, jobs []store.AppJob,
) (map[string]bool, []scheduleFailure) {
	applied := map[string]bool{}
	var failures []scheduleFailure
	for _, job := range jobs {
		if !job.Enabled {
			continue
		}
		name := kube.CronJobName(appSlug, job.Name)

		// BuildCronJob canonicalises the schedule, so what the cluster is
		// given is the string this panel's parser agrees with, and the same
		// string the Advanced view shows. See cron.Canonical.
		object, err := kube.BuildCronJob(kube.RunSpec{
			App: spec, Name: name, Command: job.Command, Kind: kube.RunKindScheduled,
		}, job.Schedule)
		if err != nil {
			failures = append(failures, scheduleFailure{Job: job.Name, Reason: err.Error()})
			continue
		}
		if err := applier.Apply(ctx, object); err != nil {
			failures = append(failures, scheduleFailure{Job: job.Name, Reason: err.Error()})
			continue
		}
		applied[name] = true
	}
	return applied, failures
}

// applyScheduledJobs applies an app's scheduled commands and removes the ones
// it no longer has.
//
// They are applied with the rest of the app's objects, from the same image, so
// a nightly job always runs the version that is deployed rather than whatever
// it was when somebody wrote the schedule.
//
// A scheduled command the cluster refuses does not fail the deployment. It
// used to, and that was wrong twice over: the app's own objects are applied
// before this runs, so the failure arrived after the new version was already
// rolling out and the panel recorded as failed a deployment the cluster was
// busy completing — and one schedule the API server would not take blocked
// every future deploy of that app, for ever, with a Kubernetes message about a
// CronJob on a page about an app. The refusal is written into the deployment's
// log instead, where the person who caused it is looking.
func (d *Deployer) applyScheduledJobs(ctx context.Context, deploymentID string, spec kube.AppSpec, app store.App) error {
	jobs, err := d.db.ListAppJobs(ctx, app.ID)
	if err != nil {
		return err
	}

	wanted, failures := applyScheduledCommands(ctx, d.cluster.Client().Applier(), spec, app.Slug, jobs)
	for _, failure := range failures {
		d.appendLog(ctx, deploymentID,
			"The scheduled command \""+failure.Job+"\" could not be scheduled: "+failure.Reason)
		d.log.Warn("a scheduled command was refused by the cluster",
			"app", app.ID, "job", failure.Job, "error", failure.Reason)
	}

	// A schedule that was removed or switched off has to stop running, and
	// server-side apply removes fields rather than whole objects.
	existing, err := d.cluster.Client().Clientset().BatchV1().CronJobs(spec.Namespace).
		List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=" + spec.Name + ",app.kubernetes.io/component=run",
		})
	if err != nil {
		d.log.Warn("could not list scheduled commands", "app", app.ID, "error", err)
		return nil
	}
	for _, cron := range existing.Items {
		if wanted[cron.Name] {
			continue
		}
		if err := d.cluster.Client().Applier().Delete(ctx,
			"batch/v1", "CronJob", spec.Namespace, cron.Name); err != nil {
			d.log.Warn("could not remove a scheduled command",
				"app", app.ID, "job", cron.Name, "error", err)
		}
	}
	return nil
}
