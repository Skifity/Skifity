package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"skifity/internal/store"
)

// An app's GPUs through the API: what is refused, what is only warned about,
// who may change them, and marking a server.

// gpuCluster answers Summary with servers that have cards, and records the
// labels it is asked to set.
type gpuClusterFake struct {
	Cluster
	summary ClusterSummary
	marked  map[string]bool
}

func (c *gpuClusterFake) Summary(context.Context) (ClusterSummary, error) { return c.summary, nil }

func (c *gpuClusterFake) SetNodeGPULabel(_ context.Context, node string, nvidia bool) error {
	if c.marked == nil {
		c.marked = map[string]bool{}
	}
	c.marked[node] = nvidia
	return nil
}

// twoGPUServers is a cluster with two NVIDIA A10s on one server and one T4 on
// another, one of the A10s taken, and a server with none.
func twoGPUServers() ClusterSummary {
	return ClusterSummary{Reachable: true, Nodes: []NodeInfo{
		{Name: "gpu-1", Ready: true, Schedulable: true, GPUs: []NodeGPU{{
			Vendor: "nvidia", Resource: "nvidia.com/gpu", Capacity: 2, Allocatable: 2, InUse: 1, Product: "NVIDIA-A10",
		}}},
		{Name: "gpu-2", Ready: true, Schedulable: true, GPUs: []NodeGPU{{
			Vendor: "nvidia", Resource: "nvidia.com/gpu", Capacity: 1, Allocatable: 1, Product: "Tesla-T4",
		}}},
		// Cordoned: its card places nothing.
		{Name: "gpu-3", Ready: true, Schedulable: false, GPUs: []NodeGPU{{
			Vendor: "nvidia", Resource: "nvidia.com/gpu", Capacity: 8, Allocatable: 8,
		}}},
		{Name: "cpu-1", Ready: true, Schedulable: true},
	}}
}

func setGPU(t *testing.T, h *harness, as tenant, app store.App, body map[string]any) (int, gpuSettings, string) {
	t.Helper()
	status, raw := h.do(as, http.MethodPut, "/api/apps/"+app.ID+"/gpu", body)
	var view gpuSettings
	if status == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &view); err != nil {
			t.Fatalf("decode: %v\n%s", err, raw)
		}
	}
	return status, view, raw
}

func TestAnAppIsGivenGPUsTheClusterHas(t *testing.T) {
	h := newHarness(t)
	h.withCluster(&gpuClusterFake{summary: twoGPUServers()})
	acme := h.newTenant("acme")
	app := h.app(acme, "ollama")

	status, view, body := setGPU(t, h, acme, app, map[string]any{"count": 1, "product": "NVIDIA-A10"})
	if status != http.StatusOK {
		t.Fatalf("one GPU answered %d: %s", status, body)
	}
	// The vendor defaults to NVIDIA's, and the app alone is written as nothing.
	if view.GPU.Count != 1 || view.GPU.Vendor != "nvidia" || view.GPU.Product != "NVIDIA-A10" || len(view.GPU.Workloads) != 0 {
		t.Errorf("stored as %+v", view.GPU)
	}
	stored, _ := h.db.GetAppGPU(t.Context(), app.ID)
	if stored.Count != 1 || stored.Vendor != "nvidia" {
		t.Errorf("the database has %+v", stored)
	}

	// What the cluster offers, added up and never naming a server.
	nvidia := view.Cluster.Vendors[0]
	if !view.Cluster.Known || nvidia.Vendor != "nvidia" || nvidia.Allocatable != 3 || nvidia.InUse != 1 ||
		nvidia.MostOnOneServer != 2 || nvidia.Servers != 2 || !slices.Equal(nvidia.Products, []string{"NVIDIA-A10", "Tesla-T4"}) {
		t.Errorf("the cluster offers %+v; a cordoned server's cards are not on offer", nvidia)
	}
	if strings.Contains(body, "gpu-1") || strings.Contains(body, "cpu-1") {
		t.Error("the answer names the cluster's servers")
	}

	// Taking them away removes the row, and with it the vendor chosen.
	if status, view, body := setGPU(t, h, acme, app, map[string]any{"count": 0}); status != http.StatusOK || view.GPU.Count != 0 || view.GPU.Vendor != "" {
		t.Fatalf("no GPU answered %d %+v: %s", status, view.GPU, body)
	}
	if stored, _ := h.db.GetAppGPU(t.Context(), app.ID); stored.Count != 0 {
		t.Errorf("the GPUs are still stored: %+v", stored)
	}
}

