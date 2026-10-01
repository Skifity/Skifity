package store

import (
	"slices"
	"testing"
)

func TestAnAppsGPUsAreKeptAndRemoved(t *testing.T) {
	db, err := OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := t.Context()
	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	project := Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	env := Environment{ProjectID: project.ID, Name: "production", Slug: "production", Namespace: "shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatal(err)
	}
	app := App{EnvironmentID: env.ID, Name: "ollama", Slug: "ollama", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatal(err)
	}

	// No row is no GPU, and an empty list rather than null.
	none, err := db.GetAppGPU(ctx, app.ID)
	if err != nil || none.Count != 0 || none.Workloads == nil {
		t.Fatalf("an app with no GPU reads %+v, %v", none, err)
	}

	want := AppGPU{AppID: app.ID, Count: 2, Vendor: "nvidia", Product: "NVIDIA-A10", Workloads: []string{"web", "worker"}}
	if err := db.SetAppGPU(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAppGPU(ctx, app.ID)
	if err != nil || got.Count != 2 || got.Vendor != "nvidia" || got.Product != "NVIDIA-A10" || !slices.Equal(got.Workloads, want.Workloads) {
		t.Fatalf("stored %+v, read %+v (%v)", want, got, err)
	}

	if err := db.SetAppGPU(ctx, AppGPU{AppID: app.ID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetAppGPU(ctx, app.ID); got.Count != 0 || got.Vendor != "" {
		t.Errorf("count 0 left %+v", got)
	}

	// The app goes, and its GPUs with it.
	if err := db.SetAppGPU(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_gpus`).Scan(&left); err != nil || left != 0 {
		t.Errorf("%d rows outlived their app (%v)", left, err)
	}
}
