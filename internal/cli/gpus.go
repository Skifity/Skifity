package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// gpuAnswer is an app's GPU card as the panel answers it.
type gpuAnswer struct {
	GPU struct {
		Count     int      `json:"count"`
		Vendor    string   `json:"vendor"`
		Product   string   `json:"product"`
		Workloads []string `json:"workloads"`
	} `json:"gpu"`
	Processes []string `json:"processes"`
	Cluster   struct {
		Known   bool `json:"known"`
		Vendors []struct {
			Vendor          string   `json:"vendor"`
			Allocatable     int64    `json:"allocatable"`
			InUse           int64    `json:"in_use"`
			MostOnOneServer int64    `json:"most_on_one_server"`
			Servers         int      `json:"servers"`
			Products        []string `json:"products"`
		} `json:"vendors"`
	} `json:"cluster"`
	Warnings []struct {
		Code string `json:"code"`
		Text string `json:"text"`
	} `json:"warnings"`
}

// cmdGPUs shows or changes the GPUs an app's instances are given.
//
//	skifity gpus                             what it has, and what the cluster offers
//	skifity gpus --count 1                   one NVIDIA card for each instance
//	skifity gpus --count 2 --vendor amd
//	skifity gpus --count 1 --product NVIDIA-A10 --on web,worker
//	skifity gpus --count 0                   none
//
// Flags only, like scale: each is a setting of the one thing, and the ones
// not given are left as they are.
func cmdGPUs(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("gpus", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	count := flags.Int("count", -1, "how many GPUs each instance is given; 0 for none")
	vendor := flags.String("vendor", "", "nvidia (the default), amd or intel")
	product := flags.String("product", "", `a model to prefer, as servers are labelled with it, such as NVIDIA-A10; "any" for none`)
	on := flags.String("on", "", "which of the app's workloads get them, comma-separated: web is the app itself, anything else a process")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) > 0 {
		return errdoc.BadRequest(fmt.Sprintf("%s gpus takes flags only, such as --count 1. %q is not one.",
			version.Binary, positional[0]))
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	app, err := resolveApp(ctx, client, cfg, *appID)
	if err != nil {
		return err
	}
	path := "/api/apps/" + app + "/gpu"

	body := gpuChanges(*count, *vendor, *product, *on)
	var answer gpuAnswer
	if len(body) == 0 {
		err = client.Do(ctx, "GET", path, nil, &answer)
	} else {
		err = client.Do(ctx, "PUT", path, body, &answer)
	}
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, answer)
	}
	printGPUs(out, answer, len(body) > 0)
	return nil
}

// gpuChanges is the request body for what was given on the command line.
func gpuChanges(count int, vendor, product, on string) map[string]any {
	body := map[string]any{}
	if count >= 0 {
		body["count"] = count
	}
	if vendor != "" {
		body["vendor"] = vendor
	}
	switch product {
	case "":
	case "any":
		body["product"] = ""
	default:
		body["product"] = product
	}
	if on != "" {
		workloads := []string{}
		for _, name := range strings.Split(on, ",") {
			if name = strings.TrimSpace(name); name != "" {
				workloads = append(workloads, name)
			}
		}
		body["workloads"] = workloads
	}
	return body
}

func printGPUs(out io.Writer, answer gpuAnswer, changed bool) {
	gpu := answer.GPU
	if changed {
		fmt.Fprint(out, "GPUs updated. ")
	}
	if gpu.Count <= 0 {
		fmt.Fprintln(out, "This app has no GPU.")
	} else {
		on := "the app"
		if len(gpu.Workloads) > 0 {
			on = strings.Join(gpu.Workloads, ", ")
		}
		fmt.Fprintf(out, "Each instance of %s gets %d %s GPU", on, gpu.Count, gpu.Vendor)
		if gpu.Count > 1 {
			fmt.Fprint(out, "s")
		}
		if gpu.Product != "" {
			fmt.Fprintf(out, ", preferring a %s", gpu.Product)
		}
		fmt.Fprintln(out, ".")
	}

	if !answer.Cluster.Known {
		fmt.Fprintln(out, "\nThe cluster could not be asked what GPUs it has.")
	} else {
		fmt.Fprintln(out, "\nIn the cluster:")
		for _, v := range answer.Cluster.Vendors {
			if v.Allocatable <= 0 {
				fmt.Fprintf(out, "  %-7s none\n", v.Vendor)
				continue
			}
			fmt.Fprintf(out, "  %-7s %d on %d servers, %d in use, at most %d on one server",
				v.Vendor, v.Allocatable, v.Servers, v.InUse, v.MostOnOneServer)
			if len(v.Products) > 0 {
				fmt.Fprintf(out, " (%s)", strings.Join(v.Products, ", "))
			}
			fmt.Fprintln(out)
		}
	}
	for _, warning := range answer.Warnings {
		fmt.Fprintf(out, "\n  Note: %s\n", warning.Text)
	}
}
