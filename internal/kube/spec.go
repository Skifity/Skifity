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

	// Workload
	Image        string
	Command      []string
	Args         []string
	Port         int
	HealthPath   string
	Replicas     int
	Revision     string // set on the pod template to force a rollout
	DeploymentID string

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
func BuildFingerprint(repoURL, commitSHA, builder, dockerfilePath, rootDir string, buildArgs map[string]string) string {
	h := sha256.New()
	fmt.Fprintf(h, "repo=%s\ncommit=%s\nbuilder=%s\ndockerfile=%s\nroot=%s\n",
		repoURL, commitSHA, builder, dockerfilePath, rootDir)
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
