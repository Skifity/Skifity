#!/bin/sh
# Skifity installer.
#
#   curl -fsSL https://raw.githubusercontent.com/<repo>/<version>/installer/install.sh | sudo sh
#
# No release has been tagged yet, so there is no <version> to put in that URL.
# Until there is, install from a clone:
#
#   make image
#   sudo SKIFITY_IMAGE=<your image> sh installer/install.sh
#
# Turns a fresh Ubuntu or Debian server into a Skifity control plane: k3s, the
# panel, and a URL to open. It is safe to run again: every step checks what is
# already there before changing anything.
#
# Everything it does is written to /var/log/skifity-install.log, so a failure
# can be read afterwards rather than reconstructed from a scrolled-off terminal.
#
# Non-interactive use: set the variables below and it never asks anything.
#
#   SKIFITY_VERSION       image tag to install (default: latest)
#   SKIFITY_IMAGE         full image reference, overrides SKIFITY_VERSION
#   SKIFITY_DOMAIN        the domain the panel will answer on
#   SKIFITY_ACME_EMAIL    the address Let's Encrypt sends expiry warnings to
#   SKIFITY_ACME_STAGING  set to 1 to use Let's Encrypt's staging server
#   SKIFITY_CHANNEL       k3s channel (default: stable)
#   SKIFITY_POD_NETWORK   wireguard-native or vxlan; default: wireguard-native
#                         when the kernel has the module, vxlan otherwise
#   SKIFITY_CLI_URL       where to get the CLI when the panel cannot serve it
#   SKIFITY_SKIP_K3S      set to 1 when k3s is already installed and configured
#   SKIFITY_ASSUME_YES    set to 1 to answer every prompt with yes
#
# POSIX sh on purpose: this has to run on a minimal image where bash may not
# be installed.

set -eu

# Where this project publishes. Moving it to another repository or
# organisation is this one line: the image, the manifests and the links in
# every message below are derived from it, and nothing else names a host.
PROJECT_REPO="${SKIFITY_REPO:-Skifity/Skifity}"

# The newest published release, set by the commit that tags one — the release
# workflow refuses to build a tag whose installer disagrees with it. While it
# is empty nothing has been published, and check_release says so before
# anything on this machine changes rather than after k3s is installed.
RELEASED_VERSION=""
VERSION="${SKIFITY_VERSION:-$RELEASED_VERSION}"

# ghcr.io wants a lowercase path and a repository name keeps its owner's
# capitals, so it is lowered here rather than assumed.
IMAGE_REPO="ghcr.io/$(printf '%s' "$PROJECT_REPO" | tr '[:upper:]' '[:lower:]')"
IMAGE="${SKIFITY_IMAGE:-${IMAGE_REPO}:${VERSION}}"

# The manifests come from the release being installed, not from a branch. A
# branch moves; an install that pulled v1's image and main's objects would
# apply a Deployment the image has never seen. This also means no default
# branch has to exist for an install to work.
MANIFEST_BASE="${SKIFITY_MANIFEST_BASE:-https://raw.githubusercontent.com/${PROJECT_REPO}/${VERSION}/deploy}"
NAMESPACE="skifity-system"
CONFIG_DIR="/etc/skifity"
DATA_DIR="/var/lib/skifity"
LOG_FILE="/var/log/skifity-install.log"
MANIFEST_DIR="/var/lib/skifity/manifests"
K3S_CHANNEL="${SKIFITY_CHANNEL:-stable}"
# The pod network. Decided in pick_pod_network below, unless it is set here.
POD_NETWORK="${SKIFITY_POD_NETWORK:-}"
KUBECONFIG_PATH="/etc/rancher/k3s/k3s.yaml"
ISSUER="skifity-letsencrypt"
# The in-cluster registry built images are pushed to and pulled from. These
# three values must match internal/kube/naming.go; a Go test checks that they do.
BUILDS_NAMESPACE="skifity-builds"
REGISTRY_HOST="skifity-registry.${BUILDS_NAMESPACE}.svc.cluster.local:5000"
REGISTRY_NODE_PORT=30500
CERT_MANAGER_URL="https://github.com/cert-manager/cert-manager/releases/download/v1.21.2/cert-manager.yaml"
# The panel runs as distroless's nonroot user; its directories must be readable
# and writable by that user and by nobody else.
RUN_UID=65532
RUN_GID=65532

