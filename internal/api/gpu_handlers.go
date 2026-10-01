package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// An app's GPUs, and marking a server as having one.
//
// What is checked, and against what. An app is refused a kind of card no
// server advertises, and more cards for one instance than any one server has:
// either would be an instance that waits for a server for ever. It is refused
// a GPU while it scales to zero. Everything else is a warning, because the
// cluster changes under a setting — a card in use today is free tomorrow, a
// server is added — and a refusal would have to be argued with.
//
// The cluster is read whole, whoever asks. An app can be placed on any
// server, so what matters is every card there is; what is shown is added up
// by vendor, and never names a server another team added.

// gpuSettings is what the app's GPU card shows.
type gpuSettings struct {
	GPU store.AppGPU `json:"gpu"`
	// Processes are the app's own, which can be given the GPUs as well as or
	// instead of the app.
	Processes []string `json:"processes"`
	// Cluster is what the servers offer. Known is false when the cluster
	// could not be asked, and the rest is then empty rather than zero.
	Cluster  gpuCluster   `json:"cluster"`
	Warnings []GPUWarning `json:"warnings"`
}

type gpuCluster struct {
	Known   bool            `json:"known"`
	Vendors []gpuVendorView `json:"vendors"`
}

// gpuVendorView is one vendor's cards, added up over the cluster.
type gpuVendorView struct {
	Vendor   string `json:"vendor"`
	Resource string `json:"resource"`
	// Allocatable is how many the servers offer together, InUse how many of
	// those the pods placed on them hold.
	Allocatable int64 `json:"allocatable"`
	InUse       int64 `json:"in_use"`
	// MostOnOneServer is the most one instance can be given.
	MostOnOneServer int64 `json:"most_on_one_server"`
	Servers         int   `json:"servers"`
	// Products are the models GPU feature discovery found, to prefer one.
	Products []string `json:"products"`
}

// GPUWarning is something about an app's GPUs worth knowing that does not
// stop it being saved. The interface says it in its own words from Code and
// Params; Text is the English, for the CLI and an assistant.
type GPUWarning struct {
	Code   string            `json:"code"`
	Params map[string]string `json:"params,omitempty"`
	Text   string            `json:"text"`
}

type setGPURequest struct {
	Count     *int      `json:"count,omitempty"`
	Vendor    *string   `json:"vendor,omitempty"`
	Product   *string   `json:"product,omitempty"`
	Workloads *[]string `json:"workloads,omitempty"`
}

