package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"skifity/internal/builder"
	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/upload"
)

func problemCode(err error) string {
	var p *errdoc.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return ""
}

func TestWhichUploadADeployBuilds(t *testing.T) {
	uploads := &upload.Store{Dir: t.TempDir()}
	d := &Deployer{Uploads: uploads}
	app := store.App{ID: "app_1", Name: "web", SourceType: "upload"}

	if _, err := d.uploadToDeploy(app, ""); problemCode(err) != "upload.none" {
		t.Fatalf("a deploy before any code was sent gave %v", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("hi"))
	tw.Close()
	gz.Close()
	sent, err := uploads.Save(app.ID, &buf)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := d.uploadToDeploy(app, ""); err != nil || got != sent.SHA256 {
		t.Fatalf("a deploy naming nothing built %q (%v), not the upload just sent", got, err)
	}
	if got, err := d.uploadToDeploy(app, sent.SHA256); err != nil || got != sent.SHA256 {
		t.Fatalf("a deploy naming the upload built %q (%v)", got, err)
	}
	for _, bad := range []string{"deadbeef", "../../etc/passwd", sent.SHA256[:40]} {
		if _, err := d.uploadToDeploy(app, bad); problemCode(err) != "upload.not_found" {
			t.Errorf("a deploy naming %q gave %v", bad, err)
		}
	}
	other := store.App{ID: "app_2", Name: "other", SourceType: "upload"}
	if _, err := d.uploadToDeploy(other, sent.SHA256); problemCode(err) != "upload.not_found" {
		t.Fatalf("one app deployed another app's upload: %v", err)
	}

	if _, err := (&Deployer{}).uploadToDeploy(app, ""); problemCode(err) != "config.missing" {
		t.Fatalf("a panel with no upload store gave %v", err)
	}
}

func TestTheCodeIsHandedOverOnlyWhileTheFirstStepRuns(t *testing.T) {
	pod := func(state corev1.ContainerState, phase corev1.PodPhase) corev1.Pod {
		return corev1.Pod{Status: corev1.PodStatus{
			Phase: phase,
			InitContainerStatuses: []corev1.ContainerStatus{
				{Name: builder.SourceContainer, State: state, Image: "alpine/git:latest"},
			},
		}}
	}

	// Pending is the phase the pod has while its first step runs, and the
	// moment to deliver: waiting for Running would wait forever.
	if ready, err := sourceContainerReady(pod(corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, corev1.PodPending)); !ready || err != nil {
		t.Fatalf("a running first step was not ready: %v", err)
	}
	if ready, err := sourceContainerReady(pod(corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"}}, corev1.PodPending)); ready || err != nil {
		t.Fatalf("a step still starting was ready (%v) or failed (%v)", ready, err)
	}
	if _, err := sourceContainerReady(pod(corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "not found"}}, corev1.PodPending)); problemCode(err) != "deploy.image_pull_failed" {
		t.Fatalf("an image that cannot be pulled gave %v", err)
	}
	if _, err := sourceContainerReady(pod(corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"}}, corev1.PodPending)); problemCode(err) != "upload.delivery_failed" {
		t.Fatalf("a step that already ended gave %v", err)
	}

	unschedulable := corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{
		{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "0/1 nodes"},
	}}}
	if _, err := sourceContainerReady(unschedulable); problemCode(err) != "cluster.insufficient_capacity" {
		t.Fatalf("a pod that cannot be placed gave %v", err)
	}
}