# Minimums, chosen so that a 1 GB VPS is usable rather than technically bootable.
MIN_MEMORY_MB=900
MIN_DISK_GB=8

# --- output -----------------------------------------------------------------

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	BOLD=$(printf '\033[1m'); DIM=$(printf '\033[2m'); RED=$(printf '\033[31m')
	GREEN=$(printf '\033[32m'); YELLOW=$(printf '\033[33m'); RESET=$(printf '\033[0m')
else
	BOLD=''; DIM=''; RED=''; GREEN=''; YELLOW=''; RESET=''
fi

log() {
	printf '%s %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*" >>"$LOG_FILE" 2>/dev/null || true
}

say() { printf '%s\n' "$*"; log "$*"; }
step() { printf '%s==>%s %s\n' "$BOLD" "$RESET" "$*"; log "STEP $*"; }
ok() { printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$*"; log "OK $*"; }
note() { printf '  %s%s%s\n' "$DIM" "$*" "$RESET"; log "NOTE $*"; }
warn() { printf '  %s!%s %s\n' "$YELLOW" "$RESET" "$*"; log "WARN $*"; }

# fail prints a problem the way the panel would: what happened, what it means,
# and what to do about it. A one-line error at this stage leaves someone with a
# half-installed server and nowhere to go.
fail() {
	printf '\n%s%sInstallation stopped%s\n\n' "$BOLD" "$RED" "$RESET" >&2
	printf '%s\n' "$1" >&2
	if [ "${2:-}" != "" ]; then
		printf '\n%sWhat to do%s\n%s\n' "$BOLD" "$RESET" "$2" >&2
	fi
	printf '\nThe full log is at %s\n' "$LOG_FILE" >&2
	log "FAILED $1"
	exit 1
}

confirm() {
	[ "${SKIFITY_ASSUME_YES:-}" = "1" ] && return 0
	[ -t 0 ] || return 0
	printf '  %s [y/N] ' "$1"
	read -r answer || answer=""
	case "$answer" in
	y | Y | yes | YES) return 0 ;;
	*) return 1 ;;
	esac
}

have() { command -v "$1" >/dev/null 2>&1; }

# --- preflight --------------------------------------------------------------

# An install needs two things this script cannot invent: an image to run, and
# the Kubernetes objects that go with it. Called by preflight before anything
# on this machine changes, because the alternative is installing k3s, changing
# the firewall and writing to /etc, and only then failing on a pull — leaving a
# half-built cluster on somebody's server.
check_release() {
	if [ -z "${SKIFITY_IMAGE:-}" ] && [ -z "$VERSION" ]; then
		fail \
			"No Skifity release has been published yet, so there is no image to install." \
			"Build one from the repository and point the installer at it:

  git clone https://github.com/${PROJECT_REPO}.git && cd ${PROJECT_REPO##*/}
  make image                     # builds and tags a local image
  sudo SKIFITY_IMAGE=<your image> sh installer/install.sh

Run from inside a clone it reads deploy/*.yaml from disk, so no manifest is
fetched either. Nothing on this server has been changed."
	fi

	# An image was named but no release and no manifests: the objects would be
	# fetched from a URL with no version in it, which is a 404 after k3s is
	# already installed.
	if [ -z "$VERSION" ] && [ -z "${SKIFITY_MANIFEST_BASE:-}" ] && [ ! -f "${SOURCE_DIR:-.}/deploy/panel.yaml" ]; then
		fail \
			"SKIFITY_IMAGE names an image, but there is nowhere to read the Kubernetes objects from." \
			"Run this script from inside a clone, so deploy/*.yaml is read from disk:

  git clone https://github.com/${PROJECT_REPO}.git && cd ${PROJECT_REPO##*/}
  sudo SKIFITY_IMAGE=<your image> sh installer/install.sh

Or point SKIFITY_MANIFEST_BASE at a copy of deploy/. Nothing on this server has
been changed."
	fi
}

