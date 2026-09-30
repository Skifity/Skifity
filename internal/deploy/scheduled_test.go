package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"

	"skifity/internal/kube"
	"skifity/internal/store"
)

// refusingApplier takes everything except the objects whose name contains a
// marker, which stands in for the API server refusing one object.
type refusingApplier struct {
	refuse  string
	applied []string
	// schedules records what the cluster was actually asked to run, which is
	// the thing the panel's validation is supposed to agree with.
	schedules map[string]string
}

func (a *refusingApplier) Apply(_ context.Context, obj any) error {
	cronJob, ok := obj.(*batchv1.CronJob)
	if !ok {
		return errors.New("that is not a CronJob")
	}
	if a.refuse != "" && strings.Contains(cronJob.Name, a.refuse) {
		return errors.New(`spec.schedule: Invalid value: "` + cronJob.Spec.Schedule + `"`)
	}
	a.applied = append(a.applied, cronJob.Name)
	if a.schedules == nil {
		a.schedules = map[string]string{}
	}
	a.schedules[cronJob.Name] = cronJob.Spec.Schedule
	return nil
}

func appSpec() kube.AppSpec {
	return kube.AppSpec{Name: "web", Namespace: "team-production", Image: "registry.test/web@sha256:" +
		"0000000000000000000000000000000000000000000000000000000000000000"}
}

// One scheduled command the cluster will not take must not take the other two
// down with it, and must not fail the deployment: the app's own objects are
// applied before this runs, so a failure here marks as failed a deployment the
// cluster is busy completing.
func TestARefusedScheduleDoesNotStopTheOthers(t *testing.T) {
	applier := &refusingApplier{refuse: "broken"}
	jobs := []store.AppJob{
		{Name: "nightly report", Schedule: "0 3 * * *", Command: "bin/report", Enabled: true},
		{Name: "broken", Schedule: "0 4 * * *", Command: "bin/thing", Enabled: true},
		{Name: "hourly sweep", Schedule: "0 * * * *", Command: "bin/sweep", Enabled: true},
	}

	applied, failures := applyScheduledCommands(context.Background(), applier, appSpec(), "web", jobs)

	if len(applied) != 2 {
		t.Errorf("%d scheduled commands applied, expected the two the cluster accepted: %v", len(applied), applied)
	}
	if len(failures) != 1 {
		t.Fatalf("%d failures reported, expected one: %+v", len(failures), failures)
	}
	if failures[0].Job != "broken" {
		t.Errorf("the failure names %q rather than the job the person wrote", failures[0].Job)
	}
	if failures[0].Reason == "" {
		t.Error("the failure has no reason, so the deployment log would say nothing useful")
	}
}

// The reason this whole path was wrong: the panel validated one dialect and
// handed the cluster another. Sunday is spelled two ways and only one of them
// is safe to send.
func TestTheClusterIsGivenTheScheduleThePanelValidated(t *testing.T) {
	applier := &refusingApplier{}
	jobs := []store.AppJob{
		{Name: "sunday", Schedule: "0 3 * * 7", Command: "bin/weekly", Enabled: true},
	}

	_, failures := applyScheduledCommands(context.Background(), applier, appSpec(), "web", jobs)
	if len(failures) != 0 {
		t.Fatalf("a valid weekly schedule was refused: %+v", failures)
	}

	name := kube.CronJobName("web", "sunday")
	if got := applier.schedules[name]; got != "0 3 * * 0" {
		t.Errorf("the cluster was asked to run %q; the panel validated the form ending in 0", got)
	}
}

// A schedule that cannot be parsed at all is reported against its job rather
// than reaching the cluster as a rejected object.
func TestAnUnparseableScheduleIsCaughtBeforeTheCluster(t *testing.T) {
	applier := &refusingApplier{}
	jobs := []store.AppJob{
		{Name: "nonsense", Schedule: "every tuesday", Command: "bin/thing", Enabled: true},
	}

	applied, failures := applyScheduledCommands(context.Background(), applier, appSpec(), "web", jobs)
	if len(applied) != 0 {
		t.Errorf("an unparseable schedule was sent to the cluster: %v", applied)
	}
	if len(failures) != 1 || failures[0].Job != "nonsense" {
		t.Fatalf("expected one failure named after the job, got %+v", failures)
	}
}

// A switched-off command is not applied, and is not a failure either.
func TestASwitchedOffCommandIsNeitherAppliedNorReported(t *testing.T) {
	applier := &refusingApplier{}
	jobs := []store.AppJob{
		{Name: "paused", Schedule: "0 3 * * *", Command: "bin/thing", Enabled: false},
	}

	applied, failures := applyScheduledCommands(context.Background(), applier, appSpec(), "web", jobs)
	if len(applied) != 0 || len(failures) != 0 {
		t.Errorf("a switched-off command produced %v and %+v", applied, failures)
	}
}
