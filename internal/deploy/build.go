package deploy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/builder"
	"skifity/internal/cluster"
	"skifity/internal/errdoc"
	"skifity/internal/gitsrc"
	"skifity/internal/kube"
	"skifity/internal/logging"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// build runs a build Job and streams its output, returning the image it pushed.
func (d *Deployer) build(ctx context.Context, deployment *store.Deployment, app store.App, env store.Environment) (string, error) {
	if d.cluster == nil {
		return "", errdoc.ClusterUnreachable(nil)
	}

	// Held for the whole build, including the push. The registry sweep takes
	// the same lock exclusively, so it never deletes a blob out from under a
	// layer that is still being written.
	defer d.cluster.BeginBuild()()

	// The registry and the builder are installed on first use, which is what
	// keeps a fresh install small.
	d.appendLog(ctx, deployment.ID, "Preparing the builder.")
	if err := d.cluster.EnsureComponent(ctx, "registry"); err != nil {
		return "", err
	}
	if err := d.cluster.EnsureComponent(ctx, "buildkit"); err != nil {
		return "", err
	}

	registry, insecure, registrySecret, err := d.registryAddress(ctx)
	if err != nil {
		return "", err
	}

	tag := deployment.CommitSHA
	if len(tag) > 12 {
		tag = tag[:12]
	}
	if tag == "" {
		// Without a commit the deployment number keeps tags unique and
		// meaningful in the registry.
		tag = fmt.Sprintf("d%d", deployment.Number)
	}
	image := builder.ImageName(registry, env.Namespace, app.Slug, tag)

	chosen, err := d.chooseBuilder(ctx, app)
	if err != nil {
		return "", err
	}
	buildArgs, err := d.buildTimeVariables(ctx, app)
	if err != nil {
		return "", err
	}

	spec := builder.JobSpec{
		Name: kube.ResourceName("build-"+app.Slug, shortID(deployment.ID)),
		// Builds run in their own namespace, away from the panel's master key
		// and database. See cluster.EnsureBuildNamespace.
		Namespace:      d.cluster.Client().BuildNamespace(),
		AppID:          app.ID,
		DeploymentID:   deployment.ID,
		RepoURL:        app.RepoURL,
		CommitSHA:      deployment.CommitSHA,
		Branch:         app.Branch,
		RootDir:        app.RootDir,
		Builder:        chosen,
		DockerfilePath: app.DockerfilePath,
		// Both were detected and then dropped on the floor: the app carried
		// them, the build never read them, and every static build copied the
		// whole repository into a web server instead of building it.
		StaticDir:        app.StaticDir,
		BuildCommand:     app.BuildCommand,
		Image:            image,
		RegistryInsecure: insecure,
		RegistrySecret:   registrySecret,
		BuildArgs:        buildArgs,
		BuildKitAddress:  d.buildKitAddress(),
	}
	if app.SourceType == "upload" {
		spec.SourceUpload = true
		spec.RepoURL = ""
		spec.Branch = ""
	} else if app.GitSourceID != "" {
		attached, err := d.attachCloneSecret(ctx, app, &spec)
		if err != nil {
			return "", err
		}
		if !attached {
			d.appendLog(ctx, deployment.ID,
				"This repository is not on the host the connected Git account is for, so the build runs without credentials.")
		}
	}

	job, err := builder.BuildJob(spec)
	if err != nil {
		return "", errdoc.BadRequest(err.Error())
	}

	// Remove any leftover Job with the same name, so a retry is not rejected
	// because a finished Job is still sitting there — and wait for it to go,
	// because an apply onto an object that is still being deleted is accepted
	// and then collected, which would leave this build waiting for a pod that
	// never arrives.
	if err := d.cluster.Client().Applier().DeleteAndWait(ctx, "batch/v1", "Job",
		spec.Namespace, spec.Name, time.Minute); err != nil {
		return "", fmt.Errorf("clear the previous build: %w", err)
	}

	d.appendLog(ctx, deployment.ID, fmt.Sprintf("Building %s with the %s builder.", app.Name, chosen))
	if err := d.cluster.Client().Applier().Apply(ctx, job); err != nil {
		return "", fmt.Errorf("start the build: %w", err)
	}

	if spec.SourceUpload {
		if err := d.deliverUpload(ctx, deployment, app, spec.Namespace, spec.Name); err != nil {
			// The build is waiting for code that is not coming. Stopping it
			// frees its slot now instead of after the wait runs out.
			if delErr := d.cluster.Client().Applier().Delete(ctx, "batch/v1", "Job", spec.Namespace, spec.Name); delErr != nil {
				d.log.Warn("could not stop a build that never got its code", "job", spec.Name, "error", delErr)
			}
			return "", err
		}
	}

	if err := d.streamBuild(ctx, deployment, spec.Namespace, spec.Name, chosen); err != nil {
		return "", err
	}
	return image, nil
}

