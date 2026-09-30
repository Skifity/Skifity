package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/builder"
	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// deliverUpload hands an uploaded folder to a build that is waiting for it.
//
// The build cannot come and get it — the build namespace may not reach the
// panel, on purpose — so the panel waits for the build's first container to be
// running and streams the archive into it. See builder.ReceiveCommand, and
// ADR-0022 for why it is this way round.
func (d *Deployer) deliverUpload(ctx context.Context, deployment *store.Deployment, app store.App, namespace, jobName string) error {
	pod, err := d.waitForSourceContainer(ctx, namespace, jobName)
	if err != nil {
		return err
	}
	file, err := d.Uploads.Open(app.ID, deployment.CommitSHA)
	if err != nil {
		// Checked when the deploy was queued; gone since only if it was pruned
		// by ten newer uploads in the meantime, which the newest deploy wins.
		return errdoc.UploadNotFound(deployment.CommitSHA)
	}
	defer file.Close()

	d.appendLog(ctx, deployment.ID, "Sending the uploaded code to the build.")
	output, err := d.cluster.Client().ExecWithInput(ctx, namespace, pod,
		builder.SourceContainer, builder.ReceiveCommand(), file)
	if err != nil {
		detail := err.Error()
		if output != "" {
			detail += ": " + output
		}
		return errdoc.UploadDeliveryFailed(detail)
	}
	return nil
}

// waitForSourceContainer waits until the build's first container is running,
// which is the only moment code can be handed to it.
//
// waitForBuildPod cannot be used for this. It waits for the pod to be Running,
// and a pod stays Pending for as long as an init container runs — so it would
// wait for a container that is itself waiting for this, until both time out.
func (d *Deployer) waitForSourceContainer(ctx context.Context, namespace, jobName string) (string, error) {
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
			ready, err := sourceContainerReady(pod)
			if err != nil {
				return "", err
			}
			if ready {
				return pod.Name, nil
			}
		}
		if time.Now().After(deadline) {
			return "", buildNotStarted()
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// sourceContainerReady reads a build pod: true when its first container is
// running and can be given the code, an error when it never will be.
func sourceContainerReady(pod corev1.Pod) (bool, error) {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse &&
			cond.Reason == "Unschedulable" {
			return false, errdoc.InsufficientCapacity("The build", cond.Message)
		}
	}
	for _, status := range pod.Status.InitContainerStatuses {
		if status.Name != builder.SourceContainer {
			continue
		}
		switch {
		case status.State.Running != nil:
			return true, nil
		case status.State.Terminated != nil:
			// It ended before anything was sent, so it will never take it.
			return false, errdoc.UploadDeliveryFailed(fmt.Sprintf(
				"the build's first step stopped before the code arrived (%s, exit code %d)",
				orReason(status.State.Terminated.Reason), status.State.Terminated.ExitCode))
		case status.State.Waiting != nil && pullFailure(status.State.Waiting.Reason):
			return false, errdoc.ImagePullFailed(status.Image, status.State.Waiting.Message)
		}
	}
	if pod.Status.Phase == corev1.PodFailed {
		return false, errdoc.UploadDeliveryFailed("the build pod failed before the code could be sent: " +
			orReason(pod.Status.Reason))
	}
	return false, nil
}

func pullFailure(reason string) bool {
	switch reason {
	case "ErrImagePull", "ImagePullBackOff", "InvalidImageName":
		return true
	}
	return false
}

func orReason(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "no reason given"
	}
	return reason
}
