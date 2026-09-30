package provision

import (
	"fmt"
	"strings"

	"skifity/internal/kube"
	"skifity/internal/settings"
	"skifity/internal/shellsafe"
	"skifity/internal/version"
)

// managedNodeLabel marks a node this panel installed.
//
// Built from version.LabelKey rather than written out, because the label was
// spelled here by hand and everywhere else through that function: renaming the
// vendor domain in one file would have left the nodes carrying a key nothing
// else looked for. It is a package-level value because the functions below take
// a parameter called version, which shadows the package inside them.
var managedNodeLabel = "--node-label=" + version.LabelKey("managed") + "=true"

// The shell that runs on the servers being added.
//
// Everything here is POSIX sh, is safe to run twice, and prints machine-readable
// output. It is kept as Go string constants rather than uploaded files so that
// one binary is genuinely all a user needs.
//
// Every value interpolated into one of these goes through shellsafe.Quote. It
// used to go through %q, which reads as though it were shell quoting and is
// not: a shell expands $ and a backtick inside a double-quoted string, and Go's
// %q escapes neither. See that package.

// PreflightScript inspects a server without changing anything on it.
const PreflightScript = `
set -u

# Distribution
if [ -r /etc/os-release ]; then
  . /etc/os-release
  echo "os_name=${NAME:-unknown}"
  echo "os_version=${VERSION_ID:-unknown}"
  echo "os_id=${ID:-unknown}"
else
  echo "os_name=unknown"
  echo "os_id="
fi

echo "kernel=$(uname -r 2>/dev/null || echo unknown)"
echo "arch=$(uname -m 2>/dev/null || echo unknown)"

# systemd is required: k3s installs as a unit.
if [ -d /run/systemd/system ]; then echo "has_systemd=yes"; else echo "has_systemd=no"; fi

# The memory cgroup controller. Without it the kubelet will not start, and what
# it says on the way out is about cgroups rather than about the thing to change.
# Raspberry Pi OS ships with it off, which is the usual way somebody meets this.
if [ -r /sys/fs/cgroup/cgroup.controllers ]; then
  # cgroup v2: one file lists what is available.
  if grep -qw memory /sys/fs/cgroup/cgroup.controllers; then
    echo "has_memory_cgroup=yes"
  else
    echo "has_memory_cgroup=no"
  fi
elif [ -r /proc/cgroups ]; then
  # cgroup v1: the last column is 1 when the controller is enabled.
  if awk '$1 == "memory" && $4 == 1 {found=1} END {exit !found}' /proc/cgroups; then
    echo "has_memory_cgroup=yes"
  else
    echo "has_memory_cgroup=no"
  fi
else
  # Nothing to read means nothing to claim.
  echo "has_memory_cgroup=unknown"
fi

# Cores, memory and free disk on the root filesystem.
cores=$(nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo 2>/dev/null || echo 0)
echo "cpu_cores=${cores}"

if [ -r /proc/meminfo ]; then
  mem_kb=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)
  echo "memory_mb=$((mem_kb / 1024))"
fi

disk=$(df -BG --output=avail / 2>/dev/null | tail -n1 | tr -dc '0-9')
[ -n "${disk}" ] && echo "disk_gb=${disk}"

# Addresses. The default route's source address is the one other servers can
# reach, which is what matters for a cluster spanning providers.
private_ip=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1); exit}')
[ -n "${private_ip}" ] && echo "private_ip=${private_ip}"

public_ip=""
for url in https://api.ipify.org https://ifconfig.me/ip https://icanhazip.com; do
  public_ip=$(curl -fsS --max-time 5 "$url" 2>/dev/null | tr -dc '0-9.') || true
  [ -n "${public_ip}" ] && break
done
# A server behind NAT with no outbound access still has its route address.
[ -z "${public_ip}" ] && public_ip="${private_ip}"
[ -n "${public_ip}" ] && echo "public_ip=${public_ip}"

# WireGuard, for encrypted traffic between providers.
if modprobe wireguard 2>/dev/null || lsmod 2>/dev/null | grep -q '^wireguard' || [ -d /sys/module/wireguard ]; then
  echo "has_wireguard=yes"
else
  echo "has_wireguard=no"
fi

# Is this already a node?
if command -v k3s >/dev/null 2>&1 || [ -x /usr/local/bin/k3s ]; then
  echo "k3s_installed=yes"
else
  echo "k3s_installed=no"
fi

# Software that would get in the way.
conflicts=""
for svc in docker nginx apache2 caddy containerd microk8s; do
  if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "$svc" 2>/dev/null; then
    conflicts="${conflicts} ${svc}"
  fi
done
echo "conflicts=${conflicts# }"

# Ports the cluster needs, that something else already holds.
in_use=""
for port in 80 443 6443 10250 2379 2380; do
  if command -v ss >/dev/null 2>&1; then
    if ss -lnt 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${port}\$"; then
      in_use="${in_use} ${port}"
    fi
  elif command -v netstat >/dev/null 2>&1; then
    if netstat -lnt 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${port}\$"; then
      in_use="${in_use} ${port}"
    fi
  fi
done
echo "ports_in_use=${in_use# }"

# And the UDP ports the pod network needs, which the TCP scan above cannot see.
#
# 51820 is the one that bites: a VPS running a WireGuard VPN of its own is
# holding exactly the port flannel's wireguard-native backend wants, and the
# failure is a cluster that comes up with every node Ready and no traffic
# between pods.
udp_in_use=""
for port in 8472 51820 51821; do
  if command -v ss >/dev/null 2>&1; then
    if ss -lnu 2>/dev/null | awk '{print $5}' | grep -qE "[:.]${port}\$"; then
      udp_in_use="${udp_in_use} ${port}"
    fi
  elif command -v netstat >/dev/null 2>&1; then
    if netstat -lnu 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${port}\$"; then
      udp_in_use="${udp_in_use} ${port}"
    fi
  fi
done
echo "udp_ports_in_use=${udp_in_use# }"
`

