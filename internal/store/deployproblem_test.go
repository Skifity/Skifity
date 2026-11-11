package store

import (
	"testing"

	"skifity/internal/errdoc"
)

// A failed deployment keeps its whole problem, values and context, and only
// the failure it was failed with: one cancelled in between is left alone.
func TestAFailedDeploymentKeepsItsProblem(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, _, _, env := seedTeam(t, db)
	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web"}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatal(err)
	}
	failed := Deployment{AppID: app.ID}
	if err := db.CreateDeployment(ctx, &failed); err != nil {
		t.Fatal(err)
	}
	problem := errdoc.DeployCrashed("web", "web-2-a", 2, 137, "OOMKilled", "out_of_memory", "FATAL: out of memory")
	if err := db.UpdateDeploymentStatus(ctx, failed.ID, DeployFailed, problem.Code, problem.Error(), problem.Fix); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDeploymentProblem(ctx, failed.ID, problem); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetDeployment(ctx, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Problem == nil || got.Problem.Code != "deploy.crashed" ||
		got.Problem.Context["kind"] != "out_of_memory" || got.Problem.Context["log_tail"] != "FATAL: out of memory" ||
		len(got.Problem.Args.Cause) != 4 {
		t.Fatalf("the deployment kept %+v", got.Problem)
	}

	cancelled := Deployment{AppID: app.ID}
	if err := db.CreateDeployment(ctx, &cancelled); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateDeploymentStatus(ctx, cancelled.ID, DeployCancelled, "cancelled", "Cancelled.", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDeploymentProblem(ctx, cancelled.ID, problem); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetDeployment(ctx, cancelled.ID); got.Problem != nil {
		t.Errorf("a cancelled deployment was given a crash it never had: %+v", got.Problem)
	}
}
