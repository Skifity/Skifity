package kube

import "testing"

// Every rendering of a scheduled command has to agree.
//
// Kubernetes parses the schedule string with its own implementation, and cron
// has two spellings for Sunday. Canonicalising in the caller that applies to
// the cluster, and not in the one that renders the Advanced view, meant the
// panel sent one string and showed another.
func TestACronJobAlwaysCarriesTheScheduleThePanelAgreesWith(t *testing.T) {
	spec := RunSpec{
		App:     AppSpec{Name: "web", Namespace: "team-production", Image: "registry.test/web:abc"},
		Name:    "web-job-weekly",
		Command: "bin/weekly",
		Kind:    RunKindScheduled,
	}
	job, err := BuildCronJob(spec, "0 3 * * 7")
	if err != nil {
		t.Fatalf("render a weekly command: %v", err)
	}
	if got := job.Spec.Schedule; got != "0 3 * * 0" {
		t.Errorf("the object carries %q; the panel validated the form ending in 0", got)
	}
}

// A schedule this panel cannot read is refused here, rather than becoming an
// object the API server rejects later.
func TestACronJobRefusesAScheduleThePanelCannotRead(t *testing.T) {
	spec := RunSpec{
		App:  AppSpec{Name: "web", Namespace: "team-production", Image: "registry.test/web:abc"},
		Name: "web-job-nonsense", Command: "bin/thing", Kind: RunKindScheduled,
	}
	if _, err := BuildCronJob(spec, "every tuesday"); err == nil {
		t.Error("a schedule that is not one was rendered into an object")
	}
}
