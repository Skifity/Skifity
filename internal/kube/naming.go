// Package kube holds everything that talks to, or names things in, Kubernetes.
package kube

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Kubernetes name rules this package enforces:
//
//   - RFC 1123 label: lowercase letters, digits and '-', starting and ending
//     with an alphanumeric, at most 63 characters. Used for most object names.
//   - RFC 1123 subdomain: the same, plus '.', at most 253 characters.
//
// Users type free text ("Tegar's Shop!"), so every name goes through Slugify,
// and anything that could still collide or overflow is suffixed with a short
// hash of the original.

const maxLabelLength = 63

var (
	nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)
	trimDashes      = regexp.MustCompile(`^-+|-+$`)
	validLabel      = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	validHostname   = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
)

// Slugify turns arbitrary text into a valid Kubernetes name.
//
// Non-ASCII input is common (a Russian or Hindi project name), and stripping it
// would leave an empty string, so a hash suffix keeps the result both valid and
// unique.
func Slugify(input string) string {
	lowered := strings.ToLower(strings.TrimSpace(input))
	// Transliterate the handful of separators people actually type before
	// discarding everything else.
	lowered = strings.NewReplacer("_", "-", " ", "-", ".", "-", "/", "-", "@", "-at-", "&", "-and-").Replace(lowered)

	slug := nonAlphanumeric.ReplaceAllString(lowered, "-")
	slug = trimDashes.ReplaceAllString(slug, "")

	if slug == "" {
		// The name was entirely non-Latin or punctuation. Derive something
		// stable from the original so the same input always maps to the same
		// slug.
		return "x-" + shortHash(input, 8)
	}
	if len(slug) > maxLabelLength {
		// Truncating alone risks two long names colliding, so keep room for a
		// hash of the full original.
		slug = strings.TrimRight(slug[:maxLabelLength-9], "-") + "-" + shortHash(input, 8)
	}
	if !unicode.IsLetter(rune(slug[0])) && !unicode.IsDigit(rune(slug[0])) {
		slug = "x" + slug
	}
	return slug
}

// ValidLabel reports whether a name is already a valid Kubernetes name.
func ValidLabel(name string) bool {
	return name != "" && len(name) <= maxLabelLength && validLabel.MatchString(name)
}

// ValidHostname reports whether a string is a usable DNS hostname.
func ValidHostname(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" || len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) > maxLabelLength {
			return false
		}
	}
	return validHostname.MatchString(host)
}

// NamespaceFor builds the namespace for an environment.
//
// The shape is <team>-<project>-<environment>, truncated with a hash when the
// pieces are long, which keeps namespaces readable in `kubectl get ns` while
// staying unique.
func NamespaceFor(teamSlug, projectSlug, envSlug string) string {
	full := fmt.Sprintf("%s-%s-%s", teamSlug, projectSlug, envSlug)
	if len(full) <= maxLabelLength && ValidLabel(full) {
		return full
	}
	// Keep the environment visible, since that is what distinguishes namespaces
	// a person is looking at side by side.
	suffix := "-" + envSlug + "-" + shortHash(full, 6)
	room := maxLabelLength - len(suffix)
	if room < 1 {
		return "ns-" + shortHash(full, 16)
	}
	head := strings.TrimRight(full[:min(room, len(full))], "-")
	return head + suffix
}

// ResourceName builds a Kubernetes object name for an app and a suffix, such as
// the name of its Service or its HorizontalPodAutoscaler.
func ResourceName(appSlug, suffix string) string {
	if suffix == "" {
		return appSlug
	}
	name := appSlug + "-" + suffix
	if len(name) <= maxLabelLength {
		return name
	}
	keep := maxLabelLength - len(suffix) - 8
	if keep < 1 {
		return shortHash(name, 16) + "-" + suffix
	}
	return strings.TrimRight(appSlug[:keep], "-") + "-" + shortHash(appSlug, 6) + "-" + suffix
}

// shortHash returns n lowercase base32 characters derived from the input. Base32
// avoids the digits and letters that look alike in a terminal font.
func shortHash(input string, n int) string {
	sum := sha256.Sum256([]byte(input))
	encoded := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
	if n > len(encoded) {
		n = len(encoded)
	}
	return encoded[:n]
}

// DefaultAppPort is where an app is expected to listen when nothing says
// otherwise.
//
// This is not a guess. The panel sets PORT in the container's environment, so
// the number here is an instruction the app is told to follow, the same
// contract every buildpack in the world already implements. The alternative is
// a port of zero, which renders no Service, no Ingress and no URL: an app that
// deployed successfully and cannot be reached.
const DefaultAppPort = 8080

