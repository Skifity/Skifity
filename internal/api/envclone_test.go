package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"skifity/internal/store"
)

// Cloning an environment: its apps and everything that is theirs, resealed
// under the copies, and a new empty database of each kind, linked the same
// way — never the original's data, domains or ports.
func TestAnEnvironmentIsClonedWithoutItsDataDomainsOrPorts(t *testing.T) {
	h, log := withDatabases(t, "")
	acme := h.newTenant("acme")
	web := h.app(acme, "web")
	ctx := context.Background()

	web.SourceType, web.RepoURL, web.AutoDeploy = "git", "https://git.example.test/acme/web.git", true
	if err := h.db.UpdateApp(ctx, &web); err != nil {
		t.Fatal(err)
	}
	seal := func(value, context string) string {
		sealed, err := h.keyring.Seal([]byte(value), context)
		if err != nil {
			t.Fatal(err)
		}
		return sealed
	}
	if err := h.db.SetVariable(ctx, &store.Variable{AppID: web.ID, Key: "STRIPE_KEY", IsSecret: true},
		seal("sk_test_not_real", variableContext(web.ID, "STRIPE_KEY"))); err != nil {
		t.Fatal(err)
	}
	if err := h.db.SetFile(ctx, &store.AppFile{AppID: web.ID, Path: "/etc/app.conf", Size: 5},
		seal("a = 1", store.FileContext(web.ID, "/etc/app.conf"))); err != nil {
		t.Fatal(err)
	}
	if err := h.db.SetProcess(ctx, &store.AppProcess{AppID: web.ID, Name: "worker", Command: "bin/worker", Instances: 2}); err != nil {
		t.Fatal(err)
	}
	if err := h.db.CreateAppJob(ctx, &store.AppJob{AppID: web.ID, Name: "nightly", Schedule: "0 3 * * *", Command: "bin/report", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := h.db.CreateVolume(ctx, &store.Volume{AppID: web.ID, Name: "uploads", MountPath: "/uploads", SizeGB: 5}); err != nil {
		t.Fatal(err)
	}
	if err := h.db.CreateDomain(ctx, &store.Domain{AppID: web.ID, Hostname: "shop.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := h.db.AddPort(ctx, &store.AppPort{AppID: web.ID, Port: 25565, Protocol: "tcp", PublicPort: 25565}); err != nil {
		t.Fatal(err)
	}
	database := store.Database{EnvironmentID: acme.env.ID, Name: "shop-db", Slug: "shop-db", Engine: "postgres", EngineVersion: "17", Status: "running", StorageGB: 10, Instances: 1}
	if err := h.db.CreateDatabase(ctx, &database); err != nil {
		t.Fatal(err)
	}
	if err := h.db.LinkDatabase(ctx, database.ID, web.ID, "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	if err := h.db.SetVariable(ctx, &store.Variable{AppID: web.ID, Key: "DATABASE_URL", IsSecret: true},
		seal("postgres://production", variableContext(web.ID, "DATABASE_URL"))); err != nil {
		t.Fatal(err)
	}

	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/clone", map[string]any{"name": "Staging"})
	if status != http.StatusCreated {
		t.Fatalf("cloning answered %d: %s", status, body)
	}
	var result clonedEnvironment
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	if result.Environment.Slug != "staging" || len(result.Apps) != 1 {
		t.Fatalf("the clone is %+v", result)
	}
	copy := result.Apps[0]
	if copy.AutoDeploy {
		t.Error("the copy deploys every push to production's branch")
	}

	vars, _ := h.db.ListVariables(ctx, copy.ID)
	got := map[string]string{}
	for _, row := range vars {
		// The link's variable is the new database's, written by linking it:
		// the fake writes a placeholder where production's address was.
		if row.Key == "DATABASE_URL" {
			if row.Sealed != "sealed-in-test" {
				t.Error("the copy points at the original's database")
			}
			continue
		}
		plaintext, err := h.keyring.Open(row.Sealed, variableContext(copy.ID, row.Key))
		if err != nil {
			t.Fatalf("%s was not resealed for the copy: %v", row.Key, err)
		}
		got[row.Key] = string(plaintext)
	}
	if got["STRIPE_KEY"] != "sk_test_not_real" {
		t.Errorf("the copy's variables are %v", got)
	}
	if len(result.Databases) != 1 || result.Databases[0].EnvironmentID != result.Environment.ID {
		t.Errorf("the databases made are %+v", result.Databases)
	}
	if want := "link db_postgres as DATABASE_URL"; !contains(log.all(), want) {
		t.Errorf("the new database was not linked: %v", log.all())
	}

	if files, _ := h.db.ListFiles(ctx, copy.ID); len(files) != 1 {
		t.Errorf("the copy has files %+v", files)
	} else if _, err := h.keyring.Open(files[0].Sealed, store.FileContext(copy.ID, "/etc/app.conf")); err != nil {
		t.Errorf("the file was not resealed for the copy: %v", err)
	}
	if processes, _ := h.db.ListProcesses(ctx, copy.ID); len(processes) != 1 || processes[0].Instances != 2 {
		t.Errorf("the copy's processes are %+v", processes)
	}
	if jobs, _ := h.db.ListAppJobs(ctx, copy.ID); len(jobs) != 1 {
		t.Errorf("the copy's schedules are %+v", jobs)
	}
	if volumes, _ := h.db.ListVolumes(ctx, copy.ID); len(volumes) != 1 {
		t.Errorf("the copy's volumes are %+v", volumes)
	}
	if domains, _ := h.db.ListDomains(ctx, copy.ID); len(domains) != 0 {
		t.Errorf("the copy took the original's domains: %+v", domains)
	}
	if ports, _ := h.db.ListPorts(ctx, copy.ID); len(ports) != 0 {
		t.Errorf("the copy took the original's ports: %+v", ports)
	}
	if len(result.Notes) < 3 {
		t.Errorf("the notes do not say what was left behind: %v", result.Notes)
	}
}

func TestOnlyTheTeamClonesItsEnvironments(t *testing.T) {
	h, _ := withDatabases(t, "")
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	if status, _ := h.do(globex, http.MethodPost, "/api/environments/"+acme.env.ID+"/clone", map[string]any{"name": "x"}); status != http.StatusNotFound {
		t.Errorf("another team cloned it: %d", status)
	}
	viewer := h.newMember(acme, "auditor", store.RoleViewer)
	if status, _ := h.do(viewer, http.MethodPost, "/api/environments/"+acme.env.ID+"/clone", map[string]any{"name": "x"}); status != http.StatusForbidden {
		t.Errorf("a viewer cloned it: %d", status)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
