package provision

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"skifity/internal/settings"
)

// checkShellSyntax runs the generated script through `sh -n`, which parses it
// without running anything. A quoting mistake in a generated script would
// otherwise only show up on a real server, halfway through adding it.
func checkShellSyntax(t *testing.T, name, script string) {
	t.Helper()
	cmd := exec.Command("sh", "-n")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s is not valid POSIX shell: %v\n%s\n--- script ---\n%s",
			name, err, output, numberLines(script))
	}
}

func numberLines(s string) string {
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		b.WriteString(strings.TrimSuffix(line, "\r"))
		b.WriteByte('\n')
		_ = i
	}
	return b.String()
}

func TestGeneratedScriptsAreValidShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell available to check syntax with")
	}

	checkShellSyntax(t, "PreflightScript", PreflightScript)
	checkShellSyntax(t, "NodeTokenScript", NodeTokenScript)
	checkShellSyntax(t, "UninstallScript", UninstallScript)
	checkShellSyntax(t, "InstallKeyScript",
		InstallKeyScript("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 skifity@panel", "root"))
	checkShellSyntax(t, "InstallKeyScript (non-root)",
		InstallKeyScript("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 skifity@panel", "deploy"))
	checkShellSyntax(t, "FirewallScript",
		FirewallScript([]string{"203.0.113.10", "203.0.113.11"}, true))
	checkShellSyntax(t, "FirewallScript (no members)", FirewallScript(nil, false))
	checkShellSyntax(t, "InstallServerScript",
		InstallServerScript("v1.33.1+k3s1", "a-token", "203.0.113.10", "", nil))
	checkShellSyntax(t, "JoinServerScript",
		JoinServerScript("", "a-token", "https://203.0.113.10:6443", "203.0.113.11", ""))
	checkShellSyntax(t, "JoinAgentScript",
		JoinAgentScript("", "a-token", "https://203.0.113.10:6443", "203.0.113.12",
			map[string]string{"skifity.com/location": "frankfurt", "skifity.com/size": "small"}))
	checkShellSyntax(t, "ConnectivityScript", ConnectivityScript("203.0.113.10", 6443))
}

func TestFirewallScriptOpensOnlyToMembers(t *testing.T) {
	script := FirewallScript([]string{"203.0.113.10"}, false)

	// The API server and the pod network must never be open to the world.
	if !strings.Contains(script, `allow_from '203.0.113.10' 6443 'tcp'`) {
		t.Fatal("the API server port was not opened to the other cluster member")
	}
	if strings.Contains(script, "allow_public 6443") {
		t.Fatal("the Kubernetes API was opened to the internet")
	}
	if strings.Contains(script, "allow_public 10250") {
		t.Fatal("the kubelet port was opened to the internet")
	}
	// HTTP and HTTPS are the only public ports.
	if !strings.Contains(script, "allow_public 80 tcp") || !strings.Contains(script, "allow_public 443 tcp") {
		t.Fatal("the web ports were not opened")
	}
	// etcd ports belong only on control plane nodes.
	if strings.Contains(script, "2379") {
		t.Fatal("etcd ports were opened on a worker")
	}

	controlPlane := FirewallScript([]string{"203.0.113.10"}, true)
	if !strings.Contains(controlPlane, `allow_from '203.0.113.10' 2380 'tcp'`) {
		t.Fatal("etcd peer port was not opened on a control plane server")
	}
}

func TestFirewallScriptIsIdempotent(t *testing.T) {
	script := FirewallScript([]string{"203.0.113.10"}, false)
	// iptables -I without a -C check accumulates a duplicate rule on every
	// retry, and retries are expected here.
	if !strings.Contains(script, "iptables -C INPUT") {
		t.Fatal("the iptables path does not check before inserting, so retrying would add duplicate rules")
	}
	// ufw and firewalld are idempotent by design, but firewalld's rules are
	// permanent ones, which is not the same as applied ones.
	if !strings.Contains(script, "firewall-cmd --reload") {
		t.Fatal("firewalld rules are added permanently and never reloaded, so none of them take effect")
	}
}

