package kube

// PodSecurity is how strictly an environment's pods are confined.
//
// Every environment namespace carries a Pod Security Admission label, and until
// now that label was "restricted" everywhere, with no way to change it. That is
// the right default and it is the wrong only option, because "restricted"
// refuses a container whose image starts as root — and most images do. The
// official WordPress, Nextcloud, MediaWiki and phpMyAdmin images all start as
// root and drop privileges themselves, which is a perfectly ordinary thing for
// a container to do and one that restricted has no way to permit.
//
// So the level is a property of the environment. A team that only runs what
// Skifity builds never touches it; a team that wants an off-the-shelf image
// lowers one environment and is told, in the panel, exactly what that gives up.
//
// What baseline still refuses, and it is most of what matters: privileged
// containers, host namespaces, host ports, hostPath volumes, adding capabilities
// beyond the container runtime's default set, and unsafe sysctls. An app cannot
// reach the node it runs on. What it permits is a process inside the container
// running as uid 0 of that container's own user namespace.
type PodSecurity string

const (
	// PodSecurityRestricted is the default and the strictest profile.
	PodSecurityRestricted PodSecurity = "restricted"
	// PodSecurityBaseline permits an image that starts as root.
	PodSecurityBaseline PodSecurity = "baseline"
)

// PodSecurityLevels are the levels an operator may choose, strictest first.
var PodSecurityLevels = []PodSecurity{PodSecurityRestricted, PodSecurityBaseline}

// NormalizePodSecurity reads a stored value, defaulting to the strict one.
//
// An empty string is every environment that existed before this was a setting,
// and the answer for those is the level they were already running at.
func NormalizePodSecurity(value string) PodSecurity {
	if PodSecurity(value) == PodSecurityBaseline {
		return PodSecurityBaseline
	}
	return PodSecurityRestricted
}

// ValidPodSecurity reports whether a value is one the panel offers.
//
// "privileged" is deliberately not one of them. It is a real Pod Security level
// and it is not a choice this panel puts in front of anybody: it permits host
// mounts and host networking, which is a tenant reading another tenant's disk.
func ValidPodSecurity(value string) bool {
	for _, level := range PodSecurityLevels {
		if PodSecurity(value) == level {
			return true
		}
	}
	return false
}

// AllowsRoot reports whether an image that starts as root can run at this level.
func (p PodSecurity) AllowsRoot() bool { return p == PodSecurityBaseline }

// Confinement decides one app's security context.
//
// It is a type rather than a handful of `if`s inside BuildDeployment because
// the rules are the interesting part and they need to be readable on their own
// and testable without rendering a Deployment.
type Confinement struct {
	// Level is the environment's Pod Security Admission level.
	Level PodSecurity
	// BuiltHere is true when Skifity's own builder produced the image.
	BuiltHere bool
	// Port is what the app listens on, which decides one sysctl.
	Port int
}

// RunAsUser is the uid to pin, or nil to let the image decide.
//
// Pinned only for an image Skifity built, because only then is the uid a fact
// rather than a guess: Railpack and Nixpacks both produce a process running as
// 1000. For anybody else's image the right answer is the USER the image
// declares, which is what leaving this unset means.
func (c Confinement) RunAsUser() *int64 {
	if !c.BuiltHere {
		return nil
	}
	uid := int64(1000)
	return &uid
}

// RunAsNonRoot reports whether the kubelet must refuse an image that starts as
// root, or nil to say nothing about it.
//
// Nothing is said at the baseline level for an image somebody else built, which
// is the whole point of choosing that level: the official WordPress, Nextcloud
// and MediaWiki images start as root and drop privileges themselves.
//
// An image Skifity built is held to non-root at every level. Lowering an
// environment so that a third-party image can run is not a reason to stop
// checking the one image whose contents are known.
func (c Confinement) RunAsNonRoot() *bool {
	if !c.BuiltHere && c.Level.AllowsRoot() {
		return nil
	}
	yes := true
	return &yes
}

// DropAllCapabilities reports whether the container starts with none.
//
// False for somebody else's image at the baseline level, and that is not
// laziness. An image that starts as root and drops to its own user calls
// setuid, which needs CAP_SETUID and CAP_SETGID; nginx and Apache both do
// exactly that. Dropping ALL from a root container breaks the very images the
// level exists to run, and would do it with a message about a permission
// nobody mentioned.
//
// What is left is the container runtime's default set, which is what every
// `docker run` in the world gives a container, and which the baseline Pod
// Security profile is defined in terms of: nothing may be added beyond it.
func (c Confinement) DropAllCapabilities() bool {
	return c.BuiltHere || !c.Level.AllowsRoot()
}

// UnprivilegedPortStart is the sysctl value that lets a non-root process bind a
// low port, or an empty string when it is not needed.
//
// This is the second half of why a template listening on port 80 could never
// start. With every capability dropped, a process needs CAP_NET_BIND_SERVICE to
// bind below 1024 — and it has none. Containerd, which is what k3s runs, leaves
// net.ipv4.ip_unprivileged_port_start at the kernel default of 1024, where
// Docker sets it to 0; so the same image that works under `docker run` does not
// work here, which is the worst kind of difference.
//
// Setting it to 0 is narrower than handing back CAP_NET_BIND_SERVICE: it lets
// this pod's processes bind a low port in this pod's own network namespace, and
// grants no capability to anything. It is one of the sysctls Kubernetes calls
// safe — namespaced, and unable to affect any other pod or the node — so it
// needs no kubelet configuration, and it is on the list both the baseline and
// the restricted profile allow.
func (c Confinement) UnprivilegedPortStart() string {
	if c.Port <= 0 || c.Port >= 1024 {
		return ""
	}
	if !c.DropAllCapabilities() {
		// The runtime's default set includes CAP_NET_BIND_SERVICE, so the
		// process can already bind the port and nothing needs relaxing.
		return ""
	}
	return "0"
}