// chooseBuilder resolves the app's builder setting into a concrete strategy.
func (d *Deployer) chooseBuilder(ctx context.Context, app store.App) (builder.Builder, error) {
	switch app.Builder {
	case "dockerfile":
		return builder.BuilderDockerfile, nil
	case "nixpacks":
		return builder.BuilderNixpacks, nil
	case "static":
		return builder.BuilderStatic, nil
	case "railpack":
		if app.DockerfilePath != "" {
			return builder.BuilderDockerfile, nil
		}
		return builder.BuilderRailpack, nil
	case "auto", "":
		// A Dockerfile is a decision the repository's author already made.
		if app.DockerfilePath != "" {
			return builder.BuilderDockerfile, nil
		}
		return d.defaultBuilder(ctx), nil
	default:
		return "", errdoc.BadRequest(fmt.Sprintf("%q is not a builder Skifity knows.", app.Builder))
	}
}

// defaultBuilder is the zero-config builder an operator chose for this panel.
//
// The setting has existed since there were settings — "Which builder to use
// when a repository has no Dockerfile" — and nothing read it, so an operator
// who had picked Nixpacks got Railpack on every app anyway and had to set it
// per app to make it stick.
func (d *Deployer) defaultBuilder(ctx context.Context) builder.Builder {
	choice, _, err := d.db.GetSetting(ctx, settings.KeyBuilderDefault)
	if err != nil {
		d.log.Warn("could not read the default builder setting", "error", err)
		return builder.BuilderRailpack
	}
	if strings.TrimSpace(choice) == "nixpacks" {
		return builder.BuilderNixpacks
	}
	return builder.BuilderRailpack
}

func (d *Deployer) buildKitAddress() string {
	return fmt.Sprintf("tcp://%s.%s.svc.cluster.local:%d",
		cluster.BuildKitService, d.cluster.Client().BuildNamespace(), cluster.BuildKitPort)
}

// streamBuild follows the build pod's logs and reports the outcome.
func (d *Deployer) streamBuild(ctx context.Context, deployment *store.Deployment, namespace, jobName string, chosen builder.Builder) error {
	pod, err := d.waitForBuildPod(ctx, namespace, jobName)
	if err != nil {
		return err
	}

	// Each container's logs are streamed in turn: init containers first, so the
	// clone and the plan appear before the build output.
	containers := []string{"clone"}
	if chosen == builder.BuilderRailpack {
		containers = append(containers, "prepare")
	}
	containers = append(containers, "build")

	var tail strings.Builder
	for _, container := range containers {
		if err := d.streamContainer(ctx, namespace, pod, container, deployment, &tail); err != nil {
			// A container that never started is covered by the Job status
			// check below, which produces a better message than this would.
			d.log.Debug("could not stream a build container", "container", container, "error", err)
		}
	}

	// The Job's own status is what decides success: a container can print
	// nothing and still fail.
	stage, exitCode, err := d.waitForJob(ctx, namespace, jobName)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return errdoc.BuildFailed(deployment.AppID, stage, tail.String()).
			With("exit_code", fmt.Sprint(exitCode)).
			With("builder", string(chosen))
	}
	return nil
}

