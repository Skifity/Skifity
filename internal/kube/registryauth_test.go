package kube

import (
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// One Secret holds every registry a team pulls from; the kubelet and buildctl
// each pick the entry for the host an image names.
func TestAPullSecretHoldsEveryLoginUnderTheHostItsImagesName(t *testing.T) {
	secret, err := PullSecret(TeamRegistriesSecretName, "acme-production", []RegistryLogin{
		{Host: "ghcr.io", Username: "acme-bot", Password: "ghp_not_real"},
		{Host: "docker.io", Username: "acme", Password: "dckr_pat_not_real"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if secret.Type != corev1.SecretTypeDockerConfigJson {
		t.Fatalf("type %s", secret.Type)
	}
	var config struct {
		Auths map[string]map[string]string `json:"auths"`
	}
	if err := json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &config); err != nil {
		t.Fatal(err)
	}
	if config.Auths["ghcr.io"]["username"] != "acme-bot" {
		t.Errorf("ghcr.io is filed as %v", config.Auths)
	}
	// Docker Hub's credential under the name Docker's config uses, or the
	// kubelet never finds it.
	if config.Auths["https://index.docker.io/v1/"]["username"] != "acme" {
		t.Errorf("Docker Hub is filed as %v", config.Auths)
	}
}

func TestARegistryHostIsWhatAnImageReferenceCarries(t *testing.T) {
	for input, want := range map[string]string{
		"ghcr.io": "ghcr.io", "https://GHCR.io/": "ghcr.io", "hub.docker.com": "docker.io",
		"index.docker.io": "docker.io", "registry.example.com:5000": "registry.example.com:5000",
	} {
		if got, err := NormalizeRegistryHost(input); err != nil || got != want {
			t.Errorf("%q became %q (%v), want %q", input, got, err, want)
		}
	}
	for _, bad := range []string{"", "ghcr.io/acme/app", "not a host", "-bad.example.com"} {
		if _, err := NormalizeRegistryHost(bad); err == nil {
			t.Errorf("%q was accepted as a registry", bad)
		}
	}
	for image, want := range map[string]string{
		"nginx:1.27": "docker.io", "acme/web:1": "docker.io", "ghcr.io/acme/web:1": "ghcr.io",
		"registry.example.com:5000/web:1": "registry.example.com:5000", "localhost/web:1": "localhost",
	} {
		if got := ImageHost(image); got != want {
			t.Errorf("%s pulls from %q, want %q", image, got, want)
		}
	}
}

func TestAnAppPullsWithThePanelsAndTheTeamsCredentials(t *testing.T) {
	s := baseSpec()
	s.ImagePullSecret = RegistrySecretName
	s.TeamPullSecret = TeamRegistriesSecretName
	pull := BuildDeployment(s).Spec.Template.Spec.ImagePullSecrets
	if len(pull) != 2 || pull[0].Name != RegistrySecretName || pull[1].Name != TeamRegistriesSecretName {
		t.Errorf("the app pulls with %v", pull)
	}
	if pull := BuildDeployment(baseSpec()).Spec.Template.Spec.ImagePullSecrets; len(pull) != 0 {
		t.Errorf("an app with no credentials names %v, which are not there", pull)
	}
}