func TestAGPUNoServerCanGiveIsRefused(t *testing.T) {
	h := newHarness(t)
	h.withCluster(&gpuClusterFake{summary: twoGPUServers()})
	acme := h.newTenant("acme")
	app := h.app(acme, "comfy")

	cases := []struct {
		name string
		body map[string]any
		code string
	}{
		{"a vendor no server offers", map[string]any{"count": 1, "vendor": "amd"}, "app.gpu_unavailable"},
		{"more than one server has", map[string]any{"count": 3}, "app.gpu_too_many"},
		{"no such vendor", map[string]any{"count": 1, "vendor": "voodoo"}, "request.invalid"},
		{"too many to be a number of cards", map[string]any{"count": 40}, "request.invalid"},
		{"a model for AMD", map[string]any{"count": 1, "vendor": "amd", "product": "MI300X"}, "request.invalid"},
		{"a workload no process could be", map[string]any{"count": 1, "workloads": []string{"Web Server"}}, "request.invalid"},
	}
	for _, c := range cases {
		status, _, body := setGPU(t, h, acme, app, c.body)
		if status < 400 || !strings.Contains(body, `"`+c.code+`"`) {
			t.Errorf("%s answered %d, want %s: %s", c.name, status, c.code, body)
		}
	}
	if stored, _ := h.db.GetAppGPU(t.Context(), app.ID); stored.Count != 0 {
		t.Errorf("a refused setting was stored: %+v", stored)
	}
}

func TestAnAppWithAGPUAndScaleToZeroIsRefusedFromEitherSide(t *testing.T) {
	h := newHarness(t)
	h.withCluster(&gpuClusterFake{summary: twoGPUServers()})
	acme := h.newTenant("acme")
	app := h.app(acme, "whisper")

	// Asleep first, then a GPU.
	app.SourceType, app.ScaleToZero = "image", true
	if err := h.db.UpdateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	if status, _, body := setGPU(t, h, acme, app, map[string]any{"count": 1}); status != http.StatusConflict ||
		!strings.Contains(body, "app.gpu_scale_to_zero") {
		t.Errorf("a GPU on an app that scales to zero answered %d: %s", status, body)
	}
	// Its worker never sleeps, so it may have one.
	if status, _, body := setGPU(t, h, acme, app, map[string]any{"count": 1, "workloads": []string{"worker"}}); status != http.StatusOK {
		t.Errorf("a GPU for the worker of an app that scales to zero answered %d: %s", status, body)
	}

	// A GPU first, then asleep.
	other := h.app(acme, "vllm")
	if status, _, body := setGPU(t, h, acme, other, map[string]any{"count": 1}); status != http.StatusOK {
		t.Fatalf("a GPU answered %d: %s", status, body)
	}
	status, body := h.do(acme, http.MethodPut, "/api/apps/"+other.ID+"/scaling", map[string]any{"scale_to_zero": true})
	if status != http.StatusConflict || !strings.Contains(body, "app.gpu_scale_to_zero") {
		t.Errorf("scale to zero on an app with a GPU answered %d: %s", status, body)
	}
	if got, _ := h.db.GetApp(t.Context(), other.ID); got.ScaleToZero {
		t.Error("scale to zero was saved anyway")
	}
}

func TestWhatIsOnlyWorthKnowingIsAWarning(t *testing.T) {
	h := newHarness(t)
	h.withCluster(&gpuClusterFake{summary: twoGPUServers()})
	acme := h.newTenant("acme")
	app := h.app(acme, "inference")
	app.SourceType = "image"
	app.Autoscale, app.MinReplicas, app.MaxReplicas, app.CPUTarget = true, 1, 5, 70
	app.PreviewDeploys = true
	if err := h.db.UpdateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}

	status, view, body := setGPU(t, h, acme, app, map[string]any{
		"count": 1, "product": "NVIDIA-H100", "workloads": []string{"web", "trainer"},
	})
	if status != http.StatusOK {
		t.Fatalf("answered %d: %s", status, body)
	}
	codes := map[string]map[string]string{}
	for _, warning := range view.Warnings {
		codes[warning.Code] = warning.Params
		if warning.Text == "" {
			t.Errorf("the warning %s has no English for the CLI", warning.Code)
		}
	}
	// Five instances at most, a card each, and three in the cluster.
	if params, ok := codes["capacity"]; !ok || params["needed"] != "5" || params["allocatable"] != "3" {
		t.Errorf("the capacity warning is %v", codes["capacity"])
	}
	if _, ok := codes["product_missing"]; !ok {
		t.Error("a model no server has was not warned about")
	}
	if params := codes["process_missing"]; params["name"] != "trainer" {
		t.Errorf("a process that does not exist was not warned about: %v", codes)
	}
	if _, ok := codes["previews"]; !ok {
		t.Error("the previews going without were not said")
	}
	if view.GPU.Workloads[0] != "web" {
		t.Errorf("the workloads are %v; the app comes first", view.GPU.Workloads)
	}
}

func TestWithoutAClusterAGPUIsSavedUnchecked(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "ollama")
	status, view, body := setGPU(t, h, acme, app, map[string]any{"count": 1})
	if status != http.StatusOK || view.Cluster.Known {
		t.Fatalf("answered %d: %s", status, body)
	}
	if len(view.Warnings) != 1 || view.Warnings[0].Code != "unchecked" {
		t.Errorf("warnings are %+v", view.Warnings)
	}
}

