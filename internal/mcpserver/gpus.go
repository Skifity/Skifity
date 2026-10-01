package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/kube"
)

// Giving an app's instances GPUs. One tool, like scale_app: it answers with
// what the cluster offers, so an assistant asked for "a GPU" learns in the
// same call whether there is one to give.

type setGPUsInput struct {
	AppID string `json:"app_id" jsonschema:"the app's id"`
	Count int    `json:"count" jsonschema:"how many GPUs each instance is given; 0 takes them away"`
	// Left out, each of these keeps what the app has.
	Vendor  string   `json:"vendor,omitempty" jsonschema:"whose cards: nvidia, the default, amd or intel"`
	Product string   `json:"product,omitempty" jsonschema:"an NVIDIA model to prefer, as servers are labelled with it, such as NVIDIA-A10; any card of the vendor's is used when none is free"`
	On      []string `json:"on,omitempty" jsonschema:"which workloads get them: web is the app itself, anything else one of its processes; the app alone when left out"`
}

type gpuVendorSummary struct {
	Vendor          string   `json:"vendor"`
	Allocatable     int64    `json:"allocatable"`
	InUse           int64    `json:"in_use"`
	MostOnOneServer int64    `json:"most_on_one_server"`
	Products        []string `json:"products,omitempty"`
}

type setGPUsOutput struct {
	Count         int                `json:"count"`
	Vendor        string             `json:"vendor,omitempty"`
	Product       string             `json:"product,omitempty"`
	On            []string           `json:"on"`
	ClusterKnown  bool               `json:"cluster_known"`
	ClusterOffers []gpuVendorSummary `json:"cluster_offers"`
	Warnings      []string           `json:"warnings,omitempty"`
}

func (s *Server) registerGPUs() {
	addTool(s, &mcp.Tool{
		Name: "set_gpus",
		// Destructive: it restarts every instance, and taking GPUs away from
		// an app that needs one stops it working.
		Annotations: changes("Give an app GPUs", true, true),
		InputSchema: inputSchema[setGPUsInput](func(p map[string]*jsonschema.Schema) {
			oneOf(p["vendor"], kube.GPUVendors()...)
			p["count"].Minimum, p["count"].Maximum = new(0.0), new(float64(kube.MaxGPUs))
		}),
		Description: "Give each instance of an app GPUs, or take them away with count 0: for a model server such as Ollama, vLLM or ComfyUI. " +
			"A rollout, never a rebuild; the instances restart one at a time, the old one stopping first, because the new one needs its card. " +
			"Refused when no server offers that vendor's GPUs, when one instance would need more than any one server has, and while the app scales to zero. " +
			"The answer says what the cluster offers. A server whose card is not offered yet is set up on the Servers page by an administrator.",
	}, s.setGPUs)
}

func (s *Server) setGPUs(ctx context.Context, _ *mcp.CallToolRequest, in setGPUsInput) (*mcp.CallToolResult, setGPUsOutput, error) {
	body := map[string]any{"count": in.Count}
	if in.Vendor != "" {
		body["vendor"] = in.Vendor
	}
	if in.Product != "" {
		body["product"] = in.Product
	}
	if len(in.On) > 0 {
		body["workloads"] = in.On
	}
	var response struct {
		GPU struct {
			Count     int      `json:"count"`
			Vendor    string   `json:"vendor"`
			Product   string   `json:"product"`
			Workloads []string `json:"workloads"`
		} `json:"gpu"`
		Cluster struct {
			Known   bool               `json:"known"`
			Vendors []gpuVendorSummary `json:"vendors"`
		} `json:"cluster"`
		Warnings []struct {
			Text string `json:"text"`
		} `json:"warnings"`
	}
	if err := s.client.Do(ctx, "PUT", appPath(in.AppID, "/gpu"), body, &response); err != nil {
		return errorResult(err), setGPUsOutput{}, nil
	}

	out := setGPUsOutput{
		Count: response.GPU.Count, Vendor: response.GPU.Vendor, Product: response.GPU.Product,
		On: response.GPU.Workloads, ClusterKnown: response.Cluster.Known, ClusterOffers: response.Cluster.Vendors,
	}
	if len(out.On) == 0 {
		out.On = []string{kube.GPUWeb}
	}
	for _, warning := range response.Warnings {
		out.Warnings = append(out.Warnings, warning.Text)
	}
	summary := "The app has no GPU now."
	if out.Count > 0 {
		summary = fmt.Sprintf("Each instance of %s gets %d %s GPU(s).", strings.Join(out.On, ", "), out.Count, out.Vendor)
	}
	if len(out.Warnings) > 0 {
		summary += " " + strings.Join(out.Warnings, " ")
	}
	return textResult(summary), out, nil
}
