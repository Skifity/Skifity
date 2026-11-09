package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/logging"
	"skifity/internal/store"
)

// crashTailLines is how much of a crashed container's log goes into the
// deployment's: enough for a stack trace's top and the line before it.
const crashTailLines = 40

// crashed says in the deployment's log how the new version stopped and what
// it printed before it did, and answers with the failure that says what
// fixes it.
func (d *Deployer) crashed(ctx context.Context, deployment store.Deployment, app store.App, env store.Environment,
	variables resolved, crash *kube.CrashError) *errdoc.Problem {
	read, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tail, err := d.cluster.Client().PreviousLogTail(read, env.Namespace, crash.Pod, crash.Container, crashTailLines)
	if err != nil {
		d.log.Warn("could not read the log of an instance that stopped", "app", app.ID, "instance", crash.Pod, "error", err)
	}
	// appendLog stores what it is given, and an app's own output is where its
	// secrets turn up: a connection string in a stack trace, a configuration
	// printed at start. The deployment log is read by everybody on the team,
	// including the ones a secret variable is hidden from.
	secrets := d.appSecrets(ctx, app, env, variables)
	tail = logging.RedactValues(tail, secrets)

	d.appendLog(ctx, deployment.ID, fmt.Sprintf("The new version stopped %s: %s.",
		kube.Times(crash.Stops), kube.DescribeStop(crash.Reason, crash.ExitCode)))
	if strings.TrimSpace(tail) == "" {
		d.appendLog(ctx, deployment.ID, "It printed nothing before it stopped.")
	} else {
		d.appendLog(ctx, deployment.ID, "Its last lines:")
		for _, line := range strings.Split(tail, "\n") {
			d.appendLog(ctx, deployment.ID, "  "+line)
		}
	}
	// What the container wrote to its termination message, when that is not
	// one of the lines already shown: a JVM's out-of-memory report, a Go
	// program's panic when the log was cut short.
	if message := logging.RedactValues(crash.Message, secrets); message != "" && !strings.Contains(tail, message) {
		d.appendLog(ctx, deployment.ID, "Its termination message: "+message)
	}
	return errdoc.DeployCrashed(app.Name, crash.Pod, crash.Stops, int(crash.ExitCode), orKubelet(crash.Reason), crash.Kind(), tail)
}

// orKubelet is the kubelet's word for a termination, which it always gives;
// "Error" where a fake or an odd runtime gave none.
func orKubelet(reason string) string {
	if reason == "" {
		return "Error"
	}
	return reason
}

// appSecrets are the values an app's output must not show: its secret
// variables, the project's shared ones, and every value read from a secret
// manager, which is a secret whatever it is marked.
func (d *Deployer) appSecrets(ctx context.Context, app store.App, env store.Environment, variables resolved) []string {
	secrets := d.cluster.SecretValues(ctx, app, env)
	for key := range variables.references {
		secrets = append(secrets, variables.values[key])
	}
	return secrets
}

// putBackServing applies the version that is serving again, after a version
// that did not start, for an app that updates by rolling.
//
// Kubernetes kept the old instances serving, and the new revision's stayed
// with them: an instance that kept restarting beside the live version, using
// its memory and filling its log, until somebody deployed again. Applying the
// live version's configuration makes it the Deployment's template once more,
// so the controller removes the instance that would never start.
//
// Not while another deployment is rolling out — that one is the version to
// apply now — and nothing when there is no version that worked to go back to.
func (d *Deployer) putBackServing(ctx context.Context, app store.App, failed store.Deployment) {
	if d.cluster == nil {
		return
	}
	// Its own time: the failure's is a minute for everything it reports, and
	// applying an app waits for the rollout back, which is quick only because
	// the old instances never stopped.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
	defer cancel()
	live, err := d.db.LatestSuccessfulDeployment(ctx, app.ID)
	if err != nil || live.Number >= failed.Number {
		return
	}
	if _, err := d.db.DeploymentRollingOut(ctx, app.ID); !errors.Is(err, store.ErrNotFound) {
		return
	}
	d.appendLog(ctx, failed.ID, "")
	// Not "still serving": an app with a GPU stops its old instance first,
	// and this is what starts it again.
	d.appendLog(ctx, failed.ID, fmt.Sprintf(
		"Applying version %d again, so it is what runs and the instances of this version that did not start are removed.",
		live.Number))
	if err := d.Sync(ctx, app.ID); err != nil {
		d.appendLog(ctx, failed.ID, "It could not be applied again: "+errdoc.From(err).Error())
		d.log.Warn("could not apply the serving version again after a failed deploy",
			"app", app.ID, "failed", failed.ID, "live", live.ID, "error", err)
	}
}