preflight() {
	step "Checking this server"

	[ "$(id -u)" = "0" ] || fail \
		"This installer has to run as root: it installs k3s and writes to /etc." \
		"Run it again with sudo:

  sudo sh installer/install.sh"

	mkdir -p "$(dirname "$LOG_FILE")" 2>/dev/null || true
	: >>"$LOG_FILE" 2>/dev/null || LOG_FILE=/dev/null
	log "skifity installer starting, image ${IMAGE}"

	check_release

	OS_NAME="unknown"; OS_VERSION=""
	if [ -r /etc/os-release ]; then
		# shellcheck disable=SC1091
		. /etc/os-release
		OS_NAME="${ID:-unknown}"
		OS_VERSION="${VERSION_ID:-}"
	fi
	case "$OS_NAME" in
	ubuntu | debian) ok "${PRETTY_NAME:-$OS_NAME $OS_VERSION}" ;;
	*)
		warn "This is $OS_NAME, and Skifity is tested on Ubuntu 24.04 and Debian 12."
		confirm "Carry on anyway?" || fail \
			"Stopped at your request." \
			"Install on Ubuntu 24.04 or Debian 12, or set SKIFITY_ASSUME_YES=1 to skip this question."
		;;
	esac

	ARCH=$(uname -m)
	case "$ARCH" in
	x86_64 | amd64 | aarch64 | arm64) ok "Architecture $ARCH" ;;
	*) fail \
		"Skifity does not have builds for $ARCH." \
		"Use a 64-bit x86 or ARM server. 32-bit and other architectures are not supported." ;;
	esac

	MEMORY_MB=$(awk '/MemTotal/ {print int($2 / 1024)}' /proc/meminfo 2>/dev/null || echo 0)
	if [ "$MEMORY_MB" -lt "$MIN_MEMORY_MB" ]; then
		fail \
			"This server has ${MEMORY_MB} MB of memory, and Kubernetes plus the panel need about ${MIN_MEMORY_MB} MB before your apps get anything." \
			"Use a server with at least 1 GB of memory. 2 GB is a comfortable starting point."
	fi
	ok "Memory ${MEMORY_MB} MB"

	DISK_GB=$(df -P -k / | awk 'NR==2 {print int($4 / 1024 / 1024)}')
	if [ "$DISK_GB" -lt "$MIN_DISK_GB" ]; then
		fail \
			"There is ${DISK_GB} GB free on /, and a working install needs about ${MIN_DISK_GB} GB for the container images alone." \
			"Free some space, or move to a server with a larger disk, and run this again."
	fi
	ok "Disk ${DISK_GB} GB free"

	if [ ! -d /run/systemd/system ]; then
		fail \
			"This server is not running systemd, and k3s installs itself as a systemd service." \
			"Use a normal Ubuntu or Debian server. Alpine and anything else on OpenRC will not work this way, and neither will a container with no init system, such as an unprivileged LXC or a Docker container."
	fi
	ok "systemd is running"

	# The memory cgroup controller. The kubelet will not start without it, and
	# what it prints on the way out is about cgroups rather than about the one
	# line somebody has to change. Raspberry Pi OS ships with it switched off,
	# and this installer supports arm64, so it is a path people will take.
	memory_cgroup=unknown
	if [ -r /sys/fs/cgroup/cgroup.controllers ]; then
		grep -qw memory /sys/fs/cgroup/cgroup.controllers && memory_cgroup=yes || memory_cgroup=no
	elif [ -r /proc/cgroups ]; then
		awk '$1 == "memory" && $4 == 1 {found=1} END {exit !found}' /proc/cgroups &&
			memory_cgroup=yes || memory_cgroup=no
	fi
	if [ "$memory_cgroup" = no ]; then
		fail \
			"The memory cgroup controller is switched off on this server, and the kubelet cannot start without it." \
			"On Raspberry Pi OS and Ubuntu for the Pi, add this to the end of the single line in /boot/firmware/cmdline.txt and reboot:

  cgroup_memory=1 cgroup_enable=memory

On other systems, look for cgroup_disable=memory on the kernel command line."
	fi
	[ "$memory_cgroup" = yes ] && ok "The memory cgroup is enabled"

	for port in 80 443 6443; do
		if port_in_use "$port"; then
			# 6443 already listening usually means k3s is installed, which is fine.
			if [ "$port" = "6443" ] && [ -f "$KUBECONFIG_PATH" ]; then
				continue
			fi
			fail \
				"Something is already listening on port ${port}, and Skifity needs it to serve your apps." \
				"Find it with:

  ss -lptn 'sport = :${port}'

Stop that service, or move it to another port, and run this again. A web server such as nginx or Apache installed by your provider is the usual cause."
		fi
	done
	ok "Ports 80, 443 and 6443 are free"

	have curl || fail \
		"The installer needs curl, and it is not on this server." \
		"Install it first:

  apt-get update && apt-get install -y curl"
	ok "curl is available"
}

