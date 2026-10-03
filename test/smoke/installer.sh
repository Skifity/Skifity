#!/bin/sh
# Smoke test: the installer's own logic, without installing anything.
#
# A real install needs root, systemd and a kernel k3s can use, so this checks
# everything that can be checked safely: the scripts parse, the manifests render
# from the repository, a failure explains itself, and the uninstaller refuses to
# delete data without an explicit confirmation.
#
# Running the real installer belongs in a throwaway VM, never on a machine
# anybody cares about.
#
# Nearly every check runs in a subshell, so that what it sets changes nothing
# for the next one; the linter's note that such a change is lost is the point.
# shellcheck disable=SC2030,SC2031
set -eu

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORKDIR="$(mktemp -d)"
FAILURES=0

cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT INT TERM

# Named so they cannot be shadowed: this script sources install.sh, which
# defines its own fail(), ok() and note().
t_pass() { printf '  ok   %s\n' "$1"; }
t_fail() { printf '  FAIL %s\n' "$1" >&2; FAILURES=$((FAILURES + 1)); }
t_check() { if [ "$1" = "0" ]; then t_pass "$2"; else t_fail "$2"; fi; }

printf '\n== Skifity smoke test: installer ==\n\n'

# --- the scripts parse ------------------------------------------------------

for script in "$ROOT"/installer/*.sh; do
  if sh -n "$script" 2>"$WORKDIR/syntax.err"; then
    t_pass "$(basename "$script") is valid POSIX shell"
  else
    t_fail "$(basename "$script"): $(cat "$WORKDIR/syntax.err")"
  fi
done

# The installer must not need bash. A bashism here would fail on the minimal
# images people actually install on.
if command -v dash >/dev/null 2>&1; then
  for script in "$ROOT"/installer/*.sh; do
    if dash -n "$script" 2>/dev/null; then
      t_pass "$(basename "$script") parses under dash"
    else
      t_fail "$(basename "$script") does not parse under dash"
    fi
  done
fi

# --- the installer's helpers ------------------------------------------------

# shellcheck source=../../installer/install.sh
SKIFITY_INSTALLER_LIB=1 . "$ROOT/installer/install.sh"

# Every message helper has to work when the output is not a terminal, which is
# how it runs under `curl | sh` in a CI job or a provisioning script.
( say "hello" >/dev/null 2>&1 ) && t_check 0 "messages work without a terminal" || t_check 1 "messages work without a terminal"

# A failure must name a cause and a fix, and exit non-zero. An installer that
# stops with a bare error leaves someone with a half-installed server.
if ( fail "The thing did not work." "Do this instead." >/dev/null 2>&1 ); then
  t_fail "the installer's fail() should exit non-zero"
else
  t_pass "a failure exits non-zero"
fi

fail_text=$( (fail "The thing did not work." "Do this instead.") 2>&1 || true)
case "$fail_text" in
*"The thing did not work."*) t_pass "a failure says what happened" ;;
*) t_fail "a failure should say what happened" ;;
esac
case "$fail_text" in
*"What to do"*"Do this instead."*) t_pass "a failure says how to fix it" ;;
*) t_fail "a failure should say how to fix it" ;;
esac

# --- the ingress keeps the visitor's address ---------------------------------

# Without it every visitor reaches Traefik from ServiceLB's own address, and the
# firewall's address and country rules see one address for everybody.
manifests="$WORKDIR/manifests"
( K3S_MANIFESTS_DIR="$manifests"; LOG_FILE=/dev/null; configure_ingress >/dev/null 2>&1 )
if grep -q 'externalTrafficPolicy: Local' "$manifests/skifity-traefik.yaml" 2>/dev/null &&
  grep -q 'kind: DaemonSet' "$manifests/skifity-traefik.yaml"; then
  t_pass "Traefik is told to keep the visitor's address, on every server"
else
  t_fail "the ingress configuration was not written"
fi
# Without finer buckets every app's response time reads as about 50 ms.
if grep -q 'buckets: "0.005,' "$manifests/skifity-traefik.yaml" 2>/dev/null; then
  t_pass "Traefik is asked for latency buckets fine enough to draw"
else
  t_fail "Traefik was left with latency buckets too coarse to draw"
fi
# An app that scales to zero is routed through an ExternalName Service.
if grep -q 'allowExternalNameServices: true' "$manifests/skifity-traefik.yaml" 2>/dev/null; then
  t_pass "Traefik routes to the service an app that scales to zero is reached through"
else
  t_fail "Traefik refuses ExternalName backends, so an app that scales to zero answers 404"
fi
# An operator's own configuration of Traefik is not fought over.
theirs="$WORKDIR/theirs"
mkdir -p "$theirs"
printf 'apiVersion: helm.cattle.io/v1\nkind: HelmChartConfig\nmetadata:\n  name: traefik\n  namespace: kube-system\n' >"$theirs/traefik-config.yaml"
out=$( (K3S_MANIFESTS_DIR="$theirs"; LOG_FILE=/dev/null; configure_ingress) 2>&1 || true)
if [ ! -f "$theirs/skifity-traefik.yaml" ]; then
  t_pass "an operator's own Traefik configuration is left alone"
else
  t_fail "a second configuration of Traefik was written beside the operator's"
fi
case "$out" in
*"externalTrafficPolicy: Local"*) t_pass "and they are told what to set in it" ;;
*) t_fail "the operator should be told what to set, got: $out" ;;
esac

# --- the installer refuses to install what does not exist -------------------

# Two things an install needs and this script cannot invent: an image to run,
# and the Kubernetes objects that go with it. Both have to be settled before
# k3s is installed, not after, or a failure leaves a half-built cluster behind.

# Nothing published, nothing named: refuse.
( VERSION=""; SKIFITY_IMAGE=""; SOURCE_DIR="$WORKDIR"; check_release >/dev/null 2>&1 ) &&
  t_fail "the installer should refuse when no release is published and no image is named" ||
  t_pass "an install with no release and no image is refused"

release_text=$( (VERSION=""; SKIFITY_IMAGE=""; SOURCE_DIR="$WORKDIR"; check_release) 2>&1 || true)
case "$release_text" in
*"No Skifity release has been published yet"*) t_pass "the refusal says there is no release" ;;
*) t_fail "the refusal should say there is no release, got: $release_text" ;;
esac
case "$release_text" in
*"make image"*"SKIFITY_IMAGE"*) t_pass "the refusal says how to build one and pass it" ;;
*) t_fail "the refusal should say how to build an image and pass it" ;;
esac
case "$release_text" in
*"Nothing on this server has been changed"*) t_pass "the refusal says the server is untouched" ;;
*) t_fail "the refusal should say nothing was changed" ;;
esac
case "$release_text" in
*"github.com/${PROJECT_REPO}"*) t_pass "the refusal names the repository to clone" ;;
*) t_fail "the refusal should name the repository, got: $release_text" ;;
esac

# An image, but nowhere to read the objects from: refuse too. This is the
# curl | sh case with SKIFITY_IMAGE set and no version, where the manifest URL
# has an empty ref in it and 404s after k3s is already installed.
( VERSION=""; SKIFITY_IMAGE="registry.example.test/skifity:1.2.3"; SOURCE_DIR="$WORKDIR"; check_release >/dev/null 2>&1 ) &&
  t_fail "the installer should refuse an image with no manifests to go with it" ||
  t_pass "an image with nowhere to read the objects from is refused"

manifest_text=$( (VERSION=""; SKIFITY_IMAGE="registry.example.test/skifity:1.2.3"; SOURCE_DIR="$WORKDIR"; check_release) 2>&1 || true)
case "$manifest_text" in
*"nowhere to read the Kubernetes objects"*) t_pass "that refusal says which half is missing" ;;
*) t_fail "the refusal should say the objects have no source, got: $manifest_text" ;;
esac

# The two ways an install is allowed to go ahead.
( VERSION=""; SKIFITY_IMAGE="registry.example.test/skifity:1.2.3"; SOURCE_DIR="$ROOT"; check_release >/dev/null 2>&1 ) &&
  t_pass "an image plus a clone on disk is accepted" ||
  t_fail "check_release should accept an image read alongside deploy/ on disk"

( VERSION="v9.9.9"; SKIFITY_IMAGE=""; SOURCE_DIR="$WORKDIR"; check_release >/dev/null 2>&1 ) &&
  t_pass "a published release is accepted" ||
  t_fail "check_release should accept a published release"

# The manifests must come from the release being installed, not from a branch:
# a branch moves, and v1's image with main's objects is a Deployment the image
# has never seen.
case "$(SKIFITY_INSTALLER_LIB=1 SKIFITY_VERSION=v1.2.3 sh -c '. '"$ROOT"'/installer/install.sh; printf "%s" "$MANIFEST_BASE"' 2>/dev/null)" in
*"/v1.2.3/deploy") t_pass "the manifests are pinned to the release being installed" ;;
*) t_fail "the manifest base is not pinned to the version being installed" ;;
esac

# And it has to be asked before the machine is touched: preflight runs first,
# and check_release runs inside it before any step that changes anything.
if awk '/^preflight\(\) \{/,/^\}/' "$ROOT/installer/install.sh" | grep -q 'check_release'; then
  t_pass "the release is checked inside preflight"
else
  t_fail "check_release is no longer called from preflight"
fi
if awk '/^[[:space:]]*preflight$/{p=1} /^[[:space:]]*install_k3s$/{if (p) print "ordered"}' "$ROOT/installer/install.sh" | grep -q ordered; then
  t_pass "preflight runs before k3s is installed"
else
  t_fail "install.sh no longer runs preflight before installing k3s"
fi

# --- the manifests render ---------------------------------------------------

SOURCE_DIR="$ROOT"
NAMESPACE="skifity-system"
IMAGE="registry.example.test/skifity:0.0.0-test"
NODE_NAME="test-node"
PANEL_HOST="panel.example.test"
PUBLIC_URL="https://panel.example.test"
CONFIG_DIR="/etc/skifity"
DATA_DIR="/var/lib/skifity"
ISSUER="skifity-letsencrypt"
POD_NETWORK=""

for manifest in panel.yaml ingress.yaml ingress-tls.yaml; do
  if render "deploy/$manifest" >"$WORKDIR/$manifest" 2>"$WORKDIR/render.err"; then
    t_pass "deploy/$manifest renders"
  else
    t_fail "deploy/$manifest: $(cat "$WORKDIR/render.err")"
    continue
  fi

  # The cluster-issuer has placeholders the caller fills in separately; these
  # three must come out complete.
  if grep -q '__[A-Z0-9_]*__' "$WORKDIR/$manifest"; then
    t_fail "deploy/$manifest still has a placeholder: $(grep -o '__[A-Z0-9_]*__' "$WORKDIR/$manifest" | head -1)"
  else
    t_pass "deploy/$manifest has nothing left to substitute"
  fi
done

grep -q "image: $IMAGE" "$WORKDIR/panel.yaml" &&
  t_pass "the image is the one asked for" ||
  t_fail "the rendered Deployment does not use $IMAGE"

grep -q "kubernetes.io/hostname: $NODE_NAME" "$WORKDIR/panel.yaml" &&
  t_pass "the panel is pinned to the node holding its data" ||
  t_fail "the rendered Deployment is not pinned to $NODE_NAME"

# The installer chooses the pod network on the first node; the panel installs
# every server after that and can only match a choice it was handed. A node on
# the other backend joins without an error and then reaches nothing.
POD_NETWORK="vxlan"
render "deploy/panel.yaml" >"$WORKDIR/panel-vxlan.yaml" 2>/dev/null
if grep -q 'value: "vxlan"' "$WORKDIR/panel-vxlan.yaml"; then
  t_pass "the pod network the installer chose is handed to the panel"
else
  t_fail "the rendered Deployment does not tell the panel which pod network this cluster uses"
fi
POD_NETWORK=""

# And the choice itself: WireGuard when the kernel can do it, vxlan when it
# cannot, never a silent default that half the cluster disagrees with.
# The single quotes are the point: what is searched for is the literal text
# ${POD_NETWORK} as it appears in the installer, not its value here.
# shellcheck disable=SC2016
if grep -q 'flannel-backend=${POD_NETWORK}' "$ROOT/installer/install.sh" &&
  grep -q 'POD_NETWORK="wireguard-native"' "$ROOT/installer/install.sh" &&
  grep -q 'POD_NETWORK="vxlan"' "$ROOT/installer/install.sh"; then
  t_pass "the first node is installed with the pod network the kernel supports"
else
  t_fail "install.sh no longer picks the pod network from the kernel"
fi

grep -q "host: $PANEL_HOST" "$WORKDIR/ingress.yaml" &&
  t_pass "the route uses the chosen hostname" ||
  t_fail "the rendered Ingress does not use $PANEL_HOST"

grep -q "cert-manager.io/cluster-issuer: $ISSUER" "$WORKDIR/ingress-tls.yaml" &&
  t_pass "the HTTPS route asks cert-manager for a certificate" ||
  t_fail "the rendered TLS Ingress has no issuer"

# The plain-HTTP route must not claim TLS it does not have.
if grep -q "tls:" "$WORKDIR/ingress.yaml"; then
  t_fail "the plain-HTTP route should not have a tls block"
else
  t_pass "the plain-HTTP route does not pretend to have a certificate"
fi

# --- a k3s somebody else installed still gets what the panel needs ----------

# SKIFITY_SKIP_K3S used to return before the registry mirror was written, so
# on such an install no image the panel built could ever be pulled. Both files
# are redirected into the work directory.
out=$( (SKIFITY_SKIP_K3S=1; K3S_CONFIG_DIR="$WORKDIR/k3s"; K3S_MANIFESTS_DIR="$WORKDIR/k3s-manifests"
  LOG_FILE=/dev/null; install_k3s) 2>&1 || true)
if grep -q "http://127.0.0.1:${REGISTRY_NODE_PORT}" "$WORKDIR/k3s/registries.yaml" 2>/dev/null; then
  t_pass "with SKIFITY_SKIP_K3S the registry mirror is still written"
else
  t_fail "with SKIFITY_SKIP_K3S no registry mirror was written, so built images cannot be pulled"
fi
if grep -q "allowExternalNameServices: true" "$WORKDIR/k3s-manifests/skifity-traefik.yaml" 2>/dev/null; then
  t_pass "and so is the ingress configuration"
else
  t_fail "with SKIFITY_SKIP_K3S the ingress configuration was not written"
fi
case "$out" in
*"only when it starts"*) t_pass "and the operator is told to restart the k3s they started" ;;
*) t_fail "a changed mirror on somebody else's k3s should say it needs a restart, got: $out" ;;
esac

# --- the uninstaller the install names is put where it says ------------------

# The install ends by naming skifity-uninstall, and nothing used to install it.
# From a clone it is copied from beside the installer; the path is redirected
# into the work directory so the test installs nothing.
mkdir -p "$WORKDIR/bin"
( SOURCE_DIR="$ROOT"; UNINSTALLER_PATH="$WORKDIR/bin/skifity-uninstall"; LOG_FILE=/dev/null
  install_uninstaller >/dev/null 2>&1 )
if [ -x "$WORKDIR/bin/skifity-uninstall" ] && cmp -s "$WORKDIR/bin/skifity-uninstall" "$ROOT/installer/uninstall.sh"; then
  t_pass "the uninstaller is installed from the clone, executable"
else
  t_fail "install_uninstaller did not put installer/uninstall.sh at the path it names"
fi

# No clone and no release to fetch from: a warning, nothing half-written, and
# the last lines of the install do not name a command that is not there.
out=$( (SOURCE_DIR="$WORKDIR"; VERSION=""; UNINSTALLER_PATH="$WORKDIR/bin/none"; LOG_FILE=/dev/null
  install_uninstaller) 2>&1 || true)
if [ ! -e "$WORKDIR/bin/none" ] && [ ! -e "$WORKDIR/bin/none.new" ]; then
  t_pass "with nowhere to get it from, nothing is left behind"
else
  t_fail "install_uninstaller left a file behind with nothing to install"
fi
case "$out" in
*"Could not install skifity-uninstall"*) t_pass "and it says so" ;;
*) t_fail "install_uninstaller should warn when it cannot install, got: $out" ;;
esac
out=$( (UNINSTALLER_PATH="$WORKDIR/bin/none"; PUBLIC_URL="http://192.0.2.1.sslip.io"; PANEL_SCHEME=http
  PANEL_HOST=192.0.2.1.sslip.io; SETUP_TOKEN="fake-setup-token"; LOG_FILE=/dev/null; finish) 2>&1 || true)
case "$out" in
*"skifity-uninstall"*) t_fail "the install names skifity-uninstall when it is not there" ;;
*) t_pass "an uninstaller that is not there is not named" ;;
esac

# --- the options ------------------------------------------------------------

INSTALLER="$ROOT/installer/install.sh"

# make_stub DIR NAME writes an executable stand-in for a command, with the
# body it reads from stdin.
make_stub() {
  mkdir -p "$1"
  { printf '#!/bin/sh\n'; cat; } >"$1/$2"
  chmod +x "$1/$2"
}


# --help has to work for somebody reading it before deciding to run it as
# root, and it is the only thing that runs without root.
#
# SKIFITY_INSTALLER_LIB is emptied for these: a shell in POSIX mode exports
# the assignment made for the `.` above, and the script would stop at once.
out=$(SKIFITY_INSTALLER_LIB="" sh "$INSTALLER" --help 2>&1) && status=0 || status=$?
if [ "$status" = 0 ]; then
  t_pass "--help exits zero, without root"
else
  t_fail "--help exited $status"
fi
for option in --domain --email --public-ip --version --image --skip-firewall --yes; do
  case "$out" in
  *"$option"*) ;;
  *) t_fail "--help does not mention $option" ;;
  esac
done
t_pass "--help lists the options"

out=$(SKIFITY_INSTALLER_LIB="" sh "$INSTALLER" --nonsense 2>&1) && status=0 || status=$?
case "$status:$out" in
2:*"Unknown option: --nonsense"*) t_pass "an unknown option is refused before anything runs" ;;
*) t_fail "an unknown option should exit 2 and say so, got $status: $out" ;;
esac

out=$(SKIFITY_INSTALLER_LIB="" sh "$INSTALLER" --domain 2>&1) && status=0 || status=$?
case "$status:$out" in
2:*"--domain needs a value"*) t_pass "an option missing its value is refused" ;;
*) t_fail "--domain with no value should be refused, got $status: $out" ;;
esac

# What somebody pastes is a URL as often as a name.
got=$( (parse_args --domain 'HTTPS://Panel.Example.TEST./setup' && printf '%s' "$SKIFITY_DOMAIN") 2>&1)
if [ "$got" = "panel.example.test" ]; then
  t_pass "a pasted URL becomes the bare domain"
else
  t_fail "--domain should be normalised to panel.example.test, got: $got"
fi
got=$( (parse_args --domain=panel.example.test --email=ops@example.test --yes &&
  printf '%s %s %s' "$SKIFITY_DOMAIN" "$SKIFITY_ACME_EMAIL" "$SKIFITY_ASSUME_YES") 2>&1)
if [ "$got" = "panel.example.test ops@example.test 1" ]; then
  t_pass "--option=value works as well as --option value"
else
  t_fail "--option=value was not read, got: $got"
fi
got=$( (parse_args --version v9.9.9 && derive_release && printf '%s' "$IMAGE $MANIFEST_BASE") 2>&1)
case "$got" in
*":v9.9.9 "*"/v9.9.9/deploy") t_pass "--version moves the image and the manifests together" ;;
*) t_fail "--version should change both the image and the manifests, got: $got" ;;
esac

# --version latest follows GitHub's redirect from /releases/latest to the
# newest release's tag; with no release at all, GitHub sends it to /releases.
latest_stub="$WORKDIR/latest-stub"
make_stub "$latest_stub" curl <<'STUB'
for arg in "$@"; do last=$arg; done
case "$last" in
*/releases/latest) printf '%s' "$STUB_LANDED" ;;
*) exit 22 ;;
esac
STUB
got=$( (PATH="$latest_stub:$PATH"; STUB_LANDED="https://github.com/Skifity/Skifity/releases/tag/v9.8.7"
  export STUB_LANDED; SKIFITY_VERSION=latest; SKIFITY_IMAGE=""
  resolve_version && derive_release && printf '%s %s' "$SKIFITY_VERSION" "$IMAGE") 2>&1)
