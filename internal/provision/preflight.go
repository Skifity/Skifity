// Package provision turns a bare VPS into a cluster node and back.
package provision

import (
	"strconv"
	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/settings"
)

// Preflight is what the panel learned about a server before touching it.
type Preflight struct {
	OSName    string `json:"os_name"`
	OSVersion string `json:"os_version"`
	OSID      string `json:"os_id"`
	Kernel    string `json:"kernel"`
	Arch      string `json:"arch"`
	CPUCores  int    `json:"cpu_cores"`
	MemoryMB  int    `json:"memory_mb"`
	DiskGB    int    `json:"disk_gb"`
	// PublicIP is what the server believes its own address is.
	PublicIP  string `json:"public_ip"`
	PrivateIP string `json:"private_ip"`
	// Conflicts are things already installed that would get in the way.
	Conflicts []string `json:"conflicts"`
	// HasWireGuard reports whether the kernel can do encrypted pod networking.
	HasWireGuard bool `json:"has_wireguard"`
	// HasSystemd is required: k3s installs as a systemd unit.
	HasSystemd bool `json:"has_systemd"`
	// MemoryCgroup is "yes", "no" or "unknown". The kubelet will not start
	// without the memory controller, and it says so in terms of cgroups rather
	// than in terms of the one line somebody has to change.
	MemoryCgroup string `json:"memory_cgroup"`
	// PortsInUse are the cluster ports something else is already listening on.
	PortsInUse []int `json:"ports_in_use"`
	// UDPPortsInUse are the pod network's UDP ports something else holds.
	//
	// Separate from PortsInUse because a TCP listener on 6443 and a UDP
	// listener on 51820 are different problems with different fixes, and
	// because the TCP scan cannot see a UDP socket at all — which is why this
	// went unnoticed: `ss -lnt` lists no UDP and the pod network's ports are
	// all UDP.
	UDPPortsInUse []int `json:"udp_ports_in_use"`
	// K3sInstalled reports whether this machine is already a node, which makes
	// adding it again a no-op rather than a failure.
	K3sInstalled bool `json:"k3s_installed"`
	// Warnings are things that work but are worth saying.
	Warnings []string `json:"warnings"`
}

// Requirements are what a server must meet to join this cluster.
type Requirements struct {
	MinMemoryMB int
	MinDiskGB   int
	MinCPUCores int
	// FlannelBackend is the pod network this cluster already uses. A server
	// cannot join with a different one, so a kernel that cannot run the
	// cluster's backend is a reason to stop rather than a warning. Empty means
	// the default, which is WireGuard.
	FlannelBackend string
}

// DefaultRequirements is what the panel enforces.
//
// 1 GB is the real floor for a k3s agent plus a small app: below that the
// kubelet starts evicting as soon as anything runs. The control plane node
// needs more, which is checked separately.
func DefaultRequirements() Requirements {
	return Requirements{MinMemoryMB: 900, MinDiskGB: 8, MinCPUCores: 1, FlannelBackend: settings.FlannelWireGuard}
}

// ControlPlaneRequirements are stricter, because etcd and the API server live
// there too.
func ControlPlaneRequirements() Requirements {
	return Requirements{MinMemoryMB: 1800, MinDiskGB: 20, MinCPUCores: 2, FlannelBackend: settings.FlannelWireGuard}
}

