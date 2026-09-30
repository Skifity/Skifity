package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"skifity/internal/version"
)

// AppSpec is everything needed to render an app's Kubernetes objects.
//
// It is a plain struct with no database or cluster types in it, so manifest
// generation is a pure function: the same spec always produces the same
// objects, which is what makes the golden tests meaningful.
type AppSpec struct {
	// Identity
	Name        string // the app's slug, used as the object name
	Namespace   string
	AppID       string
	ProjectID   string
	TeamID      string
	Environment string
	DisplayName string
	// ProcessOf is the app's slug when this is one of its processes, whose
	// Name is its own Deployment's. The app reads it as SKIFITY_APP either
	// way: a worker keying a queue on its app's name must see the web's.
	ProcessOf string
	// CommitSHA is the commit the running image was built from, when there is
	// one. The app reads it as SKIFITY_COMMIT_SHA.
	CommitSHA string
	// URL is the address the app is reached at: a domain of the team's own
	// when there is one, otherwise the one the panel gave it. A preview's
	// address changes with every pull request, and an app that has to know it
	// — for a sign-in callback, a link in an email — has nowhere else to read
	// it from.
	URL string
	// Preview is true in a pull request's or branch's preview environment,
	// and PullRequest is its number when there is one.
	Preview     bool
	PullRequest int

	// Workload
	Image        string
	Command      []string
	Args         []string
	Port         int
	HealthPath   string
	Replicas     int
	Revision     string // set on the pod template to force a rollout
	DeploymentID string

	// Health checks; see health.go. Zero values are what every app had
	// before these could be chosen: the check follows HealthPath, two
	// minutes to start, three seconds to answer.
	HealthCheck          string
	HealthStartSeconds   int
	HealthTimeoutSeconds int

	// Resources, in Kubernetes units
	CPURequestM  int
	CPULimitM    int
	MemRequestMB int
	MemLimitMB   int

	// Configuration
	// EnvFromSecret is the name of the Secret holding this app's variables.
	EnvFromSecret string
	// PlainEnv holds values that are not secret and are useful to see in
	// `kubectl describe`, such as PORT.
	PlainEnv map[string]string

	// Scaling
	Autoscale    bool
	MinReplicas  int
	MaxReplicas  int
	CPUTarget    int
	MemoryTarget int
	ScaleToZero  bool

	// Storage
	Volumes []VolumeSpec

	// Routing
	Domains []DomainSpec
	// ClusterIssuer is the cert-manager issuer to request certificates from.
	ClusterIssuer string

	// Scheduling
	// SpreadAcrossServers adds a topology spread constraint so instances do not
	// all land on one server.
	SpreadAcrossServers bool

	// Confinement
	// PodSecurity is the environment's Pod Security Admission level. Empty
	// means the strict one.
	PodSecurity PodSecurity
	// Protected is true when this app has firewall rules switched on, which
	// puts the guard's middleware in front of its Ingress.
	Protected bool
	// PasswordUsers is the htpasswd line of the one account allowed through
	// the app's password, "user:bcrypt-hash". Empty means the app has none.
	PasswordUsers string
	// ImageBuiltHere is true when Skifity's own builder produced this image.
	//
	// It is the difference between knowing what is inside a container and
	// guessing. Railpack and Nixpacks both produce an image whose process runs
	// as uid 1000, so for those the panel can pin the user, drop every
	// capability and be sure the result starts. For an image somebody else
	// built — a template, or a registry reference the user typed — pinning a
	// uid is a guess, and it was wrong for 120 of the 124 catalogue images
	// whose configuration could be read from their registries.
	ImageBuiltHere bool

	// ImagePullSecret is the Secret the kubelet reads to pull this image, for
	// an app whose image lives in a registry that is not the one in the
	// cluster. Empty for everything else, which is the common case.
	ImagePullSecret string
	// TeamPullSecret holds the team's own registry credentials, when it has
	// any: a private image of theirs on ghcr.io or Docker Hub.
	TeamPullSecret string

	// Files are mounted read-only into every container the app runs, from
	// the Secret FilesSecretName names. See files.go.
	Files []FileMount

	// PublicPorts take connections that are not HTTP. See ports.go.
	PublicPorts []PublicPort
}