port_in_use() {
	if have ss; then
		ss -lntH "sport = :$1" 2>/dev/null | grep -q . && return 0
		return 1
	fi
	if have netstat; then
		netstat -lnt 2>/dev/null | awk '{print $4}' | grep -qE "[:.]$1\$" && return 0
		return 1
	fi
	# No way to tell. k3s will report the conflict itself if there is one.
	return 1
}

# --- k3s --------------------------------------------------------------------

install_k3s() {
	step "Installing Kubernetes"

	if [ "${SKIFITY_SKIP_K3S:-}" = "1" ]; then
		note "Skipped: SKIFITY_SKIP_K3S is set"
		return 0
	fi

	# Before the early return below: an install that is being re-run still has
	# to end up with the mirror configured.
	configure_registry_mirror

	if have k3s && systemctl is-active --quiet k3s 2>/dev/null; then
		ok "k3s is already installed and running"
		return 0
	fi

	pick_pod_network

	note "This downloads and starts k3s, which takes a minute or two."
	# --cluster-init starts embedded etcd even on one node, so a second and third
	# control plane server can join later without rebuilding the cluster.
	curl -fsSL https://get.k3s.io >/tmp/skifity-k3s-install.sh 2>>"$LOG_FILE" || fail \
		"Could not download the k3s installer from get.k3s.io." \
		"Check that this server can reach the internet:

  curl -fsSL https://get.k3s.io | head"

	INSTALL_K3S_CHANNEL="$K3S_CHANNEL" \
		INSTALL_K3S_EXEC="server --cluster-init --flannel-backend=${POD_NETWORK} --write-kubeconfig-mode=0600 --secrets-encryption" \
		sh /tmp/skifity-k3s-install.sh >>"$LOG_FILE" 2>&1 || fail \
		"k3s did not install." \
		"The last lines of ${LOG_FILE} say why. The usual causes are no outbound network access to get.k3s.io, or a kernel without the modules k3s needs.

Try the download on its own to see the error:

  sh /tmp/skifity-k3s-install.sh"

	rm -f /tmp/skifity-k3s-install.sh
	ok "k3s installed"
}

# pick_pod_network decides how pods on different servers reach each other.
#
# WireGuard encrypts that traffic. On a single server it costs nothing, and it
# means adding a server over the public internet later is not a change of
# security model — so it is the default whenever the kernel can do it.
#
# The fallback is real, and it is only free here, on the first node: there is no
# cluster yet to disagree with. Once this is chosen the panel is told about it
# and installs every server added later the same way, because a node that joins
# with the other backend joins without complaint and then never exchanges a
# packet with the rest.
pick_pod_network() {
	if [ -n "$POD_NETWORK" ]; then
		note "Pod network: ${POD_NETWORK} (SKIFITY_POD_NETWORK)"
		return 0
	fi
	if modprobe wireguard 2>>"$LOG_FILE" || lsmod 2>/dev/null | grep -q '^wireguard' ||
		[ -d /sys/module/wireguard ]; then
		POD_NETWORK="wireguard-native"
		note "Pod network: wireguard-native, so traffic between servers is encrypted"
		return 0
	fi
	POD_NETWORK="vxlan"
	note "This kernel has no WireGuard module, so the pod network will be vxlan."
	note "Traffic between servers will not be encrypted. To get encryption,"
	note "install it first and run this again:  apt-get install -y wireguard-tools"
}

# containerd runs on the host, not in the cluster. It cannot resolve the
# registry's Kubernetes Service name, and it refuses plain HTTP to anything
# that is not loopback, so an image the panel builds could never be pulled.
# The mirror sends that name to the registry's NodePort on this machine.
#
# It is written before k3s starts, because that is when k3s reads it.
configure_registry_mirror() {
	mkdir -p /etc/rancher/k3s
	cat >/etc/rancher/k3s/registries.yaml.new <<EOF
# Written by Skifity. Do not edit.
mirrors:
  "${REGISTRY_HOST}":
    endpoint:
      - "http://127.0.0.1:${REGISTRY_NODE_PORT}"
EOF
	if cmp -s /etc/rancher/k3s/registries.yaml.new /etc/rancher/k3s/registries.yaml 2>/dev/null; then
		rm -f /etc/rancher/k3s/registries.yaml.new
		return 0
	fi
	mv /etc/rancher/k3s/registries.yaml.new /etc/rancher/k3s/registries.yaml
	ok "the container runtime knows where the panel's registry is"

	# k3s reads this at start-up only, so a change to a running node needs a
	# restart. A fresh install has not started yet and skips this.
	if systemctl is-active --quiet k3s 2>/dev/null; then
		note "Restarting k3s so it picks up the registry configuration"
		systemctl restart k3s >>"$LOG_FILE" 2>&1 || warn "k3s could not be restarted; do it by hand"
	fi
}