// TestTheFirewallEveryRedHatServerActuallyHas: firewalld is the default on
// AlmaLinux, Rocky, RHEL, CentOS and Fedora, active out of the box on their
// cloud images, and every one of those is on the list of distributions this
// product says it expects to work. It was not handled at all. The fallback put
// rules in with `+"`iptables -I INPUT`"+`, which firewalld discards on its next
// reload — and there is no netfilter-persistent on those systems either, so a
// reboot lost them too. The ports were open until something touched the
// firewall, and then were not.
func TestTheFirewallEveryRedHatServerActuallyHas(t *testing.T) {
	script := FirewallScript([]string{"203.0.113.10"}, true)

	if !strings.Contains(script, "firewall-cmd --state") {
		t.Fatal("nothing checks whether firewalld is the firewall on this server")
	}
	// Per source address, not to the world: a rich rule is the only way
	// firewalld says that.
	if !strings.Contains(script, "--add-rich-rule=") {
		t.Error("the firewalld path has no way to open a port to one address, so it would have to open it to everybody")
	}
	if !strings.Contains(script, "--permanent") {
		t.Error("firewalld rules are not permanent, so a reload or a reboot loses the cluster's ports")
	}

	// The pod and service networks are trusted wholesale, by every firewall.
	// Traffic between pods is not on a fixed port and cannot be enumerated,
	// and k3s's own documentation asks for exactly this.
	for _, cidr := range []string{PodCIDR, ServiceCIDR} {
		if !strings.Contains(script, cidr) {
			t.Errorf("%s is not trusted, so pod traffic that is not on a listed port is dropped", cidr)
		}
	}
	for _, want := range []string{
		"--zone=trusted --add-source",  // firewalld
		`ufw allow from "$POD_CIDR"`,   // ufw
		`iptables -C INPUT -s "$cidr"`, // and the fallback
	} {
		if !strings.Contains(script, want) {
			t.Errorf("one of the three firewalls does not trust the cluster network: %s is missing", want)
		}
	}
}

// TestOneFirewallIsChosenNotThree: running ufw commands on a firewalld server
// leaves rules in a tool nobody is looking at, and the reverse is worse — two
// firewalls both half-configured is how a cluster becomes unreachable in a way
// nobody can diagnose.
func TestOneFirewallIsChosenNotThree(t *testing.T) {
	script := FirewallScript([]string{"203.0.113.10"}, false)
	if strings.Count(script, "FIREWALL=ufw") != 1 ||
		strings.Count(script, "FIREWALL=firewalld") != 1 ||
		strings.Count(script, "FIREWALL=iptables") != 1 {
		t.Fatal("the three firewalls are not decided once, in one place")
	}
	if !strings.Contains(script, "echo \"firewall_configured=${FIREWALL}\"") {
		t.Fatal("the script does not report which firewall it used, so a failure cannot be read afterwards")
	}
}

func TestInstallKeyScriptIsIdempotent(t *testing.T) {
	script := InstallKeyScript("ssh-ed25519 AAAA test@host", "root")
	if !strings.Contains(script, "grep -qF") {
		t.Fatal("the key is appended without checking, so retrying would add it repeatedly")
	}
	if !strings.Contains(script, "chmod 700") || !strings.Contains(script, "chmod 600") {
		t.Fatal("the .ssh permissions are not set; sshd ignores a group-writable authorized_keys")
	}
	// A non-root user's keys go in their own home directory.
	if !strings.Contains(InstallKeyScript("k", "deploy"), "/home/deploy") {
		t.Fatal("a non-root user's home directory was not used")
	}
}

func TestInstallScriptsCarryTheRightFlags(t *testing.T) {
	first := InstallServerScript("", "tok", "203.0.113.10", "", nil)
	// --cluster-init is what makes the single node HA-ready later (ADR-0002).
	if !strings.Contains(first, "--cluster-init") {
		t.Fatal("the first server is installed without --cluster-init, so it could never be made highly available")
	}
	// wireguard-native is what encrypts traffic between providers (ADR-0003).
	if !strings.Contains(first, "--flannel-backend=wireguard-native") {
		t.Fatal("the pod network is not encrypted")
	}
	if !strings.Contains(first, "--tls-san=203.0.113.10") {
		t.Fatal("the public address is not in the certificate, so kubectl from outside would fail")
	}
	if !strings.Contains(first, "--node-external-ip=203.0.113.10") {
		t.Fatal("the external address is not set, which breaks clusters spanning providers")
	}
	// Without a pinned version the stable channel is followed.
	if !strings.Contains(first, `INSTALL_K3S_CHANNEL="stable"`) {
		t.Fatal("no version and no channel were set")
	}

	pinned := InstallServerScript("v1.33.1+k3s1", "tok", "203.0.113.10", "", nil)
	if !strings.Contains(pinned, `INSTALL_K3S_VERSION='v1.33.1+k3s1'`) {
		t.Fatal("the pinned version was ignored")
	}
	if strings.Contains(pinned, "INSTALL_K3S_CHANNEL") {
		t.Fatal("both a version and a channel were set, which the installer rejects")
	}

	agent := JoinAgentScript("", "tok", "https://203.0.113.10:6443", "203.0.113.12", nil)
	if !strings.Contains(agent, `K3S_URL='https://203.0.113.10:6443'`) {
		t.Fatal("the agent does not know which server to join")
	}
	if strings.Contains(agent, "--cluster-init") {
		t.Fatal("an agent was given --cluster-init, which would try to start a second cluster")
	}
	if !strings.Contains(agent, `INSTALL_K3S_EXEC='agent`) {
		t.Fatal("the agent role was not set")
	}

	controlPlane := JoinServerScript("", "tok", "https://203.0.113.10:6443", "203.0.113.11", "")
	if !strings.Contains(controlPlane, `INSTALL_K3S_EXEC='server`) {
		t.Fatal("a promoted server was not installed in server mode")
	}
	if strings.Contains(controlPlane, "--cluster-init") {
		t.Fatal("joining a control plane node used --cluster-init, which starts a separate cluster")
	}
}

