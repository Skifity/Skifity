// Package manifests renders the panel's own Kubernetes objects.
//
// The files in deploy/ carry __PLACEHOLDER__ values that the installer fills
// in, and the panel fills in for its own route (see PanelRoute). Rendering
// them goes through here so that one rule decides what a placeholder looks
// like, and so that a manifest applied with a placeholder still in it is an
// error rather than a Deployment that pulls an image called "__IMAGE__".
package manifests

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"skifity/deploy"
)

// placeholderPattern matches __NAME__, which is the only substitution the
// manifests use. Anything else in the files is literal YAML.
var placeholderPattern = regexp.MustCompile(`__[A-Z0-9_]+__`)

// Placeholders lists the distinct placeholder names a manifest uses, sorted.
//
// The names are returned without their underscores, so __IMAGE__ comes back as
// "IMAGE".
func Placeholders(source string) []string {
	seen := map[string]bool{}
	for _, match := range placeholderPattern.FindAllString(source, -1) {
		seen[strings.Trim(match, "_")] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Render substitutes every placeholder and reports any that had no value.
//
// An empty value is allowed: an ACME account without an email address is a
// legitimate choice. A missing key is not.
func Render(source string, values map[string]string) (string, error) {
	var missing []string
	out := placeholderPattern.ReplaceAllStringFunc(source, func(match string) string {
		name := strings.Trim(match, "_")
		value, ok := values[name]
		if !ok {
			missing = append(missing, name)
			return match
		}
		return value
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("no value for %s", strings.Join(unique(missing), ", "))
	}
	return out, nil
}

func unique(values []string) []string {
	out := values[:0:0]
	var last string
	for i, value := range values {
		if i == 0 || value != last {
			out = append(out, value)
		}
		last = value
	}
	return out
}

// The panel's own route in, as the panel writes it.
//
// The installer applies ingress.yaml or ingress-tls.yaml and, on its next run,
// reads back the Ingress called skifity-panel to keep the panel's address:
// the host of its first rule, and whether it has a certificate. Moving the
// panel to a domain of its own from Settings writes that same Ingress from the
// same file, so the installer reads the panel's choice and keeps it, rather
// than putting the panel back on sslip.io the next time it runs.

// PanelRouteName is the panel's Ingress, which the installer reads back.
const PanelRouteName = "skifity-panel"

// PanelCertificateSecret is where the certificate of the panel's own domain is
// kept: the Secret ingress-tls.yaml names, and the cert-manager Certificate
// made for it, which has the same name.
const PanelCertificateSecret = "skifity-panel-tls"

// PanelHost is a host the panel's route answers on beside its own domain: an
// address it answered on before, kept so that a webhook registered there
// still arrives.
type PanelHost struct {
	Host string
	// Secret is the Secret its certificate is kept in. Empty serves it over
	// plain HTTP, which is what an sslip.io address always was.
	Secret string
}

// PanelRoute renders the panel's Ingress for a domain of its own:
// ingress-tls.yaml, with host in the first rule and in the certificate, and a
// rule after it for each of also, in order.
func PanelRoute(namespace, host, issuer string, also ...PanelHost) (map[string]any, error) {
	route, err := renderRoute("ingress-tls.yaml", map[string]string{
		"NAMESPACE": namespace, "HOST": host, "ISSUER": issuer,
	})
	if err != nil {
		return nil, err
	}
	spec, _ := route["spec"].(map[string]any)
	rules, _ := spec["rules"].([]any)
	if len(rules) != 1 {
		return nil, fmt.Errorf("ingress-tls.yaml has %d rules, not the one the panel's own domain goes in", len(rules))
	}
	tls, _ := spec["tls"].([]any)
	for _, extra := range also {
		if extra.Host == "" || extra.Host == host {
			continue
		}
		rule, err := deepCopy(rules[0])
		if err != nil {
			return nil, err
		}
		rule["host"] = extra.Host
		rules = append(rules, rule)
		if extra.Secret != "" {
			tls = append(tls, map[string]any{"hosts": []any{extra.Host}, "secretName": extra.Secret})
		}
	}
	spec["rules"], spec["tls"] = rules, tls
	return route, nil
}

// PlainPanelRoute renders the panel's Ingress over plain HTTP at one host:
// ingress.yaml, which is what the installer applies with no domain.
func PlainPanelRoute(namespace, host string) (map[string]any, error) {
	return renderRoute("ingress.yaml", map[string]string{"NAMESPACE": namespace, "HOST": host})
}

// FreeAddress reports whether a host is an IP address or an sslip.io name,
// which the installer makes from one: a host no certificate is asked for,
// because every Skifity install shares Let's Encrypt's limits for sslip.io
// (ADR-0015).
func FreeAddress(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == "sslip.io" || strings.HasSuffix(host, ".sslip.io") || net.ParseIP(strings.Trim(host, "[]")) != nil
}

// PreviousCertificateSecret names the Secret for the certificate of a domain
// the panel answered on before. Not PanelCertificateSecret, which the new
// domain's certificate takes over; and one per host, so a host that is let go
// of takes nothing else with it.
func PreviousCertificateSecret(host string) string {
	name := PanelCertificateSecret + "-" + strings.ReplaceAll(strings.ToLower(host), ".", "-")
	if len(name) <= 63 {
		return name
	}
	sum := sha256.Sum256([]byte(host))
	return strings.TrimRight(name[:54], "-") + "-" + hex.EncodeToString(sum[:4])
}

// renderRoute renders one of the panel's route files and checks it is the
// Ingress the installer reads back.
func renderRoute(file string, values map[string]string) (map[string]any, error) {
	source, err := deploy.Routes.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	rendered, err := Render(string(source), values)
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", file, err)
	}
	var route map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &route); err != nil {
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}
	metadata, _ := route["metadata"].(map[string]any)
	if route["kind"] != "Ingress" || metadata["name"] != PanelRouteName {
		return nil, fmt.Errorf("%s is not the Ingress called %s", file, PanelRouteName)
	}
	return route, nil
}

// deepCopy copies a piece of a parsed manifest, so that a rule copied for a
// second host does not share its paths with the first.
func deepCopy(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}