wait_for_cluster() {
	step "Waiting for the cluster"

	i=0
	while [ "$i" -lt 120 ]; do
		if kubectl get --raw /readyz >/dev/null 2>&1; then
			break
		fi
		i=$((i + 1))
		sleep 2
	done
	if [ "$i" -lt 120 ]; then
		ok "The API server is answering"
	else
		fail \
			"The Kubernetes API server did not come up within four minutes." \
			"Look at what k3s is saying:

  journalctl -u k3s -n 50 --no-pager"
	fi

	i=0
	while [ "$i" -lt 90 ]; do
		if [ "$(kubectl get nodes --no-headers 2>/dev/null | awk '$2 == "Ready"' | wc -l)" -ge 1 ]; then
			break
		fi
		i=$((i + 1))
		sleep 2
	done
	[ "$i" -lt 90 ] || fail \
		"The node never became ready." \
		"Look at what is wrong with:

  kubectl describe node
  journalctl -u k3s -n 50 --no-pager"

	NODE_NAME=$(kubectl get nodes --no-headers -o custom-columns=NAME:.metadata.name | head -n 1)
	ok "Node ${NODE_NAME} is ready"
}

# --- the panel --------------------------------------------------------------

prepare_directories() {
	step "Preparing the panel's directories"

	mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$MANIFEST_DIR"
	# 0700 and owned by the user the panel runs as: the master key lives here.
	chown "$RUN_UID:$RUN_GID" "$CONFIG_DIR" "$DATA_DIR" "$MANIFEST_DIR"
	chmod 0700 "$CONFIG_DIR" "$DATA_DIR"
	ok "$CONFIG_DIR and $DATA_DIR"
}

# The panel adds servers to this cluster, and a server can only join with the
# token the cluster was started with. The panel is not on the host and cannot
# read k3s's own copy, so it is put next to the master key, where the panel
# already looks and nothing else can.
#
# Without it the panel would have no token, invent one, and give the next
# server --cluster-init — building a second, separate cluster that looks like
# it worked until somebody wonders why their app is not running anywhere.
copy_cluster_token() {
	step "Giving the panel this cluster's join token"

	if [ ! -r /var/lib/rancher/k3s/server/token ]; then
		warn "k3s has not written its token yet; servers cannot be added until it is copied"
		note "Once k3s is running: cp /var/lib/rancher/k3s/server/token ${CONFIG_DIR}/cluster-token"
		return 0
	fi

	run cp /var/lib/rancher/k3s/server/token "$CONFIG_DIR/cluster-token"
	run chown "$RUN_UID:$RUN_GID" "$CONFIG_DIR/cluster-token"
	run chmod 0600 "$CONFIG_DIR/cluster-token"
	ok "the panel can add servers to this cluster"
}

generate_setup_token() {
	step "Preparing first-run setup"

	if [ -s "$CONFIG_DIR/setup-token" ]; then
		SETUP_TOKEN=$(cat "$CONFIG_DIR/setup-token")
		ok "Reusing the setup token from a previous run"
		return 0
	fi

	SETUP_TOKEN=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-40)
	[ -n "$SETUP_TOKEN" ] || fail \
		"Could not generate a setup token from /dev/urandom." \
		"That normally means /dev is not mounted properly. Reboot the server and try again."
	printf '%s\n' "$SETUP_TOKEN" >"$CONFIG_DIR/setup-token"
	chown "$RUN_UID:$RUN_GID" "$CONFIG_DIR/setup-token"
	chmod 0600 "$CONFIG_DIR/setup-token"
	ok "Setup token written to $CONFIG_DIR/setup-token"

	# The master key is deliberately not generated here: the panel creates it on
	# first start, so exactly one piece of code decides its format. This keeps
	# an installer that is one version out of step from writing a key the panel
	# cannot read.
	note "The panel will create its master key at $CONFIG_DIR/master.key on first start."
}