// Problem is one reason a server cannot join, with the fix.
type Problem struct {
	// Code identifies this exact problem, and is what the panel looks a
	// translation up by. Check groups several of them — three problems are
	// about the operating system and three about a UDP port — so it is not
	// enough on its own.
	Code   string `json:"code"`
	Check  string `json:"check"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
	// Args are the values interpolated into Detail and Fix, so a translated
	// sentence carries the same ones. See errdoc.Sprintf.
	Args ProblemArgs `json:"args,omitzero"`
	// Fatal problems stop the process; others are warnings.
	Fatal bool `json:"fatal"`
}

// ProblemArgs carries one preflight problem's interpolated values, in the
// order the Go format string used them.
type ProblemArgs struct {
	Detail []string `json:"detail,omitempty"`
	Fix    []string `json:"fix,omitempty"`
}

// The networks k3s gives pods and services.
//
// They live in internal/kube because two things need them and neither may
// import the other: the server firewall here, which has to trust them wholesale
// because traffic between pods is not on a fixed port and cannot be enumerated,
// and the request firewall, which has to know which addresses are its own
// proxies rather than a visitor.
const (
	PodCIDR     = kube.PodCIDR
	ServiceCIDR = kube.ServiceCIDR
)

// ClusterPorts are the ports cluster members must reach on each other.
var ClusterPorts = []struct {
	Port     int
	Protocol string
	Purpose  string
	// ControlPlaneOnly ports are needed only on servers running etcd.
	ControlPlaneOnly bool
}{
	{Port: 6443, Protocol: "tcp", Purpose: "Kubernetes API"},
	{Port: 10250, Protocol: "tcp", Purpose: "kubelet metrics and logs"},
	{Port: 8472, Protocol: "udp", Purpose: "pod network (vxlan)"},
	{Port: 51820, Protocol: "udp", Purpose: "pod network (WireGuard IPv4)"},
	{Port: 51821, Protocol: "udp", Purpose: "pod network (WireGuard IPv6)"},
	{Port: 2379, Protocol: "tcp", Purpose: "etcd client", ControlPlaneOnly: true},
	{Port: 2380, Protocol: "tcp", Purpose: "etcd peers", ControlPlaneOnly: true},
}

// supportedDistros are the ones the installer is tested on. Others are allowed
// with a warning rather than refused: k3s runs on far more than we can test.
var supportedDistros = map[string]bool{
	"ubuntu": true,
	"debian": true,
}

// knownWorkingDistros run k3s fine but are not covered by our smoke tests.
//
// Every one of them ships systemd, which is not a detail: the check below makes
// its absence fatal, because Skifity installs k3s as a unit and then manages it
// with systemctl. Alpine used to be on this list and does not ship systemd, so
// the file said "runs k3s fine" and refused it four lines later. k3s itself
// supports OpenRC; Skifity does not, and saying so is better than listing a
// distribution nobody could actually use.
var knownWorkingDistros = map[string]bool{
	"almalinux": true,
	"rocky":     true,
	"fedora":    true,
	"centos":    true,
	"rhel":      true,
	"opensuse":  true,
	"sles":      true,
	"arch":      true,
}

// Evaluate checks a preflight report against the requirements.
func Evaluate(p Preflight, req Requirements, controlPlane bool) []Problem {
	var problems []Problem

	if !p.HasSystemd {
		problems = append(problems, Problem{
			Code:   "systemd",
			Check:  "systemd",
			Detail: "This server does not appear to use systemd.",
			Fix:    "Skifity installs Kubernetes as a systemd service and manages it with systemctl. Use a distribution with systemd, such as Ubuntu 24.04 or Debian 12. Alpine and anything else on OpenRC will not work, and neither will a container without an init system — an unprivileged LXC or a Docker container, for instance.",
			Fatal:  true,
		})
	}

	// The kubelet refuses to start without the memory controller. Raspberry Pi
	// OS ships with it off, and Skifity builds for arm64, so this is a real
	// path rather than a theoretical one — and the error k3s gives is about
	// cgroups, which is not the thing anybody would think to change.
	if p.MemoryCgroup == "no" {
		problems = append(problems, Problem{
			Code:   "cgroups",
			Check:  "cgroups",
			Detail: "The memory cgroup controller is switched off on this server.",
			Fix:    "The kubelet cannot start without it. On Raspberry Pi OS and Ubuntu for the Pi, add `cgroup_memory=1 cgroup_enable=memory` to the end of the single line in /boot/firmware/cmdline.txt and reboot. On other systems, check the kernel command line for `cgroup_disable=memory`.",
			Fatal:  true,
		})
	}

	switch {
	case supportedDistros[p.OSID]:
	case knownWorkingDistros[p.OSID]:
		detail, args := errdoc.Sprintf("%s %s is not one of the distributions Skifity is tested on.", p.OSName, p.OSVersion)
		problems = append(problems, Problem{
			Code:   "os_untested",
			Check:  "os",
			Detail: detail,
			Fix:    "It should work, but if you hit trouble, Ubuntu 24.04 and Debian 12 are the tested options.",
			Args:   ProblemArgs{Detail: args},
		})
	case p.OSID == "":
		problems = append(problems, Problem{
			Code:   "os_unreadable",
			Check:  "os",
			Detail: "Skifity could not work out which distribution this server runs.",
			Fix:    "Check that /etc/os-release exists and is readable. Ubuntu 24.04 and Debian 12 are the tested options.",
		})
	default:
		detail, args := errdoc.Sprintf("%s is not a distribution Skifity recognises.", p.OSName)
		problems = append(problems, Problem{
			Code:   "os_unsupported",
			Check:  "os",
			Detail: detail,
			Fix:    "Use Ubuntu 24.04 or Debian 12. Other systemd distributions usually work, but are not tested.",
			Args:   ProblemArgs{Detail: args},
		})
	}

	switch p.Arch {
	case "x86_64", "amd64", "aarch64", "arm64":
	case "":
		problems = append(problems, Problem{
			Code: "arch_unknown", Check: "arch",
			Detail: "The server's CPU architecture could not be determined.",
			Fix:    "Check that `uname -m` works over SSH.", Fatal: true,
		})
	default:
		detail, args := errdoc.Sprintf("This server is %s, which Skifity does not build for.", p.Arch)
		problems = append(problems, Problem{
			Code:   "arch_unsupported",
			Check:  "arch",
			Detail: detail,
			Fix:    "Use a 64-bit x86 (amd64) or ARM (arm64) server.",
			Args:   ProblemArgs{Detail: args},
			Fatal:  true,
		})
	}

	if p.MemoryMB > 0 && p.MemoryMB < req.MinMemoryMB {
		// Two codes rather than one with the role spliced into it. "a worker"
		// and "a control plane server" were English phrases dropped into the
		// middle of the sentence, and no locale can put a noun phrase from
		// another language where its own grammar needs one.
		code := "memory_worker"
		format := "This server has %d MB of memory; a worker needs at least %d MB."
		if controlPlane {
			code = "memory_control_plane"
			format = "This server has %d MB of memory; a control plane server needs at least %d MB."
		}
		detail, args := errdoc.Sprintf(format, p.MemoryMB, req.MinMemoryMB)
		problems = append(problems, Problem{
			Code:   code,
			Check:  "memory",
			Detail: detail,
			Fix:    "Resize the server, or add it as a worker rather than a control plane server.",
			Args:   ProblemArgs{Detail: args},
			Fatal:  true,
		})
	}
	if p.DiskGB > 0 && p.DiskGB < req.MinDiskGB {
		detail, args := errdoc.Sprintf("This server has %d GB of free disk; at least %d GB is needed.", p.DiskGB, req.MinDiskGB)
		problems = append(problems, Problem{
			Code:   "disk",
			Check:  "disk",
			Detail: detail,
			Fix:    "Free up space or resize the disk. Container images alone usually need several gigabytes.",
			Args:   ProblemArgs{Detail: args},
			Fatal:  true,
		})
	}
	if p.CPUCores > 0 && p.CPUCores < req.MinCPUCores {
		detail, args := errdoc.Sprintf("This server has %d CPU core(s); at least %d is needed.", p.CPUCores, req.MinCPUCores)
		problems = append(problems, Problem{
			Code:   "cpu",
			Check:  "cpu",
			Detail: detail,
			Fix:    "Resize the server to one with more cores.",
			Args:   ProblemArgs{Detail: args},
			Fatal:  true,
		})
	}

	for _, conflict := range p.Conflicts {
		switch conflict {
		case "docker":
			problems = append(problems, Problem{
				Code:   "conflict_docker",
				Check:  "conflict",
				Detail: "Docker is already running on this server.",
				Fix:    "Docker and k3s can coexist, but they compete for memory and both manage iptables rules. On a small server, remove Docker first with: systemctl disable --now docker",
			})
		case "nginx", "apache2", "caddy":
			detail, detailArgs := errdoc.Sprintf("%s is listening on this server.", conflict)
			fix, fixArgs := errdoc.Sprintf("It is using ports 80 and 443, which the cluster's ingress needs. Stop it with: systemctl disable --now %s", conflict)
			problems = append(problems, Problem{
				Code:   "conflict_web_server",
				Check:  "conflict",
				Detail: detail,
				Fix:    fix,
				Args:   ProblemArgs{Detail: detailArgs, Fix: fixArgs},
				Fatal:  true,
			})
		case "k3s", "k8s":
			// Not a problem: this is how re-adding a server works.
		default:
			detail, args := errdoc.Sprintf("%s is running on this server.", conflict)
			problems = append(problems, Problem{
				Code:   "conflict_other",
				Check:  "conflict",
				Detail: detail,
				Fix:    "Check that it does not use ports 80, 443 or 6443.",
				Args:   ProblemArgs{Detail: args},
			})
		}
	}

	for _, port := range p.PortsInUse {
		detail, detailArgs := errdoc.Sprintf("Something is already listening on port %d.", port)
		fix, fixArgs := errdoc.Sprintf("Find it with: ss -lntup 'sport = :%d' and stop it, or use a server with that port free.", port)
		problems = append(problems, Problem{
			Code:   "port",
			Check:  "port",
			Detail: detail,
			Fix:    fix,
			Args:   ProblemArgs{Detail: detailArgs, Fix: fixArgs},
			Fatal:  port == 80 || port == 443 || port == 6443,
		})
	}

	// A UDP port the pod network needs, held by something else.
	//
	// Fatal for the backend this cluster is actually going to use, and a
	// warning for the other one. 51820 is the one that bites: a VPS running a
	// WireGuard VPN of its own holds exactly the port flannel's
	// wireguard-native backend wants, and the failure is a cluster that comes
	// up with every node Ready and no traffic between pods — which looks like
	// anything except a port conflict.
	for _, port := range p.UDPPortsInUse {
		wireGuardPort := port == 51820 || port == 51821
		used := wireGuardPort == wireGuardRequired(req)
		problem := Problem{Check: "udp_port", Fatal: used}
		problem.Detail, problem.Args.Detail = errdoc.Sprintf(
			"Something is already using UDP port %d, which the pod network needs.", port)
		switch {
		case wireGuardPort && used:
			problem.Code = "udp_port_wireguard"
			problem.Fix, problem.Args.Fix = errdoc.Sprintf(
				"That is almost always a WireGuard VPN of your own. Move it to another port, "+
					"or set the pod network to %q under Settings, then Cluster — every server in "+
					"the cluster must then use that one. Find the holder with: ss -lnup 'sport = :%d'",
				settings.FlannelVXLAN, port)
		case used:
			problem.Code = "udp_port_needed"
			problem.Fix, problem.Args.Fix = errdoc.Sprintf(
				"Find it with: ss -lnup 'sport = :%d' and stop it, or use a server with that port free.", port)
		default:
			problem.Code = "udp_port_unused"
			problem.Detail, problem.Args.Detail = errdoc.Sprintf(
				"Something is using UDP port %d. This cluster's pod network does not use it, so it is only a problem if you ever change that setting.", port)
			problem.Fix, problem.Args.Fix = errdoc.Sprintf(
				"Nothing to do now. Find the holder with: ss -lnup 'sport = :%d'", port)
		}
		problems = append(problems, problem)
	}

	// There is no fallback here, and there used to be a message promising one.
	// Every node in a cluster has to use the same pod network: a node that
	// joins with vxlan while the others run wireguard-native joins without
	// complaint and then never exchanges a packet with them. So when this
	// cluster runs WireGuard, a kernel without the module stops the server
	// being added, and the two fixes are the two that actually work.
	if !p.HasWireGuard && wireGuardRequired(req) {
		problems = append(problems, Problem{
			Code:   "wireguard",
			Check:  "wireguard",
			Detail: "This kernel does not have the WireGuard module, and this cluster encrypts traffic between servers with WireGuard.",
			Fix:    "Install it on this server: apt-get install -y wireguard-tools (then reboot if the module still does not load). If the kernel cannot have it at all, switch the whole cluster to vxlan in Settings -> Cluster -> Pod network, which turns that encryption off for every server.",
			Fatal:  true,
		})
	}

	if p.PublicIP == "" {
		problems = append(problems, Problem{
			Code:   "network",
			Check:  "network",
			Detail: "Skifity could not determine this server's public address.",
			Fix:    "Servers at different providers must reach each other by public address. Check that the server has one, and that outbound connections work.",
		})
	}

	return problems
}

// wireGuardRequired reports whether this cluster's pod network needs the
// WireGuard kernel module.
func wireGuardRequired(req Requirements) bool {
	return req.FlannelBackend == "" || req.FlannelBackend == settings.FlannelWireGuard
}

// Fatal reports whether any problem stops the server from being added.
func Fatal(problems []Problem) bool {
	for _, p := range problems {
		if p.Fatal {
			return true
		}
	}
	return false
}

// ParsePreflight reads the output of the preflight script.
//
// The script writes `key=value` lines, which keeps the parsing trivial and the
// script readable. Anything it could not determine is simply absent, and the
// zero value means "unknown" rather than "zero".
func ParsePreflight(output string) Preflight {
	var p Preflight
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)

		switch key {
		case "os_name":
			p.OSName = value
		case "os_version":
			p.OSVersion = value
		case "os_id":
			p.OSID = strings.ToLower(value)
		case "kernel":
			p.Kernel = value
		case "arch":
			p.Arch = value
		case "cpu_cores":
			p.CPUCores = atoi(value)
		case "memory_mb":
			p.MemoryMB = atoi(value)
		case "disk_gb":
			p.DiskGB = atoi(value)
		case "public_ip":
			p.PublicIP = value
		case "private_ip":
			p.PrivateIP = value
		case "has_wireguard":
			p.HasWireGuard = value == "yes"
		case "has_systemd":
			p.HasSystemd = value == "yes"
		case "has_memory_cgroup":
			p.MemoryCgroup = value
		case "k3s_installed":
			p.K3sInstalled = value == "yes"
		case "conflicts":
			for _, c := range strings.Fields(strings.ReplaceAll(value, ",", " ")) {
				if c != "" {
					p.Conflicts = append(p.Conflicts, strings.ToLower(c))
				}
			}
		case "ports_in_use":
			for _, portText := range strings.Fields(strings.ReplaceAll(value, ",", " ")) {
				if port := atoi(portText); port > 0 {
					p.PortsInUse = append(p.PortsInUse, port)
				}
			}
		case "udp_ports_in_use":
			for _, portText := range strings.Fields(strings.ReplaceAll(value, ",", " ")) {
				if port := atoi(portText); port > 0 {
					p.UDPPortsInUse = append(p.UDPPortsInUse, port)
				}
			}
		}
	}
	return p
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