func TestAgentLabelsAreStable(t *testing.T) {
	labels := map[string]string{"b": "2", "a": "1", "c": "3"}
	first := JoinAgentScript("", "t", "u", "ip", labels)
	for range 10 {
		if JoinAgentScript("", "t", "u", "ip", labels) != first {
			t.Fatal("the generated command changes between runs, so a retry would look like a different install")
		}
	}
	if !strings.Contains(first, "--node-label=a=1 --node-label=b=2 --node-label=c=3") {
		t.Fatalf("labels are not in a stable order:\n%s", first)
	}
}

// TestEveryServerAgreesOnHowNodesTalk: a control plane node that joins without
// --flannel-backend defaults to vxlan while the first node is on WireGuard.
// The two never exchange a packet, and nothing says the flags disagree: the
// symptom is pods that cannot reach pods on the other machine.
//
// So whichever backend the cluster chose, every server has to be installed with
// that one — including the cluster that had to choose vxlan, where getting this
// wrong is just as fatal and the wrong answer is the default.
func TestEveryServerAgreesOnHowNodesTalk(t *testing.T) {
	for _, backend := range []string{settings.FlannelWireGuard, settings.FlannelVXLAN} {
		first := InstallServerScript("", "tok", "203.0.113.10", backend, nil)
		joined := JoinServerScript("", "tok", "https://203.0.113.10:6443", "203.0.113.20", backend)

		for _, flag := range []string{
			"--flannel-backend=" + backend,
			"--secrets-encryption",
			"--write-kubeconfig-mode=0600",
		} {
			if !strings.Contains(first, flag) {
				t.Errorf("the first server is installed without %s", flag)
			}
			if !strings.Contains(joined, flag) {
				t.Errorf("a joining control plane node is installed without %s, so it disagrees with the first", flag)
			}
		}
	}

	// An unset backend is the cluster nobody chose for, and it has to land on
	// the same answer everywhere rather than on each caller's idea of a default.
	unsetFirst := InstallServerScript("", "tok", "203.0.113.10", "", nil)
	unsetJoined := JoinServerScript("", "tok", "https://203.0.113.10:6443", "203.0.113.20", "")
	for _, script := range []string{unsetFirst, unsetJoined} {
		if !strings.Contains(script, "--flannel-backend="+settings.FlannelWireGuard) {
			t.Errorf("an unset backend did not fall back to %s:\n%s", settings.FlannelWireGuard, script)
		}
	}
}

// TestTheInstallerAndThePanelStartTheSameKindOfCluster: install.sh creates the
// first node on most installs and the panel creates it on the rest. A cluster
// whose shape depends on which one made it is a cluster that breaks when the
// other one adds to it.
func TestTheInstallerAndThePanelStartTheSameKindOfCluster(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "installer", "install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	installer := string(script)
	panel := InstallServerScript("", "tok", "203.0.113.10", "", nil)

	for _, flag := range []string{
		"--cluster-init",
		"--secrets-encryption",
		"--write-kubeconfig-mode=0600",
	} {
		if !strings.Contains(installer, flag) {
			t.Errorf("install.sh no longer passes %s", flag)
		}
		if !strings.Contains(panel, flag) {
			t.Errorf("the panel no longer passes %s", flag)
		}
	}

	// The pod network is the one flag the two are allowed to differ on, because
	// the installer picks it from the kernel it is standing on. What is not
	// allowed is picking it and keeping it: the panel installs every later
	// server, and it can only match a choice it was told about.
	if !strings.Contains(installer, "--flannel-backend=${POD_NETWORK}") {
		t.Error("install.sh no longer installs the first node with the backend it picked")
	}
	for _, expected := range []string{
		`POD_NETWORK="` + settings.FlannelWireGuard + `"`,
		`POD_NETWORK="` + settings.FlannelVXLAN + `"`,
		"__POD_NETWORK__",
	} {
		if !strings.Contains(installer, expected) {
			t.Errorf("install.sh no longer contains %s, so the panel cannot learn which pod network this cluster uses", expected)
		}
	}

	manifest, err := os.ReadFile(filepath.Join("..", "..", "deploy", "panel.yaml"))
	if err != nil {
		t.Fatalf("read panel.yaml: %v", err)
	}
	if !strings.Contains(string(manifest), "SKIFITY_POD_NETWORK") ||
		!strings.Contains(string(manifest), "__POD_NETWORK__") {
		t.Error("the panel's own manifest does not carry the pod network the installer chose")
	}
}