choose_hostname() {
	step "Working out the panel's address"

	PUBLIC_IP="${SKIFITY_PUBLIC_IP:-}"
	if [ -z "$PUBLIC_IP" ]; then
		PUBLIC_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="ExternalIP")].address}' 2>/dev/null || true)
	fi
	if [ -z "$PUBLIC_IP" ]; then
		PUBLIC_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null || true)
	fi
	[ -n "$PUBLIC_IP" ] || fail \
		"Could not work out this server's IP address." \
		"Set it yourself and run the installer again:

  SKIFITY_PUBLIC_IP=203.0.113.10 sh install.sh"

	if [ -n "${SKIFITY_DOMAIN:-}" ]; then
		PANEL_HOST="$SKIFITY_DOMAIN"
		PANEL_SCHEME="https"
		ok "The panel will answer on $PANEL_HOST"
		check_domain_points_here
	else
		# sslip.io resolves any address embedded in the name, so a brand new
		# server has a working hostname without anybody buying a domain.
		PANEL_HOST="$(printf '%s' "$PUBLIC_IP" | tr '.' '-').sslip.io"
		PANEL_SCHEME="http"
		ok "No domain given, so the panel will answer on $PANEL_HOST"
		note "Add your own domain later in Settings, and HTTPS is turned on for it automatically."
	fi
	PUBLIC_URL="${PANEL_SCHEME}://${PANEL_HOST}"
}

# check_domain_points_here says so, now, when the A record is missing.
#
# A certificate is issued by Let's Encrypt answering a challenge at this
# hostname, so a record that does not point here means no certificate — and the
# operator finds that out ten minutes later, in a cert-manager log, as a
# browser warning on a page they cannot open. Saying it here costs one lookup.
#
# It warns rather than stops: DNS takes minutes to propagate and it is entirely
# reasonable to install first and point the record afterwards.
check_domain_points_here() {
	have getent || {
		note "Point an A record for $PANEL_HOST at $PUBLIC_IP before opening it."
		return 0
	}
	resolved=$(getent ahostsv4 "$PANEL_HOST" 2>/dev/null | awk '{print $1}' | head -1)
	if [ -z "$resolved" ]; then
		warn "$PANEL_HOST does not resolve to anything yet."
		note "Create an A record for $PANEL_HOST pointing at $PUBLIC_IP. Until it exists,"
		note "Let's Encrypt cannot issue a certificate and the panel has no address to answer on."
	elif [ "$resolved" != "$PUBLIC_IP" ]; then
		warn "$PANEL_HOST resolves to $resolved, and this server is $PUBLIC_IP."
		note "Point the A record at $PUBLIC_IP. Until it does, Let's Encrypt will refuse the"
		note "certificate, because the challenge is answered by whatever is at $resolved."
	else
		ok "$PANEL_HOST already points at this server"
	fi
}

install_cert_manager() {
	[ "$PANEL_SCHEME" = "https" ] || return 0
	step "Installing certificate management"

	if kubectl get deployment cert-manager -n cert-manager >/dev/null 2>&1; then
		ok "cert-manager is already installed"
	else
		kubectl apply -f "$CERT_MANAGER_URL" >>"$LOG_FILE" 2>&1 || fail \
			"cert-manager did not install." \
			"Check that this server can reach github.com, then run the installer again. Without cert-manager the panel still works over plain HTTP; you can install it later from Settings."
		kubectl -n cert-manager rollout status deployment/cert-manager-webhook --timeout=180s >>"$LOG_FILE" 2>&1 || fail \
			"cert-manager installed but its webhook never became ready." \
			"Look at it with:

  kubectl -n cert-manager get pods"
		ok "cert-manager is ready"
	fi

	acme_server="https://acme-v02.api.letsencrypt.org/directory"
	[ "${SKIFITY_ACME_STAGING:-}" = "1" ] &&
		acme_server="https://acme-staging-v02.api.letsencrypt.org/directory"

	render deploy/cluster-issuer.yaml |
		sed -e "s|__EMAIL__|${SKIFITY_ACME_EMAIL:-}|g" \
			-e "s|__ACME_SERVER__|${acme_server}|g" \
			>"$MANIFEST_DIR/cluster-issuer.yaml"
	kubectl apply -f "$MANIFEST_DIR/cluster-issuer.yaml" >>"$LOG_FILE" 2>&1 || fail \
		"Could not create the certificate issuer." \
		"Run the installer again. If it keeps failing, the log at ${LOG_FILE} has what kubectl said."
	ok "Certificates will be issued by Let's Encrypt"
}

