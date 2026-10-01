package builder

import (
	"encoding/json"
	"strings"
	"testing"
)

// A build never has a GPU. What goes into an image does not depend on one, a
// build that asked for the card the app is using would wait for it, and the
// builders run somebody's install scripts: nothing there should be handed a
// device. JobSpec has no field an app's GPUs could arrive through, and this
// holds every builder's Job to that.
func TestABuildNeverHasAGPU(t *testing.T) {
	for _, b := range []Builder{BuilderRailpack, BuilderDockerfile, BuilderNixpacks, BuilderStatic} {
		spec := baseJob()
		spec.Builder = b
		spec.NixpacksImage = "registry.example.test/nixpacks:1"
		job, err := BuildJob(spec)
		if err != nil {
			t.Fatalf("%s: BuildJob: %v", b, err)
		}
		pod := job.Spec.Template.Spec
		if pod.RuntimeClassName != nil {
			t.Errorf("%s: the build runs under the %s runtime", b, *pod.RuntimeClassName)
		}
		rendered, err := json.Marshal(pod)
		if err != nil {
			t.Fatal(err)
		}
		for _, resource := range []string{"nvidia.com/gpu", "amd.com/gpu", "gpu.intel.com/i915", "NVIDIA_VISIBLE_DEVICES"} {
			if strings.Contains(string(rendered), resource) {
				t.Errorf("%s: the build's pod mentions %s", b, resource)
			}
		}
	}
}
