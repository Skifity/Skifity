package cluster

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/registryguard"
	"skifity/internal/version"
)

// The registry's guard: a token per build for writing, open for reading. See
// internal/registryguard for why the line is drawn there.

const (
	// RegistryKeySecret holds the key the panel signs registry tokens with,
	// and the registry's guard checks them against. It lives beside the
	// registry, and the guard is the only thing that mounts it.
	RegistryKeySecret = "skifity-registry-key"
	registryKeyField  = "key"
	// RegistryUpstreamPort is where the registry itself listens, on its own
	// pod's loopback, so that the guard in front of it is the only way in.
	// Not 5001: the registry's image starts its debug server there.
	RegistryUpstreamPort = 5080
	// registryDebugAddress is where that debug server goes instead. It
	// answers with the registry's metrics and a profiler, and listened on
	// every address of the pod, where a build could ask it.
	registryDebugAddress = "127.0.0.1:5081"
)

// RegistryKey returns the key registry tokens are signed with, making it the
// first time it is asked for.
func (c *Cluster) RegistryKey(ctx context.Context) ([]byte, error) {
	c.registryKeyMu.Lock()
	defer c.registryKeyMu.Unlock()
	if c.registryKey != nil {
		return c.registryKey, nil
	}

	namespace := c.client.BuildNamespace()
	secrets := c.client.Clientset().CoreV1().Secrets(namespace)
	secret, err := secrets.Get(ctx, RegistryKeySecret, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if err := c.EnsureBuildNamespace(ctx); err != nil {
			return nil, err
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("make the registry's key: %w", err)
		}
		secret, err = secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: RegistryKeySecret, Namespace: namespace,
				Labels: map[string]string{"app.kubernetes.io/managed-by": version.Binary},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{registryKeyField: key},
		}, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			secret, err = secrets.Get(ctx, RegistryKeySecret, metav1.GetOptions{})
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read the registry's key: %w", err)
	}
	key := secret.Data[registryKeyField]
	if len(key) < 32 {
		return nil, fmt.Errorf("the registry's key in %s/%s is %d bytes; delete the Secret and the panel makes a new one",
			namespace, RegistryKeySecret, len(key))
	}
	c.registryKey = key
	return key, nil
}

// RegistryToken signs a token that lets its holder write to one repository of
// the in-cluster registry — <namespace>/<app> — or to every one, with
// registryguard.AnyRepository, which only the panel's own sweep is given.
func (c *Cluster) RegistryToken(ctx context.Context, repository string, ttl time.Duration) (string, error) {
	key, err := c.RegistryKey(ctx)
	if err != nil {
		return "", err
	}
	return registryguard.Sign(key, repository, time.Now().Add(ttl)), nil
}