case "$got" in
"v9.8.7 "*":v9.8.7") t_pass "--version latest installs the newest release's tag" ;;
*) t_fail "--version latest should resolve to v9.8.7, got: $got" ;;
esac
out=$( (PATH="$latest_stub:$PATH"; STUB_LANDED="https://github.com/Skifity/Skifity/releases"
  export STUB_LANDED; SKIFITY_VERSION=latest; LOG_FILE=/dev/null; resolve_version) 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"which Skifity release is the newest"*"--version <tag>"*) t_pass "with no release to find, --version latest says so and how to name one" ;;
*) t_fail "--version latest with no release should stop and explain, got $status: $out" ;;
esac

for bad in "--domain not_a_domain" "--domain localhost" "--email nobody" "--email a&b@example.test" \
  "--public-ip 300.1.2.3" "--public-ip example.test" "--pod-network calico" "--version v1;rm" \
  "--image registry.example.test/a|b" "--staging=1"; do
  # Word splitting into an option and its value is the point.
  # shellcheck disable=SC2086
  out=$( (parse_args $bad) 2>&1) && status=0 || status=$?
  if [ "$status" = 2 ]; then
    t_pass "refused: $bad"
  else
    t_fail "$bad should be refused, got $status: $out"
  fi
done
for good in "--public-ip 203.0.113.10" "--public-ip 2001:db8::10" "--pod-network vxlan" \
  "--domain xn--bcher-kva.example" "--image registry.example.test/team/skifity@sha256:abc123"; do
  # shellcheck disable=SC2086
  if (parse_args $good) >/dev/null 2>&1; then
    t_pass "accepted: $good"
  else
    t_fail "$good should be accepted"
  fi
