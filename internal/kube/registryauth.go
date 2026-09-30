package kube

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/version"
)

// Credentials for a registry that is not the one in the cluster.
//
// An operator can point Skifity at their own registry — Docker Hub, GHCR, a
// Harbor — and give it a username and a password. Both were stored, sealed,
// shown on the settings page, and never turned into anything: the build Job
// mounted a Secret called skifity-registry-auth that nothing created, so the
// pod could not start at all, and the app had no pull secret either. An
// external registry broke the deploy at both ends, with a Kubernetes error
// about a missing Secret and nothing to say why.

// RegistrySecretName is the Secret holding credentials for an external
// registry. The same name in the build namespace, where it is pushed from,
// and in each app's namespace, where it is pulled from.
const RegistrySecretName = "skifity-registry-auth"

// RegistrySecret renders the dockerconfigjson Secret both ends need.
//
// server is the registry's host, as it appears in an image reference. The
// empty string for either credential is not an error: a registry that needs no
// password is a normal thing to run inside a private network, and the caller
// decides whether to apply a secret at all.
func RegistrySecret(namespace, server, username, password string) (*corev1.Secret, error) {
	server = strings.TrimSuffix(strings.TrimSpace(server), "/")
	if server == "" {
		return nil, fmt.Errorf("a registry secret needs the registry's address")
	}
	// Docker's own config uses this URL for Docker Hub rather than the short
	// name an image reference carries, and a credential filed under the short
	// name is one the kubelet never finds.
	if server == "docker.io" || server == "index.docker.io" {
		server = "https://index.docker.io/v1/"
	}

	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	config := map[string]any{
		"auths": map[string]any{
			server: map[string]string{
				"username": username,
				"password": password,
				"auth":     auth,
			},
		},
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("build the registry credentials: %w", err)
	}

	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      RegistrySecretName,
			Namespace: namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": version.Binary},
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: encoded},
	}, nil
}