// Confinement is how a pod's security context is written for this app.
func (s AppSpec) Confinement() Confinement {
	return Confinement{
		Level:     NormalizePodSecurity(string(s.PodSecurity)),
		BuiltHere: s.ImageBuiltHere,
		Port:      s.Port,
	}
}

// VolumeSpec is a persistent volume attached to an app.
type VolumeSpec struct {
	Name         string
	MountPath    string
	SizeGB       int
	StorageClass string
}

// DomainSpec is a hostname routed to an app.
type DomainSpec struct {
	Hostname string
	Path     string
	TLS      bool
}

// Validate reports problems that would make Kubernetes reject the objects, with
// messages a user can act on rather than the API server's.
func (s AppSpec) Validate() error {
	if !ValidLabel(s.Name) {
		return fmt.Errorf("app name %q is not usable as a Kubernetes name", s.Name)
	}
	if !ValidLabel(s.Namespace) {
		return fmt.Errorf("namespace %q is not usable as a Kubernetes name", s.Namespace)
	}
	if s.Image == "" {
		return fmt.Errorf("app %s has no image to run", s.Name)
	}
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("port %d is out of range", s.Port)
	}
	if s.Replicas < 0 {
		return fmt.Errorf("replica count %d is negative", s.Replicas)
	}
	if err := validateHealth(s); err != nil {
		return err
	}
	if s.Autoscale {
		if s.MinReplicas < 1 {
			return fmt.Errorf("autoscaling needs a minimum of at least 1 instance")
		}
		if s.MaxReplicas < s.MinReplicas {
			return fmt.Errorf("the maximum instance count (%d) is below the minimum (%d)", s.MaxReplicas, s.MinReplicas)
		}
		if s.CPUTarget <= 0 && s.MemoryTarget <= 0 {
			return fmt.Errorf("autoscaling needs a CPU or memory target")
		}
	}
	if s.CPULimitM > 0 && s.CPULimitM < s.CPURequestM {
		return fmt.Errorf("the CPU limit (%dm) is below the reservation (%dm)", s.CPULimitM, s.CPURequestM)
	}
	if s.MemLimitMB > 0 && s.MemLimitMB < s.MemRequestMB {
		return fmt.Errorf("the memory limit (%dMB) is below the reservation (%dMB)", s.MemLimitMB, s.MemRequestMB)
	}
	seenMounts := map[string]bool{}
	for _, v := range s.Volumes {
		if !strings.HasPrefix(v.MountPath, "/") {
			return fmt.Errorf("volume %s has a relative mount path %q", v.Name, v.MountPath)
		}
		if seenMounts[v.MountPath] {
			return fmt.Errorf("two volumes are both mounted at %s", v.MountPath)
		}
		seenMounts[v.MountPath] = true
	}
	if err := validateFiles(s); err != nil {
		return err
	}
	seenPorts := map[string]bool{}
	for _, p := range s.PublicPorts {
		if err := ValidatePublicPort(p.PublicPort, p.Protocol); err != nil {
			return err
		}
		if p.Port < 1 || p.Port > 65535 {
			return fmt.Errorf("%d is not a port a container listens on", p.Port)
		}
		key := fmt.Sprintf("%d/%s", p.PublicPort, p.Protocol)
		if seenPorts[key] {
			return fmt.Errorf("the public port %s is opened twice", key)
		}
		seenPorts[key] = true
	}
	seenHosts := map[string]bool{}
	for _, d := range s.Domains {
		if !ValidHostname(d.Hostname) {
			return fmt.Errorf("%q is not a valid hostname", d.Hostname)
		}
		if seenHosts[d.Hostname] {
			return fmt.Errorf("the domain %s is listed twice", d.Hostname)
		}
		seenHosts[d.Hostname] = true
	}
	return nil
}

// Labels are put on every object an app owns.
//
// The recommended app.kubernetes.io labels are used so that other tools
// recognise the workload, plus our own for ownership and cleanup.
func (s AppSpec) Labels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       s.Name,
		"app.kubernetes.io/instance":   s.AppID,
		"app.kubernetes.io/managed-by": version.Binary,
		"app.kubernetes.io/part-of":    s.Environment,
		version.LabelKey("app-id"):     s.AppID,
		version.LabelKey("project-id"): s.ProjectID,
		version.LabelKey("team-id"):    s.TeamID,
	}
}