// InstallKeyScript adds the panel's public key to authorized_keys, once.
//
// Appending only when absent is what makes the whole add-server flow safe to
// retry: running it five times leaves one key, not five.
func InstallKeyScript(publicKey, user string) string {
	home := "/root"
	if user != "root" {
		home = "/home/" + user
	}
	return fmt.Sprintf(`
set -eu
HOME_DIR=%s
KEY=%s

mkdir -p "$HOME_DIR/.ssh"
chmod 700 "$HOME_DIR/.ssh"
touch "$HOME_DIR/.ssh/authorized_keys"
chmod 600 "$HOME_DIR/.ssh/authorized_keys"

# Only append when the key is not already there, so retrying is harmless.
if ! grep -qF "$KEY" "$HOME_DIR/.ssh/authorized_keys" 2>/dev/null; then
  printf '%%s\n' "$KEY" >> "$HOME_DIR/.ssh/authorized_keys"
fi

# An authorized_keys file the group can write is ignored by sshd, which fails
# in a way that looks like a rejected key.
chown -R %s "$HOME_DIR/.ssh" 2>/dev/null || true
echo "key_installed=yes"
`, shellsafe.Quote(home), shellsafe.Quote(strings.TrimSpace(publicKey)), shellsafe.Quote(user))
}

