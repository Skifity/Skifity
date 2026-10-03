#!/bin/sh
# Skifity installer.
#
#   curl -fsSL https://github.com/<repo>/releases/latest/download/install.sh | sudo sh
#
# That link is always the newest release's installer: every release has this
# file attached, with RELEASED_VERSION below set to that release, so it
# installs the image and the Kubernetes objects of the release it came from.
# One release exactly is releases/download/<tag>/install.sh. Options go after
# `sh -s --`:
#
#   curl -fsSL .../install.sh | sudo sh -s -- --domain panel.example.com
#
# To install an image you built yourself, run it from inside a clone, which
# reads deploy/*.yaml from disk:
#
#   make image
#   sudo sh installer/install.sh --image <your image>
#
# Turns a fresh Ubuntu or Debian server into a Skifity control plane: k3s, the
# panel, and a URL to open. It is safe to run again: every step checks what is
# already there before changing anything, and a second run keeps the address
# the panel already has unless it is told otherwise. Two copies cannot run at
# once, and one that is interrupted can simply be started again.
#
# Everything it does is written to /var/log/skifity-install.log, so a failure
# can be read afterwards rather than reconstructed from a scrolled-off terminal.
#
# Options, each with an environment variable that does the same:
#
#   --domain NAME        SKIFITY_DOMAIN        the domain the panel answers on, with HTTPS
#   --email ADDRESS      SKIFITY_ACME_EMAIL    the contact address registered with Let's Encrypt
#   --staging            SKIFITY_ACME_STAGING=1  use Let's Encrypt's staging server
#   --public-ip ADDRESS  SKIFITY_PUBLIC_IP     the address the internet reaches this server on
#   --version TAG        SKIFITY_VERSION       the release to install (default: RELEASED_VERSION below;
#                                              `latest` asks GitHub for the newest)
#   --image REFERENCE    SKIFITY_IMAGE         a full image reference, overriding --version
#   --pod-network NAME   SKIFITY_POD_NETWORK   wireguard-native or vxlan; default: wireguard-native
#                                              when the kernel has the module, vxlan otherwise
#   --channel NAME       SKIFITY_CHANNEL       the k3s channel (default: stable)
#   --skip-k3s           SKIFITY_SKIP_K3S=1    k3s is already installed and configured
#   --skip-firewall      SKIFITY_SKIP_FIREWALL=1  leave the host firewall alone
#   --yes, -y            SKIFITY_ASSUME_YES=1  never ask anything
#   --help, -h                                 print the options and exit
#
#   SKIFITY_CLI_URL       where to get the CLI when the panel cannot serve it
#
# Without --yes it asks at most two things, and only when there is a terminal
# to ask on: a domain for the panel on a fresh install, and whether to carry on
# on a system it is not tested on. With no terminal and no --yes, the second is
# a refusal rather than a guess.
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
RELEASED_VERSION="v0.1.0"

# derive_release works out what is installed and where its objects come from.
# It runs when this file is read and again after the options are parsed,
# because --version and --image change all of it.
derive_release() {
	VERSION="${SKIFITY_VERSION:-$RELEASED_VERSION}"

	# ghcr.io wants a lowercase path and a repository name keeps its owner's
	# capitals, so it is lowered here rather than assumed.
	IMAGE_REPO="ghcr.io/$(printf '%s' "$PROJECT_REPO" | tr '[:upper:]' '[:lower:]')"
	IMAGE="${SKIFITY_IMAGE:-${IMAGE_REPO}:${VERSION}}"

	# The manifests come from the release being installed, not from a branch. A
	# branch moves; an install that pulled v1's image and main's objects would
	# apply a Deployment the image has never seen. This also means no default
	# branch has to exist for an install to work.
	RELEASE_BASE="https://raw.githubusercontent.com/${PROJECT_REPO}/${VERSION}"
	MANIFEST_BASE="${SKIFITY_MANIFEST_BASE:-${RELEASE_BASE}/deploy}"
}
derive_release

NAMESPACE="skifity-system"
CONFIG_DIR="/etc/skifity"
DATA_DIR="/var/lib/skifity"
LOG_FILE="/var/log/skifity-install.log"
MANIFEST_DIR="/var/lib/skifity/manifests"
K3S_CHANNEL="${SKIFITY_CHANNEL:-stable}"
# The pod network. Decided in pick_pod_network below, unless it is set here.
POD_NETWORK="${SKIFITY_POD_NETWORK:-}"
KUBECONFIG_PATH="/etc/rancher/k3s/k3s.yaml"
# Where k3s reads registries.yaml and config.yaml from, at start-up only.
K3S_CONFIG_DIR="/etc/rancher/k3s"
# The unit k3s's own installer writes, with the flags it was started with.
K3S_UNIT_PATH="/etc/systemd/system/k3s.service"
# The token a server needs to join this cluster, written by k3s.
K3S_TOKEN_PATH="/var/lib/rancher/k3s/server/token"
# k3s applies every file here, and applies one again when it changes.
K3S_MANIFESTS_DIR="/var/lib/rancher/k3s/server/manifests"
# Where the command line tool and the uninstaller go.
CLI_PATH="/usr/local/bin/skifity"
UNINSTALLER_PATH="/usr/local/bin/skifity-uninstall"
# Held while an install runs, so two cannot interleave. /run is cleared on
# boot, so a lock cannot outlive the machine it was taken on.
LOCK_DIR="/run/skifity-install.lock"
# What the preflight reads. Named so the smoke test can point them elsewhere.
SYSTEMD_DIR="/run/systemd/system"
OS_RELEASE="/etc/os-release"
MEMINFO="/proc/meminfo"
CGROUP_CONTROLLERS="/sys/fs/cgroup/cgroup.controllers"
PROC_CGROUPS="/proc/cgroups"
# Where a question is asked and answered. Not stdin: under `curl | sh`, stdin
# is this script, and reading an answer from it would eat the next line.
TTY_DEV="/dev/tty"
ISSUER="skifity-letsencrypt"
# The in-cluster registry built images are pushed to and pulled from. These
# three values must match internal/kube/naming.go; a Go test checks that they do.
BUILDS_NAMESPACE="skifity-builds"
REGISTRY_HOST="skifity-registry.${BUILDS_NAMESPACE}.svc.cluster.local:5000"
REGISTRY_NODE_PORT=30500
# k3s's pod and service networks, which a host firewall has to trust. These
# must match internal/kube/namespace.go; a Go test checks that they do.
POD_CIDR="10.42.0.0/16"
SERVICE_CIDR="10.43.0.0/16"
CERT_MANAGER_URL="https://github.com/cert-manager/cert-manager/releases/download/v1.21.2/cert-manager.yaml"
# The panel runs as distroless's nonroot user; its directories must be readable
# and writable by that user and by nobody else.
RUN_UID=65532
RUN_GID=65532

# Minimums, chosen so that a 1 GB VPS is usable rather than technically bootable.
MIN_MEMORY_MB=900
MIN_DISK_GB=8
# Below this, with no swap, one build can take the server down with it.
COMFORTABLE_MEMORY_MB=1800

# State the steps below hand to each other. Set here so that every one of them
# exists under `set -u`, whichever steps ran.
SOURCE_DIR=""
TMP_DIR=""
LOCK_TAKEN=0
START_TIME=""
WARNINGS=0
NODE_NAME=""
NODE_IP=""
PUBLIC_IP=""
PANEL_HOST=""
PANEL_SCHEME=""
PUBLIC_URL=""
SETUP_TOKEN=""
# Whether the panel answered at its own address: yes, no or unknown.
PANEL_ANSWERS="unknown"
# Whether the first account exists yet: done, pending or unknown.
SETUP_STATE="unknown"

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
warn() {
	WARNINGS=$((WARNINGS + 1))
	printf '  %s!%s %s\n' "$YELLOW" "$RESET" "$*"
	log "WARN $*"
}

# fail prints a problem the way the panel would: what happened, what it means,
# and what to do about it. A one-line error at this stage leaves someone with a
# half-installed server and nowhere to go.
fail() {
	printf '\n%s%sInstallation stopped%s\n\n' "$BOLD" "$RED" "$RESET" >&2
	printf '%s\n' "$1" >&2
	if [ "${2:-}" != "" ]; then
		printf '\n%sWhat to do%s\n%s\n' "$BOLD" "$RESET" "$2" >&2
	fi
	printf '\nThe full log is at %s. Running the installer again is safe.\n' "$LOG_FILE" >&2
	log "FAILED $1"
	exit 1
}