# render prints a manifest with the common placeholders filled in. The manifests
# come from the installer's own directory when it was downloaded as a file, and
# are embedded below when it was piped from curl.
render() {
	name=$(basename "$1")
	if [ -f "$SOURCE_DIR/$1" ]; then
		cat "$SOURCE_DIR/$1"
	else
		fetch_manifest "$name"
	fi |
		sed -e "s|__NAMESPACE__|${NAMESPACE}|g" \
			-e "s|__IMAGE__|${IMAGE}|g" \
			-e "s|__NODE__|${NODE_NAME}|g" \
			-e "s|__HOST__|${PANEL_HOST}|g" \
			-e "s|__PUBLIC_URL__|${PUBLIC_URL}|g" \
			-e "s|__POD_NETWORK__|${POD_NETWORK}|g" \
			-e "s|__CONFIG_DIR__|${CONFIG_DIR}|g" \
			-e "s|__DATA_DIR__|${DATA_DIR}|g" \
			-e "s|__ISSUER__|${ISSUER}|g"
}

fetch_manifest() {
	url="${MANIFEST_BASE}/$1"
	curl -fsSL "$url" || fail \
		"Could not download the manifest ${1} from ${url}." \
		"Check that this server can reach the internet, or clone the repository and run installer/install.sh from inside it so the manifests are read from disk."
}

install_panel() {
	step "Installing the panel"

	# Rendered to disk first, so that a failure to fetch a manifest stops the
	# installer rather than being swallowed by a pipeline, and so that the
	# operator can see exactly what was applied.
	render deploy/panel.yaml >"$MANIFEST_DIR/panel.yaml"
	kubectl apply -f "$MANIFEST_DIR/panel.yaml" >>"$LOG_FILE" 2>&1 || fail \
		"The panel's Kubernetes objects could not be applied." \
		"The log at ${LOG_FILE} has what kubectl said. Applying them again is safe."
	ok "Panel objects applied"

	if [ "$PANEL_SCHEME" = "https" ]; then
		render deploy/ingress-tls.yaml >"$MANIFEST_DIR/ingress.yaml"
	else
		render deploy/ingress.yaml >"$MANIFEST_DIR/ingress.yaml"
	fi
	kubectl apply -f "$MANIFEST_DIR/ingress.yaml" >>"$LOG_FILE" 2>&1 || fail \
		"The panel's route in could not be created." \
		"The log at ${LOG_FILE} has what kubectl said."
	ok "Route to the panel created"

	step "Waiting for the panel to start"
	note "The first start pulls the image, which takes a minute on a new server."
	kubectl -n "$NAMESPACE" rollout status deployment/skifity-panel --timeout=300s >>"$LOG_FILE" 2>&1 || fail \
		"The panel did not start within five minutes." \
		"See what it is waiting for:

  kubectl -n ${NAMESPACE} get pods
  kubectl -n ${NAMESPACE} describe pod -l app.kubernetes.io/component=panel
  kubectl -n ${NAMESPACE} logs -l app.kubernetes.io/component=panel

If the image could not be pulled, check that ${IMAGE} exists and that this server can reach the registry."
	ok "The panel is running"
}