// waitForBuildPod finds the pod a Job created and waits for it to start.
func (d *Deployer) waitForBuildPod(ctx context.Context, namespace, jobName string) (string, error) {
	clientset := d.cluster.Client().Clientset()
	deadline := time.Now().Add(5 * time.Minute)

	for {
		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "job-name=" + jobName,
		})
		if err != nil {
			return "", fmt.Errorf("find the build pod: %w", err)
		}
		for _, pod := range pods.Items {
			switch pod.Status.Phase {
			case corev1.PodRunning, corev1.PodSucceeded, corev1.PodFailed:
				return pod.Name, nil
			case corev1.PodPending:
				// A pod that cannot be scheduled will never start; saying so
				// beats waiting five minutes for a timeout.
				for _, cond := range pod.Status.Conditions {
					if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse &&
						cond.Reason == "Unschedulable" {
						return "", errdoc.InsufficientCapacity("The build", cond.Message)
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return "", buildNotStarted()
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// buildNotStarted is a build pod that never got going.
func buildNotStarted() *errdoc.Problem {
	return errdoc.New("build.pod_not_started", "The build did not start").
		WithCause("No build pod became ready within five minutes.").
		WithImpact("Nothing was built or deployed.").
		WithFix("Check that the cluster has free CPU and memory, and that the builder component is running.").
		Retry()
}

// streamContainer follows one container's logs into the deployment's log.
func (d *Deployer) streamContainer(ctx context.Context, namespace, pod, container string, deployment *store.Deployment, tail *strings.Builder) error {
	stream, err := d.cluster.Client().StreamClientset().CoreV1().Pods(namespace).
		GetLogs(pod, &corev1.PodLogOptions{Container: container, Follow: true}).Stream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Lines are batched: a build emits thousands, and one insert per line would
	// dominate the build's own cost.
	batch := make([]store.LogLine, 0, 64)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := d.db.AppendBuildLogs(ctx, deployment.ID, batch); err != nil {
			d.log.Warn("could not store build logs", "deployment", deployment.ID, "error", err)
		}
		batch = batch[:0]
	}
	defer flush()

	for scanner.Scan() {
		// A build prints whatever the application's own tooling prints, which
		// sometimes includes a token from an environment variable.
		line := logging.Scrub(scanner.Text())
		batch = append(batch, store.LogLine{Line: line, At: time.Now()})
		d.hub.Publish(buildTopic(deployment.ID), "log",
			map[string]any{"stream": container, "line": line})

		// Keep the end of the output for the failure message.
		tail.WriteString(line)
		tail.WriteByte('\n')
		if tail.Len() > 16*1024 {
			trimmed := tail.String()
			tail.Reset()
			tail.WriteString(trimmed[len(trimmed)-8*1024:])
		}

		if len(batch) == cap(batch) {
			flush()
		}
	}
	return scanner.Err()
}

// waitForJob waits for a build Job to finish and reports which stage failed.
func (d *Deployer) waitForJob(ctx context.Context, namespace, name string) (stage string, exitCode int, err error) {
	clientset := d.cluster.Client().Clientset()
	deadline := time.Now().Add(40 * time.Minute)

	for {
		job, err := clientset.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", 0, fmt.Errorf("read the build status: %w", err)
		}
		if job.Status.Succeeded > 0 {
			return "", 0, nil
		}
		if job.Status.Failed > 0 {
			stage, code := d.failedStage(ctx, namespace, name)
			return stage, code, nil
		}
		if time.Now().After(deadline) {
			return "", 0, errdoc.New("build.timeout", "The build took too long and was stopped").
				WithCause("The build did not finish within 40 minutes.").
				WithImpact("Nothing was deployed. The previous version is still running.").
				WithFix("Builds this long usually mean a dependency is being compiled from source. Add a Dockerfile with a cached dependency layer, or raise the build timeout.").
				Retry()
		}
		select {
		case <-ctx.Done():
			return "", 0, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// failedStage works out which container failed, so the message can name it.
func (d *Deployer) failedStage(ctx context.Context, namespace, jobName string) (string, int) {
	pods, err := d.cluster.Client().Clientset().CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	if err != nil || len(pods.Items) == 0 {
		return "build", 1
	}
	pod := pods.Items[0]
	for _, status := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
		if status.State.Terminated != nil && status.State.Terminated.ExitCode != 0 {
			return stageName(status.Name), int(status.State.Terminated.ExitCode)
		}
	}
	return "build", 1
}

// stageName turns a container name into something a user recognises.
func stageName(container string) string {
	switch container {
	case "clone":
		return "fetching the repository"
	case "prepare":
		return "working out how to build it"
	default:
		return "building the image"
	}
}

// attachCloneSecret gives the build the team's Git token, but only when the
// repository is on the host that token is for.
//
// Without the host check, an app pointed at a repository of somebody's
// choosing, with the team's GitHub connection selected, sends that token
// straight to them: the clone puts it in the URL, and the remote receives it.
// Any member who can create an app could do it.
func (d *Deployer) attachCloneSecret(ctx context.Context, app store.App, spec *builder.JobSpec) (bool, error) {
	source, err := d.db.GetGitSource(ctx, app.GitSourceID)
	if err != nil {
		return false, err
	}
	if !gitsrc.SameHost(app.RepoURL, source.BaseURL) {
		d.log.Warn("not sending a Git token to a host the connection is not for",
			"app", app.ID, "git_source", source.ID)
		return false, nil
	}
	if err := d.ensureCloneSecret(ctx, app.GitSourceID, spec.Namespace); err != nil {
		return false, err
	}
	spec.CloneSecret = cloneSecretName(app.GitSourceID)
	return true, nil
}

// ensureCloneSecret copies a Git connection's token into a Secret the build can
// read, in the system namespace where builds run.
func (d *Deployer) ensureCloneSecret(ctx context.Context, gitSourceID, namespace string) error {
	source, err := d.db.GetGitSource(ctx, gitSourceID)
	if err != nil {
		return err
	}
	if source.ConfigEnc == "" {
		return nil
	}
	raw, err := d.keyring.Open(source.ConfigEnc, "git_source:"+source.TeamID+":"+source.Name)
	if err != nil {
		return fmt.Errorf("read the Git credentials: %w", err)
	}
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("read the Git credentials: %w", err)
	}
	token := config["token"]
	if token == "" {
		return nil
	}

	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cloneSecretName(gitSourceID),
			Namespace: namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "skifity"},
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"token": token},
	}
	return d.cluster.Client().Applier().Apply(ctx, secret)
}

func cloneSecretName(gitSourceID string) string {
	return kube.ResourceName("git", strings.ReplaceAll(gitSourceID, "_", "-"))
}

func shortID(id string) string {
	if idx := strings.IndexByte(id, '_'); idx >= 0 {
		id = id[idx+1:]
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func buildTopic(deploymentID string) string { return "deployment:" + deploymentID }
