package provision

import (
	"strings"
	"testing"

	"skifity/internal/settings"
)

// realPreflightOutput is what the script prints on a healthy Ubuntu server.
const realPreflightOutput = `os_name=Ubuntu
os_version=24.04
os_id=ubuntu
kernel=6.8.0-45-generic
arch=x86_64
has_systemd=yes
has_memory_cgroup=yes
cpu_cores=2
memory_mb=3936
disk_gb=38
private_ip=10.0.0.5
public_ip=203.0.113.10
has_wireguard=yes
k3s_installed=no
conflicts=
ports_in_use=
`

func TestParsePreflight(t *testing.T) {
	p := ParsePreflight(realPreflightOutput)

	if p.OSID != "ubuntu" || p.OSVersion != "24.04" {
		t.Fatalf("distribution parsed as %q %q", p.OSID, p.OSVersion)
	}
	if p.Arch != "x86_64" {
		t.Fatalf("architecture is %q", p.Arch)
	}
	if p.CPUCores != 2 || p.MemoryMB != 3936 || p.DiskGB != 38 {
		t.Fatalf("resources parsed as %d cores, %d MB, %d GB", p.CPUCores, p.MemoryMB, p.DiskGB)
	}
	if p.PublicIP != "203.0.113.10" || p.PrivateIP != "10.0.0.5" {
		t.Fatalf("addresses parsed as %q and %q", p.PublicIP, p.PrivateIP)
	}
	if !p.HasWireGuard || !p.HasSystemd {
		t.Fatal("capability flags were not read")
	}
	if len(p.Conflicts) != 0 || len(p.PortsInUse) != 0 {
		t.Fatalf("empty lists became %v and %v", p.Conflicts, p.PortsInUse)
	}
}

func TestParsePreflightWithProblems(t *testing.T) {
	output := `os_name=Debian GNU/Linux
os_id=DEBIAN
arch=aarch64
has_systemd=yes
memory_mb=512
disk_gb=4
conflicts=docker nginx
ports_in_use=80 443
has_wireguard=no
k3s_installed=yes
# a comment line
not a key value line
`
	p := ParsePreflight(output)
	if p.OSID != "debian" {
		t.Fatalf("the distribution id was not lowercased: %q", p.OSID)
	}
	if len(p.Conflicts) != 2 || p.Conflicts[0] != "docker" {
		t.Fatalf("conflicts parsed as %v", p.Conflicts)
	}
	if len(p.PortsInUse) != 2 || p.PortsInUse[0] != 80 {
		t.Fatalf("ports parsed as %v", p.PortsInUse)
	}
	if !p.K3sInstalled {
		t.Fatal("an existing k3s installation was not noticed")
	}
	// A comment and a junk line must not derail the rest.
	if p.MemoryMB != 512 {
		t.Fatalf("memory is %d after a junk line", p.MemoryMB)
	}
}

func TestParsePreflightHandlesQuotedValues(t *testing.T) {
	// /etc/os-release quotes its values, and the script echoes them through.
	p := ParsePreflight(`os_name="Ubuntu"` + "\n" + `os_version="24.04"` + "\n")
	if p.OSName != "Ubuntu" || p.OSVersion != "24.04" {
		t.Fatalf("quotes were not stripped: %q %q", p.OSName, p.OSVersion)
	}
}

func TestEvaluateAcceptsAHealthyServer(t *testing.T) {
	problems := Evaluate(ParsePreflight(realPreflightOutput), DefaultRequirements(), false)
	if Fatal(problems) {
		t.Fatalf("a healthy Ubuntu server was rejected: %+v", problems)
	}
	if len(problems) != 0 {
		t.Fatalf("a healthy server produced warnings: %+v", problems)
	}
}