done

# --- asking ---------------------------------------------------------------

# Under `curl | sh`, stdin is the script. A question read from it ate the next
# line of the installer, so questions go to the terminal; and with no terminal
# at all, nobody is there to say yes, so the answer is no.
answers="$WORKDIR/tty"
if (SKIFITY_ASSUME_YES=""; TTY_DEV="$WORKDIR/no/such/tty"; confirm "Carry on?") </dev/null >/dev/null 2>&1; then
  t_fail "with no terminal, confirm said yes on nobody's behalf"
else
  t_pass "with no terminal and no --yes, the answer is no"
fi
if (SKIFITY_ASSUME_YES=1; TTY_DEV="$WORKDIR/no/such/tty"; confirm "Carry on?") >/dev/null 2>&1; then
  t_pass "--yes answers yes"
else
  t_fail "--yes should answer yes"
fi
printf 'y\n' >"$answers"
if (SKIFITY_ASSUME_YES=""; TTY_DEV="$answers"; confirm "Carry on?") </dev/null >/dev/null 2>&1; then
  t_pass "an answer is read from the terminal, not from stdin"
else
  t_fail "confirm did not read y from the terminal"
fi
printf 'n\n' >"$answers"
if (SKIFITY_ASSUME_YES=""; TTY_DEV="$answers"; confirm "Carry on?") </dev/null >/dev/null 2>&1; then
  t_fail "confirm took n for yes"
