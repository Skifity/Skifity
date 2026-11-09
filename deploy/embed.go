// Package deploy holds the panel's own Kubernetes objects, with placeholders.
//
// The installer renders these files and applies them. The panel renders two
// of them itself, the routes in, when it is moved to a domain of its own from
// Settings and back again, so that the Ingress the panel writes is the one the
// installer writes and reads back on its next run: same name, same host in
// the first rule, same certificate Secret. The Go file lives here because
// //go:embed can only reach files beside it, like docs/embed.go.
package deploy

import "embed"

// Routes are the panel's Ingress over plain HTTP and with a certificate.
//
//go:embed ingress.yaml ingress-tls.yaml
var Routes embed.FS