install_cli() {
	step "Installing the command line tool"

	# From the panel that is now running on this machine, not from a releases
	# page. One binary is the panel, the CLI and the MCP server, so the file
	# answering this request is the file we want on the PATH — always present,
	# always the matching version, and it needs no internet at all. It used to
	# be fetched from a releases page that did not exist, so every install
	# ended with a warning and a link to nothing.
	#
	# The panel's image is distroless, so the binary cannot simply be copied
	# out of the container: there is no shell, no cat and no tar in there.
	port=$(kubectl -n "$NAMESPACE" get svc skifity-panel -o jsonpath='{.spec.ports[0].nodePort}' 2>>"$LOG_FILE" || true)
	if [ -n "$port" ] &&
		curl -fsS --max-time 120 "http://127.0.0.1:${port}/api/cli/download" -o /usr/local/bin/skifity.new 2>>"$LOG_FILE" &&
		[ -s /usr/local/bin/skifity.new ]; then
		chmod 0755 /usr/local/bin/skifity.new
		mv /usr/local/bin/skifity.new /usr/local/bin/skifity
		ok "skifity is on your PATH, from the panel itself"
		return 0
	fi
	rm -f /usr/local/bin/skifity.new

	# An override for an air-gapped install that mirrors the binaries itself.
	if [ -n "${SKIFITY_CLI_URL:-}" ] &&
		curl -fsSL --max-time 120 "$SKIFITY_CLI_URL" -o /usr/local/bin/skifity.new 2>>"$LOG_FILE" &&
		[ -s /usr/local/bin/skifity.new ]; then
		chmod 0755 /usr/local/bin/skifity.new
		mv /usr/local/bin/skifity.new /usr/local/bin/skifity
		ok "skifity is on your PATH, from ${SKIFITY_CLI_URL}"
		return 0
	fi
	rm -f /usr/local/bin/skifity.new

	warn "Could not install the command line tool; the panel itself is unaffected."
	note "The panel serves it: curl -fsS ${PUBLIC_URL}/api/cli/download -o /usr/local/bin/skifity && chmod +x /usr/local/bin/skifity"
}

finish() {
	printf '\n%s%sSkifity is installed.%s\n\n' "$BOLD" "$GREEN" "$RESET"
	# The token travels in the fragment, not the query string: a fragment is
	# never sent to the server, so opening this link cannot put the token into
	# an access log, a proxy or a Referer header. The page fills the field in
	# and clears the address bar.
	printf '  %sOpen this and the token is already filled in:%s\n\n' "$BOLD" "$RESET"
	printf '    %s%s/setup#token=%s%s\n\n' "$BOLD" "$PUBLIC_URL" "$SETUP_TOKEN" "$RESET"
	printf '  Or open %s/setup and paste it:\n' "$PUBLIC_URL"
	printf '    %s\n\n' "$SETUP_TOKEN"
	printf '  The token is also at %s on this server.\n' "$CONFIG_DIR/setup-token"
	printf '  It creates the first account and then stops working.\n\n'

	if [ "$PANEL_SCHEME" = "http" ]; then
		printf '  %sThis address has no certificate yet.%s Add a domain in Settings and\n' "$YELLOW" "$RESET"
		printf '  Skifity turns on HTTPS for it by itself.\n\n'
	else
		printf '  If the page does not load, the DNS record for %s\n' "$PANEL_HOST"
		printf '  may not have reached your computer yet. Give it a few minutes.\n\n'
	fi

	printf '  Log        %s\n' "$LOG_FILE"
	printf '  Uninstall  skifity-uninstall\n\n'
	log "install completed, panel at ${PUBLIC_URL}"
}

# --- main -------------------------------------------------------------------

# Sourcing this file with SKIFITY_INSTALLER_LIB=1 defines the functions above
# and stops here, so the smoke test can exercise them without installing
# anything. Nothing else reads this variable.
if [ "${SKIFITY_INSTALLER_LIB:-}" = "1" ]; then
	# `return` works when this file is sourced, which is exactly what the
	# smoke test does; `exit` is the fallback when it is run instead. A linter
	# reading one file cannot see the sourcing, so it reports the second half
	# as unreachable.
	# shellcheck disable=SC2317
	return 0 2>/dev/null || exit 0
fi

SOURCE_DIR="."
case "$0" in
*/*)
	# Running from a checkout: the manifests sit next to this script.
	SOURCE_DIR=$(cd "$(dirname "$0")/.." 2>/dev/null && pwd || echo ".")
	;;
esac

printf '\n%sSkifity%s  self-hosted apps, powered by Kubernetes\n\n' "$BOLD" "$RESET"

preflight
install_k3s

# Every kubectl call from here on talks to the cluster this installer just made.
export KUBECONFIG="$KUBECONFIG_PATH"
have kubectl || PATH="/usr/local/bin:$PATH"
have kubectl || fail \
	"kubectl is not on this server, and k3s should have installed it." \
	"Check that /usr/local/bin is on your PATH, or use the copy k3s ships:

  k3s kubectl get nodes"

wait_for_cluster
prepare_directories
copy_cluster_token
generate_setup_token
choose_hostname
install_cert_manager
install_panel
install_cli
finish