else
  t_pass "n is no"
fi

printf 'not a domain\nPanel.Example.TEST\n' >"$answers"
got=$( (SKIFITY_DOMAIN=""; SKIFITY_ASSUME_YES=""; TTY_DEV="$answers"; KUBECONFIG_PATH="$WORKDIR/none"
  LOG_FILE=/dev/null; ask_for_domain && printf '%s' "$SKIFITY_DOMAIN") </dev/null 2>/dev/null)
if [ "$got" = "panel.example.test" ]; then
  t_pass "a fresh install asks for a domain, and asks again after a typo"
else
  t_fail "ask_for_domain should have taken panel.example.test, got: $got"
fi
printf '\n' >"$answers"
got=$( (SKIFITY_DOMAIN=""; SKIFITY_ASSUME_YES=""; TTY_DEV="$answers"; KUBECONFIG_PATH="$WORKDIR/none"
  LOG_FILE=/dev/null; ask_for_domain && printf '[%s]' "$SKIFITY_DOMAIN") </dev/null 2>/dev/null)
if [ "$got" = "[]" ]; then
  t_pass "Enter skips the domain"
else
  t_fail "an empty answer should leave no domain, got: $got"
fi
# A second run never asks: the panel already has an address.
: >"$WORKDIR/kubeconfig"
printf 'panel.example.test\n' >"$answers"
got=$( (SKIFITY_DOMAIN=""; SKIFITY_ASSUME_YES=""; TTY_DEV="$answers"; KUBECONFIG_PATH="$WORKDIR/kubeconfig"
  LOG_FILE=/dev/null; ask_for_domain && printf '[%s]' "$SKIFITY_DOMAIN") </dev/null 2>/dev/null)
if [ "$got" = "[]" ]; then
  t_pass "a second run does not ask for a domain"
else
  t_fail "ask_for_domain asked on a server that already has k3s, got: $got"
fi

# --- the address ------------------------------------------------------------

private_ok=0
for address in 10.0.0.5 172.16.0.1 172.31.255.255 192.168.1.10 100.64.0.1 100.127.255.255 127.0.0.1 169.254.169.254; do
  is_private_ipv4 "$address" || { t_fail "$address should count as private"; private_ok=1; }
done
for address in 203.0.113.10 172.15.0.1 172.32.0.1 100.63.255.255 100.128.0.1 8.8.8.8; do
  is_private_ipv4 "$address" && { t_fail "$address should count as public"; private_ok=1; }
done
t_check "$private_ok" "private and public addresses are told apart, carrier-grade NAT included"

# os-release sets VERSION, and VERSION is the release being installed. It used
# to be sourced into the installer, after which every message named the
# release as "24.04 LTS (Noble Numbat)".
printf 'ID=ubuntu\nVERSION="24.04 LTS (Noble Numbat)"\nPRETTY_NAME="Ubuntu 24.04 LTS"\n' >"$WORKDIR/os-release"
got=$( (VERSION=v1.2.3; OS_RELEASE="$WORKDIR/os-release"; os_field ID >/dev/null; os_field PRETTY_NAME >/dev/null
  printf '%s|%s|%s' "$VERSION" "$(os_field ID)" "$(os_field PRETTY_NAME)") 2>&1)
if [ "$got" = "v1.2.3|ubuntu|Ubuntu 24.04 LTS" ]; then
  t_pass "reading os-release leaves the release being installed alone"
else
  t_fail "os_field should not touch VERSION, got: $got"
fi

# A k3s that is already installed is asked which pod network it uses: a second
# run used to hand the panel an empty one.
mkdir -p "$WORKDIR/k3s-existing/config.yaml.d"
printf "ExecStart=/usr/local/bin/k3s \\\\\n    server \\\\\n    '--cluster-init' \\\\\n    '--flannel-backend=wireguard-native' \\\\\n" \
  >"$WORKDIR/k3s-existing/k3s.service"
got=$( (K3S_UNIT_PATH="$WORKDIR/k3s-existing/k3s.service"; K3S_CONFIG_DIR="$WORKDIR/k3s-existing"; existing_pod_network) 2>&1)
if [ "$got" = "wireguard-native" ]; then
  t_pass "the pod network of an installed k3s is read from its unit"
else
  t_fail "existing_pod_network should read wireguard-native from the unit, got: $got"