// SelectorLabels are the subset that identifies an app's pods.
//
// A Deployment's selector is immutable, so this must contain only labels that
// never change: adding the deployment id here would make every deploy fail.
func (s AppSpec) SelectorLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":     s.Name,
		"app.kubernetes.io/instance": s.AppID,
	}
}

// Annotations carry information that is useful to a human reading the object
// but that nothing selects on.
func (s AppSpec) Annotations() map[string]string {
	out := map[string]string{
		version.LabelKey("display-name"): s.DisplayName,
	}
	if s.DeploymentID != "" {
		out[version.LabelKey("deployment-id")] = s.DeploymentID
	}
	if s.Revision != "" {
		out[version.LabelKey("revision")] = s.Revision
	}
	return out
}

// ReplicasAreSomebodyElses reports whether an autoscaler owns the replica
// count, so the panel must not write one.
//
// This is the difference between autoscaling that works and autoscaling that
// looks like it works. The Deployment is applied with server-side apply and
// Force, so every field the panel sends is reasserted on every apply — and an
// apply happens on a deploy, a rollback, a variable change, a domain change and
// a scaling change. Sending `replicas` while an autoscaler also manages it
// means each of those knocks the app straight back down to the floor: an app
// the HPA had taken to six instances under load collapses to one the moment
// somebody edits a variable, and then climbs back over the next few minutes.
// With scale to zero it is the mirror image — a sleeping app is forced awake
// and billed for it.
//
// Omitting the field instead is what Kubernetes documents for this exact case.
// The autoscaler becomes the field's only owner and the panel stops arguing
// with it.
func (s AppSpec) ReplicasAreSomebodyElses() bool {
	return s.Autoscale || ScaleToZeroEnabled(s)
}

// DesiredReplicas is the replica count the app is configured for.
//
// It is what the Deployment is written with when nothing else owns that field,
// and the number the rest of the panel reasons about — a disruption budget, for
// one, which is worth having as soon as more than one instance is wanted.
func (s AppSpec) DesiredReplicas() int32 {
	if s.Autoscale {
		return int32(max(s.MinReplicas, 1))
	}
	if s.Replicas < 0 {
		return 0
	}
	return int32(s.Replicas)
}

// EnvHash is a fingerprint of the configuration that a pod reads at startup.
//
// Putting it in the pod template's annotations is what makes a variable change
// actually restart the pods: Kubernetes does not watch a Secret's contents, so
// without this a rollout would be a no-op and the new value would only appear
// after an unrelated restart.
func EnvHash(values map[string]string) string {
	keys := slices.Sorted(maps.Keys(values))
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\n", k, values[k])
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// BuildFingerprint identifies the inputs that decide what an image contains.
//
// This is the mechanism behind ADR-0007: two deploys with the same fingerprint
// can share an image, so changing an environment variable or a replica count
// never triggers a rebuild.
func BuildFingerprint(repoURL, commitSHA, builder, dockerfilePath, rootDir, buildCommand, staticDir string, buildArgs map[string]string) string {
	h := sha256.New()
	fmt.Fprintf(h, "repo=%s\ncommit=%s\nbuilder=%s\ndockerfile=%s\nroot=%s\n",
		repoURL, commitSHA, builder, dockerfilePath, rootDir)
	// A static site's build command and the folder it serves go into the
	// image as surely as the commit does: changing either and deploying used
	// to find the old image by its fingerprint and run it. Written only when
	// set, so every fingerprint made before stays what it was and nothing is
	// rebuilt for having been upgraded.
	if buildCommand != "" {
		fmt.Fprintf(h, "build=%s\n", buildCommand)
	}
	if staticDir != "" {
		fmt.Fprintf(h, "static=%s\n", staticDir)
	}
	keys := make([]string, 0, len(buildArgs))
	for k := range buildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "arg:%s=%s\n", k, buildArgs[k])
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
