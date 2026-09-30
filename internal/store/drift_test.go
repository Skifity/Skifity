package store

import (
	"testing"
	"time"
)

func TestAnAppsDriftKeepsWhenItStartedAndWhoWasTold(t *testing.T) {
	db, err := OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(t.Context(), &team); err != nil {
		t.Fatal(err)
	}
	project := Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(t.Context(), &project); err != nil {
		t.Fatal(err)
	}
	env := Environment{ProjectID: project.ID, Name: "Production", Slug: "production", Namespace: "acme-shop-production"}
	if err := db.CreateEnvironment(t.Context(), &env); err != nil {
		t.Fatal(err)
	}
	app := App{EnvironmentID: env.ID, Name: "Web", Slug: "web", SourceType: "image", Image: "nginx:1.27"}
	if err := db.CreateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}

	never, err := db.GetAppDrift(t.Context(), app.ID)
	if err != nil || never.Status != DriftInSync || never.AutoRepair || never.Items != "[]" {
		t.Fatalf("an app never checked is %+v, %v", never, err)
	}

	first := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	if err := db.RecordAppDrift(t.Context(), app.ID, DriftDrifted, `[{"kind":"Deployment"}]`, "fp1", first); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDriftNotified(t.Context(), app.ID, "fp1"); err != nil {
		t.Fatal(err)
	}
	// Found again five minutes later: still drifted since the first time.
	if err := db.RecordAppDrift(t.Context(), app.ID, DriftMissing, `[]`, "fp2", first.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAppDrift(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DriftMissing || got.Fingerprint != "fp2" || got.Notified != "fp1" ||
		!got.Since.Equal(first) || !got.CheckedAt.Equal(first.Add(5*time.Minute)) {
		t.Fatalf("got %+v", got)
	}

	if err := db.SetDriftAutoRepair(t.Context(), app.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordAppDrift(t.Context(), app.ID, DriftInSync, `[]`, "", first.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetAppDrift(t.Context(), app.ID)
	if got.Status != DriftInSync || !got.Since.IsZero() || !got.AutoRepair {
		t.Fatalf("back in sync: %+v", got)
	}

	// What an apply wrote: a whole apply replaces the list, a later one of
	// some objects adds to it.
	if got.AppliedKnown {
		t.Fatal("nothing was applied, and the list says it knows")
	}
	if err := db.RecordApplied(t.Context(), app.ID, map[string]string{"Deployment/web": "h1", "Ingress/web": "h2"}, true); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordApplied(t.Context(), app.ID, map[string]string{"Deployment/web--worker": "h3"}, false); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetAppDrift(t.Context(), app.ID)
	if !got.AppliedKnown || len(got.Applied) != 3 || got.Applied["Ingress/web"] != "h2" || !got.AutoRepair {
		t.Fatalf("after two applies: %+v", got)
	}
	if err := db.RecordApplied(t.Context(), app.ID, map[string]string{"Deployment/web": "h4"}, true); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetAppDrift(t.Context(), app.ID)
	if len(got.Applied) != 1 || got.Applied["Deployment/web"] != "h4" {
		t.Fatalf("a whole apply did not replace the list: %+v", got.Applied)
	}

	// The row goes with the app.
	if err := db.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM app_drift`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("%d rows left, %v", rows, err)
	}
}