fi
printf 'flannel-backend: "vxlan"\n' >"$WORKDIR/k3s-existing/config.yaml.d/10-network.yaml"
got=$( (K3S_UNIT_PATH="$WORKDIR/none"; K3S_CONFIG_DIR="$WORKDIR/k3s-existing"; existing_pod_network) 2>&1)
if [ "$got" = "vxlan" ]; then
  t_pass "and from its configuration files"
else
  t_fail "existing_pod_network should read vxlan from config.yaml.d, got: $got"
fi

# --- one install at a time --------------------------------------------------

lock="$WORKDIR/lock"
mkdir -p "$lock"
printf '%s\n' "$$" >"$lock/pid"
out=$( (LOCK_DIR="$lock"; LOG_FILE=/dev/null; take_lock) 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"already running"*) t_pass "a second install is refused while the first is running" ;;
*) t_fail "take_lock should refuse a live lock, got $status: $out" ;;
esac
sh -c 'exit 0' &
dead=$!
wait "$dead" || true
printf '%s\n' "$dead" >"$lock/pid"
if (LOCK_DIR="$lock"; LOG_FILE=/dev/null; take_lock) >/dev/null 2>&1; then
  t_pass "a lock left by an install that was killed is taken over"
else
  t_fail "take_lock should take over a lock whose process is gone"
fi
rm -rf "$lock"

out=$( (LOG_FILE=/dev/null; interrupted) 2>&1) && status=0 || status=$?
case "$status:$out" in
130:*"run the installer again"*) t_pass "an interrupted install says running it again is how to finish" ;;
*) t_fail "interrupted should exit 130 and say to run again, got $status: $out" ;;
esac

# The whole install is one function called on the last line, so a download cut
# off halfway defines functions and runs nothing.
if [ "$(grep -v '^[[:space:]]*$' "$INSTALLER" | tail -n 1)" = 'main "$@"' ]; then
  t_pass "nothing runs until the whole script has arrived"
else
  t_fail "the last line of install.sh should be main \"\$@\""
fi

# --- the host firewall ------------------------------------------------------

fw="$WORKDIR/fw-ufw"
make_stub "$fw" ufw <<'STUB'
printf "ufw %s\n" "$*" >>"$FW_LOG"; [ "$1" = status ] && echo "Status: active"; exit 0
STUB
(PATH="$fw:$PATH"; FW_LOG="$WORKDIR/ufw.log"; export FW_LOG; LOG_FILE=/dev/null; SKIFITY_SKIP_FIREWALL=""
  configure_firewall) >/dev/null 2>&1
fw_ok=0
for want in "ufw allow 80/tcp" "ufw allow 443/tcp" "ufw allow from 10.42.0.0/16 to any" "ufw allow from 10.43.0.0/16 to any"; do
  grep -qx "$want" "$WORKDIR/ufw.log" 2>/dev/null || { t_fail "an active ufw was not told: $want"; fw_ok=1; }
done
grep -q "ufw allow 6443" "$WORKDIR/ufw.log" 2>/dev/null && { t_fail "the Kubernetes API was opened to the world"; fw_ok=1; }
t_check "$fw_ok" "an active ufw lets in HTTP, HTTPS and the cluster's networks, and nothing else"

fw="$WORKDIR/fw-firewalld"
make_stub "$fw" firewall-cmd <<'STUB'
printf "firewall-cmd %s\n" "$*" >>"$FW_LOG"; [ "$1" = --state ] && echo running; exit 0
STUB
(PATH="$fw:$PATH"; FW_LOG="$WORKDIR/firewalld.log"; export FW_LOG; LOG_FILE=/dev/null; SKIFITY_SKIP_FIREWALL=""
  have() { [ "$1" != ufw ] && command -v "$1" >/dev/null 2>&1; }
  configure_firewall) >/dev/null 2>&1
fw_ok=0
for want in "firewall-cmd --permanent --add-port=443/tcp" "firewall-cmd --permanent --zone=trusted --add-source=10.42.0.0/16" "firewall-cmd --reload"; do
  grep -qx -- "$want" "$WORKDIR/firewalld.log" 2>/dev/null || { t_fail "a running firewalld was not told: $want"; fw_ok=1; }
done
t_check "$fw_ok" "a running firewalld is configured, and reloaded so it takes effect"

# Oracle Cloud's images reject everything but SSH in iptables itself.
fw="$WORKDIR/fw-iptables"
make_stub "$fw" iptables <<'STUB'
printf "iptables %s\n" "$*" >>"$FW_LOG"
case "$1" in
-S) printf "%s\n" "-P INPUT ACCEPT" "-A INPUT -p tcp --dport 22 -j ACCEPT" "-A INPUT -j REJECT --reject-with icmp-host-prohibited" ;;
-C) exit 1 ;;
esac
exit 0
STUB
(PATH="$fw:$PATH"; FW_LOG="$WORKDIR/iptables.log"; export FW_LOG; LOG_FILE=/dev/null; SKIFITY_SKIP_FIREWALL=""
  have() { case "$1" in ufw | firewall-cmd | netfilter-persistent | iptables-save) return 1 ;; esac; command -v "$1" >/dev/null 2>&1; }
  configure_firewall) >/dev/null 2>&1
if grep -qx -- "iptables -I INPUT -p tcp --dport 443 -j ACCEPT" "$WORKDIR/iptables.log" 2>/dev/null &&
  grep -qx -- "iptables -I INPUT -s 10.42.0.0/16 -j ACCEPT" "$WORKDIR/iptables.log"; then
  t_pass "iptables rules that reject by default are opened for HTTP, HTTPS and the cluster"
else
  t_fail "a rejecting iptables INPUT chain was left closed: $(cat "$WORKDIR/iptables.log" 2>/dev/null)"
fi
make_stub "$fw" iptables <<'STUB'
printf "iptables %s\n" "$*" >>"$FW_LOG"
case "$1" in
-S) printf "%s\n" "-P INPUT ACCEPT" "-A INPUT -s 198.51.100.7/32 -j DROP" ;;
esac
exit 0
STUB
rm -f "$WORKDIR/iptables.log"
(PATH="$fw:$PATH"; FW_LOG="$WORKDIR/iptables.log"; export FW_LOG; LOG_FILE=/dev/null; SKIFITY_SKIP_FIREWALL=""
  have() { case "$1" in ufw | firewall-cmd) return 1 ;; esac; command -v "$1" >/dev/null 2>&1; }
  configure_firewall) >/dev/null 2>&1
if grep -q -- "-I INPUT" "$WORKDIR/iptables.log" 2>/dev/null; then
  t_fail "iptables was changed although nothing rejects by default"
else
  t_pass "iptables that only drops one address is left alone"
fi

# --- a whole install, against stand-ins ---------------------------------------