// AutoHostname builds the automatic subdomain for an app.
//
// With a wildcard domain configured it is <app>-<env>.<wildcard>. Without one it
// falls back to a magic DNS service so that a fresh install still gives every app
// a working URL, which is the difference between "it works" and "now go buy a
// domain" on the first deploy.
//
// discriminator is appended to the label when the plain name is already taken
// by another app, and is empty the rest of the time. Two projects both calling
// an app "web" want the same address, which is not a rare case but the usual
// one — "web", "api" and "app" are what people call things. The caller asks who
// holds the plain name and passes a short, stable string derived from the app's
// id when somebody else does, so the second app gets an address of its own
// rather than none at all.
func AutoHostname(appSlug, envSlug, wildcardDomain, clusterIP string) string {
	return autoHostname(appSlug, envSlug, wildcardDomain, clusterIP, "")
}

// AutoHostnameFor is AutoHostname with a discriminator for an app whose
// preferred name is taken. It is derived from the app id, so it is the same on
// every deploy: an address that changed under the user would be worse than a
// shared one.
func AutoHostnameFor(appSlug, envSlug, wildcardDomain, clusterIP, appID string) string {
	return autoHostname(appSlug, envSlug, wildcardDomain, clusterIP, shortHash(appID, 5))
}

func autoHostname(appSlug, envSlug, wildcardDomain, clusterIP, discriminator string) string {
	label := appSlug
	if envSlug != "" && envSlug != "production" {
		label = appSlug + "-" + envSlug
	}
	if discriminator != "" {
		label += "-" + discriminator
	}
	if len(label) > maxLabelLength {
		label = strings.TrimRight(label[:maxLabelLength-7], "-") + "-" + shortHash(label, 6)
	}
	if wildcardDomain != "" {
		return label + "." + strings.TrimPrefix(strings.TrimSpace(wildcardDomain), "*.")
	}
	if clusterIP == "" {
		return ""
	}
	// sslip.io resolves <anything>.<ip-with-dashes>.sslip.io to that IP, with no
	// account and no configuration.
	return label + "." + strings.ReplaceAll(clusterIP, ".", "-") + ".sslip.io"
}

// SanitiseEnvKey reports whether a string is usable as an environment variable
// name, which is stricter than it looks: a shell will not export `MY-VAR`.
func SanitiseEnvKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("a variable name cannot be empty")
	}
	if len(key) > 256 {
		return "", fmt.Errorf("a variable name cannot be longer than 256 characters")
	}
	for i, r := range key {
		switch {
		case r >= 'A' && r <= 'Z', r == '_':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return "", fmt.Errorf("a variable name cannot start with a digit: %q", key)
			}
		default:
			return "", fmt.Errorf("a variable name can only contain letters, digits and underscores: %q", key)
		}
	}
	return key, nil
}

// The in-cluster registry.
//
// Built images are pushed here and pulled from here by every node's container
// runtime. The two halves of that sentence do not see the same network, which
// is why the addresses below exist in this shape.
const (
	// RegistryService is the registry's Service name, in BuildsNamespace.
	RegistryService = "skifity-registry"
	// RegistryPort is what the registry listens on inside the cluster.
	RegistryPort = 5000
	// RegistryNodePort is how a node's container runtime reaches it.
	//
	// containerd runs on the host, not in the cluster: it cannot resolve a
	// Service name, and it refuses plain HTTP to anything that is not
	// localhost. A fixed NodePort is reachable on 127.0.0.1 from every node,
	// which is both resolvable and, to containerd, trusted.
	RegistryNodePort = 30500
)

// RegistryHost is the address images are tagged with.
func RegistryHost() string {
	return fmt.Sprintf("%s.%s.svc.cluster.local:%d", RegistryService, BuildsNamespace, RegistryPort)
}

// RegistriesYAML is the containerd mirror configuration every node needs.
//
// Without it a node cannot pull the images the panel builds: it would try to
// resolve a Kubernetes Service name against the host's resolver, which does
// not know it, and then refuse the plain HTTP it would have found. The mirror
// sends that name to the registry's NodePort on this machine instead.
//
// It is written before k3s starts, and never changes: nothing in it depends on
// an address that is only known once the cluster is running.
func RegistriesYAML() string {
	return fmt.Sprintf(`# Written by Skifity. Do not edit.
#
# Images the panel builds are tagged with the in-cluster registry's Service
# name. containerd runs on the host and cannot resolve that name, so it is
# mirrored to the registry's NodePort on this machine, which containerd
# reaches over loopback and trusts without TLS.
mirrors:
  "%s":
    endpoint:
      - "http://127.0.0.1:%d"
`, RegistryHost(), RegistryNodePort)
}
