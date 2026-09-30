package manifests

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/yaml"

	"skifity/internal/kube"
)

// values are what the installer substitutes, with obviously fake stand-ins.
var values = map[string]string{
	"NAMESPACE":   "skifity-system",
	"IMAGE":       "ghcr.io/example/skifity:0.0.0-test",
	"NODE":        "test-node-1",
	"PUBLIC_URL":  "https://panel.example.test",
	"POD_NETWORK": "wireguard-native",
	"HOST":        "panel.example.test",
	"CONFIG_DIR":  "/etc/skifity",
	"DATA_DIR":    "/var/lib/skifity",
	"ISSUER":      "skifity-letsencrypt",
	"EMAIL":       "nobody@example.test",
	"ACME_SERVER": "https://acme-staging-v02.api.letsencrypt.org/directory",
}

func deployDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "deploy"))
	if err != nil {
		t.Fatalf("locate deploy/: %v", err)
	}
	return dir
}

func read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(deployDir(t), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func manifestNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(deployDir(t))
	if err != nil {
		t.Fatalf("read deploy/: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".yaml") {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("deploy/ has no manifests")
	}
	return names
}

// Every manifest must render with the values the installer supplies, and the
// result must be valid YAML with nothing left to substitute. A placeholder that
// survives into a cluster is a Deployment that pulls an image called
// "__IMAGE__", which is a confusing way to find out about a typo.
func TestEveryManifestRenders(t *testing.T) {
	for _, name := range manifestNames(t) {
		t.Run(name, func(t *testing.T) {
			rendered, err := Render(read(t, name), values)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if strings.Contains(rendered, "__") {
				// The comment header names the placeholders, so a bare "__" is
				// only suspicious when it is still wrapped in a placeholder.
				for _, line := range strings.Split(rendered, "\n") {
					if placeholderPattern.MatchString(line) {
						t.Errorf("placeholder left in: %s", strings.TrimSpace(line))
					}
				}
			}
			for i, doc := range strings.Split(rendered, "\n---\n") {
				if strings.TrimSpace(stripComments(doc)) == "" {
					continue
				}
				var parsed map[string]any
				if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
					t.Fatalf("document %d is not valid YAML: %v", i, err)
				}
				if parsed["kind"] == nil {
					t.Errorf("document %d has no kind", i)
				}
			}
		})
	}
}

// Render must refuse rather than silently apply a manifest that is missing a
// value.
func TestRenderReportsMissingValues(t *testing.T) {
	_, err := Render(read(t, "panel.yaml"), map[string]string{"NAMESPACE": "x"})
	if err == nil {
		t.Fatal("rendering with no image should have failed")
	}
	if !strings.Contains(err.Error(), "IMAGE") {
		t.Errorf("the error should name the missing placeholder, got %q", err)
	}
	// Every missing name should appear once, not once per occurrence.
	if strings.Count(err.Error(), "IMAGE") != 1 {
		t.Errorf("a missing placeholder should be reported once, got %q", err)
	}
}