# Every step, in order, from the options to the last line, with each command
# that would change this machine or reach the network replaced by a stand-in,
# and every path moved into a directory of its own. The steps were only ever
# tested one at a time, and one of them called a function that did not exist:
# every real install stopped at the cluster token, under set -e, with
# "run: not found" and no explanation.
STUBS="$WORKDIR/stubs"
REAL_UID=$(id -u)
REAL_GID=$(id -g)
make_stub "$STUBS" id <<'STUB'
[ "$1" = -u ] && { echo 0; exit 0; }; exec /usr/bin/env -i PATH=/usr/bin:/bin id "$@"
STUB
make_stub "$STUBS" sleep <<'STUB'
exit 0
STUB
make_stub "$STUBS" modprobe <<'STUB'
exit 0
STUB
make_stub "$STUBS" ss <<'STUB'
exit 0
STUB
make_stub "$STUBS" df <<'STUB'
printf "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/vda1 104857600 10485760 94371840 10%% /\n"
STUB
make_stub "$STUBS" timedatectl <<'STUB'
echo yes
STUB
make_stub "$STUBS" ufw <<'STUB'
echo "Status: inactive"
STUB
make_stub "$STUBS" iptables <<'STUB'
echo "-P INPUT ACCEPT"
STUB
make_stub "$STUBS" k3s <<'STUB'
exit 0
STUB
make_stub "$STUBS" getent <<'STUB'
case "$2" in panel.example.test) echo "203.0.113.10    STREAM panel.example.test" ;; *) exit 2 ;; esac
STUB
make_stub "$STUBS" systemctl <<'STUB'
printf "systemctl %s\n" "$*" >>"$STUB_LOG"
case "$*" in
"is-active --quiet k3s") exit "${STUB_K3S_ACTIVE:-3}" ;;
"is-active --quiet "*) exit 3 ;;
esac
exit 0
STUB
make_stub "$STUBS" kubectl <<'STUB'
printf "kubectl %s\n" "$*" >>"$STUB_LOG"
case "$*" in
"get --raw /readyz") exit 0 ;;
"get nodes --no-headers") echo "node-1   Ready   control-plane,etcd   1m   v1.33.4+k3s1" ;;
"get nodes --no-headers -o custom-columns=NAME:.metadata.name") echo node-1 ;;
*ExternalIP*) ;;
*InternalIP*) printf "%s" "${STUB_NODE_IP:-10.0.0.5}" ;;
*"get ingress skifity-panel"*) [ -n "${STUB_ROUTE:-}" ] || exit 1; printf "%s" "$STUB_ROUTE" ;;
*"get deployment cert-manager"*) exit "${STUB_CERT_MANAGER:-1}" ;;
*"get deployment skifity-panel"*) [ -n "${STUB_PANEL_IMAGE:-}" ] || exit 1; printf "%s" "$STUB_PANEL_IMAGE" ;;
*"get clusterissuer"*) [ -n "${STUB_ISSUER_EMAIL:-}" ] || exit 1; printf "%s" "$STUB_ISSUER_EMAIL" ;;
"apply -f "*)
  file=$3
  [ -s "$file" ] || { echo "error: no objects passed to apply" >&2; exit 1; }
  cp "$file" "$STUB_DIR/applied-$(basename "$file")" ;;
*"rollout status"*) ;;
*"get svc skifity-panel"*) printf 30080 ;;
*"get certificate skifity-panel-tls"*) printf True ;;
*) printf "UNHANDLED kubectl %s\n" "$*" >>"$STUB_LOG" ;;
esac
exit 0
STUB
make_stub "$STUBS" curl <<'STUB'
out=""; url=""
while [ $# -gt 0 ]; do
  case "$1" in
  -o) out=$2; shift ;;
  http://* | https://*) url=$1 ;;
  esac
  shift
done
printf "curl %s\n" "$url" >>"$STUB_LOG"
emit() { if [ -n "$out" ]; then cat >"$out"; else cat; fi; }
case "$url" in
https://get.k3s.io) emit <"$STUB_DIR/fake-k3s-install.sh" ;;
*/cert-manager.yaml) printf "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: cert-manager\n" | emit ;;
https://api.ipify.org) printf "%s" "${STUB_OUTSIDE_IP:-203.0.113.10}" | emit ;;
*/api/health) [ "${STUB_PANEL_DOWN:-0}" = 1 ] && exit 7; printf "{\"status\":\"ok\"}" | emit ;;
*/api/setup/status) printf "{\"needs_setup\":%s,\"product\":\"Skifity\"}" "${STUB_NEEDS_SETUP:-true}" | emit ;;
*/api/cli/download) printf "#!/bin/sh\necho skifity\n" | emit ;;
*/releases/latest) printf "https://github.com/Skifity/Skifity/releases/tag/%s" "${STUB_LATEST:-v9.9.9}" ;;
*) printf "UNHANDLED curl %s\n" "$url" >>"$STUB_LOG"; exit 22 ;;
esac
STUB

# k3s's installer, as far as the steps after it can tell: a unit with the
# flags it was given, a join token and a kubeconfig.
cat >"$WORKDIR/fake-k3s-install.sh" <<'FAKE'
#!/bin/sh
set -eu
printf '%s\n' "$INSTALL_K3S_EXEC" >"$STUB_DIR/k3s-exec"
printf '%s\n' "$INSTALL_K3S_CHANNEL" >"$STUB_DIR/k3s-channel"
mkdir -p "$STUB_ROOT/etc/systemd/system" "$STUB_ROOT/var/lib/rancher/k3s/server" "$STUB_ROOT/etc/rancher/k3s"
for arg in $INSTALL_K3S_EXEC; do printf "    '%s' \\\\\n" "$arg"; done >"$STUB_ROOT/etc/systemd/system/k3s.service"
printf 'K10fake::server:not-a-real-token\n' >"$STUB_ROOT/var/lib/rancher/k3s/server/token"
printf 'apiVersion: v1\n' >"$STUB_ROOT/etc/rancher/k3s/k3s.yaml"
# Under curl | sh, stdin is the rest of the installer.
if read -r line; then printf '%s\n' "$line" >"$STUB_DIR/k3s-read-stdin"; fi
FAKE

# fake_root prepares the machine an install runs on: Ubuntu, 1.5 GB and no
# swap, systemd.
fake_root() {
  rm -rf "$1"
  mkdir -p "$1/etc" "$1/proc" "$1/run/systemd/system" "$1/sys/fs/cgroup" "$1/var/log" "$1/usr/local/bin" "$1/stub" "$1/tmp"
  printf 'ID=ubuntu\nVERSION="24.04 LTS (Noble Numbat)"\nPRETTY_NAME="Ubuntu 24.04 LTS"\n' >"$1/etc/os-release"
  printf 'MemTotal:        1536000 kB\nSwapTotal:             0 kB\n' >"$1/proc/meminfo"
  printf 'cpuset cpu io memory pids\n' >"$1/sys/fs/cgroup/cgroup.controllers"
  cp "$WORKDIR/fake-k3s-install.sh" "$1/stub/fake-k3s-install.sh"
}

