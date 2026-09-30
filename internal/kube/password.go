package kube

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// A password in front of an app.
//
// Traefik's basicAuth middleware, reading its one account from a Secret in the
// app's own namespace. Both objects are the app's, named after it, so two apps
// in one environment can have different passwords, and turning one off removes
// exactly its own two objects.
//
// The Secret holds a bcrypt hash, which is what Traefik compares against on
// every request. The password itself never reaches the cluster.

// PasswordMiddlewareName is the Middleware an app's Ingress names when it has a
// password.
func PasswordMiddlewareName(app string) string { return ResourceName(app, "password") }

// PasswordSecretName is the Secret that middleware reads.
func PasswordSecretName(app string) string { return ResourceName(app, "password") }

// BuildPasswordSecret renders the account the middleware checks against. Nil
// when the app has no password.
func BuildPasswordSecret(s AppSpec) *corev1.Secret {
	if s.PasswordUsers == "" {
		return nil
	}
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      PasswordSecretName(s.Name),
			Namespace: s.Namespace,
			Labels:    s.Labels(),
		},
		Type: corev1.SecretTypeOpaque,
		// data, as the app's variables are: stringData cannot be taken away.
		Data: map[string][]byte{"users": []byte(s.PasswordUsers)},
	}
}

// BuildPasswordMiddleware renders the basicAuth middleware. Nil when the app
// has no password.
//
// removeHeader takes the Authorization header off before the request reaches
// the app. The app did not ask for the password and has no business reading
// it, and an app that logs its request headers would otherwise write it into
// every log line.
func BuildPasswordMiddleware(s AppSpec) *unstructured.Unstructured {
	if s.PasswordUsers == "" {
		return nil
	}
	labels := map[string]any{}
	for key, value := range s.Labels() {
		labels[key] = value
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "Middleware",
		"metadata": map[string]any{
			"name":      PasswordMiddlewareName(s.Name),
			"namespace": s.Namespace,
			"labels":    labels,
		},
		"spec": map[string]any{
			"basicAuth": map[string]any{
				"secret":       PasswordSecretName(s.Name),
				"removeHeader": true,
				"realm":        s.Name,
			},
		},
	}}
}