func TestEvaluateRejectsTooSmall(t *testing.T) {
	p := ParsePreflight(realPreflightOutput)
	p.MemoryMB = 480
	problems := Evaluate(p, DefaultRequirements(), false)
	if !Fatal(problems) {
		t.Fatal("a 480 MB server was accepted")
	}
	found := false
	for _, problem := range problems {
		if problem.Check == "memory" {
			found = true
			// The message must say what to do, not just what is wrong.
			if problem.Fix == "" {
				t.Fatal("the memory problem has no suggested fix")
			}
			if !strings.Contains(problem.Detail, "480") {
				t.Fatalf("the problem does not say how much memory was found: %q", problem.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("no memory problem was reported: %+v", problems)
	}
}

func TestControlPlaneNeedsMore(t *testing.T) {
	p := ParsePreflight(realPreflightOutput)
	p.MemoryMB = 1024
	p.CPUCores = 1

	// A 1 GB single-core server is a fine worker.
	if Fatal(Evaluate(p, DefaultRequirements(), false)) {
		t.Fatal("a 1 GB server was rejected as a worker")
	}
	// But etcd and the API server need more than that.
	if !Fatal(Evaluate(p, ControlPlaneRequirements(), true)) {
		t.Fatal("a 1 GB single-core server was accepted as a control plane server")
	}
}

func TestEvaluateBlocksWebServerConflicts(t *testing.T) {
	p := ParsePreflight(realPreflightOutput)
	p.Conflicts = []string{"nginx"}
	problems := Evaluate(p, DefaultRequirements(), false)
	if !Fatal(problems) {
		t.Fatal("a server already running nginx on ports 80 and 443 was accepted")
	}
	for _, problem := range problems {
		if problem.Check == "conflict" && !strings.Contains(problem.Fix, "systemctl disable") {
			t.Fatalf("the conflict message does not give the command to fix it: %q", problem.Fix)
		}
	}
}

func TestDockerIsAWarningNotAFailure(t *testing.T) {
	// Docker and k3s can coexist; refusing would be unhelpful.
	p := ParsePreflight(realPreflightOutput)
	p.Conflicts = []string{"docker"}
	problems := Evaluate(p, DefaultRequirements(), false)
	if Fatal(problems) {
		t.Fatal("a server running Docker was refused outright")
	}
	if len(problems) == 0 {
		t.Fatal("Docker was not mentioned at all")
	}
}

func TestExistingK3sIsNotAConflict(t *testing.T) {
	// Re-adding a server that is already a node must work: that is how a
	// retry after a partial failure gets through.
	p := ParsePreflight(realPreflightOutput)
	p.Conflicts = []string{"k3s"}
	p.K3sInstalled = true
	if problems := Evaluate(p, DefaultRequirements(), false); len(problems) != 0 {
		t.Fatalf("an existing k3s installation was treated as a problem: %+v", problems)
	}
}

// TestMissingWireGuardStopsTheServerJoiningAWireGuardCluster: this used to be a
// warning that promised a fallback — "traffic between your servers will use
// vxlan" — and there was no fallback. The other nodes stay on wireguard-native,
// this one comes up on vxlan, k3s reports nothing wrong, and its pods can reach
// nothing. Refusing is the only honest answer, so the message is now the two
// things that actually fix it.
func TestMissingWireGuardStopsTheServerJoiningAWireGuardCluster(t *testing.T) {
	p := ParsePreflight(realPreflightOutput)
	p.HasWireGuard = false
	problems := Evaluate(p, DefaultRequirements(), false)

	var problem Problem
	for _, candidate := range problems {
		if candidate.Check == "wireguard" {
			problem = candidate
		}
	}
	if problem.Check == "" {
		t.Fatal("a kernel without WireGuard was accepted into a WireGuard cluster without a word")
	}
	if !problem.Fatal {
		t.Fatal("a kernel without WireGuard was only warned about; the server would join and never reach the others")
	}
	if !strings.Contains(problem.Fix, "wireguard-tools") {
		t.Errorf("the fix does not say how to install the module: %q", problem.Fix)
	}
	if !strings.Contains(problem.Fix, "vxlan") {
		t.Errorf("the fix does not offer the other way out, changing the whole cluster: %q", problem.Fix)
	}
	if strings.Contains(problem.Fix, "will use vxlan") {
		t.Errorf("the fix still promises a fallback that does not happen: %q", problem.Fix)
	}
}

// TestMissingWireGuardIsFineOnAVXLANCluster: the module only matters because of
// what the rest of the cluster runs. A cluster that already chose vxlan has
// nothing to disagree with, and refusing the server there would be refusing it
// for no reason.
func TestMissingWireGuardIsFineOnAVXLANCluster(t *testing.T) {
	p := ParsePreflight(realPreflightOutput)
	p.HasWireGuard = false
	req := DefaultRequirements()
	req.FlannelBackend = settings.FlannelVXLAN

	for _, problem := range Evaluate(p, req, false) {
		if problem.Check == "wireguard" {
			t.Fatalf("a vxlan cluster still complained about the WireGuard module: %q", problem.Detail)
		}
	}
}

func TestNoSystemdIsFatal(t *testing.T) {
	p := ParsePreflight(realPreflightOutput)
	p.HasSystemd = false
	problems := Evaluate(p, DefaultRequirements(), false)
	if !Fatal(problems) {
		t.Fatal("a server without systemd was accepted, but k3s installs as a systemd unit")
	}

	// And it has to say which distributions that rules out. The list of
	// known-working ones used to include Alpine, which has no systemd, four
	// lines above the check that refuses it — so somebody who picked Alpine
	// was told it was fine and then refused with no explanation.
	var said string
	for _, problem := range problems {
		if problem.Check == "systemd" {
			said = problem.Fix
		}
	}
	if said == "" {
		t.Fatal("nothing in the report names systemd, so the operator is left guessing")
	}
	for _, want := range []string{"Alpine", "OpenRC", "Ubuntu"} {
		if !strings.Contains(said, want) {
			t.Errorf("the fix does not mention %s, which is what somebody in this position needs to know:\n%s", want, said)
		}
	}
}

func TestUnknownArchitectureIsFatal(t *testing.T) {
	p := ParsePreflight(realPreflightOutput)
	p.Arch = "riscv64"
	if !Fatal(Evaluate(p, DefaultRequirements(), false)) {
		t.Fatal("an architecture with no builds was accepted")
	}
	// arm64 must be accepted: cheap ARM VPSes are a large part of the audience.
	for _, arch := range []string{"aarch64", "arm64", "amd64", "x86_64"} {
		p.Arch = arch
		if Fatal(Evaluate(p, DefaultRequirements(), false)) {
			t.Fatalf("%s was rejected", arch)
		}
	}
}

func TestUntestedDistroIsAWarning(t *testing.T) {
	p := ParsePreflight(realPreflightOutput)
	p.OSID = "almalinux"
	p.OSName = "AlmaLinux"
	problems := Evaluate(p, DefaultRequirements(), false)
	if Fatal(problems) {
		t.Fatal("AlmaLinux was refused; k3s runs on it fine")
	}
	if len(problems) == 0 {
		t.Fatal("an untested distribution produced no warning at all")
	}
}

func TestPortsInUseAreFatalForTheOnesThatMatter(t *testing.T) {
	p := ParsePreflight(realPreflightOutput)
	p.PortsInUse = []int{443}
	if !Fatal(Evaluate(p, DefaultRequirements(), false)) {
		t.Fatal("a server with port 443 taken was accepted; the ingress could never start")
	}

	p.PortsInUse = []int{10250}
	problems := Evaluate(p, DefaultRequirements(), false)
	if Fatal(problems) {
		t.Fatal("port 10250 being in use was treated as fatal; it is worth a warning")
	}
	if len(problems) == 0 {
		t.Fatal("a busy cluster port produced no warning")
	}
}

// TestEveryDistributionWeCallKnownWorkingActuallyPasses: a list that says
// "these run fine" has to mean it. Each is evaluated on an otherwise healthy
// server, and must produce a warning about not being tested — never a refusal.
func TestEveryDistributionWeCallKnownWorkingActuallyPasses(t *testing.T) {
	for distro := range knownWorkingDistros {
		p := ParsePreflight(realPreflightOutput)
		p.OSID = distro
		problems := Evaluate(p, DefaultRequirements(), false)
		if Fatal(problems) {
			t.Errorf("%s is listed as known-working and is refused: %+v", distro, problems)
		}
	}
}

// TestNoMemoryCgroupIsFatalAndSaysWhichLineToChange: the kubelet will not start
// without the memory controller, and what it prints on the way out is about
// cgroups — not about the one line in cmdline.txt somebody has to add. Raspberry
// Pi OS ships with it off and Skifity builds for arm64, so this is a path
// somebody will take.
func TestNoMemoryCgroupIsFatalAndSaysWhichLineToChange(t *testing.T) {
	p := ParsePreflight(realPreflightOutput)
	p.MemoryCgroup = "no"
	problems := Evaluate(p, DefaultRequirements(), false)
	if !Fatal(problems) {
		t.Fatalf("a server with no memory cgroup was accepted: %+v", problems)
	}
	var said string
	for _, problem := range problems {
		if problem.Check == "cgroups" {
			said = problem.Fix
		}
	}
	if !strings.Contains(said, "cgroup_memory=1") || !strings.Contains(said, "cmdline.txt") {
		t.Errorf("the fix does not say what to change:\n%s", said)
	}

	// Unknown is not the same as no. A server whose cgroups cannot be read
	// must not be refused on a guess.
	p.MemoryCgroup = "unknown"
	if Fatal(Evaluate(p, DefaultRequirements(), false)) {
		t.Error("a server whose cgroups could not be read was refused, which is a guess rather than a check")
	}
}

// The port scan could not see a UDP socket at all: `ss -lnt` lists TCP, and
// every port the pod network uses is UDP. A VPS running a WireGuard VPN of its
// own holds exactly the port flannel's wireguard-native backend wants, and the
// failure is a cluster that comes up with every node Ready and no traffic
// between pods.
func TestAWireGuardVPNOnTheServerIsCaught(t *testing.T) {
	report := ParsePreflight(realPreflightOutput)
	report.UDPPortsInUse = []int{51820}
	problems := Evaluate(report, DefaultRequirements(), false)

	var found *Problem
	for i := range problems {
		if problems[i].Check == "udp_port" {
			found = &problems[i]
		}
	}
	if found == nil {
		t.Fatal("a VPN holding the pod network's port was not reported")
	}
	if !found.Fatal {
		t.Error("it must stop the server being added, not warn after the fact")
	}
	if !strings.Contains(found.Fix, settings.FlannelVXLAN) {
		t.Errorf("the fix must name the other backend; got %q", found.Fix)
	}

	// On a cluster that uses vxlan instead, the same VPN is somebody else's
	// business and must not block anything.
	vxlan := DefaultRequirements()
	vxlan.FlannelBackend = settings.FlannelVXLAN
	problems = Evaluate(report, vxlan, false)
	for _, p := range problems {
		if p.Check == "udp_port" && p.Fatal {
			t.Errorf("a port this cluster does not use stopped the server being added: %q", p.Detail)
		}
	}
}

// And the parser has to read the new line, or the check is a script writing to
// nobody.
func TestPreflightReadsTheUDPPorts(t *testing.T) {
	report := ParsePreflight("os_id=ubuntu\nudp_ports_in_use=8472 51820\n")
	if len(report.UDPPortsInUse) != 2 {
		t.Fatalf("parsed %v, want two ports", report.UDPPortsInUse)
	}
}