# run_install ROOT [options...] runs main against ROOT, in a shell of its own
# so nothing it sets leaks into the next run. STUB_* settings pass through.
run_install() {
  fake="$1"
  shift
  # The script is in single quotes so the inner shell expands it.
  # shellcheck disable=SC2016
  env PATH="$STUBS:$PATH" STUB_DIR="$fake/stub" STUB_ROOT="$fake" STUB_LOG="$fake/stub/calls.log" \
    TMPDIR="$fake/tmp" REAL_UID="$REAL_UID" REAL_GID="$REAL_GID" \
    sh -c '
      SKIFITY_INSTALLER_LIB=1 . "$1/installer/install.sh"
      SOURCE_DIR="$1"
      R="$2"
      shift 2
      CONFIG_DIR="$R/etc/skifity"
      DATA_DIR="$R/var/lib/skifity"
      MANIFEST_DIR="$R/var/lib/skifity/manifests"
      LOG_FILE="$R/var/log/skifity-install.log"
      KUBECONFIG_PATH="$R/etc/rancher/k3s/k3s.yaml"
      K3S_CONFIG_DIR="$R/etc/rancher/k3s"
      K3S_UNIT_PATH="$R/etc/systemd/system/k3s.service"
      K3S_TOKEN_PATH="$R/var/lib/rancher/k3s/server/token"
      K3S_MANIFESTS_DIR="$R/var/lib/rancher/k3s/server/manifests"
      CLI_PATH="$R/usr/local/bin/skifity"
      UNINSTALLER_PATH="$R/usr/local/bin/skifity-uninstall"
      LOCK_DIR="$R/run/skifity-install.lock"
      SYSTEMD_DIR="$R/run/systemd/system"
      OS_RELEASE="$R/etc/os-release"
      MEMINFO="$R/proc/meminfo"
      CGROUP_CONTROLLERS="$R/sys/fs/cgroup/cgroup.controllers"
      PROC_CGROUPS="$R/proc/cgroups"
      TTY_DEV="$R/dev/tty"
      RUN_UID="$REAL_UID"
      RUN_GID="$REAL_GID"
      main "$@"
    ' run-install "$ROOT" "$fake" "$@"
}

IMAGE_UNDER_TEST="registry.example.test/skifity:0.0.0-test"

# A fresh server behind NAT, no domain: the common case.
FAKE="$WORKDIR/root-http"
fake_root "$FAKE"
out=$(run_install "$FAKE" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
if [ "$status" = 0 ]; then
  t_pass "a whole install runs from start to finish"
else
  t_fail "the install stopped with $status:
$out"
fi
token=$(cat "$FAKE/etc/skifity/setup-token" 2>/dev/null || true)
case "$out" in
*"http://203-0-113-10.sslip.io/setup#token=${token}"*) t_pass "it ends with the setup link, on the public address of a server behind NAT" ;;
*) t_fail "the install should end with the setup link on 203-0-113-10.sslip.io, got:
$out" ;;
esac
case "$out" in
*"10.0.0.5 on its own network and 203.0.113.10 on the internet"*) t_pass "and says which address is which" ;;
*) t_fail "the NAT lookup should name both addresses" ;;
esac
if grep -q -- "--flannel-backend=wireguard-native" "$FAKE/stub/k3s-exec" 2>/dev/null &&
  grep -q -- "--cluster-init" "$FAKE/stub/k3s-exec"; then
  t_pass "k3s is installed with embedded etcd and an encrypted pod network"
else
  t_fail "k3s was not installed with the expected flags: $(cat "$FAKE/stub/k3s-exec" 2>/dev/null)"
fi
if [ -e "$FAKE/stub/k3s-read-stdin" ]; then
  t_fail "k3s's installer was handed the installer's stdin"
else
  t_pass "k3s's installer cannot read the rest of a piped script"
fi
if cmp -s "$FAKE/var/lib/rancher/k3s/server/token" "$FAKE/etc/skifity/cluster-token" &&
  [ -n "$(find "$FAKE/etc/skifity/cluster-token" -perm 0600)" ]; then
  t_pass "the panel gets the cluster's join token, readable by nobody else"
else
  t_fail "the cluster token was not copied for the panel, or is readable by others"
fi
if [ -x "$FAKE/usr/local/bin/skifity" ] && [ -x "$FAKE/usr/local/bin/skifity-uninstall" ]; then
  t_pass "the CLI and the uninstaller are on the PATH"
else
  t_fail "the CLI or the uninstaller was not installed"
fi
if grep -q '__[A-Z0-9_]*__' "$FAKE/stub/applied-panel.yaml" "$FAKE/stub/applied-ingress.yaml" 2>/dev/null; then
  t_fail "a placeholder reached the cluster"
elif grep -q 'value: "http://203-0-113-10.sslip.io"' "$FAKE/stub/applied-panel.yaml" 2>/dev/null &&
  grep -q 'value: "wireguard-native"' "$FAKE/stub/applied-panel.yaml" &&
  grep -q "image: $IMAGE_UNDER_TEST" "$FAKE/stub/applied-panel.yaml"; then
  t_pass "the panel is applied with its address, its pod network and the image asked for"
else
  t_fail "the applied panel is missing its address, pod network or image"
fi
if [ -e "$FAKE/run/skifity-install.lock" ] || [ -n "$(ls -A "$FAKE/tmp" 2>/dev/null)" ]; then
  t_fail "the install left its lock or its temporary files behind"
else
  t_pass "the lock and the temporary files are gone afterwards"
fi
if [ -n "$(find "$FAKE/var/log/skifity-install.log" -perm 0600)" ]; then
  t_pass "the log is readable by root alone"
else
  t_fail "the install log is readable by other users"
fi
case "$out" in
*"no swap"*) t_pass "a server under 2 GB with no swap is warned about" ;;
*) t_fail "1.5 GB with no swap should be warned about" ;;
esac
if grep -q UNHANDLED "$FAKE/stub/calls.log"; then
  t_fail "the install made a call the stand-ins do not know: $(grep UNHANDLED "$FAKE/stub/calls.log" | head -3)"
else
  t_pass "every command the install ran was one the stand-ins expected"
fi

# The same server, run again after the first account exists, to upgrade.
rm -f "$FAKE/stub/k3s-exec"
out=$(export STUB_K3S_ACTIVE=0 STUB_NEEDS_SETUP=false STUB_ROUTE="203-0-113-10.sslip.io " \
  STUB_PANEL_IMAGE="registry.example.test/skifity:0.0.0-old"
  run_install "$FAKE" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
if [ "$status" = 0 ] && [ ! -e "$FAKE/stub/k3s-exec" ]; then
  t_pass "a second run leaves a running k3s alone"
else
  t_fail "the second run stopped with $status, or reinstalled k3s:
$out"
fi
case "$out" in
*"setup#token"*) t_fail "a second run after setup printed a setup link that no longer works" ;;
*"Sign in at "*"http://203-0-113-10.sslip.io"*) t_pass "after setup, a second run says where to sign in instead" ;;
*) t_fail "a second run after setup should say where to sign in, got:
$out" ;;
esac
case "$out" in
*"Upgrading the panel from registry.example.test/skifity:0.0.0-old"*) t_pass "an upgrade says what it is upgrading from" ;;
*) t_fail "the second run should say it is upgrading the panel" ;;
esac
if grep -q 'value: "wireguard-native"' "$FAKE/stub/applied-panel.yaml" 2>/dev/null; then
  t_pass "a second run tells the panel the pod network k3s was installed with"