// The panel holds the master key and can reach the whole cluster, so the
// properties below are the ones worth a test: if a refactor drops one, the
// blast radius of a compromised panel grows quietly.
func TestPanelDeploymentIsHardened(t *testing.T) {
	rendered, err := Render(read(t, "panel.yaml"), values)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var deployment appsv1.Deployment
	found := false
	for _, doc := range strings.Split(rendered, "\n---\n") {
		var head struct {
			Kind string `json:"kind"`
		}
		if err := yaml.Unmarshal([]byte(doc), &head); err != nil || head.Kind != "Deployment" {
			continue
		}
		if err := yaml.Unmarshal([]byte(doc), &deployment); err != nil {
			t.Fatalf("parse the Deployment: %v", err)
		}
		found = true
	}
	if !found {
		t.Fatal("panel.yaml has no Deployment")
	}

	pod := deployment.Spec.Template.Spec
	if len(pod.Containers) != 1 {
		t.Fatalf("expected one container, got %d", len(pod.Containers))
	}
	container := pod.Containers[0]

	if pod.SecurityContext == nil || pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot {
		t.Error("the panel must not run as root")
	}
	if container.SecurityContext == nil {
		t.Fatal("the container has no security context")
	}
	if escalation := container.SecurityContext.AllowPrivilegeEscalation; escalation == nil || *escalation {
		t.Error("privilege escalation must be off")
	}
	if readOnly := container.SecurityContext.ReadOnlyRootFilesystem; readOnly == nil || !*readOnly {
		t.Error("the root filesystem must be read-only")
	}
	if container.SecurityContext.Capabilities == nil ||
		len(container.SecurityContext.Capabilities.Drop) == 0 ||
		string(container.SecurityContext.Capabilities.Drop[0]) != "ALL" {
		t.Error("every capability must be dropped")
	}

	// Two panels writing to one SQLite file is corruption, so the rollout must
	// stop the old one before starting the new one.
	if deployment.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("the panel must use the Recreate strategy, got %q", deployment.Spec.Strategy.Type)
	}
	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas != 1 {
		t.Errorf("the panel runs as a single instance, got %d", *deployment.Spec.Replicas)
	}

	// The database and the master key are on one node's disk.
	if pod.NodeSelector["kubernetes.io/hostname"] != values["NODE"] {
		t.Error("the panel must be pinned to the node holding its data")
	}

	// A memory limit protects the server; a CPU limit would throttle the one
	// tool available for fixing a struggling cluster.
	if container.Resources.Limits.Memory().IsZero() {
		t.Error("the panel needs a memory limit")
	}
	if !container.Resources.Limits.Cpu().IsZero() {
		t.Error("the panel must not have a CPU limit")
	}

	// A panel with no probes looks healthy while it is failing to start.
	if container.StartupProbe == nil || container.ReadinessProbe == nil || container.LivenessProbe == nil {
		t.Error("the panel needs startup, readiness and liveness probes")
	}
}

// The installer and the manifests have to agree: a placeholder the manifests
// use but the installer never substitutes reaches the cluster verbatim.
func TestInstallerSubstitutesEveryPlaceholder(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "installer", "install.sh"))
	if err != nil {
		t.Fatalf("read the installer: %v", err)
	}

	for _, name := range manifestNames(t) {
		for _, placeholder := range Placeholders(read(t, name)) {
			needle := "s|__" + placeholder + "__|"
			if !strings.Contains(string(script), needle) {
				t.Errorf("%s uses __%s__, but install.sh never substitutes it", name, placeholder)
			}
		}
	}
}

// stripComments removes whole-line comments so an all-comment YAML document is
// recognised as empty.
func stripComments(doc string) string {
	var b strings.Builder
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestInstallerAndPanelAgreeOnTheRegistry: the installer writes the container
// runtime's mirror configuration before the panel exists, so the two have to
// agree about the registry's address without ever talking to each other. If
// they drift, every image the panel builds becomes unpullable and the symptom
// is an ImagePullBackOff nobody can trace back to here.
func TestInstallerAndPanelAgreeOnTheRegistry(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "installer", "install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	text := string(script)

	for _, want := range []string{
		`BUILDS_NAMESPACE="` + kube.BuildsNamespace + `"`,
		`REGISTRY_HOST="` + kube.RegistryService + `.${BUILDS_NAMESPACE}.svc.cluster.local:` +
			strconv.Itoa(kube.RegistryPort) + `"`,
		"REGISTRY_NODE_PORT=" + strconv.Itoa(kube.RegistryNodePort),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("install.sh does not set %s", want)
		}
	}

	// And the file it writes has to be the one the panel would write for a
	// server added later, or the first node and every other node disagree.
	yaml := kube.RegistriesYAML()
	if !strings.Contains(yaml, kube.RegistryHost()) {
		t.Errorf("the mirror configuration does not name the registry:\n%s", yaml)
	}
	if !strings.Contains(yaml, "http://127.0.0.1:"+strconv.Itoa(kube.RegistryNodePort)) {
		t.Errorf("the mirror configuration does not point at the node port:\n%s", yaml)
	}
}
