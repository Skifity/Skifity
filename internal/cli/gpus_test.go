package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const gpuAnswerBody = `{"gpu":{"count":1,"vendor":"nvidia","product":"","workloads":["web","worker"]},
"processes":["worker"],
"cluster":{"known":true,"vendors":[{"vendor":"nvidia","resource":"nvidia.com/gpu","allocatable":2,"in_use":1,"most_on_one_server":2,"servers":1,"products":["NVIDIA-A10"]}]},
"warnings":[{"code":"previews","text":"The app's previews run without a GPU."}]}`

// gpuPanel answers the GPU route and remembers what it was sent.
func gpuPanel(t *testing.T) *[]map[string]any {
	t.Helper()
	sent := &[]map[string]any{}
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/apps/app_1/gpu" {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		body := map[string]any{"method": r.Method}
		if r.Method == http.MethodPut {
			_ = json.NewDecoder(r.Body).Decode(&body)
			body["method"] = r.Method
		}
		*sent = append(*sent, body)
		_, _ = io.WriteString(w, gpuAnswerBody)
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	return sent
}

func TestGPUsShowsWithoutFlagsAndSendsOnlyWhatWasGiven(t *testing.T) {
	sent := gpuPanel(t)
	var out bytes.Buffer

	if err := cmdGPUs(t.Context(), []string{"--app", "app_1"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 || (*sent)[0]["method"] != http.MethodGet {
		t.Fatalf("with no flags it sent %v; it should only read", *sent)
	}
	if !strings.Contains(out.String(), "1 nvidia GPU") || !strings.Contains(out.String(), "NVIDIA-A10") {
		t.Errorf("printed:\n%s", out.String())
	}

	out.Reset()
	if err := cmdGPUs(t.Context(), []string{"--app", "app_1", "--count", "2", "--on", "web, worker", "--product", "any"}, &out); err != nil {
		t.Fatal(err)
	}
	put := (*sent)[1]
	if put["method"] != http.MethodPut || put["count"] != float64(2) || put["product"] != "" {
		t.Errorf("sent %v", put)
	}
	if _, ok := put["vendor"]; ok {
		t.Errorf("a vendor nobody gave was sent: %v", put)
	}
	workloads, _ := put["workloads"].([]any)
	if !slices.Equal(workloads, []any{"web", "worker"}) {
		t.Errorf("workloads sent as %v", put["workloads"])
	}

	// Taking them away is a count of nothing, which is still a change.
	if err := cmdGPUs(t.Context(), []string{"--app", "app_1", "--count", "0"}, &out); err != nil {
		t.Fatal(err)
	}
	if last := (*sent)[2]; last["method"] != http.MethodPut || last["count"] != float64(0) {
		t.Errorf("--count 0 sent %v", last)
	}

	if err := cmdGPUs(t.Context(), []string{"--app", "app_1", "two"}, &out); err == nil {
		t.Error("a word that is not a flag was accepted")
	}
}

func TestGPUsJSONIsThePanelsAnswer(t *testing.T) {
	gpuPanel(t)
	var out bytes.Buffer
	if err := cmdGPUs(t.Context(), []string{"--app", "app_1", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var answer gpuAnswer
	if err := json.Unmarshal(out.Bytes(), &answer); err != nil {
		t.Fatalf("--json printed something that is not JSON: %v\n%s", err, out.String())
	}
	if answer.GPU.Count != 1 || !answer.Cluster.Known || len(answer.Warnings) != 1 || answer.Warnings[0].Code != "previews" {
		t.Errorf("decoded %+v", answer)
	}
}