// FirewallScript opens the cluster ports to the other members only.
//
// Three firewalls, because a server has whichever one its distribution shipped
// and none of them can be assumed. ufw on Ubuntu and Debian, firewalld on the
// Red Hat family, and iptables when neither is running. Each is used the way
// its own users would, so that what Skifity added is visible in the tool the
// server's owner already looks at.
//
// firewalld was missing entirely, which mattered more than it sounds: it is the
// default on AlmaLinux, Rocky, RHEL, CentOS and Fedora, active out of the box
// on their cloud images, and every one of those is on the list of distributions
// this product says it expects to work. The fallback path put rules in with
// `iptables -I INPUT`, which firewalld discards on its next reload, and there is
// no netfilter-persistent on those systems to survive a reboot either — so the
// cluster's ports were open until something reloaded the firewall, and then
// were not.
//
// Ports are opened per source address rather than to the world: the pod network
// and the API server should never be reachable from the internet. The pod and
// service networks are trusted by CIDR as well as by interface, which is what
// k3s's own documentation asks for.
func FirewallScript(memberIPs []string, controlPlane bool) string {
	var rules strings.Builder
	for _, port := range ClusterPorts {
		if port.ControlPlaneOnly && !controlPlane {
			continue
		}
		for _, ip := range memberIPs {
			if ip == "" {
				continue
			}
			fmt.Fprintf(&rules, "allow_from %s %d %s\n",
				shellsafe.Quote(ip), port.Port, shellsafe.Quote(port.Protocol))
		}
	}

	return fmt.Sprintf(`
set -u

# k3s's own pod and service networks. Trusted wholesale, because the traffic
# between pods is not on a fixed port and cannot be enumerated.
POD_CIDR=%s
SERVICE_CIDR=%s

FIREWALL=none
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | head -n1 | grep -qi active; then
  FIREWALL=ufw
elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state 2>/dev/null | grep -qi running; then
  FIREWALL=firewalld
elif command -v iptables >/dev/null 2>&1; then
  FIREWALL=iptables
fi

allow_from() {
  ip="$1"; port="$2"; proto="$3"
  case "$FIREWALL" in
  ufw)
    # ufw is idempotent: adding a rule twice keeps one.
    ufw allow from "$ip" to any port "$port" proto "$proto" >/dev/null 2>&1 || true
    ;;
  firewalld)
    # A rich rule is the only way firewalld expresses "this port, from this
    # address". --permanent survives a reload and a reboot, which is the whole
    # reason this branch exists.
    firewall-cmd --permanent --add-rich-rule="rule family=ipv4 source address=${ip}/32 port port=${port} protocol=${proto} accept" >/dev/null 2>&1 || true
    ;;
  iptables)
    # -C checks first, so this does not accumulate duplicate rules.
    if ! iptables -C INPUT -p "$proto" -s "$ip" --dport "$port" -j ACCEPT 2>/dev/null; then
      iptables -I INPUT -p "$proto" -s "$ip" --dport "$port" -j ACCEPT 2>/dev/null || true
    fi
    ;;
  esac
}

allow_public() {
  port="$1"; proto="$2"
  case "$FIREWALL" in
  ufw)
    ufw allow "$port"/"$proto" >/dev/null 2>&1 || true
    ;;
  firewalld)
    firewall-cmd --permanent --add-port="${port}/${proto}" >/dev/null 2>&1 || true
    ;;
  iptables)
    if ! iptables -C INPUT -p "$proto" --dport "$port" -j ACCEPT 2>/dev/null; then
      iptables -I INPUT -p "$proto" --dport "$port" -j ACCEPT 2>/dev/null || true
    fi
    ;;
  esac
}

%s
# HTTP and HTTPS are the only ports the world needs.
allow_public 80 tcp
allow_public 443 tcp

# Pods talk to each other over the cluster network; without this the CNI
# traffic that is not on a fixed port is dropped.
case "$FIREWALL" in
ufw)
  ufw allow in on cni0 >/dev/null 2>&1 || true
  ufw allow in on flannel.1 >/dev/null 2>&1 || true
  ufw allow in on flannel-wg >/dev/null 2>&1 || true
  ufw allow from "$POD_CIDR" to any >/dev/null 2>&1 || true
  ufw allow from "$SERVICE_CIDR" to any >/dev/null 2>&1 || true
  ;;
firewalld)
  firewall-cmd --permanent --zone=trusted --add-source="$POD_CIDR" >/dev/null 2>&1 || true
  firewall-cmd --permanent --zone=trusted --add-source="$SERVICE_CIDR" >/dev/null 2>&1 || true
  firewall-cmd --permanent --zone=trusted --add-interface=cni0 >/dev/null 2>&1 || true
  firewall-cmd --permanent --zone=trusted --add-interface=flannel.1 >/dev/null 2>&1 || true
  firewall-cmd --permanent --zone=trusted --add-interface=flannel-wg >/dev/null 2>&1 || true
  # Nothing above is live until this, and --permanent alone is a rule nobody
  # is applying.
  firewall-cmd --reload >/dev/null 2>&1 || true
  ;;
iptables)
  for cidr in "$POD_CIDR" "$SERVICE_CIDR"; do
    if ! iptables -C INPUT -s "$cidr" -j ACCEPT 2>/dev/null; then
      iptables -I INPUT -s "$cidr" -j ACCEPT 2>/dev/null || true
    fi
  done
  # Persist where the tooling exists, so a reboot does not undo this. ufw and
  # firewalld persist on their own.
  if command -v netfilter-persistent >/dev/null 2>&1; then
    netfilter-persistent save >/dev/null 2>&1 || true
  elif command -v iptables-save >/dev/null 2>&1 && [ -d /etc/iptables ]; then
    iptables-save >/etc/iptables/rules.v4 2>/dev/null || true
  fi
  ;;
esac

echo "firewall_configured=${FIREWALL}"
`, shellsafe.Quote(PodCIDR), shellsafe.Quote(ServiceCIDR), rules.String())
}