func TestOnlyAMemberChangesAnAppsGPUsAndOnlyAnAdministratorMarksAServer(t *testing.T) {
	h := newHarness(t)
	fake := &gpuClusterFake{summary: twoGPUServers()}
	h.withCluster(fake)
	acme := h.newTenant("acme")
	app := h.app(acme, "ollama")
	viewer := h.newMember(acme, "viewer", store.RoleViewer)
	other := h.newTenant("other")

	if status, _ := h.do(viewer, http.MethodGet, "/api/apps/"+app.ID+"/gpu", nil); status != http.StatusOK {
		t.Errorf("a viewer reading the GPUs answered %d", status)
	}
	if status, _, _ := setGPU(t, h, viewer, app, map[string]any{"count": 1}); status != http.StatusForbidden {
		t.Errorf("a viewer changing the GPUs answered %d", status)
	}
	if status, _ := h.do(other, http.MethodGet, "/api/apps/"+app.ID+"/gpu", nil); status != http.StatusNotFound {
		t.Errorf("another team reading the GPUs answered %d", status)
	}

	server := h.node(acme, "gpu-box")
	server.NodeName = "gpu-2"
	if err := h.db.UpdateServer(t.Context(), &server); err != nil {
		t.Fatal(err)
	}
	// The team's owner is not the panel's administrator: the label is on a
	// node every team's apps share.
	status, body := h.do(acme, http.MethodPut, "/api/servers/"+server.ID+"/gpu", map[string]any{"nvidia": true})
	if status != http.StatusForbidden {
		t.Errorf("a team owner marking a server answered %d: %s", status, body)
	}
	if _, err := h.db.Exec(t.Context(), `UPDATE users SET is_admin = 1 WHERE id = ?`, acme.user.ID); err != nil {
		t.Fatal(err)
	}
	status, body = h.do(acme, http.MethodPut, "/api/servers/"+server.ID+"/gpu", map[string]any{"nvidia": true})
	if status != http.StatusOK || !fake.marked["gpu-2"] {
		t.Errorf("an administrator marking a server answered %d (%v): %s", status, fake.marked, body)
	}
	if status, _ := h.do(other, http.MethodPut, "/api/servers/"+server.ID+"/gpu", map[string]any{"nvidia": true}); status != http.StatusNotFound {
		t.Errorf("another team marking the server answered %d", status)
	}
}

func TestATemplatesGPUIsUsedWhenThereIsOne(t *testing.T) {
	install := func(summary ClusterSummary) (installedTemplate, *harness) {
		h := newHarness(t)
		h.withCluster(&gpuClusterFake{summary: summary})
		acme := h.newTenant("acme")
		status, body := h.do(acme, http.MethodPost, "/api/templates/ollama-with-open-webui/install",
			map[string]any{"environment_id": acme.env.ID})
		if status != http.StatusCreated {
			t.Fatalf("installing answered %d: %s", status, body)
		}
		var out installedTemplate
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return out, h
	}
	gpuOf := func(h *harness, out installedTemplate, name string) store.AppGPU {
		for _, app := range out.Apps {
			if app.Name == name {
				gpu, _ := h.db.GetAppGPU(t.Context(), app.ID)
				return gpu
			}
		}
		t.Fatalf("no app called %s in %+v", name, out.Apps)
		return store.AppGPU{}
	}

	out, h := install(twoGPUServers())
	if gpu := gpuOf(h, out, "ollama-api"); gpu.Count != 1 || gpu.Vendor != "nvidia" {
		t.Errorf("Ollama was installed with %+v on a cluster with cards", gpu)
	}
	if gpu := gpuOf(h, out, "open-webui"); gpu.Count != 0 {
		t.Errorf("the web interface was given a GPU it never asked for: %+v", gpu)
	}
	if len(out.WithoutGPU) != 0 {
		t.Errorf("without_gpu is %v on a cluster with cards", out.WithoutGPU)
	}

	// No card anywhere: installed all the same, and said so.
	out, h = install(ClusterSummary{Reachable: true, Nodes: []NodeInfo{{Name: "cpu-1", Ready: true, Schedulable: true}}})
	if gpu := gpuOf(h, out, "ollama-api"); gpu.Count != 0 {
		t.Errorf("Ollama was given %+v on a cluster with none", gpu)
	}
	if !slices.Equal(out.WithoutGPU, []string{"ollama-api"}) {
		t.Errorf("without_gpu is %v", out.WithoutGPU)
	}
}

func TestAClonedEnvironmentAsksForTheSameGPUs(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "vllm")
	if err := h.db.SetAppGPU(t.Context(), store.AppGPU{AppID: app.ID, Count: 2, Vendor: "nvidia", Workloads: []string{"web", "worker"}}); err != nil {
		t.Fatal(err)
	}
	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/clone", map[string]any{"name": "staging"})
	if status != http.StatusCreated {
		t.Fatalf("cloning answered %d: %s", status, body)
	}
	var cloned struct {
		Apps []store.App `json:"apps"`
	}
	if err := json.Unmarshal([]byte(body), &cloned); err != nil || len(cloned.Apps) != 1 {
		t.Fatalf("decode %v: %s", err, body)
	}
	gpu, _ := h.db.GetAppGPU(t.Context(), cloned.Apps[0].ID)
	if gpu.Count != 2 || !slices.Equal(gpu.Workloads, []string{"web", "worker"}) {
		t.Errorf("the copy asks for %+v", gpu)
	}
}