have() { command -v "$1" >/dev/null 2>&1; }

# tty_usable says whether there is somebody to ask. /dev/tty exists on every
# system; opening it fails when there is no controlling terminal, which is the
# case in a provisioning script, a CI job or cloud-init.
tty_usable() { (exec <"$TTY_DEV") 2>/dev/null; }

# confirm asks a yes/no question on the terminal. With no terminal the answer
# is no: an installer that guesses yes on somebody's behalf, unattended, is
# making a decision nobody made. --yes is how an unattended run says yes.
confirm() {
	[ "${SKIFITY_ASSUME_YES:-}" = "1" ] && return 0
	tty_usable || return 1
	printf '  %s [y/N] ' "$1" >>"$TTY_DEV"
	answer=""
	read -r answer <"$TTY_DEV" || answer=""
	case "$answer" in
	y | Y | yes | YES | Yes) return 0 ;;
	*) return 1 ;;
	esac
}

# retry runs a command until it succeeds, for the steps that can fail for a
# moment just after something they depend on has started.
#
#   retry ATTEMPTS DELAY command [args...]
retry() {
	attempts=$1
	delay=$2
	shift 2
	attempt=1
	while ! "$@"; do
		[ "$attempt" -lt "$attempts" ] || return 1
		attempt=$((attempt + 1))
		sleep "$delay"
	done
}

# download fetches a URL to a file, never to a pipe: a transfer that breaks
# halfway is a failed download, not half a script handed to sh or half a
# manifest handed to kubectl. Transient failures are retried.
#
#   download URL FILE [MAX_SECONDS]
download() {
	curl -fsSL --connect-timeout 20 --retry 5 --retry-delay 2 --max-time "${3:-600}" \
		-o "$2" "$1" 2>>"$LOG_FILE"
}

# latest_release prints the newest published release's tag. It follows the
# redirect from /releases/latest to /releases/tag/<tag> rather than calling
# GitHub's API, which lets an address make sixty anonymous calls an hour — a
# limit a shared NAT address or a CI runner can already have spent.
latest_release() {
	landed=$(curl -fsSL --connect-timeout 20 --max-time 60 --retry 3 -o /dev/null \
		-w '%{url_effective}' "https://github.com/${PROJECT_REPO}/releases/latest" 2>/dev/null) || return 1
	case "$landed" in
	*/releases/tag/?*) ;;
	*) return 1 ;;
	esac
	tag=${landed##*/}
	printf '%s' "$tag" | grep -Eq '^[A-Za-z0-9._-]+$' || return 1
	printf '%s' "$tag"
}

# resolve_version turns --version latest into the tag it means, once, before
# anything is derived from it.
resolve_version() {
	[ "${SKIFITY_VERSION:-}" = "latest" ] || return 0
	resolved=$(latest_release) || fail \
		"Could not find out which Skifity release is the newest." \
		"Check that this server can reach github.com, or name the release:

  sudo sh install.sh --version <tag>

The releases are listed at https://github.com/${PROJECT_REPO}/releases."
	SKIFITY_VERSION=$resolved
}

# --- options ----------------------------------------------------------------