// serverArgs are the flags every control plane node has to agree on.
//
// Not a matter of taste. --flannel-backend chooses how nodes reach each other,
// and a node that joins with a different one never exchanges a packet with the
// others: the symptom is pods that cannot reach pods on the other machine
// rather than anything saying "these do not match". It is a cluster-wide
// setting for exactly that reason, read once and passed to every node.
// --secrets-encryption is the same shape of problem: a member that disagrees
// writes Secrets the others cannot read.
//
// installer/install.sh starts the first node with exactly these, and a test
// checks that the two lists have not drifted apart.
func serverArgs(publicIP, backend string) []string {
	if backend == "" {
		backend = settings.FlannelWireGuard
	}
	return []string{
		"--flannel-backend=" + backend,
		"--secrets-encryption",
		"--write-kubeconfig-mode=0600",
		fmt.Sprintf("--tls-san=%s", publicIP),
		fmt.Sprintf("--node-external-ip=%s", publicIP),
		managedNodeLabel,
	}
}

// InstallServerScript installs the first control plane node.
//
// backend is the pod network every node in this cluster will use. It is decided
// once, here, and every server that joins later is given the same one: a node
// that joins with a different backend never exchanges a packet with the others,
// and nothing reports that the flags disagree.
func InstallServerScript(version, token, publicIP, backend string, extraArgs []string) string {
	args := append([]string{"--cluster-init"}, serverArgs(publicIP, backend)...)
	args = append(args, extraArgs...)
	return installScript(version, token, "", strings.Join(args, " "), true)
}

// JoinServerScript joins another control plane node for high availability.
func JoinServerScript(version, token, serverURL, publicIP, backend string) string {
	args := append([]string{"--server", serverURL}, serverArgs(publicIP, backend)...)
	return installScript(version, token, "", strings.Join(args, " "), true)
}

// JoinAgentScript joins a worker node.
func JoinAgentScript(version, token, serverURL, publicIP string, labels map[string]string) string {
	args := []string{
		fmt.Sprintf("--node-external-ip=%s", publicIP),
		managedNodeLabel,
	}
	for _, pair := range sortedPairs(labels) {
		args = append(args, fmt.Sprintf("--node-label=%s=%s", pair[0], pair[1]))
	}
	return installScript(version, token, serverURL, strings.Join(args, " "), false)
}