else
  t_fail "a second run handed the panel no pod network"
fi

# With a domain: cert-manager, the issuer, and a route with a certificate.
FAKE="$WORKDIR/root-https"
fake_root "$FAKE"
out=$(run_install "$FAKE" --image "$IMAGE_UNDER_TEST" --domain Panel.Example.TEST --email ops@example.test 2>&1) &&
  status=0 || status=$?
if [ "$status" = 0 ] && grep -q "tls:" "$FAKE/stub/applied-ingress.yaml" 2>/dev/null &&
  grep -q "host: panel.example.test" "$FAKE/stub/applied-ingress.yaml"; then
  t_pass "with --domain, the panel's route asks for a certificate for that name"
else
  t_fail "the HTTPS install did not apply a TLS route for panel.example.test ($status):
$out"
fi
if grep -q "kubectl apply -f .*/cert-manager.yaml" "$FAKE/stub/calls.log" 2>/dev/null &&
  grep -q "email: ops@example.test" "$FAKE/stub/applied-cluster-issuer.yaml" 2>/dev/null &&
  grep -q "acme-v02.api.letsencrypt.org" "$FAKE/stub/applied-cluster-issuer.yaml"; then
  t_pass "cert-manager is downloaded, applied, and given an issuer with the address"
else
  t_fail "cert-manager or its issuer was not set up"
fi
case "$out" in
*"https://panel.example.test/setup#token="*) t_pass "and the setup link is on HTTPS" ;;
*) t_fail "the setup link should be on https://panel.example.test" ;;
esac

# Run again to upgrade, with none of the first run's options. This used to
# move a panel with a domain back to plain HTTP on an sslip.io name.
out=$(export STUB_K3S_ACTIVE=0 STUB_CERT_MANAGER=0 STUB_ROUTE="panel.example.test skifity-panel-tls" \
  STUB_ISSUER_EMAIL=ops@example.test
  run_install "$FAKE" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
if [ "$status" = 0 ] && grep -q "tls:" "$FAKE/stub/applied-ingress.yaml" 2>/dev/null &&
  grep -q "email: ops@example.test" "$FAKE/stub/applied-cluster-issuer.yaml" 2>/dev/null; then
  t_pass "a second run without --domain keeps the domain, its certificate and the issuer's address"
else
  t_fail "a second run without --domain lost the panel's domain ($status):
$out"
fi

# No version and no image named, but `latest`: the release GitHub calls the
# newest is the image installed, and the one named on the way in.
FAKE="$WORKDIR/root-latest"
fake_root "$FAKE"
out=$(run_install "$FAKE" --version latest 2>&1) && status=0 || status=$?
if [ "$status" = 0 ] && grep -q "image: ghcr.io/skifity/skifity:v9.9.9" "$FAKE/stub/applied-panel.yaml" 2>/dev/null; then
  t_pass "an install with --version latest installs the newest release's image"
else
  t_fail "--version latest did not install the newest release ($status):
$out"
fi
case "$out" in
*"Installing v9.9.9"*"Skifity v9.9.9 is installed"*) t_pass "and names it, at the start and at the end" ;;
*) t_fail "the install should name v9.9.9 at both ends" ;;
esac

# A route that does not reach the panel is a warning that says where to look,
# not a silent success.
FAKE="$WORKDIR/root-down"
fake_root "$FAKE"
out=$(export STUB_PANEL_DOWN=1; run_install "$FAKE" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
case "$status:$out" in
0:*"does not reach it through the ingress"*"describe ingress skifity-panel"*"warnings above"*)
  t_pass "a panel its address does not reach is reported, with where to look" ;;
*) t_fail "an unreachable panel should be a warning with diagnostics, got $status:
$out" ;;
esac

# Not Ubuntu or Debian, and nobody to ask: stop before changing anything.
FAKE="$WORKDIR/root-other"
fake_root "$FAKE"
printf 'ID=gentoo\nPRETTY_NAME="Gentoo Linux"\n' >"$FAKE/etc/os-release"
out=$(run_install "$FAKE" --image "$IMAGE_UNDER_TEST" </dev/null 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"nobody confirmed"*"--yes"*) t_pass "an untested system with nobody to ask is a refusal, not a yes" ;;
*) t_fail "an untested system without a terminal should stop and mention --yes, got $status:
$out" ;;
esac
if [ -e "$FAKE/stub/k3s-exec" ]; then
  t_fail "k3s was installed although the install was refused"
else
  t_pass "and nothing was installed"
fi

# --- nothing was installed --------------------------------------------------

# The point of sourcing the installer is that it changes nothing. If any of
# this appeared, a step ran that should not have.
for path in /etc/rancher /var/lib/rancher /usr/local/bin/k3s; do
  if [ -e "$path" ]; then
    t_fail "$path exists: the smoke test must not install anything"
  fi
done
t_pass "nothing was installed on this machine"

# --- the uninstaller refuses to delete data without being told --------------

UNINSTALL="$ROOT/installer/uninstall.sh"

# Every check below is a dry run. The uninstaller deletes the master key, and a
# test that could delete it on the machine running the test is not a test worth
# having, however carefully the confirmation is worded.
out=$(sh "$UNINSTALL" --dry-run --all --purge 2>&1 || true)
case "$out" in
*"Nothing was changed"*) t_pass "a dry run changes nothing" ;;
*) t_fail "a dry run should say nothing was changed, got: $out" ;;
esac
case "$out" in
*"would run: rm -rf"*) t_pass "a dry run says what it would delete" ;;
*) t_fail "a dry run should name what it would delete" ;;
esac
case "$out" in
*"✓"*) t_fail "a dry run must not tick off work it did not do" ;;
*) t_pass "a dry run does not claim work it did not do" ;;
esac

# The typed confirmation is what stands between a stray --purge and an
# unreadable backup, so check it is asked for.
grep -q 'delete my data' "$UNINSTALL" &&
  t_pass "purging asks for a typed confirmation" ||
  t_fail "purging should require a typed confirmation"

out=$(sh "$UNINSTALL" --nonsense 2>&1 || true)
case "$out" in
*"Unknown option"*) t_pass "an unknown option is refused" ;;
*) t_fail "an unknown option should be refused, got: $out" ;;
esac

out=$(sh "$UNINSTALL" --help 2>&1 || true)
case "$out" in
*"--purge"*) t_pass "the uninstaller documents itself" ;;
*) t_fail "--help should list the options" ;;
esac

printf '\n'
if [ "$FAILURES" -gt 0 ]; then
  printf '%s installer smoke check(s) failed.\n\n' "$FAILURES"
  exit 1
fi
printf 'All installer smoke checks passed.\n\n'