func (s *Server) handleGetAppGPU(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	gpu, err := s.db.GetAppGPU(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := s.gpuView(r.Context(), app, gpu)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleSetAppGPU(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setGPURequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	gpu, err := s.db.GetAppGPU(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	was := gpu
	if req.Count != nil {
		gpu.Count = *req.Count
	}
	if req.Vendor != nil {
		gpu.Vendor = strings.ToLower(strings.TrimSpace(*req.Vendor))
	}
	if req.Product != nil {
		gpu.Product = strings.TrimSpace(*req.Product)
	}
	if req.Workloads != nil {
		gpu.Workloads = cleanWorkloads(*req.Workloads)
	}
	if gpu.Count > 0 && gpu.Vendor == "" {
		// NVIDIA's is the card nearly everybody asking has.
		gpu.Vendor = kube.GPUVendorNVIDIA
	}
	if gpu.Count <= 0 {
		// Nothing asked for is nothing kept: the next time GPUs are turned
		// on they start from the defaults, not from a vendor chosen a year
		// ago.
		gpu = store.AppGPU{AppID: app.ID, Workloads: []string{}}
	}
	if err := kube.ValidateGPURequest(gpuRequest(gpu)); err != nil {
		writeError(w, r, errdoc.BadRequest(err.Error()))
		return
	}
	if gpuRequest(gpu).Covers(kube.GPUWeb) && app.ScaleToZero {
		writeError(w, r, errdoc.GPUScaleToZero())
		return
	}
	if gpu.Count > 0 && s.cluster != nil {
		if summary, err := s.cluster.Summary(r.Context()); err == nil && summary.Reachable {
			if err := gpuFits(gpu, clusterGPUs(summary)); err != nil {
				writeError(w, r, err)
				return
			}
		}
	}

	if err := s.db.SetAppGPU(r.Context(), gpu); err != nil {
		writeError(w, r, err)
		return
	}
	// A rollout, never a build: which card an instance gets is not in its
	// image (ADR-0007).
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			s.log.Warn("could not apply the GPU change to the cluster", "app", app.ID, "error", err)
		}
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "app.gpus_changed", "app", app.ID, describeGPU(was)+" → "+describeGPU(gpu))

	view, err := s.gpuView(r.Context(), app, gpu)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// gpuRequest is the stored setting as the manifests read it.
func gpuRequest(gpu store.AppGPU) kube.GPURequest {
	return kube.GPURequest{Count: gpu.Count, Vendor: gpu.Vendor, Product: gpu.Product, Workloads: gpu.Workloads}
}

// cleanWorkloads trims, de-duplicates and orders the workloads named, the app
// first. The app alone is written as nothing, which is what it defaults to.
func cleanWorkloads(in []string) []string {
	out := []string{}
	for _, name := range in {
		name = strings.TrimSpace(name)
		if name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		switch {
		case a == kube.GPUWeb:
			return -1
		case b == kube.GPUWeb:
			return 1
		}
		return strings.Compare(a, b)
	})
	if len(out) == 1 && out[0] == kube.GPUWeb {
		return []string{}
	}
	return out
}

// describeGPU is a setting in a few words, for the activity log.
func describeGPU(gpu store.AppGPU) string {
	if gpu.Count <= 0 {
		return "none"
	}
	out := fmt.Sprintf("%d %s", gpu.Count, gpu.Vendor)
	if gpu.Product != "" {
		out += " (" + gpu.Product + ")"
	}
	if len(gpu.Workloads) > 0 {
		out += " on " + strings.Join(gpu.Workloads, ", ")
	}
	return out
}

// clusterGPUs adds up what the servers offer, by vendor, in vendor order.
// Every vendor is in the answer, so a client can say "none" as easily as
// "two".
func clusterGPUs(summary ClusterSummary) []gpuVendorView {
	out := make([]gpuVendorView, 0, len(kube.GPUVendors()))
	for _, vendor := range kube.GPUVendors() {
		resource, _ := kube.GPUResource(vendor)
		view := gpuVendorView{Vendor: vendor, Resource: string(resource), Products: []string{}}
		var products []string
		for _, node := range summary.Nodes {
			for _, gpu := range node.GPUs {
				if gpu.Vendor != vendor || gpu.Allocatable <= 0 {
					continue
				}
				// A server that is down or cordoned places nothing, whatever
				// its cards.
				if !node.Ready || !node.Schedulable {
					continue
				}
				view.Allocatable += gpu.Allocatable
				view.InUse += gpu.InUse
				view.MostOnOneServer = max(view.MostOnOneServer, gpu.Allocatable)
				view.Servers++
				products = append(products, gpu.Product)
			}
		}
		view.Products = kube.SortGPUProducts(products)
		out = append(out, view)
	}
	return out
}

// clusterGPUsNow is what the servers offer, or nil when the cluster cannot be
// asked.
func (s *Server) clusterGPUsNow(r *http.Request) []gpuVendorView {
	if s.cluster == nil {
		return nil
	}
	summary, err := s.cluster.Summary(r.Context())
	if err != nil || !summary.Reachable {
		return nil
	}
	return clusterGPUs(summary)
}

// gpuFits refuses what could never be placed on the cluster as it is.
func gpuFits(gpu store.AppGPU, vendors []gpuVendorView) error {
	for _, view := range vendors {
		if view.Vendor != gpu.Vendor {
			continue
		}
		if view.Allocatable <= 0 {
			return errdoc.GPUUnavailable(gpu.Vendor, view.Resource)
		}
		if int64(gpu.Count) > view.MostOnOneServer {
			return errdoc.GPUTooMany(gpu.Count, view.Resource, view.MostOnOneServer)
		}
	}
	return nil
}

// gpuView is the setting with what the cluster offers and what to know.
func (s *Server) gpuView(ctx context.Context, app store.App, gpu store.AppGPU) (gpuSettings, error) {
	view := gpuSettings{GPU: gpu, Processes: []string{}, Cluster: gpuCluster{Vendors: []gpuVendorView{}}}
	if view.GPU.Workloads == nil {
		view.GPU.Workloads = []string{}
	}
	processes, err := s.db.ListProcesses(ctx, app.ID)
	if err != nil {
		return view, err
	}
	instances := map[string]int{}
	for _, process := range processes {
		view.Processes = append(view.Processes, process.Name)
		instances[process.Name] = process.Instances
	}
	if s.cluster != nil {
		if summary, err := s.cluster.Summary(ctx); err == nil && summary.Reachable {
			view.Cluster = gpuCluster{Known: true, Vendors: clusterGPUs(summary)}
		}
	}
	view.Warnings = gpuWarnings(app, gpu, instances, view.Cluster)
	return view, nil
}

// gpuWarnings are what is worth knowing about a setting that stands.
func gpuWarnings(app store.App, gpu store.AppGPU, processes map[string]int, cluster gpuCluster) []GPUWarning {
	warnings := []GPUWarning{}
	if gpu.Count <= 0 {
		return warnings
	}
	request := gpuRequest(gpu)
	add := func(code, text string, params map[string]string) {
		warnings = append(warnings, GPUWarning{Code: code, Params: params, Text: text})
	}

	for _, workload := range gpu.Workloads {
		if _, ok := processes[workload]; workload != kube.GPUWeb && !ok {
			add("process_missing", fmt.Sprintf(
				"%s is not one of this app's processes yet; it is given GPUs once it is.", workload),
				map[string]string{"name": workload})
		}
	}

	if !cluster.Known {
		add("unchecked", "The cluster could not be asked whether it has these GPUs, so this was not checked against it.", nil)
	} else {
		// At its most: the app's own instances at the top of their range,
		// and every process given them at the number it runs.
		needed := 0
		if request.Covers(kube.GPUWeb) {
			instances := app.Replicas
			if app.Autoscale {
				instances = app.MaxReplicas
			}
			needed += gpu.Count * max(instances, 0)
		}
		for name, count := range processes {
			if request.Covers(name) {
				needed += gpu.Count * max(count, 0)
			}
		}
		for _, view := range cluster.Vendors {
			if view.Vendor != gpu.Vendor {
				continue
			}
			if int64(needed) > view.Allocatable {
				add("capacity", fmt.Sprintf(
					"At its most this app asks for %d %s GPUs, and the cluster has %d: the instances past that wait for a card to come free.",
					needed, gpu.Vendor, view.Allocatable),
					map[string]string{"needed": strconv.Itoa(needed), "vendor": gpu.Vendor,
						"allocatable": strconv.FormatInt(view.Allocatable, 10)})
			}
			if gpu.Product != "" && !slices.Contains(view.Products, gpu.Product) {
				add("product_missing", fmt.Sprintf(
					"No server says it has a %s, so the app runs on any %s card.", gpu.Product, gpu.Vendor),
					map[string]string{"product": gpu.Product, "vendor": gpu.Vendor})
			}
		}
	}

	if app.PreviewDeploys && request.Covers(kube.GPUWeb) {
		add("previews", "The app's previews run without a GPU, so a preview never holds the card production needs.", nil)
	}
	return warnings
}

// --- marking a server ---

type setServerGPURequest struct {
	// NVIDIA is whether the server has an NVIDIA card.
	NVIDIA bool `json:"nvidia"`
}

// handleSetServerGPU marks a server as having an NVIDIA card, for a cluster
// with nothing else that can tell: that is what puts the device plugin there.
//
// A panel administrator's, like adding the server was. The label is on the
// node, which every team's apps share, and it decides where a container with
// a path onto the host's kubelet directory runs.
func (s *Server) handleSetServerGPU(w http.ResponseWriter, r *http.Request) {
	server, user, err := s.authorizeServer(r, chi.URLParam(r, "serverID"), store.RoleAdmin)
	if err == nil {
		err = serverAdmin(user)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setServerGPURequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	if server.NodeName == "" {
		writeError(w, r, errdoc.BadRequest("This server is not in the cluster yet, so there is nothing to mark. Wait for it to finish joining."))
		return
	}
	if err := s.cluster.SetNodeGPULabel(r.Context(), server.NodeName, req.NVIDIA); err != nil {
		writeError(w, r, err)
		return
	}
	detail := server.Name + ": no NVIDIA GPU"
	if req.NVIDIA {
		detail = server.Name + ": NVIDIA GPU"
	}
	s.audit(r, server.TeamID, "server.gpu_marked", "server", server.ID, detail)
	writeJSON(w, http.StatusOK, map[string]any{"server_id": server.ID, "node": server.NodeName, "nvidia": req.NVIDIA})
}