// installScript builds the k3s installation command.
//
// The official installer is idempotent: run again with the same arguments it
// reconfigures rather than breaking, which is what makes Retry safe.
func installScript(version, token, serverURL, args string, isServer bool) string {
	role := "agent"
	if isServer {
		role = "server"
	}
	var env strings.Builder
	fmt.Fprintf(&env, "INSTALL_K3S_EXEC=%s ", shellsafe.Quote(role+" "+args))
	if version != "" {
		fmt.Fprintf(&env, "INSTALL_K3S_VERSION=%s ", shellsafe.Quote(version))
	} else {
		// Without a version the installer follows the stable channel, which is
		// what we want by default: it moves forward without a Skifity release.
		env.WriteString(`INSTALL_K3S_CHANNEL="stable" `)
	}
	if token != "" {
		fmt.Fprintf(&env, "K3S_TOKEN=%s ", shellsafe.Quote(token))
	}
	if serverURL != "" {
		fmt.Fprintf(&env, "K3S_URL=%s ", shellsafe.Quote(serverURL))
	}

	return fmt.Sprintf(`
set -eu

# The panel streams this output, so progress is visible rather than a long wait.
#
# The mirror configuration is written before k3s starts, because that is when
# k3s reads it. Re-running this script rewrites it and restarts the service,
# which is how an existing node picks up a change.
echo "==> Telling the container runtime where the panel's registry is"
mkdir -p /etc/rancher/k3s
cat > /etc/rancher/k3s/registries.yaml <<'SKIFITY_REGISTRIES'
%s
SKIFITY_REGISTRIES

echo "==> Downloading the k3s installer"
if command -v curl >/dev/null 2>&1; then
  curl -sfL https://get.k3s.io -o /tmp/k3s-install.sh
elif command -v wget >/dev/null 2>&1; then
  wget -qO /tmp/k3s-install.sh https://get.k3s.io
else
  echo "Neither curl nor wget is installed on this server." >&2
  echo "Install one of them and try again: apt-get install -y curl" >&2
  exit 20
fi
chmod +x /tmp/k3s-install.sh

echo "==> Installing Kubernetes (k3s)"
%s sh /tmp/k3s-install.sh

echo "==> Waiting for the service to start"
for i in $(seq 1 60); do
  if systemctl is-active --quiet k3s 2>/dev/null || systemctl is-active --quiet k3s-agent 2>/dev/null; then
    echo "k3s is running"
    exit 0
  fi
  sleep 2
done

echo "k3s did not start within two minutes. The last log lines were:" >&2
journalctl -u k3s -u k3s-agent --no-pager -n 40 2>/dev/null >&2 || true
exit 21
`, kube.RegistriesYAML(), env.String())
}

// NodeTokenScript reads the join token from the first control plane node.
const NodeTokenScript = `
set -eu
if [ -r /var/lib/rancher/k3s/server/node-token ]; then
  echo "node_token=$(cat /var/lib/rancher/k3s/server/node-token)"
else
  echo "This server is not a control plane node, so it has no join token." >&2
  exit 1
fi
`

// UninstallScript removes k3s from a server.
//
// The official uninstall script is used where it exists, because it knows how
// to unmount everything k3s mounted; leaving those mounts behind makes a
// machine impossible to reuse without a reboot.
const UninstallScript = `
set -u

if [ -x /usr/local/bin/k3s-uninstall.sh ]; then
  echo "==> Removing the k3s server"
  /usr/local/bin/k3s-uninstall.sh || true
elif [ -x /usr/local/bin/k3s-agent-uninstall.sh ]; then
  echo "==> Removing the k3s agent"
  /usr/local/bin/k3s-agent-uninstall.sh || true
else
  echo "k3s is not installed on this server; nothing to remove."
fi

rm -rf /etc/rancher/k3s /var/lib/rancher/k3s 2>/dev/null || true
echo "uninstalled=yes"
`

// ConnectivityScript checks that this server can reach another on a port.
//
// It is run on the server being added and pointed at an existing member, which
// tests the direction that a provider firewall usually blocks.
func ConnectivityScript(targetIP string, port int) string {
	return fmt.Sprintf(`
set -u
TARGET=%s
PORT=%d

# Prefer a real TCP connect; fall back to whatever the machine has.
if command -v nc >/dev/null 2>&1; then
  if nc -z -w 5 "$TARGET" "$PORT" 2>/dev/null; then echo "reachable=yes"; exit 0; fi
elif command -v timeout >/dev/null 2>&1; then
  if timeout 5 sh -c "exec 3<>/dev/tcp/$TARGET/$PORT" 2>/dev/null; then echo "reachable=yes"; exit 0; fi
fi
echo "reachable=no"
`, shellsafe.Quote(targetIP), port)
}

// sortedPairs returns a map's entries in key order, so a generated command is
// stable and two runs produce the same thing.
func sortedPairs(m map[string]string) [][2]string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	out := make([][2]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, [2]string{k, m[k]})
	}
	return out
}