func TestNothingATypedValueContainsBecomesACommand(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell available")
	}

	// Every generated script used %q, which is Go's quoting and not the
	// shell's: a shell expands $ and a backtick inside a double-quoted string
	// and Go escapes neither, so `1.2.3.4$(id)` ran id. These are the values
	// that reach a script, with the payload that used to work.
	hostile := `x$(touch /tmp/skifity-injection-marker)` + "`id`"

	scripts := map[string]string{
		"InstallKeyScript":    InstallKeyScript("ssh-ed25519 AAAA test", hostile),
		"FirewallScript":      FirewallScript([]string{hostile}, true),
		"ConnectivityScript":  ConnectivityScript(hostile, 6443),
		"InstallServerScript": InstallServerScript(hostile, hostile, "203.0.113.10", "", nil),
		"JoinAgentScript":     JoinAgentScript("", hostile, hostile, "203.0.113.12", nil),
	}
	for name, script := range scripts {
		// Still valid shell after quoting, which is the first thing quoting
		// gets wrong.
		cmd := exec.Command("sh", "-n")
		cmd.Stdin = strings.NewReader(script)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s is not valid shell after quoting: %v\n%s", name, err, output)
			continue
		}
		// And the payload is inert: inside single quotes a shell reads $( and
		// a backtick as characters. Finding the text is expected; finding it
		// in a form the shell would run is not.
		for _, escape := range []string{`"` + hostile, hostile + `"`} {
			if strings.Contains(script, escape) {
				t.Errorf("%s left a value where a shell would expand it:\n%s", name, script)
			}
		}
	}
}

func TestTheInstallKeyScriptQuotesTheAccountName(t *testing.T) {
	// The account names a home directory and is handed to chown, in a script
	// that runs as root on the server being added.
	script := InstallKeyScript("ssh-ed25519 AAAA test", "deploy")
	if !strings.Contains(script, `HOME_DIR='/home/deploy'`) {
		t.Fatalf("the home directory is not quoted:\n%s", script)
	}
	if !strings.Contains(script, `chown -R 'deploy' "$HOME_DIR/.ssh"`) {
		t.Fatalf("the account name reaches chown unquoted:\n%s", script)
	}
	// root keeps its own home, which is not under /home.
	if !strings.Contains(InstallKeyScript("k", "root"), `HOME_DIR='/root'`) {
		t.Error("root was given a home under /home")
	}
}

// TestTheResearchPageSaysWhatTheCodeDoes: docs/research/stack.md lists the k3s
// flags Skifity installs a server with, and it drifted — it documented
// --write-kubeconfig-mode=0644 while the code used 0600, and a node label the
// code never sets. A research page that is wrong about the thing it researched
// is worse than no page, and nothing was checking it.
//
// Only the flags are checked, because they are the part a reader would copy.
func TestTheResearchPageSaysWhatTheCodeDoes(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "research", "stack.md"))
	if err != nil {
		t.Fatalf("read stack.md: %v", err)
	}

	server := InstallServerScript("", "tok", "203.0.113.10", settings.FlannelWireGuard, nil)
	agent := JoinAgentScript("", "tok", "https://203.0.113.10:6443", "203.0.113.12",
		map[string]string{"skifity.com/location": "fra"})

	var flags []string
	for _, line := range strings.Split(string(page), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "--") {
			continue
		}
		// The page annotates some flags with a comment, and writes the values
		// that vary as placeholders. The placeholders are filled in with what
		// the scripts above were given, so the flags carrying a value are
		// checked too rather than skipped.
		flag, _, _ := strings.Cut(line, " #")
		flag = strings.NewReplacer(
			"<public ip>", "203.0.113.10",
			"<location>", "fra",
		).Replace(strings.TrimSpace(flag))
		if strings.Contains(flag, "<") {
			// The backend is the cluster's choice, and the size was not given.
			continue
		}
		flags = append(flags, flag)
	}
	if len(flags) < 7 {
		t.Fatalf("only %d flags were found in stack.md; the page or this test is wrong", len(flags))
	}

	for _, flag := range flags {
		if strings.Contains(server, flag) || strings.Contains(agent, flag) {
			continue
		}
		t.Errorf("stack.md documents %s and no generated script passes it", flag)
	}
}