usage() {
	cat <<EOF
Skifity installer, release ${RELEASED_VERSION:-(unreleased)}

Turns this server into a Skifity control plane: k3s, the panel, and a URL to
open. Safe to run again.

Usage:
  curl -fsSL https://github.com/${PROJECT_REPO}/releases/latest/download/install.sh | sudo sh -s -- [options]
  sudo sh installer/install.sh [options]

Options:
  --domain NAME        the domain the panel answers on, with an HTTPS certificate
                       from Let's Encrypt; without one it answers on a free
                       sslip.io address over plain HTTP
  --email ADDRESS      the contact address registered with Let's Encrypt
  --staging            use Let's Encrypt's staging server, for trying things out
  --public-ip ADDRESS  the address this server is reached on, when the one it
                       finds is wrong
  --version TAG        the release to install (default: ${RELEASED_VERSION:-none},
                       the release this installer came from); \`latest\` asks
                       GitHub for the newest
  --image REFERENCE    a full image reference, overriding --version
  --pod-network NAME   wireguard-native or vxlan (default: wireguard-native when
                       the kernel has the module)
  --channel NAME       the k3s release channel (default: stable)
  --skip-k3s           use the k3s already installed here as it is
  --skip-firewall      leave the host firewall alone
  -y, --yes            never ask anything
  -h, --help           print this and exit

Each option has an environment variable as well: SKIFITY_DOMAIN,
SKIFITY_ACME_EMAIL, SKIFITY_ACME_STAGING=1, SKIFITY_PUBLIC_IP, SKIFITY_VERSION,
SKIFITY_IMAGE, SKIFITY_POD_NETWORK, SKIFITY_CHANNEL, SKIFITY_SKIP_K3S=1,
SKIFITY_SKIP_FIREWALL=1 and SKIFITY_ASSUME_YES=1.

The log is written to ${LOG_FILE}.
EOF
}

usage_error() {
	printf '%s\n\nRun with --help to see the options.\n' "$1" >&2
	exit 2
}

# normalise_domain takes what somebody would naturally paste — a URL, capitals,
# a trailing slash or dot — and returns the bare name.
normalise_domain() {
	name=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
	name=${name#http://}
	name=${name#https://}
	name=${name%%/*}
	name=${name%.}
	printf '%s' "$name"
}

valid_domain() {
	[ "${#1}" -le 253 ] &&
		printf '%s' "$1" | grep -Eq '^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$'
}

# The address is substituted into a manifest with sed, so it is held to what
# an address can contain, and nothing sed would read as an instruction.
valid_email() {
	printf '%s' "$1" | grep -Eq '^[A-Za-z0-9._%+-]+@([A-Za-z0-9-]+\.)+[A-Za-z]{2,}$'
}

valid_ipv4() {
	printf '%s' "$1" | grep -Eq '^([0-9]{1,3}\.){3}[0-9]{1,3}$' || return 1
	old_ifs=$IFS
	IFS=.
	# Split on the dots on purpose.
	# shellcheck disable=SC2086
	set -- $1
	IFS=$old_ifs
	for octet in "$@"; do
		[ "$octet" -le 255 ] || return 1
	done
}

valid_ipv6() {
	case "$1" in
	*:*:*) printf '%s' "$1" | grep -Eq '^[0-9A-Fa-f:.]+$' ;;
	*) return 1 ;;
	esac
}

parse_args() {
	while [ $# -gt 0 ]; do
		opt="$1"
		value=""
		inline=0
		case "$opt" in
		--*=*)
			value="${opt#*=}"
			opt="${opt%%=*}"
			inline=1
			;;
		esac

		case "$opt" in
		--domain | --email | --public-ip | --version | --image | --pod-network | --channel)
			if [ "$inline" = 0 ]; then
				[ $# -ge 2 ] || usage_error "$opt needs a value."
				value="$2"
				shift
			fi
			[ -n "$value" ] || usage_error "$opt needs a value."
			case "$opt" in
			--domain) SKIFITY_DOMAIN="$value" ;;
			--email) SKIFITY_ACME_EMAIL="$value" ;;
			--public-ip) SKIFITY_PUBLIC_IP="$value" ;;
			--version) SKIFITY_VERSION="$value" ;;
			--image) SKIFITY_IMAGE="$value" ;;
			--pod-network) SKIFITY_POD_NETWORK="$value" ;;
			--channel) SKIFITY_CHANNEL="$value" ;;
			esac
			;;
		--staging | --skip-k3s | --skip-firewall | --yes | -y | --help | -h)
			[ "$inline" = 0 ] || usage_error "$opt does not take a value."
			case "$opt" in
			--staging) SKIFITY_ACME_STAGING=1 ;;
			--skip-k3s) SKIFITY_SKIP_K3S=1 ;;
			--skip-firewall) SKIFITY_SKIP_FIREWALL=1 ;;
			--yes | -y) SKIFITY_ASSUME_YES=1 ;;
			--help | -h)
				usage
				exit 0
				;;
			esac
			;;
		*) usage_error "Unknown option: $1" ;;
		esac
		shift
	done
	validate_settings
}

# validate_settings checks every value before anything is changed, so a typo
# is a one-line message now rather than a broken certificate later.
validate_settings() {
	if [ -n "${SKIFITY_DOMAIN:-}" ]; then
		SKIFITY_DOMAIN=$(normalise_domain "$SKIFITY_DOMAIN")
		valid_domain "$SKIFITY_DOMAIN" ||
			usage_error "\"$SKIFITY_DOMAIN\" is not a domain name. Give one such as panel.example.com, without http:// or a path."
	fi
	if [ -n "${SKIFITY_ACME_EMAIL:-}" ]; then
		valid_email "$SKIFITY_ACME_EMAIL" ||
			usage_error "\"$SKIFITY_ACME_EMAIL\" is not an email address."
	fi
	if [ -n "${SKIFITY_PUBLIC_IP:-}" ]; then
		valid_ipv4 "$SKIFITY_PUBLIC_IP" || valid_ipv6 "$SKIFITY_PUBLIC_IP" ||
			usage_error "\"$SKIFITY_PUBLIC_IP\" is not an IP address."
	fi
	if [ -n "${SKIFITY_VERSION:-}" ]; then
		printf '%s' "$SKIFITY_VERSION" | grep -Eq '^[A-Za-z0-9._-]+$' ||
			usage_error "\"$SKIFITY_VERSION\" is not a release tag, such as ${RELEASED_VERSION:-v1.0.0}."
	fi
	if [ -n "${SKIFITY_IMAGE:-}" ]; then
		printf '%s' "$SKIFITY_IMAGE" | grep -Eq '^[A-Za-z0-9._/:@-]+$' ||
			usage_error "\"$SKIFITY_IMAGE\" is not an image reference, such as registry.example.com/skifity:1.0."
	fi
	case "${SKIFITY_POD_NETWORK:-}" in
	"" | wireguard-native | vxlan) ;;
	*) usage_error "The pod network is wireguard-native or vxlan, not \"$SKIFITY_POD_NETWORK\"." ;;
	esac
	if [ -n "${SKIFITY_CHANNEL:-}" ]; then
		printf '%s' "$SKIFITY_CHANNEL" | grep -Eq '^[A-Za-z0-9._+-]+$' ||
			usage_error "\"$SKIFITY_CHANNEL\" is not a k3s channel, such as stable or latest."
	fi
	K3S_CHANNEL="${SKIFITY_CHANNEL:-stable}"
	POD_NETWORK="${SKIFITY_POD_NETWORK:-}"
}

# --- one at a time ------------------------------------------------------------

# take_lock makes sure two installs never run at once. Two would both install
# k3s, both apply the panel and both write the setup token, and what came out
# would depend on which one wrote last. mkdir is atomic, which is all a lock
# needs; the process id inside it tells a live lock from a stale one.
take_lock() {
	if [ ! -d "$(dirname "$LOCK_DIR")" ]; then
		return 0
	fi
	if mkdir "$LOCK_DIR" 2>/dev/null; then
		printf '%s\n' "$$" >"$LOCK_DIR/pid"
		LOCK_TAKEN=1
		return 0
	fi
	holder=$(cat "$LOCK_DIR/pid" 2>/dev/null || true)
	if [ -n "$holder" ] && kill -0 "$holder" 2>/dev/null; then
		fail \
			"Another copy of the installer is already running on this server, as process ${holder}." \
			"Let it finish: two installs at once would undo each other. If it is stuck, stop it and run this again:

  kill ${holder}"
	fi
	# Left behind by an install that was killed outright. Its process is gone,
	# so the lock is nobody's.
	rm -rf "$LOCK_DIR"
	mkdir "$LOCK_DIR" 2>/dev/null || fail \
		"Could not take the installer's lock at ${LOCK_DIR}." \
		"Another copy may have started at the same moment. Wait for it, or remove ${LOCK_DIR} and run this again."
	printf '%s\n' "$$" >"$LOCK_DIR/pid"
	LOCK_TAKEN=1
}

on_exit() {
	if [ -n "$TMP_DIR" ]; then
		rm -rf "$TMP_DIR"
	fi
	if [ "$LOCK_TAKEN" = 1 ]; then
		rm -rf "$LOCK_DIR"
	fi
}

# interrupted says, on Ctrl-C, the one thing somebody needs to know: every
# step checks what is there before it acts, so starting again is how to finish.
interrupted() {
	trap - INT TERM HUP
	printf '\n\n%sInterrupted.%s Nothing was left in a state a second run cannot finish:\n' "$BOLD" "$RESET" >&2
	printf 'run the installer again and it carries on from where this one stopped.\n' >&2
	log "INTERRUPTED"
	exit 130
}

make_tmp_dir() {
	TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/skifity-install.XXXXXX") || fail \
		"Could not create a temporary directory." \
		"Check that ${TMPDIR:-/tmp} exists and has free space, then run this again."
}

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

# os_field reads one field of os-release in a subshell. Sourcing it here would
# set VERSION, ID and NAME in this script — and VERSION is the release being
# installed, which the next message would then name as "24.04 LTS (Noble Numbat)".
os_field() {
	# shellcheck disable=SC1090
	(
		. "$OS_RELEASE" 2>/dev/null || exit 0
		case "$1" in
		ID) printf '%s' "${ID:-}" ;;
		PRETTY_NAME) printf '%s' "${PRETTY_NAME:-}" ;;
		esac
	) || true
}

preflight() {
	step "Checking this server"

	[ "$(id -u)" = "0" ] || fail \
		"This installer has to run as root: it installs k3s and writes to /etc." \
		"Run it again with sudo:

  sudo sh installer/install.sh"

	mkdir -p "$(dirname "$LOG_FILE")" 2>/dev/null || true
	# The log can hold what kubectl and the k3s installer said, so it is
	# readable by root alone.
	if (umask 077 && : >>"$LOG_FILE") 2>/dev/null; then
		chmod 0600 "$LOG_FILE" 2>/dev/null || true
	else
		LOG_FILE=/dev/null
	fi
	log "skifity installer starting: release ${RELEASED_VERSION:-none}, image ${IMAGE}"

	check_release

	OS_ID=unknown
	OS_PRETTY=""
	if [ -r "$OS_RELEASE" ]; then
		OS_ID=$(os_field ID)
		OS_PRETTY=$(os_field PRETTY_NAME)
		[ -n "$OS_ID" ] || OS_ID=unknown
	fi
	case "$OS_ID" in
	ubuntu | debian) ok "${OS_PRETTY:-$OS_ID}" ;;
	*)
		warn "This is ${OS_PRETTY:-$OS_ID}, and Skifity is tested on Ubuntu 24.04 and Debian 12."
		confirm "Carry on anyway?" || fail \
			"Stopped before changing anything: this system is not one Skifity is tested on, and nobody confirmed carrying on." \
			"Install on Ubuntu 24.04 or Debian 12, or run this again with --yes to carry on anyway."
		;;
	esac

	ARCH=$(uname -m)
	case "$ARCH" in
	x86_64 | amd64 | aarch64 | arm64) ok "Architecture $ARCH" ;;
	*) fail \
		"Skifity does not have builds for $ARCH." \
		"Use a 64-bit x86 or ARM server. 32-bit and other architectures are not supported." ;;
	esac

	MEMORY_MB=$(awk '/^MemTotal:/ {print int($2 / 1024)}' "$MEMINFO" 2>/dev/null || true)
	SWAP_MB=$(awk '/^SwapTotal:/ {print int($2 / 1024)}' "$MEMINFO" 2>/dev/null || true)
	MEMORY_MB=${MEMORY_MB:-0}
	SWAP_MB=${SWAP_MB:-0}
	if [ "$MEMORY_MB" -lt "$MIN_MEMORY_MB" ]; then
		fail \
			"This server has ${MEMORY_MB} MB of memory, and Kubernetes plus the panel need about ${MIN_MEMORY_MB} MB before your apps get anything." \
			"Use a server with at least 1 GB of memory. 2 GB is a comfortable starting point."
	fi
	ok "Memory ${MEMORY_MB} MB"
	# Not a refusal: a 1 GB server runs the panel and small apps. But a build
	# is the hungriest thing it will do, and without swap the kernel's answer
	# to running out is to kill something — possibly the panel.
	if [ "$MEMORY_MB" -lt "$COMFORTABLE_MEMORY_MB" ] && [ "$SWAP_MB" -eq 0 ]; then
		warn "With ${MEMORY_MB} MB of memory and no swap, building an app can run this server out of memory."
		note "Two gigabytes of swap is cheap insurance:"
		note "  fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile"
		note "  echo '/swapfile none swap sw 0 0' >> /etc/fstab"
	fi

	DISK_GB=$(df -P -k / | awk 'NR==2 {print int($4 / 1024 / 1024)}')
	if [ "$DISK_GB" -lt "$MIN_DISK_GB" ]; then
		fail \
			"There is ${DISK_GB} GB free on /, and a working install needs about ${MIN_DISK_GB} GB for the container images alone." \
			"Free some space, or move to a server with a larger disk, and run this again."
	fi
	ok "Disk ${DISK_GB} GB free"

	# Only the k3s this script installs needs systemd. A k3s somebody already
	# runs their own way (SKIFITY_SKIP_K3S) is theirs to start and stop, and
	# refusing it for want of an init system the script will never call was
	# refusing an install that would have worked.
	if [ "${SKIFITY_SKIP_K3S:-}" = "1" ]; then
		note "k3s is already installed (--skip-k3s), so systemd is not needed"
	elif [ ! -d "$SYSTEMD_DIR" ]; then
		fail \
			"This server is not running systemd, and k3s installs itself as a systemd service." \
			"Use a normal Ubuntu or Debian server. Alpine and anything else on OpenRC will not work this way, and neither will a container with no init system, such as an unprivileged LXC or a Docker container.

If k3s is already installed and running here, run this again with --skip-k3s
and the installer uses it as it is."
	else
		ok "systemd is running"
	fi

	# The memory cgroup controller. The kubelet will not start without it, and
	# what it prints on the way out is about cgroups rather than about the one
	# line somebody has to change. Raspberry Pi OS ships with it switched off,
	# and this installer supports arm64, so it is a path people will take.
	memory_cgroup=unknown
	if [ -r "$CGROUP_CONTROLLERS" ]; then
		grep -qw memory "$CGROUP_CONTROLLERS" && memory_cgroup=yes || memory_cgroup=no
	elif [ -r "$PROC_CGROUPS" ]; then
		awk '$1 == "memory" && $4 == 1 {found=1} END {exit !found}' "$PROC_CGROUPS" &&
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

	check_clock
	check_docker
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

# check_clock warns about a clock nothing keeps right. Let's Encrypt refuses a
# request from a clock far enough out, every certificate the panel checks is
# checked against it, and the codes an authenticator app shows stop matching
# the ones the panel expects. None of them say "your clock is wrong".
check_clock() {
	have timedatectl || return 0
	synced=$(timedatectl show -p NTPSynchronized --value 2>/dev/null || true)
	if [ "$synced" = "no" ]; then
		warn "This server's clock is not synchronised, and certificates and sign-in codes depend on it."
		note "Turn time synchronisation on with:  timedatectl set-ntp true"
	fi
}

# check_docker mentions Docker when it is running. It is not in the way — k3s
# brings its own container runtime and the two run side by side — but what
# Docker runs is invisible to the panel and shares this server's memory.
check_docker() {
	have systemctl || return 0
	if systemctl is-active --quiet docker 2>/dev/null; then
		note "Docker is running here too. It can stay: k3s runs its own containers beside it,"
		note "but what Docker runs is not managed by Skifity and shares this server's memory."
	fi
}

# ask_for_domain offers HTTPS from the start, on a fresh install where somebody
# is there to answer. On a second run the panel already has its address, and
# asking again could only offer a way to lose it.
ask_for_domain() {
	[ -z "${SKIFITY_DOMAIN:-}" ] || return 0
	[ "${SKIFITY_ASSUME_YES:-}" != "1" ] || return 0
	[ ! -f "$KUBECONFIG_PATH" ] || return 0
	tty_usable || return 0

	{
		printf '\n  %sDo you have a domain for the panel?%s With one, it gets HTTPS straight away.\n' "$BOLD" "$RESET"
		printf '  Type it, such as panel.example.com, and point its A record at this server.\n'
		printf '  Or press Enter for a free sslip.io address over plain HTTP; a domain can be\n'
		printf '  added later.\n\n'
	} >>"$TTY_DEV"
	# Opened once for every answer, so a second answer is the next line rather
	# than the first one again.
	exec 3<"$TTY_DEV"
	tries=0
	while [ "$tries" -lt 3 ]; do
		printf '  Domain: ' >>"$TTY_DEV"
		answer=""
		read -r answer <&3 || answer=""
		if [ -z "$answer" ]; then
			exec 3<&-
			printf '\n' >>"$TTY_DEV"
			return 0
		fi
		answer=$(normalise_domain "$answer")
		if valid_domain "$answer"; then
			exec 3<&-
			SKIFITY_DOMAIN=$answer
			log "domain chosen at the prompt: $answer"
			printf '\n' >>"$TTY_DEV"
			return 0
		fi
		printf '  %s"%s" is not a domain name.%s\n' "$YELLOW" "$answer" "$RESET" >>"$TTY_DEV"
		tries=$((tries + 1))
	done
	exec 3<&-
	fail \
		"No domain name was entered." \
		"Run the installer again and type a domain such as panel.example.com, or press Enter for an sslip.io address."
}

# --- the host firewall ------------------------------------------------------

# configure_firewall lets HTTP, HTTPS and the cluster's own traffic through a
# firewall that is running on this server, and touches nothing when none is.
#
# Without it the install finishes, every check here passes, and the panel's
# address times out from everywhere else: ufw on Ubuntu and firewalld on the
# Red Hat family drop what they were not told about, and some providers' images
# (Oracle Cloud's, for one) ship iptables rules that reject everything but SSH.
# Pods reaching the API server and DNS through the host are refused as well,
# and that fails as a crash loop in a pod nobody installed by hand.
#
# Only 80 and 443 are opened to the world. The cluster's own ports are opened
# per server when a server is added (internal/provision, FirewallScript),
# never to the internet. This mirrors that script: the same three firewalls,
# each used the way its own users would, so the rules are visible in the tool
# the server's owner already looks at.
configure_firewall() {
	step "Checking the host firewall"
	if [ "${SKIFITY_SKIP_FIREWALL:-}" = "1" ]; then
		note "Left alone (--skip-firewall). Allow TCP 80 and 443, and traffic from"
		note "${POD_CIDR} and ${SERVICE_CIDR}, or the panel cannot be reached."
		return 0
	fi

	if have ufw && ufw status 2>/dev/null | head -n 1 | grep -qi 'status: active'; then
		# ufw is idempotent: adding a rule twice keeps one.
		ufw allow 80/tcp >>"$LOG_FILE" 2>&1 || true
		ufw allow 443/tcp >>"$LOG_FILE" 2>&1 || true
		ufw allow from "$POD_CIDR" to any >>"$LOG_FILE" 2>&1 || true
		ufw allow from "$SERVICE_CIDR" to any >>"$LOG_FILE" 2>&1 || true
		for iface in cni0 flannel.1 flannel-wg; do
			ufw allow in on "$iface" >>"$LOG_FILE" 2>&1 || true
		done
		ok "ufw lets in HTTP, HTTPS and the cluster's own networks"
	elif have firewall-cmd && firewall-cmd --state 2>/dev/null | grep -qi running; then
		firewall-cmd --permanent --add-port=80/tcp >>"$LOG_FILE" 2>&1 || true
		firewall-cmd --permanent --add-port=443/tcp >>"$LOG_FILE" 2>&1 || true
		for source in "$POD_CIDR" "$SERVICE_CIDR"; do
			firewall-cmd --permanent --zone=trusted --add-source="$source" >>"$LOG_FILE" 2>&1 || true
		done
		for iface in cni0 flannel.1 flannel-wg; do
			firewall-cmd --permanent --zone=trusted --add-interface="$iface" >>"$LOG_FILE" 2>&1 || true
		done
		# Nothing above is live until this.
		firewall-cmd --reload >>"$LOG_FILE" 2>&1 || true
		ok "firewalld lets in HTTP, HTTPS and the cluster's own networks"
	elif have iptables && iptables_rejects_by_default; then
		for port in 80 443; do
			iptables -C INPUT -p tcp --dport "$port" -j ACCEPT 2>/dev/null ||
				iptables -I INPUT -p tcp --dport "$port" -j ACCEPT 2>>"$LOG_FILE" || true
		done
		for source in "$POD_CIDR" "$SERVICE_CIDR"; do
			iptables -C INPUT -s "$source" -j ACCEPT 2>/dev/null ||
				iptables -I INPUT -s "$source" -j ACCEPT 2>>"$LOG_FILE" || true
		done
		# Saved where the tooling exists, so a reboot does not undo it.
		if have netfilter-persistent; then
			netfilter-persistent save >>"$LOG_FILE" 2>&1 || true
		elif have iptables-save && [ -d /etc/iptables ]; then
			iptables-save >/etc/iptables/rules.v4 2>>"$LOG_FILE" || true
		fi
		ok "iptables lets in HTTP, HTTPS and the cluster's own networks"
	else
		ok "No firewall on this server is in the way"
	fi
	note "A firewall at your provider, if there is one, has to allow TCP 80 and 443 too."
}

# iptables_rejects_by_default is true when INPUT ends by refusing what no rule
# let in. A rule that drops one address is somebody's decision and is left
# alone; only a policy, or a catch-all rule, makes this server unreachable.
iptables_rejects_by_default() {
	rules=$(iptables -S INPUT 2>/dev/null) || return 1
	printf '%s\n' "$rules" | grep -Eq '^-P INPUT DROP|^-A INPUT -j (REJECT|DROP)( |$)'
}

# --- k3s --------------------------------------------------------------------

install_k3s() {
	step "Installing Kubernetes"

	# Before either early return below: an install that is being re-run, or
	# one onto a k3s somebody else installed, still has to end up with the
	# mirror and the ingress configured. Without the mirror no image the panel
	# builds can ever be pulled, and SKIFITY_SKIP_K3S used to return first.
	configure_registry_mirror
	configure_ingress
	pick_pod_network

	if [ "${SKIFITY_SKIP_K3S:-}" = "1" ]; then
		note "Skipped installing k3s: --skip-k3s is set"
		return 0
	fi

	if have k3s && systemctl is-active --quiet k3s 2>/dev/null; then
		ok "k3s is already installed and running"
		return 0
	fi

	note "This downloads and starts k3s, which takes a minute or two."
	download https://get.k3s.io "$TMP_DIR/k3s-install.sh" 120 || fail \
		"Could not download the k3s installer from get.k3s.io." \
		"Check that this server can reach the internet:

  curl -fsSL https://get.k3s.io | head"

	# --cluster-init starts embedded etcd even on one node, so a second and third
	# control plane server can join later without rebuilding the cluster.
	# stdin is closed: under `curl | sh` it is the rest of this script.
	INSTALL_K3S_CHANNEL="$K3S_CHANNEL" \
		INSTALL_K3S_EXEC="server --cluster-init --flannel-backend=${POD_NETWORK} --write-kubeconfig-mode=0600 --secrets-encryption" \
		sh "$TMP_DIR/k3s-install.sh" </dev/null >>"$LOG_FILE" 2>&1 || fail \
		"k3s did not install." \
		"The last lines of ${LOG_FILE} say why. The usual causes are no outbound network access to get.k3s.io and github.com, or a kernel without the modules k3s needs.

To see the error on its own, run k3s's installer by hand:

  curl -sfL https://get.k3s.io | sh -"

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
#
# So a k3s that is already here is asked rather than told. A second run used to
# hand the panel an empty pod network, and a k3s installed some other way was
# reinstalled with whatever this kernel happened to support.
pick_pod_network() {
	if [ -n "$POD_NETWORK" ]; then
		note "Pod network: ${POD_NETWORK} (--pod-network)"
		return 0
	fi
	if [ "${SKIFITY_SKIP_K3S:-}" = "1" ] || [ -f "$K3S_UNIT_PATH" ]; then
		existing=$(existing_pod_network)
		# k3s's own default, when nothing chose otherwise.
		existing=${existing:-vxlan}
		case "$existing" in
		wireguard-native | vxlan)
			POD_NETWORK=$existing
			note "Pod network: ${POD_NETWORK}, the one this k3s was installed with"
			;;
		*)
			warn "This k3s uses the ${existing} pod network, and Skifity can only add servers on wireguard-native or vxlan."
			note "The panel runs; adding a second server to this cluster will not work."
			;;
		esac
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

# existing_pod_network prints the flannel backend an installed k3s was started
# with, from its unit file or its configuration, or nothing when neither says.
existing_pod_network() {
	found=""
	for file in "$K3S_UNIT_PATH" "$K3S_CONFIG_DIR/config.yaml" "$K3S_CONFIG_DIR"/config.yaml.d/*.yaml; do
		[ -r "$file" ] || continue
		backend=$(sed -n "s/.*flannel-backend[=:][[:space:]\"']*\([a-z-]*\).*/\1/p" "$file" | tail -n 1)
		[ -n "$backend" ] && found=$backend
	done
	printf '%s' "$found"
}

# containerd runs on the host, not in the cluster. It cannot resolve the
# registry's Kubernetes Service name, and it refuses plain HTTP to anything
# that is not loopback, so an image the panel builds could never be pulled.
# The mirror sends that name to the registry's NodePort on this machine.
#
# It is written before k3s starts, because that is when k3s reads it.
configure_registry_mirror() {
	mkdir -p "$K3S_CONFIG_DIR"
	cat >"$K3S_CONFIG_DIR/registries.yaml.new" <<EOF
# Written by Skifity. Do not edit.
mirrors:
  "${REGISTRY_HOST}":
    endpoint:
      - "http://127.0.0.1:${REGISTRY_NODE_PORT}"
EOF
	if cmp -s "$K3S_CONFIG_DIR/registries.yaml.new" "$K3S_CONFIG_DIR/registries.yaml" 2>/dev/null; then
		rm -f "$K3S_CONFIG_DIR/registries.yaml.new"
		return 0
	fi
	mv "$K3S_CONFIG_DIR/registries.yaml.new" "$K3S_CONFIG_DIR/registries.yaml"
	ok "the container runtime knows where the panel's registry is"

	# k3s reads this at start-up only, so a change to a running node needs a
	# restart. A fresh install has not started yet and skips this.
	if systemctl is-active --quiet k3s 2>/dev/null; then
		note "Restarting k3s so it picks up the registry configuration"
		systemctl restart k3s >>"$LOG_FILE" 2>&1 || warn "k3s could not be restarted; do it by hand"
	elif [ "${SKIFITY_SKIP_K3S:-}" = "1" ]; then
		# Not ours to restart: it was started some other way.
		warn "k3s reads $K3S_CONFIG_DIR/registries.yaml only when it starts."
		note "Restart it the way you started it, or the apps the panel builds cannot be pulled."
	fi
}

# k3s's load balancer, ServiceLB, hands a visitor to Traefik from an address
# of its own inside the pod network unless the Service's traffic policy is
# Local. Every app then saw one address for everybody: the firewall's address
# rules matched nobody or everybody, a country was looked up for a private
# address, and — since the pod network is where the tunnel's connector runs —
# a request sent straight to a server was believed when it claimed to have come
# through Cloudflare. Local keeps the visitor's address, and running Traefik on
# every server keeps every server answering, which Local otherwise stops on a
# server with no Traefik of its own.
#
# The tunnel reaches Traefik by its cluster address, which the policy does not
# touch. An operator's own HelmChartConfig for Traefik is left alone: two files
# for one object would undo each other on every start.
#
# The panel draws each app's response times from Traefik's latency histogram
# (internal/traffic). Traefik's own buckets are 0.1, 0.3, 1.2 and 5 seconds,
# which puts nearly every web app in the first one and makes its median "about
# 50 ms" whatever it really is; these are Prometheus's defaults, fine enough to
# tell 20 ms from 80. Prometheus metrics themselves, and their per-service
# labels, are on in k3s's Traefik already and are not touched. Changing this
# file makes k3s upgrade Traefik, which restarts it one server at a time.
#
# An app that scales to zero is reached through an ExternalName Service that
# aliases the KEDA interceptor (internal/kube/scaletozero.go), and Traefik's
# Ingress provider refuses ExternalName backends unless told otherwise: every
# such app answered 404. Only the panel writes Ingresses, so allowing them
# lets nobody else point one anywhere.
configure_ingress() {
	ours="${K3S_MANIFESTS_DIR}/skifity-traefik.yaml"
	mkdir -p "$K3S_MANIFESTS_DIR"
	for other in "$K3S_MANIFESTS_DIR"/*.yaml "$K3S_MANIFESTS_DIR"/*.yml; do
		[ -f "$other" ] && [ "$other" != "$ours" ] || continue
		if grep -q 'kind: *HelmChartConfig' "$other" && grep -q 'name: *traefik *$' "$other"; then
			note "Traefik is configured by $(basename "$other"); it is left as it is."
			note "Set service.spec.externalTrafficPolicy: Local there, or the firewall sees one address for everybody."
			note "Keep Traefik's Prometheus metrics on port 9100 there, or apps' requests are not counted."
			note "Set providers.kubernetesIngress.allowExternalNameServices: true there, or apps that scale to zero answer 404."
			return 0
		fi
	done
	cat >"${ours}.new" <<'TRAEFIK'
# Written by Skifity. Do not edit: the installer writes it again.
# Why: configure_ingress in install.sh.
apiVersion: helm.cattle.io/v1
kind: HelmChartConfig
metadata:
  name: traefik
  namespace: kube-system
spec:
  valuesContent: |-
    deployment:
      kind: DaemonSet
    service:
      spec:
        externalTrafficPolicy: Local
    metrics:
      prometheus:
        buckets: "0.005,0.01,0.025,0.05,0.1,0.25,0.5,1,2.5,5,10"
    providers:
      kubernetesIngress:
        allowExternalNameServices: true
TRAEFIK
	if cmp -s "${ours}.new" "$ours" 2>/dev/null; then
		rm -f "${ours}.new"
		return 0
	fi
	mv "${ours}.new" "$ours"
	ok "the ingress keeps each visitor's own address"
}

wait_for_cluster() {
	step "Waiting for the cluster"

	tries=0
	while [ "$tries" -lt 120 ]; do
		if kubectl get --raw /readyz >/dev/null 2>&1; then
			break
		fi
		tries=$((tries + 1))
		sleep 2
	done
	if [ "$tries" -lt 120 ]; then
		ok "The API server is answering"
	else
		fail \
			"The Kubernetes API server did not come up within four minutes." \
			"Look at what k3s is saying:

  journalctl -u k3s -n 50 --no-pager"
	fi

	tries=0
	while [ "$tries" -lt 90 ]; do
		if [ "$(kubectl get nodes --no-headers 2>/dev/null | awk '$2 == "Ready"' | wc -l)" -ge 1 ]; then
			break
		fi
		tries=$((tries + 1))
		sleep 2
	done
	[ "$tries" -lt 90 ] || fail \
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

	if [ ! -r "$K3S_TOKEN_PATH" ]; then
		warn "k3s has not written its token yet; servers cannot be added until it is copied"
		note "Once k3s is running: cp ${K3S_TOKEN_PATH} ${CONFIG_DIR}/cluster-token"
		return 0
	fi

	# Written beside its final name and moved into place, so the panel never
	# reads a token that is half there, or one that anybody else could read.
	(umask 077 && cp "$K3S_TOKEN_PATH" "$CONFIG_DIR/cluster-token.new")
	chown "$RUN_UID:$RUN_GID" "$CONFIG_DIR/cluster-token.new"
	chmod 0600 "$CONFIG_DIR/cluster-token.new"
	mv "$CONFIG_DIR/cluster-token.new" "$CONFIG_DIR/cluster-token"
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
	(umask 077 && printf '%s\n' "$SETUP_TOKEN" >"$CONFIG_DIR/setup-token")
	chown "$RUN_UID:$RUN_GID" "$CONFIG_DIR/setup-token"
	chmod 0600 "$CONFIG_DIR/setup-token"
	ok "Setup token written to $CONFIG_DIR/setup-token"

	# The master key is deliberately not generated here: the panel creates it on
	# first start, so exactly one piece of code decides its format. This keeps
	# an installer that is one version out of step from writing a key the panel
	# cannot read.
	note "The panel will create its master key at $CONFIG_DIR/master.key on first start."
}

# is_private_ipv4 is true for an address the internet cannot reach: RFC 1918,
# carrier-grade NAT, loopback and link-local.
is_private_ipv4() {
	case "$1" in
	10.* | 192.168.* | 127.* | 169.254.*) return 0 ;;
	172.1[6-9].* | 172.2[0-9].* | 172.3[01].*) return 0 ;;
	100.6[4-9].* | 100.[7-9][0-9].* | 100.1[01][0-9].* | 100.12[0-7].*) return 0 ;;
	esac
	return 1
}

# lookup_public_ip asks how the internet sees this server. Two services, run
# by different companies, so one being down is not an install without an
# address. What comes back is checked to be an address before it is used.
lookup_public_ip() {
	for url in https://api.ipify.org https://ipv4.icanhazip.com; do
		found=$(curl -fsS --max-time 6 "$url" 2>/dev/null | tr -d ' \r\n' || true)
		if valid_ipv4 "$found"; then
			printf '%s' "$found"
			return 0
		fi
	done
	return 1
}

# find_public_ip sets PUBLIC_IP: the address given, else the node's external
# address, else its internal one.
#
# On most VPS providers the internal address is the public one. Behind NAT —
# AWS, Google Cloud, Oracle, Azure, a home router — it is a private address,
# and an sslip.io name built from it answers to nobody outside. So a private
# address is looked up once from outside, and the private one is still named,
# for a server only meant to be reached on its own network.
find_public_ip() {
	PUBLIC_IP="${SKIFITY_PUBLIC_IP:-}"
	if [ -n "$PUBLIC_IP" ]; then
		ok "Public address ${PUBLIC_IP} (--public-ip)"
		return 0
	fi
	PUBLIC_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="ExternalIP")].address}' 2>/dev/null || true)
	[ -n "$PUBLIC_IP" ] || PUBLIC_IP="$NODE_IP"
	[ -n "$PUBLIC_IP" ] || fail \
		"Could not work out this server's IP address." \
		"Give it to the installer and run it again:

  sudo sh install.sh --public-ip 203.0.113.10"

	if is_private_ipv4 "$PUBLIC_IP"; then
		if outside=$(lookup_public_ip); then
			ok "This server is ${PUBLIC_IP} on its own network and ${outside} on the internet"
			note "If it is only meant to be reached on your own network, run this again with --public-ip ${PUBLIC_IP}"
			PUBLIC_IP=$outside
		else
			warn "This server's address, ${PUBLIC_IP}, is a private one, and its public address could not be looked up."
			note "The panel will answer only on that network. If the server has a public address,"
			note "run this again with --public-ip <address>."
		fi
	fi
}

# existing_panel_route prints the host the panel answers on now and, after a
# space, the Secret its certificate is in — nothing at all on a first install.
# [*] rather than [0] for the certificate: kubectl's JSONPath fails the whole
# query on an index into an empty list, and an Ingress with `tls: []` would
# then read as no panel at all.
existing_panel_route() {
	kubectl -n "$NAMESPACE" get ingress skifity-panel \
		-o jsonpath='{.spec.rules[0].host}{" "}{.spec.tls[*].secretName}' 2>/dev/null || true
}

choose_hostname() {
	step "Working out the panel's address"

	NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null || true)

	# A second run keeps the address the panel already has unless it is told
	# otherwise. Without this, running the installer again to upgrade — with
	# none of the options the first run had — moved a panel that had a domain
	# and a certificate back to plain HTTP on an sslip.io name.
	route=""
	if [ -z "${SKIFITY_DOMAIN:-}" ] && [ -z "${SKIFITY_PUBLIC_IP:-}" ]; then
		route=$(existing_panel_route)
	fi
	existing_host=${route%% *}
	existing_tls=""
	case "$route" in
	*" "*) existing_tls=${route#* } ;;
	esac

	if [ -n "${SKIFITY_DOMAIN:-}" ]; then
		PANEL_HOST="$SKIFITY_DOMAIN"
		PANEL_SCHEME="https"
		find_public_ip
		ok "The panel will answer on $PANEL_HOST"
		check_domain_points_here
	elif [ -n "$existing_host" ]; then
		PANEL_HOST="$existing_host"
		PANEL_SCHEME="http"
		[ -n "$existing_tls" ] && PANEL_SCHEME="https"
		ok "The panel keeps the address it already has: $PANEL_HOST"
		note "To move it, run this again with --domain <name>."
		if [ "$PANEL_SCHEME" = "https" ]; then
			find_public_ip
			check_domain_points_here
		fi
	else
		find_public_ip
		# sslip.io resolves any address embedded in the name, so a brand new
		# server has a working hostname without anybody buying a domain. An
		# IPv6 address is written with dashes for its colons, which it reads too.
		PANEL_HOST="$(printf '%s' "$PUBLIC_IP" | tr '.:' '--').sslip.io"
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
		download "$CERT_MANAGER_URL" "$TMP_DIR/cert-manager.yaml" 300 || fail \
			"Could not download cert-manager from ${CERT_MANAGER_URL}." \
			"Check that this server can reach github.com, then run the installer again. Without cert-manager the panel still works over plain HTTP: run the installer without --domain."
		kubectl apply -f "$TMP_DIR/cert-manager.yaml" >>"$LOG_FILE" 2>&1 || fail \
			"cert-manager did not install." \
			"The log at ${LOG_FILE} has what kubectl said. Applying it again is safe: run the installer again."
		kubectl -n cert-manager rollout status deployment/cert-manager-webhook --timeout=180s >>"$LOG_FILE" 2>&1 || fail \
			"cert-manager installed but its webhook never became ready." \
			"Look at it with:

  kubectl -n cert-manager get pods"
		ok "cert-manager is ready"
	fi

	acme_server="https://acme-v02.api.letsencrypt.org/directory"
	[ "${SKIFITY_ACME_STAGING:-}" = "1" ] &&
		acme_server="https://acme-staging-v02.api.letsencrypt.org/directory"

	# A second run without --email keeps the address the first one registered.
	acme_email="${SKIFITY_ACME_EMAIL:-}"
	if [ -z "$acme_email" ]; then
		acme_email=$(kubectl get clusterissuer "$ISSUER" -o jsonpath='{.spec.acme.email}' 2>/dev/null || true)
		valid_email "$acme_email" || acme_email=""
	fi

	# Two steps, not a pipeline: see render.
	render deploy/cluster-issuer.yaml >"$TMP_DIR/cluster-issuer.yaml"
	sed -e "s|__EMAIL__|${acme_email}|g" \
		-e "s|__ACME_SERVER__|${acme_server}|g" \
		"$TMP_DIR/cluster-issuer.yaml" >"$MANIFEST_DIR/cluster-issuer.yaml"
	# cert-manager's webhook validates every issuer, and it reports ready a
	# moment before it can answer. That moment is a failure on a fresh install
	# unless it is waited out.
	retry 12 5 kubectl apply -f "$MANIFEST_DIR/cluster-issuer.yaml" >>"$LOG_FILE" 2>&1 || fail \
		"Could not create the certificate issuer." \
		"Run the installer again. If it keeps failing, the log at ${LOG_FILE} has what kubectl said."
	if [ "${SKIFITY_ACME_STAGING:-}" = "1" ]; then
		ok "Certificates will be issued by Let's Encrypt's staging server, which browsers do not trust"
	else
		ok "Certificates will be issued by Let's Encrypt"
	fi
}

# render prints a manifest with the common placeholders filled in. The manifests
# come from the installer's own directory when it was run from a clone, and are
# fetched from the release when it was piped from curl.
#
# The fetch is a step of its own rather than the first half of a pipeline: in
# a pipeline its failure was the pipeline's left side failing, which sh does
# not stop for, and an empty manifest went on to kubectl.
render() {
	src="$SOURCE_DIR/$1"
	if [ ! -f "$src" ]; then
		src="$TMP_DIR/$(basename "$1")"
		fetch_manifest "$(basename "$1")" "$src"
	fi
	sed -e "s|__NAMESPACE__|${NAMESPACE}|g" \
		-e "s|__IMAGE__|${IMAGE}|g" \
		-e "s|__NODE__|${NODE_NAME}|g" \
		-e "s|__HOST__|${PANEL_HOST}|g" \
		-e "s|__PUBLIC_URL__|${PUBLIC_URL}|g" \
		-e "s|__POD_NETWORK__|${POD_NETWORK}|g" \
		-e "s|__CONFIG_DIR__|${CONFIG_DIR}|g" \
		-e "s|__DATA_DIR__|${DATA_DIR}|g" \
		-e "s|__ISSUER__|${ISSUER}|g" \
		"$src"
}

fetch_manifest() {
	url="${MANIFEST_BASE}/$1"
	download "$url" "$2" 60 && [ -s "$2" ] || fail \
		"Could not download the manifest ${1} from ${url}." \
		"Check that this server can reach the internet, or clone the repository and run installer/install.sh from inside it so the manifests are read from disk."
}

install_panel() {
	step "Installing the panel"

	current_image=$(kubectl -n "$NAMESPACE" get deployment skifity-panel \
		-o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || true)
	if [ -n "$current_image" ] && [ "$current_image" != "$IMAGE" ]; then
		note "Upgrading the panel from ${current_image} to ${IMAGE}"
	fi

	# Rendered to disk first, so the operator can see exactly what was applied.
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
	if [ -z "$current_image" ]; then
		note "The first start pulls the image, which takes a minute on a new server."
	fi
	kubectl -n "$NAMESPACE" rollout status deployment/skifity-panel --timeout=300s >>"$LOG_FILE" 2>&1 || fail \
		"The panel did not start within five minutes." \
		"See what it is waiting for:

  kubectl -n ${NAMESPACE} get pods
  kubectl -n ${NAMESPACE} describe pod -l app.kubernetes.io/component=panel
  kubectl -n ${NAMESPACE} logs -l app.kubernetes.io/component=panel

If the image could not be pulled, check that ${IMAGE} exists and that this server can reach the registry."
	ok "The panel is running"
}

# wait_for_certificate gives Let's Encrypt a couple of minutes, and says so
# when that was not enough: until the certificate is issued the browser shows
# a warning about Traefik's own, which looks like a broken install.
wait_for_certificate() {
	[ "$PANEL_SCHEME" = "https" ] || return 0
	step "Waiting for the certificate"
	tries=0
	while [ "$tries" -lt 40 ]; do
		ready=$(kubectl -n "$NAMESPACE" get certificate skifity-panel-tls \
			-o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
		if [ "$ready" = "True" ]; then
			ok "Let's Encrypt issued a certificate for $PANEL_HOST"
			return 0
		fi
		tries=$((tries + 1))
		sleep 3
	done
	warn "The certificate for $PANEL_HOST has not been issued yet."
	note "Until it is, the browser warns about a temporary one. When the DNS record is new,"
	note "this usually sorts itself out within minutes. To see what cert-manager is waiting for:"
	note "  kubectl -n ${NAMESPACE} describe certificate skifity-panel-tls"
}

# address_for_resolve writes an address the way curl's --resolve wants it.
address_for_resolve() {
	case "$1" in
	*:*) printf '[%s]' "$1" ;;
	*) printf '%s' "$1" ;;
	esac
}

# check_panel_answers asks for the panel the way a browser would: by its name,
# through the ingress, rather than at the Pod. A running Pod behind a route
# that does not reach it is the install that "worked" and opens nothing.
#
# It is also how a second run learns the first account exists, and so prints
# the address to sign in at instead of a setup link that stopped working.
check_panel_answers() {
	step "Checking the panel answers at its address"
	port=80
	[ "$PANEL_SCHEME" = "https" ] && port=443
	target=$(address_for_resolve "${NODE_IP:-127.0.0.1}")

	tries=0
	while [ "$tries" -lt 30 ]; do
		# -k: the certificate may not be issued yet, and this is a check that
		# the route reaches the panel, not of the certificate.
		if curl -fsSk --max-time 5 --resolve "${PANEL_HOST}:${port}:${target}" \
			"${PUBLIC_URL}/api/health" >/dev/null 2>>"$LOG_FILE"; then
			PANEL_ANSWERS=yes
			break
		fi
		tries=$((tries + 1))
		sleep 2
	done
	if [ "$PANEL_ANSWERS" != "yes" ]; then
		PANEL_ANSWERS=no
		warn "The panel is running, but ${PUBLIC_URL} does not reach it through the ingress."
		note "Look at the ingress, and at the panel's route:"
		note "  kubectl -n kube-system get pods -l app.kubernetes.io/name=traefik"
		note "  kubectl -n ${NAMESPACE} describe ingress skifity-panel"
		return 0
	fi
	ok "${PUBLIC_URL} answers"

	status=$(curl -fsSk --max-time 5 --resolve "${PANEL_HOST}:${port}:${target}" \
		"${PUBLIC_URL}/api/setup/status" 2>>"$LOG_FILE" || true)
	case "$status" in
	*'"needs_setup":false'* | *'"needs_setup": false'*) SETUP_STATE="done" ;;
	*'"needs_setup":true'* | *'"needs_setup": true'*) SETUP_STATE=pending ;;
	esac

	# The public address, when it is not this server's own. Plenty of networks
	# do not carry a server's packets to its own public address and back, so a
	# failure here is a hint, not a verdict.
	if [ -n "$PUBLIC_IP" ] && [ "$PUBLIC_IP" != "$NODE_IP" ]; then
		if curl -fsSk --max-time 6 --resolve "${PANEL_HOST}:${port}:$(address_for_resolve "$PUBLIC_IP")" \
			"${PUBLIC_URL}/api/health" >/dev/null 2>>"$LOG_FILE"; then
			ok "and from its public address, ${PUBLIC_IP}"
		else
			note "From this server, its public address ${PUBLIC_IP} did not answer. Many networks do not"
			note "route a server's own public address back to it, so this may be fine; if the link"
			note "below does not open from your computer, allow TCP 80 and 443 at your provider."
		fi
	fi
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
	new="${CLI_PATH}.new"
	rm -f "$new"
	port=$(kubectl -n "$NAMESPACE" get svc skifity-panel -o jsonpath='{.spec.ports[0].nodePort}' 2>>"$LOG_FILE" || true)
	if [ -n "$port" ] &&
		curl -fsS --retry 3 --retry-delay 2 --max-time 120 "http://127.0.0.1:${port}/api/cli/download" -o "$new" 2>>"$LOG_FILE" &&
		[ -s "$new" ]; then
		chmod 0755 "$new"
		mv "$new" "$CLI_PATH"
		ok "skifity is on your PATH, from the panel itself"
		return 0
	fi
	rm -f "$new"

	# An override for an air-gapped install that mirrors the binaries itself.
	if [ -n "${SKIFITY_CLI_URL:-}" ] &&
		download "$SKIFITY_CLI_URL" "$new" 120 &&
		[ -s "$new" ]; then
		chmod 0755 "$new"
		mv "$new" "$CLI_PATH"
		ok "skifity is on your PATH, from ${SKIFITY_CLI_URL}"
		return 0
	fi
	rm -f "$new"

	warn "Could not install the command line tool; the panel itself is unaffected."
	note "The panel serves it: curl -fsS ${PUBLIC_URL}/api/cli/download -o ${CLI_PATH} && chmod +x ${CLI_PATH}"
}

# install_uninstaller puts skifity-uninstall on the PATH. The install ends by
# naming it as the way back out, and for a long time nothing put it there. It
# is the uninstaller from the same release as this script: the clone's own
# when there is one, the tag's otherwise. Without it the panel works all the
# same, so a failure here is a warning.
install_uninstaller() {
	new="${UNINSTALLER_PATH}.new"
	rm -f "$new"
	if [ -f "$SOURCE_DIR/installer/uninstall.sh" ]; then
		cp "$SOURCE_DIR/installer/uninstall.sh" "$new" 2>>"$LOG_FILE" || true
	elif [ -n "$VERSION" ]; then
		download "${RELEASE_BASE}/installer/uninstall.sh" "$new" 60 || true
	fi
	# Parsed before it is installed: a proxy's error page saved as the
	# uninstaller would be found by somebody who wants to leave.
	if [ -s "$new" ] && sh -n "$new" 2>>"$LOG_FILE"; then
		chmod 0755 "$new"
		mv "$new" "$UNINSTALLER_PATH"
		ok "skifity-uninstall is on your PATH"
		return 0
	fi
	rm -f "$new"
	warn "Could not install skifity-uninstall; the panel itself is unaffected."
	note "It is installer/uninstall.sh in the repository, at the release you installed."
}

# release_label names what was installed: the release, or the image when one
# was given instead.
release_label() {
	if [ -n "${SKIFITY_IMAGE:-}" ]; then
		printf '%s' "$IMAGE"
	else
		printf '%s' "$VERSION"
	fi
}

finish() {
	took=""
	if [ -n "$START_TIME" ]; then
		now=$(date +%s)
		secs=$((now - START_TIME))
		took=" in $((secs / 60))m $((secs % 60))s"
	fi

	if [ "$SETUP_STATE" = "done" ]; then
		# A second run, after somebody has already set the panel up. The setup
		# token stopped working when the first account was made, so printing
		# it again would send them to a page that only says no.
		printf '\n%s%sSkifity %s is installed%s.%s\n\n' "$BOLD" "$GREEN" "$(release_label)" "$took" "$RESET"
		printf '  Sign in at %s%s%s\n\n' "$BOLD" "$PUBLIC_URL" "$RESET"
	else
		printf '\n%s%sSkifity %s is installed%s.%s\n\n' "$BOLD" "$GREEN" "$(release_label)" "$took" "$RESET"
		# The token travels in the fragment, not the query string: a fragment is
		# never sent to the server, so opening this link cannot put the token into
		# an access log, a proxy or a Referer header. The page fills the field in
		# and clears the address bar.
		printf '  %sOpen this and the token is already filled in:%s\n\n' "$BOLD" "$RESET"
		printf '    %s%s/setup#token=%s%s\n\n' "$BOLD" "$PUBLIC_URL" "$SETUP_TOKEN" "$RESET"
		printf '  Or open %s/setup and paste it:\n' "$PUBLIC_URL"
		printf '    %s\n\n' "$SETUP_TOKEN"
		printf '  The token is also at %s on this server.\n' "$CONFIG_DIR/setup-token"
		printf '  It creates the first account and then stops working.\n'
		if [ "$SETUP_STATE" = "unknown" ]; then
			printf '  If this panel was set up already, sign in at %s instead.\n' "$PUBLIC_URL"
		fi
		printf '\n'
	fi

	if [ "$PANEL_SCHEME" = "http" ]; then
		printf '  %sThis address has no certificate yet.%s Add a domain in Settings and\n' "$YELLOW" "$RESET"
		printf '  Skifity turns on HTTPS for it by itself.\n\n'
	else
		printf '  If the page does not load, the DNS record for %s\n' "$PANEL_HOST"
		printf '  may not have reached your computer yet. Give it a few minutes.\n\n'
	fi

	printf '  Panel      %s\n' "$PUBLIC_URL"
	printf '  Release    %s\n' "$(release_label)"
	if [ -x "$CLI_PATH" ]; then
		printf '  CLI        skifity --help\n'
	fi
	printf '  Log        %s\n' "$LOG_FILE"
	if [ -x "$UNINSTALLER_PATH" ]; then
		printf '  Uninstall  sudo skifity-uninstall\n'
	fi
	if [ "$WARNINGS" -eq 1 ]; then
		printf '\n  %sOne warning above.%s It says what to do about it.\n' "$YELLOW" "$RESET"
	elif [ "$WARNINGS" -gt 1 ]; then
		printf '\n  %s%s warnings above.%s Each one says what to do about it.\n' "$YELLOW" "$WARNINGS" "$RESET"
	fi
	printf '\n'
	log "install completed: $(release_label), panel at ${PUBLIC_URL}, ${WARNINGS} warning(s)"
}

# --- main -------------------------------------------------------------------

# The whole install is one function, called on the last line. Piped from curl,
# sh reads and runs a script as it arrives, so a download cut off halfway used
# to run the first half of the install and stop; now a cut-off script defines
# functions and runs nothing.
main() {
	parse_args "$@"
	resolve_version
	derive_release

	if [ -z "$SOURCE_DIR" ]; then
		SOURCE_DIR="."
		case "$0" in
		*/*)
			# Running from a checkout: the manifests sit next to this script.
			SOURCE_DIR=$(cd "$(dirname "$0")/.." 2>/dev/null && pwd || echo ".")
			;;
		esac
	fi

	START_TIME=$(date +%s)
	printf '\n%sSkifity%s  self-hosted apps, powered by Kubernetes\n' "$BOLD" "$RESET"
	printf '%sInstalling %s%s\n\n' "$DIM" "$(release_label)" "$RESET"

	preflight
	take_lock
	trap on_exit EXIT
	trap interrupted INT TERM HUP
	make_tmp_dir

	ask_for_domain
	configure_firewall
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
	wait_for_certificate
	check_panel_answers
	install_cli
	install_uninstaller
	finish
}

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

main "$@"
