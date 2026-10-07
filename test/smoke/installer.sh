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

# The repository is substituted into a manifest with sed, and tells the panel
# where to look for releases, so it is held to what a repository path is.
out=$( (PROJECT_REPO='acme/skifity|evil'; validate_settings) 2>&1) && status=0 || status=$?
case "$status:$out" in
2:*"not a repository such as owner/name"*) t_pass "a repository that is not owner/name is refused" ;;
*) t_fail "SKIFITY_REPO with a | in it should be refused, got $status: $out" ;;
esac
( PROJECT_REPO="acme/skifity"; validate_settings ) >/dev/null 2>&1 &&
  t_pass "and owner/name is accepted" || t_fail "a repository such as acme/skifity should be accepted"

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

# --- the registry's port ------------------------------------------------------

# The registry is a NodePort that takes no password, and a NodePort listens on
# every address the server has. Nothing but this machine's own container runtime
# has a reason to ask, so the install drops what arrives from anywhere else.
guard="$WORKDIR/guard"
unitdir="$WORKDIR/guard-units"
mkdir -p "$unitdir"
make_stub "$guard" iptables <<'STUB'
# A rule that is not there yet: -C says no until -I has been run.
printf "iptables %s\n" "$*" >>"$FW_LOG"
case " $* " in
*" -C "*) [ -f "$FW_LOG.added" ] || exit 1 ;;
*" -I "*) : >"$FW_LOG.added" ;;
esac
exit 0
STUB
make_stub "$guard" systemctl <<'STUB'
printf "systemctl %s\n" "$*" >>"$FW_LOG"
exit 0
STUB
rm -f "$WORKDIR/guard.log" "$WORKDIR/guard.log.added"
out=$( (PATH="$guard:$PATH"; FW_LOG="$WORKDIR/guard.log"; export FW_LOG; LOG_FILE=/dev/null
  UNIT_DIR="$unitdir"; SKIFITY_SKIP_FIREWALL=""; guard_registry_port) 2>&1)
want_rule="iptables -w -t raw -I PREROUTING -p tcp --dport 30500 -m addrtype --dst-type LOCAL ! -i lo -j DROP"
if grep -qx -- "$want_rule" "$WORKDIR/guard.log"; then
  t_pass "the registry's port is dropped for anything that is not this machine, ahead of any firewall"
else
  t_fail "the registry's port was not closed: $(cat "$WORKDIR/guard.log" 2>/dev/null)"
fi
case "$out" in *"the registry's port, is closed to the network"*) t_pass "and the install says so" ;; *) t_fail "no confirmation: $out" ;; esac
if grep -q -- "-I PREROUTING -p tcp --dport 30500" "$unitdir/skifity-registry-guard.service" 2>/dev/null &&
  grep -q "^Before=.*k3s.service" "$unitdir/skifity-registry-guard.service" &&
  grep -qx "systemctl enable skifity-registry-guard.service" "$WORKDIR/guard.log"; then
  t_pass "a systemd unit repeats the rule at every boot, before k3s starts"
else
  t_fail "the rule would not survive a reboot: $(cat "$unitdir/skifity-registry-guard.service" 2>/dev/null)"
fi

# Run again: the rule is there, so it is not added twice.
: >"$WORKDIR/guard.log"
(PATH="$guard:$PATH"; FW_LOG="$WORKDIR/guard.log"; export FW_LOG; LOG_FILE=/dev/null
  UNIT_DIR="$unitdir"; SKIFITY_SKIP_FIREWALL=""; guard_registry_port) >/dev/null 2>&1
if grep -q -- " -I " "$WORKDIR/guard.log"; then
  t_fail "a second run added the rule again"
else
  t_pass "running the installer again does not stack a second rule"
fi

# --skip-firewall means leave the firewall alone, and says what that costs.
: >"$WORKDIR/guard.log"
out=$( (PATH="$guard:$PATH"; FW_LOG="$WORKDIR/guard.log"; export FW_LOG; LOG_FILE=/dev/null
  UNIT_DIR="$unitdir"; SKIFITY_SKIP_FIREWALL=1; guard_registry_port) 2>&1)
if [ ! -s "$WORKDIR/guard.log" ]; then
  t_pass "--skip-firewall touches no firewall"
else
  t_fail "--skip-firewall still ran: $(cat "$WORKDIR/guard.log")"
fi
case "$out" in *"takes no password"*"--dst-type LOCAL"*) t_pass "and says what is left open, with the command to close it" ;; *) t_fail "no warning: $out" ;; esac

# A server with no iptables, and no way to get it, says so rather than
# reporting a port closed that is not.
out=$( (LOG_FILE=/dev/null; UNIT_DIR="$unitdir"; SKIFITY_SKIP_FIREWALL=""
  have() { return 1; }
  guard_registry_port) 2>&1)
case "$out" in *"could not be closed: iptables is not on this server"*) t_pass "without iptables it says the port is open, not that it is closed" ;; *) t_fail "got: $out" ;; esac

# --- a domain behind Cloudflare -----------------------------------------------

if is_cloudflare_address 104.21.11.36 && is_cloudflare_address 172.67.1.1 && is_cloudflare_address 131.0.72.5 &&
  is_cloudflare_address 162.159.0.1 && ! is_cloudflare_address 103.171.85.54 && ! is_cloudflare_address 104.15.255.255 &&
  ! is_cloudflare_address 104.32.0.1 && ! is_cloudflare_address "" && ! is_cloudflare_address 2606:4700::1; then
  t_pass "Cloudflare's addresses are told from a server's, at the edges of its ranges too"
else
  t_fail "is_cloudflare_address is wrong"
fi
cf="$WORKDIR/cf-dns"
make_stub "$cf" getent <<'STUB'
case "$2" in
proxied.example.test) echo "104.21.11.36    STREAM $2" ;;
elsewhere.example.test) echo "198.51.100.9    STREAM $2" ;;
here.example.test) echo "203.0.113.10    STREAM $2" ;;
esac
STUB
for case_ in proxied elsewhere here; do
  out=$( (PATH="$cf:$PATH"; PANEL_HOST="$case_.example.test"; PUBLIC_IP=203.0.113.10; check_domain_points_here) 2>&1)
  case "$case_:$out" in
  proxied:*"behind Cloudflare's proxy"*"DNS only"*"Full (strict)"*) t_pass "a domain behind Cloudflare's proxy is told to go grey, not to change a record that is right" ;;
  elsewhere:*"resolves to 198.51.100.9"*"Point the A record"*) t_pass "a domain that points somewhere else is still told to point here" ;;
  here:*"already points at this server"*) t_pass "a domain that points here is left alone" ;;
  *) t_fail "$case_: $out" ;;
  esac
done
case "$(PATH="$cf:$PATH"; PANEL_HOST=proxied.example.test; PUBLIC_IP=203.0.113.10; check_domain_points_here 2>&1)" in
*"Point the A record at"*) t_fail "a Cloudflare address was told to change its record" ;;
*) t_pass "and is not told to point the record anywhere" ;;
esac

# --- saying why a pod is not ready ------------------------------------------------

# "The webhook never became ready" sent a first-time user to run commands on a
# server where they had never used kubectl. The cluster already knew why.
diag="$WORKDIR/diag"
make_stub "$diag" kubectl <<'STUB'
case "$*" in
*"get pods"*"--no-headers"*)
  case "${STUB_PODS:-pull}" in
  pull) printf '%s\n' "cert-manager-webhook-abc   0/1   ImagePullBackOff   0   3m" "cert-manager-cainjector-x   1/1   Running   0   3m" ;;
  crash) printf '%s\n' "web-1   0/1   CrashLoopBackOff   4   3m" ;;
  pending) printf '%s\n' "web-1   0/1   Pending   0   3m" ;;
  healthy) printf '%s\n' "web-1   1/1   Running   0   3m" ;;
  esac ;;
*"-o jsonpath={.spec.containers[0].image}"*) echo "quay.io/jetstack/cert-manager-webhook:v1.21.2" ;;
*"containerStatuses"*)
  case "${STUB_PODS:-pull}" in
  pull) echo "ImagePullBackOff||" ;;
  crash) echo "CrashLoopBackOff|Error|1" ;;
  pending) echo "||" ;;
  esac ;;
*"-o jsonpath={.status.phase}"*)
  case "${STUB_PODS:-pull}" in pending) echo Pending ;; *) echo Running ;; esac ;;
*"get events"*)
  case "${STUB_PODS:-pull}" in
  pull) printf '%s\n' "2m   Normal    Pulling   pod/cert-manager-webhook-abc   Pulling image" \
    "2m   Warning   Failed    pod/cert-manager-webhook-abc   Failed to pull image: lookup quay.io: Temporary failure in name resolution" ;;
  pending) printf '%s\n' "1m   Warning   FailedScheduling   pod/web-1   0/1 nodes are available: 1 Insufficient memory." ;;
  esac ;;
esac
exit 0
STUB
out=$( (PATH="$diag:$PATH"; STUB_PODS=pull; export STUB_PODS; explain_pods cert-manager; printf '%s' "$DIAGNOSIS") 2>&1)
case "$out" in
*"cert-manager-webhook-abc: ImagePullBackOff"*"Temporary failure in name resolution"*"could not download quay.io/jetstack/cert-manager-webhook:v1.21.2"*"getent hosts quay.io"*)
  t_pass "a pod that cannot pull says which image, why, and how to check the server's way to its registry" ;;
*) t_fail "diagnosis was: $out" ;;
esac
case "$out" in
*"cainjector"*) t_fail "a pod that is fine was listed as a problem: $out" ;;
*"Pulling image"*) t_fail "a routine event was shown as if it were a problem: $out" ;;
*) t_pass "pods that are ready, and events that are routine, are left out" ;;
esac
out=$( (PATH="$diag:$PATH"; STUB_PODS=crash; export STUB_PODS; explain_pods demo "-l app=web"; printf '%s' "$DIAGNOSIS") 2>&1)
case "$out" in
*"CrashLoopBackOff (last stopped: Error)"*"logs web-1 --previous"*"pod network"*) t_pass "a pod that keeps stopping says how, where its log is, and the likeliest cause" ;;
*) t_fail "diagnosis was: $out" ;;
esac
out=$( (PATH="$diag:$PATH"; STUB_PODS=pending; export STUB_PODS; explain_pods demo; printf '%s' "$DIAGNOSIS") 2>&1)
case "$out" in
*"web-1: Pending"*"Insufficient memory"*"Waiting for a place to run"*) t_pass "a pod with nowhere to run says what is missing" ;;
*) t_fail "diagnosis was: $out" ;;
esac
out=$( (PATH="$diag:$PATH"; STUB_PODS=healthy; export STUB_PODS; explain_pods demo; printf '%s' "$DIAGNOSIS") 2>&1)
case "$out" in
*"No pod in demo is reported as failing"*) t_pass "pods that are all ready are not blamed" ;;
*) t_fail "diagnosis was: $out" ;;
esac
out=$( (PATH="$diag:$PATH"; STUB_PODS=pull; export STUB_PODS; LOG_FILE=/dev/null; explain_pods cert-manager; fail "It did not work." "Try again.") 2>&1) || true
case "$out" in
*"It did not work."*"What the cluster says"*"ImagePullBackOff"*"What to do"*) t_pass "the failure message carries the cluster's own explanation, before what to do" ;;
*) t_fail "failure was: $out" ;;
esac

# --- what the node keeps for itself -------------------------------------------

kubelet_dir="$WORKDIR/kubelet"
mkdir -p "$kubelet_dir"
for case_ in "1000000:256Mi:100Mi" "3000000:512Mi:200Mi" "6008000:1Gi:300Mi"; do
  kb=${case_%%:*}
  rest=${case_#*:}
  want_system=${rest%%:*}
  want_hard=${rest#*:}
  printf 'MemTotal:  %s kB\n' "$kb" >"$kubelet_dir/meminfo"
  rm -rf "$kubelet_dir/k3s"
  (K3S_CONFIG_DIR="$kubelet_dir/k3s"; MEMINFO="$kubelet_dir/meminfo"; LOG_FILE=/dev/null; write_kubelet_config) >/dev/null 2>&1
  file="$kubelet_dir/k3s/config.yaml.d/10-skifity-kubelet.yaml"
  if grep -q "system-reserved=cpu=100m,memory=$want_system" "$file" 2>/dev/null &&
    grep -q "eviction-hard=memory.available<$want_hard,nodefs.available<10%,imagefs.available<15%,nodefs.inodesFree<5%" "$file"; then
    t_pass "a $((kb / 1024)) MB server keeps $want_system back for k3s and evicts at $want_hard free"
  else
    t_fail "the kubelet's reservations for $kb kB are wrong: $(cat "$file" 2>/dev/null)"
  fi
done
rm -rf "$kubelet_dir/k3s"
printf 'garbage\n' >"$kubelet_dir/meminfo"
(K3S_CONFIG_DIR="$kubelet_dir/k3s"; MEMINFO="$kubelet_dir/meminfo"; LOG_FILE=/dev/null; write_kubelet_config) >/dev/null 2>&1
if [ ! -e "$kubelet_dir/k3s/config.yaml.d/10-skifity-kubelet.yaml" ]; then
  t_pass "a machine whose memory cannot be read gets the defaults, not a guess"
else
  t_fail "a file was written without knowing the memory"
fi

# --- waiting, and saying so ----------------------------------------------------

# The slow parts of an install used to print nothing: a server that was working
# looked like one that had stopped. Every wait now says what it is waiting for.

if [ "$(fmt_elapsed 7)" = "0:07" ] && [ "$(fmt_elapsed 65)" = "1:05" ] && [ "$(fmt_elapsed 754)" = "12:34" ]; then
  t_pass "time so far is shown as m:ss"
else
  t_fail "fmt_elapsed is wrong: $(fmt_elapsed 7) $(fmt_elapsed 65) $(fmt_elapsed 754)"
fi
got=$( (PROGRESS_SECONDS=3; took; PROGRESS_SECONDS=12; took; PROGRESS_SECONDS=125; took) )
if [ "$got" = " (12s) (2m 5s)" ]; then
  t_pass "a finished wait says how long it took, unless it was over in a moment"
else
  t_fail "took should be silent under five seconds, got: $got"
fi
if [ "$(LC_ALL=C.UTF-8 spinner_frame 0)" = "⠋" ] && [ "$(LC_ALL=C.UTF-8 spinner_frame 10)" = "⠋" ] &&
  [ "$(LC_ALL=C LANG='' spinner_frame 0)" = "|" ] && [ "$(LC_ALL=C LANG='' spinner_frame 3)" = "$(printf '\134')" ]; then
  t_pass "the spinner is braille where the locale can show it and ASCII where it cannot"
else
  t_fail "spinner_frame is wrong"
fi
printf 'one\n\n[INFO]  Downloading binary\n[INFO]  systemd: Starting k3s\n\n' >"$WORKDIR/k3s.out"
if [ "$(k3s_install_detail "$WORKDIR/k3s.out")" = "systemd: Starting k3s" ]; then
  t_pass "what the k3s installer last said is shown without its prefix"
else
  t_fail "k3s_install_detail should say what k3s's installer last printed"
fi

# Without a terminal: a line when the wait starts, and the step's own output in
# the log and nowhere else.
wlog="$WORKDIR/wait.log"
: >"$wlog"
steps="$WORKDIR/steps"
make_stub "$steps" noisy <<'STUB'
echo "downloading the thing"
echo "line two"
exit "${NOISY_EXIT:-0}"
STUB
out=$( (PROGRESS_MODE=plain; LOG_FILE="$wlog"; TMP_DIR="$WORKDIR"; with_progress "Doing the thing" "" "$steps/noisy"; printf 'rc=%s\n' "$?") 2>&1)
case "$out" in
*"Doing the thing"*"rc=0"*) t_pass "a wait without a terminal says what it is waiting for, once, at the start" ;;
*) t_fail "with_progress printed: $out" ;;
esac
case "$out" in
*"downloading the thing"*) t_fail "the step's own output reached the terminal" ;;
*) t_pass "and what the step prints stays out of the way" ;;
esac
if grep -q "Doing the thing: [0-9]*s, exit 0" "$wlog" && grep -q "downloading the thing" "$wlog"; then
  t_pass "while the log keeps all of it, under a heading with how it ended"
else
  t_fail "the log should hold the step's output under a heading: $(cat "$wlog")"
fi

# A step that fails: its status comes back, and its last lines are kept for the
# failure message, which used to say only "see the log".
out=$( (PROGRESS_MODE=plain; LOG_FILE="$wlog"; TMP_DIR="$WORKDIR"; NOISY_EXIT=7; export NOISY_EXIT
  with_progress "Doing the thing" "" "$steps/noisy" || printf 'rc=%s\n' "$?"
  fail "It did not work." "Try again.") 2>&1) && status=0 || status=$?
case "$out" in
*"rc=7"*"It did not work."*"The last lines it printed"*"line two"*"What to do"*) t_pass "a failed step's last lines are in the failure message, and its status is kept" ;;
*) t_fail "the failure should show what the step said, got: $out" ;;
esac

# On a terminal: one line, redrawn in place, with a spinner, what is
# happening, and the time so far.
make_stub "$steps" slow <<'STUB'
sleep 1.4
STUB
detail_demo() { printf 'pulling the image'; }
( PROGRESS_MODE="tty"; LOG_FILE="$wlog"; TMP_DIR="$WORKDIR"; LC_ALL="C.UTF-8"; with_progress "Starting the panel" detail_demo "$steps/slow" ) >"$WORKDIR/tty.out" 2>&1
cr=$(printf '\r')
if grep -q "$cr" "$WORKDIR/tty.out" && grep -q "Starting the panel" "$WORKDIR/tty.out" &&
  grep -q "pulling the image" "$WORKDIR/tty.out" && grep -q "0:0[01]" "$WORKDIR/tty.out"; then
  t_pass "on a terminal the wait is one line, redrawn, with what is happening and the time so far"
else
  t_fail "the terminal progress is wrong: $(od -c "$WORKDIR/tty.out" | head -5)"
fi
if [ "$(tail -c 4 "$WORKDIR/tty.out" | od -An -c | tr -d ' ')" = '\r033[K' ]; then
  t_pass "and the line is cleared when the wait is over, so the result can take its place"
else
  t_fail "the spinner line was not cleared: $(tail -c 8 "$WORKDIR/tty.out" | od -c | head -2)"
fi
# And the line is never wider than the terminal.
( PROGRESS_MODE="tty"; PROGRESS_COLS=50; progress_draw "A label" "a very long detail that goes on and on and on and on and on" 0 5 ) >"$WORKDIR/wide.out"
if [ "$(sed 's/\x1b\[[0-9;]*[A-Za-z]//g; s/\r//' "$WORKDIR/wide.out" | wc -m | tr -d ' ')" -le 50 ]; then
  t_pass "the line is cut to the width of the terminal"
else
  t_fail "the progress line is wider than the terminal: $(cat "$WORKDIR/wide.out")"
fi

# A wait that is still going says so every so often, for a log that cannot redraw.
out=$( (PROGRESS_MODE=plain; PROGRESS_PLAIN_EVERY=1; LOG_FILE="$wlog"; TMP_DIR="$WORKDIR"
  with_progress "Starting the panel" detail_demo "$steps/slow") 2>&1)
if [ "$(printf '%s\n' "$out" | grep -c 'pulling the image')" -ge 1 ] && printf '%s' "$out" | grep -q "(0:0[12])"; then
  t_pass "without a terminal a long wait says it is still going, with what and for how long"
else
  t_fail "a long wait should repeat itself in a log, got: $out"
fi

# --verbose shows what the step prints, as it prints it, and keeps its status.
out=$( (PROGRESS_MODE=plain; SKIFITY_VERBOSE=1; LOG_FILE="$wlog"; TMP_DIR="$WORKDIR"; NOISY_EXIT=3; export NOISY_EXIT
  with_progress "Doing the thing" "" "$steps/noisy" || printf 'rc=%s\n' "$?") 2>&1)
case "$out" in
*"    downloading the thing"*"    line two"*"rc=3"*) t_pass "--verbose shows the step's own output, indented, and keeps its status" ;;
*) t_fail "--verbose should show the step's output, got: $out" ;;
esac
( parse_args --verbose && [ "$SKIFITY_VERBOSE" = 1 ] ) >/dev/null 2>&1 &&
  t_pass "--verbose is an option" || t_fail "--verbose should be accepted"

# poll_until: yes before the time is up, or no after it.
flag="$WORKDIR/ready.flag"
rm -f "$flag"
( sleep 1; : >"$flag" ) &
if (PROGRESS_MODE=plain; LOG_FILE="$wlog"; poll_until "Waiting for the flag" 10 "" test -e "$flag") >/dev/null 2>&1; then
  t_pass "a wait for something ends as soon as it is true"
else
  t_fail "poll_until should succeed once the flag exists"
fi
started=$(date +%s)
if (PROGRESS_MODE=plain; LOG_FILE="$wlog"; poll_until "Waiting for nothing" 2 "" test -e "$WORKDIR/never") >/dev/null 2>&1; then
  t_fail "poll_until succeeded for something that never happens"
elif [ $(($(date +%s) - started)) -le 5 ]; then
  t_pass "and gives up at the time it was given"
else
  t_fail "poll_until took $(($(date +%s) - started))s to give up on 2s"
fi

# An interrupt has to stop the step being waited for: a k3s installer left
# running behind a Ctrl-C would carry on installing.
#
# Waited for, not looked at: a process that has been killed and not yet
# collected by its parent still answers `kill -0`, so looking made this test
# pass or fail by how quickly the shell happened to collect it. The status says
# how it ended, and the watchdog is only there so that a step that was not
# stopped fails this test instead of hanging it.
sleep 30 &
child=$!
( sleep 8; kill -9 "$child" 2>/dev/null ) &
watchdog=$!
( PROGRESS_PID=$child; stop_progress )
child_status=0
wait "$child" 2>/dev/null || child_status=$?
kill "$watchdog" 2>/dev/null || true
wait "$watchdog" 2>/dev/null || true
if [ "$child_status" = 143 ]; then
  t_pass "an interrupt stops the step being waited for"
else
  t_fail "stop_progress left the step running (it ended with status $child_status, not 143)"
fi

# What the panel's pod is doing, in words that say what to expect.
detail_kubectl="$WORKDIR/detail-kubectl"
make_stub "$detail_kubectl" kubectl <<'STUB'
case "$*" in
*"waiting.reason"*) printf '%s' "${STUB_REASON:-}" ;;
*"status.phase"*) printf '%s' "${STUB_PHASE:-}" ;;
*"get pods --no-headers"*) printf 'a-1   1/1   Running   0   1m\na-2   0/1   ContainerCreating   0   5s\nb-3   1/1   Running   0   1m\n' ;;
esac
STUB
for pair in "ContainerCreating|pulling the image" "ErrImagePull|cannot pull the image" "ImagePullBackOff|is it public" "CrashLoopBackOff|keeps stopping"; do
  reason=${pair%%|*}
  want=${pair#*|}
  got=$( (PATH="$detail_kubectl:$PATH"; NAMESPACE=skifity-system; STUB_REASON=$reason; export STUB_REASON; panel_detail) 2>&1)
  case "$got" in
  *"$want"*) t_pass "a pod that is $reason is described: $want" ;;
  *) t_fail "panel_detail for $reason should say \"$want\", got: $got" ;;
  esac
done
got=$( (PATH="$detail_kubectl:$PATH"; NAMESPACE=skifity-system; STUB_PHASE=Running; export STUB_PHASE; panel_detail) 2>&1)
case "$got" in *"health check"*) t_pass "a running pod that is not ready yet says it is waiting for its health check" ;; *) t_fail "got: $got" ;; esac
got=$( (PATH="$detail_kubectl:$PATH"; pods_ready cert-manager) 2>&1)
[ "$got" = "2 of 3 pods ready" ] && t_pass "pods are counted: $got" || t_fail "pods_ready said: $got"

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
# What `ss -lntpH "sport = :80"` prints for a web server with a master and a
# worker, when STUB_PORT80 names one; nothing otherwise.
case "$*" in
*"sport = :80"*)
  [ -z "${STUB_PORT80:-}" ] || printf 'LISTEN 0 511 0.0.0.0:80 0.0.0.0:* users:(("%s",pid=812,fd=6),("%s",pid=811,fd=6))\n' "$STUB_PORT80" "$STUB_PORT80" ;;
*"sport = :443"*)
  [ -z "${STUB_PORT443:-}" ] || printf 'LISTEN 0 4096 0.0.0.0:443 0.0.0.0:* users:(("%s",pid=2201,fd=4))\n' "$STUB_PORT443" ;;
esac
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
# `getent hosts NAME` is the preflight's DNS check; STUB_DNS_DOWN=1 is a server
# whose resolver does not answer. `getent ahostsv4 NAME` is the domain check.
case "$1" in
hosts)
  [ "${STUB_DNS_DOWN:-0}" = 1 ] && exit 2
  [ -n "${STUB_DNS_MISSING:-}" ] && [ "$2" = "$STUB_DNS_MISSING" ] && exit 2
  echo "192.0.2.7       $2"
  ;;
ahostsv4)
  case "$2" in panel.example.test) echo "203.0.113.10    STREAM panel.example.test" ;; *) exit 2 ;; esac
  ;;
esac
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
  mkdir -p "$1/etc" "$1/etc/systemd/system" "$1/proc" "$1/run/systemd/system" "$1/sys/fs/cgroup" "$1/var/log" "$1/usr/local/bin" "$1/stub" "$1/tmp"
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
      UNIT_DIR="$R/etc/systemd/system"
      K3S_UNIT_PATH="$UNIT_DIR/k3s.service"
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
# The panel is told which repository "Check for updates" asks about, from the
# one the installer itself came from: moving the project is one line.
if grep -A1 'name: SKIFITY_UPDATE_REPOSITORY' "$FAKE/stub/applied-panel.yaml" 2>/dev/null | grep -q "value: \"${PROJECT_REPO}\""; then
  t_pass "the panel is told which repository its releases are in"
else
  t_fail "the applied panel does not say where to look for releases: $(grep -A1 UPDATE_REPOSITORY "$FAKE/stub/applied-panel.yaml" 2>/dev/null)"
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

# --- upgrading by running the installer again ---------------------------------

# A newer image migrates the database when it starts, and the older one refuses
# what a later one has migrated, so going back needs a copy taken before. The
# panel's own upgrade takes one; running the installer again used to change the
# image and nothing else.

# upgrade_root DIR lays out a server that already runs an older release.
upgrade_root() {
  fake_root "$1"
  mkdir -p "$1/var/lib/skifity" "$1/var/lib/rancher/k3s/server" "$1/etc/rancher/k3s"
  printf 'the database\n' >"$1/var/lib/skifity/panel.db"
  printf 'K10fake::server:not-a-real-token\n' >"$1/var/lib/rancher/k3s/server/token"
  printf 'apiVersion: v1\n' >"$1/etc/rancher/k3s/k3s.yaml"
}

# The CLI of the release that is running: it makes the copy, through SQLite.
cli_stub() {
  make_stub "$1/usr/local/bin" skifity <<'STUB'
echo "cli $*" >>"$STUB_LOG"
if [ "$1" = admin ] && [ "$2" = backup-db ]; then
  shift 2
  while [ "$1" = --database ]; do shift 2; done
  [ "${STUB_CLI_FAILS:-0}" = 1 ] && { echo "no room" >&2; exit 1; }
  printf 'a consistent copy\n' >"$1"
  exit 0
fi
echo skifity
STUB
}

upgrade_env() {
  export STUB_K3S_ACTIVE=0 STUB_NEEDS_SETUP=false STUB_ROUTE="203-0-113-10.sslip.io " \
    STUB_PANEL_IMAGE="registry.example.test/skifity:0.0.0-old"
}

UP="$WORKDIR/root-upgrade"
upgrade_root "$UP"
cli_stub "$UP"
out=$(upgrade_env; run_install "$UP" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
snapshot=$(find "$UP/var/lib/skifity" -name 'panel.db.before-upgrade-*-to-0.0.0-test' | head -1)
if [ "$status" = 0 ] && [ -s "$snapshot" ] && [ -n "$(find "$snapshot" -perm 0600)" ]; then
  t_pass "an upgrade copies the database first, named for the release, readable by the panel's user alone"
else
  t_fail "the database was not copied before the upgrade ($status): $(ls "$UP/var/lib/skifity")
$out"
fi
copied=$(grep -n "cli admin backup-db" "$UP/stub/calls.log" | head -1 | cut -d: -f1)
applied=$(grep -n "kubectl apply -f .*/panel.yaml" "$UP/stub/calls.log" | head -1 | cut -d: -f1)
if [ -n "$copied" ] && [ -n "$applied" ] && [ "$copied" -lt "$applied" ]; then
  t_pass "and the copy is taken before the image is changed"
else
  t_fail "the copy should come before kubectl apply (copy at ${copied:-never}, apply at ${applied:-never})"
fi
if grep -q -- "--database $UP/var/lib/skifity/panel.db" "$UP/stub/calls.log"; then
  t_pass "it copies the database the panel actually uses"
else
  t_fail "the CLI was not pointed at the panel's database: $(grep 'cli ' "$UP/stub/calls.log")"
fi
case "$out" in
*"If the new version does not come up"*"scale deploy/skifity-panel --replicas=0"*"admin restore-db --yes $snapshot"*"rollout undo deploy/skifity-panel"*"--replicas=1"*)
  t_pass "the commands that go back are printed, with the copy's path filled in" ;;
*) t_fail "the way back should be printed, got:
$out" ;;
esac

# The copy is what going back needs, so an upgrade that cannot take one does
# not start.
upgrade_root "$UP"
rm -f "$UP/usr/local/bin/skifity"
out=$(upgrade_env; run_install "$UP" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"could not be copied before upgrading"*"--no-snapshot"*) t_pass "an upgrade that cannot copy the database stops, and says how to go on anyway" ;;
*) t_fail "an upgrade with no way to copy the database should stop, got $status: $out" ;;
esac
if [ ! -e "$UP/stub/applied-panel.yaml" ]; then
  t_pass "and nothing was changed"
else
  t_fail "the panel was upgraded although its database could not be copied"
fi
upgrade_root "$UP"
cli_stub "$UP"
out=$(upgrade_env; export STUB_CLI_FAILS=1; run_install "$UP" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
if [ "$status" = 1 ] && [ ! -e "$UP/stub/applied-panel.yaml" ] && ! ls "$UP"/var/lib/skifity/panel.db.before-upgrade-* >/dev/null 2>&1; then
  t_pass "a copy that fails leaves no half-written file, and the upgrade does not start"
else
  t_fail "a failing copy should stop the upgrade and leave nothing behind ($status)"
fi

# --no-snapshot is the way through, and says what it costs.
upgrade_root "$UP"
rm -f "$UP/usr/local/bin/skifity"
out=$(upgrade_env; run_install "$UP" --image "$IMAGE_UNDER_TEST" --no-snapshot 2>&1) && status=0 || status=$?
case "$status:$out" in
0:*"nothing to go back to"*) t_pass "--no-snapshot goes ahead, and says there will be nothing to go back to" ;;
*) t_fail "--no-snapshot should upgrade with a warning, got $status: $out" ;;
esac
case "$out" in
*"does not come up"*) t_fail "the way back was printed although no copy was taken" ;;
*) t_pass "and does not print a way back it cannot offer" ;;
esac

# The same image again, and a first install, have nothing to copy.
upgrade_root "$UP"
cli_stub "$UP"
out=$(upgrade_env; export STUB_PANEL_IMAGE="$IMAGE_UNDER_TEST"; run_install "$UP" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
if [ "$status" = 0 ] && ! grep -q "backup-db" "$UP/stub/calls.log" && ! ls "$UP"/var/lib/skifity/panel.db.before-upgrade-* >/dev/null 2>&1; then
  t_pass "running the same release again copies nothing"
else
  t_fail "the same image should not be snapshotted ($status)"
fi
FAKE="$WORKDIR/root-first"
fake_root "$FAKE"
out=$(run_install "$FAKE" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
if [ "$status" = 0 ] && ! grep -q "backup-db" "$FAKE/stub/calls.log"; then
  t_pass "and so does a first install"
else
  t_fail "a first install has no database to copy ($status)"
fi

# Three copies are kept, like the panel keeps.
upgrade_root "$UP"
cli_stub "$UP"
for n in 1 2 3 4 5; do printf 'old\n' >"$UP/var/lib/skifity/panel.db.before-upgrade-2026010${n}-000000-to-v0.0.${n}"; done
out=$(upgrade_env; run_install "$UP" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
left=$(find "$UP/var/lib/skifity" -name 'panel.db.before-upgrade-*' | wc -l | tr -d ' ')
if [ "$status" = 0 ] && [ "$left" = 3 ] && [ -n "$(find "$UP/var/lib/skifity" -name 'panel.db.before-upgrade-*-to-0.0.0-test')" ] &&
  [ ! -e "$UP/var/lib/skifity/panel.db.before-upgrade-20260101-000000-to-v0.0.1" ]; then
  t_pass "the three newest copies are kept, the new one among them"
else
  t_fail "old copies were not pruned to three ($status, $left left): $(ls "$UP/var/lib/skifity")"
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

# A server that is not ready is told so before anything on it changes, with what
# is wrong and what to do about it. This is what the first real server said:
# "something is already listening on port 80", and nothing about what.
FAKE="$WORKDIR/root-busy"
fake_root "$FAKE"
out=$(export STUB_PORT80=nginx; run_install "$FAKE" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"Nothing on it has been changed"*"Port 80 is in use by nginx (processes 811, 812)"*"systemctl disable --now nginx"*)
  t_pass "a port held by nginx is named, with the command that frees it" ;;
*) t_fail "a busy port 80 should name nginx and how to stop it, got $status: $out" ;;
esac
if [ ! -e "$FAKE/stub/k3s-exec" ]; then
  t_pass "and nothing was installed"
else
  t_fail "k3s was installed although port 80 is taken"
fi
out=$(export STUB_PORT443=docker-proxy; run_install "$FAKE" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"Port 443 is in use by docker-proxy"*"docker ps --filter publish=443"*) t_pass "a port held by Docker says how to find the container" ;;
*) t_fail "a Docker-held port should say how to find the container, got $status: $out" ;;
esac

# Every problem at once: one run, not one per fix.
out=$(export STUB_PORT80=apache2 STUB_DNS_DOWN=1; run_install "$FAKE" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"2 problems that would stop the install"*"1. Port 80 is in use by apache2"*"2. This server cannot look up any name"*"/etc/resolv.conf"*"systemd-resolved"*)
  t_pass "a server with two problems is told about both at once" ;;
*) t_fail "two problems should be reported together, got $status: $out" ;;
esac
case "$out" in
*"does not resolve, and sudo says so"*"127.0.1.1"*) t_pass "and a server whose own name does not resolve is told how to fix the sudo warning" ;;
*) t_fail "the unresolvable hostname should be mentioned with its fix, got: $out" ;;
esac

# One name blocked is not the same thing as DNS being down.
out=$(export STUB_DNS_MISSING=ghcr.io; run_install "$FAKE" --image "ghcr.io/example/skifity:1" 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"cannot look up ghcr.io, which the install needs"*) t_pass "one name that does not resolve is reported by name, not as a dead resolver" ;;
*) t_fail "a single missing name should be named, got $status: $out" ;;
esac

# A hostname sudo cannot resolve is a warning, and the install goes on.
FAKE="$WORKDIR/root-hostname"
fake_root "$FAKE"
own_name=$(hostname)
out=$(STUB_DNS_MISSING="$own_name"; export STUB_DNS_MISSING; run_install "$FAKE" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
case "$status:$out" in
0:*"does not resolve, and sudo says so"*"Skifity registry.example.test"*"is installed"*) t_pass "an unresolvable hostname is a warning, and the install carries on" ;;
*) t_fail "an unresolvable hostname should only warn, got $status: $out" ;;
esac

# The installer says what it is doing while it waits, in the log of a run that
# has no terminal.
FAKE="$WORKDIR/root-waits"
fake_root "$FAKE"
out=$(run_install "$FAKE" --image "$IMAGE_UNDER_TEST" 2>&1) && status=0 || status=$?
for wait in "Installing Kubernetes (k3s)" "Waiting for the Kubernetes API server" "Waiting for the node to be ready" "Starting the panel" "to answer"; do
  case "$out" in
  *"$wait"*) ;;
  *) t_fail "the install never said it was waiting for: $wait" ;;
  esac
done
case "$out" in
*"Most of the time is spent waiting for downloads"*) t_pass "the install says up front what the long parts are, and each wait says what it is waiting for" ;;
*) t_fail "the install should say what to expect before the long parts" ;;
esac
if grep -q "Installing Kubernetes (k3s): [0-9]*s, exit 0" "$FAKE/var/log/skifity-install.log"; then
  t_pass "and the log has each wait, with how long it took and how it ended"
else
  t_fail "the log should record each wait: $(grep WAIT "$FAKE/var/log/skifity-install.log" | head -3)"
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

# --- removing it, for real, against stand-ins --------------------------------

# Everything above is a dry run, on purpose. These run the removal itself, with
# every path moved into a directory of its own and kubectl and k3s's uninstall
# script replaced by stand-ins that write down what they were asked.
USTUBS="$WORKDIR/ustubs"
make_stub "$USTUBS" id <<'STUB'
[ "$1" = -u ] && { echo "${STUB_UID:-0}"; exit 0; }
exec /usr/bin/env -i PATH=/usr/bin:/bin id "$@"
STUB
make_stub "$USTUBS" kubectl <<'STUB'
printf "kubectl %s\n" "$*" >>"$STUB_LOG"
case "$*" in
"get --raw /readyz"*) [ "${STUB_CLUSTER_DOWN:-0}" = 1 ] && exit 1; exit 0 ;;
"get namespaces"*) printf "namespace/env-a\nnamespace/env-b\n" ;;
"delete namespace skifity-system"*) [ "${STUB_FAIL_NS:-0}" = 1 ] && { echo "error: timed out" >&2; exit 1; } ;;
esac
exit 0
STUB

make_stub "$USTUBS" iptables <<'STUB'
# The registry's rule is "there" until it has been deleted once.
printf "iptables %s\n" "$*" >>"$STUB_LOG"
case " $* " in
*" -C "*) [ -f "$STUB_LOG.rule" ] || exit 1 ;;
*" -D "*) rm -f "$STUB_LOG.rule" ;;
esac
exit 0
STUB
make_stub "$USTUBS" systemctl <<'STUB'
printf "systemctl %s\n" "$*" >>"$STUB_LOG"
exit 0
STUB

# fake_uroot DIR lays out a server that has Skifity on it.
fake_uroot() {
  rm -rf "$1"
  mkdir -p "$1/etc/skifity" "$1/var/lib/skifity" "$1/etc/rancher/k3s" "$1/etc/systemd/system" "$1/bin" "$1/run"
  printf '[Unit]\n' >"$1/etc/systemd/system/skifity-registry-guard.service"
  : >"$1/calls.log.rule"
  printf 'fake master key\n' >"$1/etc/skifity/master.key"
  printf 'fake database\n' >"$1/var/lib/skifity/panel.db"
  printf 'apiVersion: v1\n' >"$1/etc/rancher/k3s/k3s.yaml"
  printf 'mirrors: {}\n' >"$1/etc/rancher/k3s/registries.yaml"
  printf '#!/bin/sh\n' >"$1/bin/skifity"
  printf '#!/bin/sh\n' >"$1/bin/skifity-uninstall"
  chmod +x "$1/bin/skifity" "$1/bin/skifity-uninstall"
  make_stub "$1/bin" k3s-uninstall.sh <<'STUB'
echo "k3s-uninstall" >>"$STUB_LOG"
exit "${STUB_K3S_FAIL:-0}"
STUB
}

# run_uninstall ROOT [options...] runs main against ROOT in a shell of its own.
# Answers for the terminal go in ROOT/tty; with no such file there is none.
run_uninstall() {
  ur="$1"
  shift
  # The script is in single quotes so the inner shell expands it.
  # shellcheck disable=SC2016
  env PATH="$USTUBS:$PATH" STUB_LOG="$ur/calls.log" \
    sh -c '
      SKIFITY_UNINSTALLER_LIB=1 . "$1/installer/uninstall.sh"
      R="$2"
      shift 2
      CONFIG_DIR="$R/etc/skifity"
      DATA_DIR="$R/var/lib/skifity"
      KUBECONFIG_PATH="$R/etc/rancher/k3s/k3s.yaml"
      REGISTRIES_PATH="$R/etc/rancher/k3s/registries.yaml"
      REGISTRY_GUARD_UNIT="$R/etc/systemd/system/skifity-registry-guard.service"
      CLI_PATH="$R/bin/skifity"
      UNINSTALLER_PATH="$R/bin/skifity-uninstall"
      K3S_UNINSTALL="$R/bin/k3s-uninstall.sh"
      K3S_AGENT_UNINSTALL="$R/bin/k3s-agent-uninstall.sh"
      LOCK_DIR="$R/run/skifity-install.lock"
      LOG_FILE="$R/uninstall.log"
      TTY_DEV="$R/tty"
      main "$@"
    ' run-uninstall "$ROOT" "$ur" "$@"
}

# With no terminal and no --yes there is nobody to ask, and nothing is removed.
UR="$WORKDIR/uroot"
fake_uroot "$UR"
out=$(run_uninstall "$UR" </dev/null 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"nobody to ask"*"--yes"*"Nothing was changed"*) t_pass "with no terminal and no --yes, nothing is removed" ;;
*) t_fail "an uninstall with nobody to ask should refuse, got $status: $out" ;;
esac
# It looked at the cluster to say what would go, which changes nothing.
if [ -f "$UR/var/lib/skifity/panel.db" ] && ! grep -q "delete\|k3s-uninstall" "$UR/calls.log"; then
  t_pass "and nothing was deleted"
else
  t_fail "a refused uninstall still deleted something: $(cat "$UR/calls.log")"
fi

# The plain removal: the panel goes, in an order that leaves nothing dangling,
# and the data and k3s stay.
out=$(run_uninstall "$UR" --yes 2>&1) && status=0 || status=$?
if [ "$status" = 0 ]; then
  t_pass "removing the panel exits zero"
else
  t_fail "removing the panel exited $status:
$out"
fi
if [ "$(grep -n 'delete namespace skifity-system' "$UR/calls.log" | cut -d: -f1)" -gt "$(grep -n 'delete clusterrolebinding' "$UR/calls.log" | cut -d: -f1)" ] &&
  grep -q "delete namespace skifity-builds" "$UR/calls.log"; then
  t_pass "the namespace goes after the objects that point at it"
else
  t_fail "the panel was not removed in a safe order: $(cat "$UR/calls.log")"
fi
if grep -q k3s-uninstall "$UR/calls.log"; then
  t_fail "k3s was removed without --all"
elif [ -f "$UR/var/lib/skifity/panel.db" ] && [ -f "$UR/etc/skifity/master.key" ]; then
  t_pass "k3s, the database and the master key stay"
else
  t_fail "the data was deleted without --purge"
fi
if [ ! -e "$UR/bin/skifity" ] && [ -e "$UR/bin/skifity-uninstall" ]; then
  t_pass "the CLI goes, and the uninstaller stays for the next step"
else
  t_fail "the CLI or the uninstaller was handled wrongly"
fi
case "$out" in
*"Skifity has been removed"*"skifity-uninstall --all"*"skifity-uninstall --purge"*) t_pass "it says what is still on the server and how to remove it" ;;
*) t_fail "the summary should list what is left, got: $out" ;;
esac
if [ "$(find "$UR/uninstall.log" -perm 0600)" ]; then
  t_pass "the log is readable by root alone"
else
  t_fail "the uninstall log is readable by others"
fi

# Removing k3s asks for a typed phrase, and says how many environments go with it.
fake_uroot "$UR"
printf 'yes\n' >"$UR/tty"
out=$(run_uninstall "$UR" --all 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"2 environments are on it"*"Nothing was changed"*) t_pass "--all names the environments it would delete, and a plain yes is not enough" ;;
*) t_fail "--all should count the environments and want a typed phrase, got $status: $out" ;;
esac
if ! grep -q "delete\|k3s-uninstall" "$UR/calls.log"; then
  t_pass "and nothing was deleted either"
else
  t_fail "a refused --all deleted something: $(cat "$UR/calls.log")"
fi
printf 'remove k3s\n' >"$UR/tty"
out=$(run_uninstall "$UR" --all 2>&1) && status=0 || status=$?
if [ "$status" = 0 ] && grep -q k3s-uninstall "$UR/calls.log"; then
  t_pass "the typed phrase removes k3s"
else
  t_fail "--all with its phrase should remove k3s ($status): $out"
fi
if [ -f "$UR/etc/skifity/master.key" ]; then
  t_pass "and still keeps the master key"
else
  t_fail "--all deleted the master key"
fi
if [ ! -e "$UR/etc/systemd/system/skifity-registry-guard.service" ] && [ ! -e "$UR/calls.log.rule" ] &&
  grep -q "iptables -w -t raw -D PREROUTING -p tcp --dport 30500" "$UR/calls.log" &&
  grep -q "systemctl disable --now skifity-registry-guard.service" "$UR/calls.log"; then
  t_pass "and takes away the rule that kept the registry's port closed, and the unit that repeated it"
else
  t_fail "--all left the registry's port rule behind: $(cat "$UR/calls.log")"
fi

# The data needs the longer phrase, and the wrong one removes nothing.
fake_uroot "$UR"
printf 'remove k3s\n' >"$UR/tty"
out=$(run_uninstall "$UR" --purge 2>&1) && status=0 || status=$?
if [ "$status" = 1 ] && [ -f "$UR/etc/skifity/master.key" ]; then
  t_pass "--purge refuses the phrase for removing k3s"
else
  t_fail "--purge accepted the wrong phrase ($status): $out"
fi
printf 'delete my data\n' >"$UR/tty"
out=$(run_uninstall "$UR" --purge 2>&1) && status=0 || status=$?
if [ "$status" = 0 ] && [ ! -e "$UR/etc/skifity" ] && [ ! -e "$UR/var/lib/skifity" ] && [ ! -e "$UR/etc/rancher/k3s/registries.yaml" ]; then
  t_pass "--purge with its phrase deletes the data and the registry mirror"
else
  t_fail "--purge did not delete the data ($status): $out"
fi

# Everything: nothing is left for the uninstaller to remove, so it removes itself.
fake_uroot "$UR"
out=$(run_uninstall "$UR" --all --purge --yes 2>&1) && status=0 || status=$?
if [ "$status" = 0 ] && [ ! -e "$UR/bin/skifity-uninstall" ]; then
  t_pass "--all --purge leaves nothing, the uninstaller included"
else
  t_fail "--all --purge should remove everything ($status): $out"
fi
case "$out" in
*"Still on this server"*) t_fail "everything was removed, but the summary says something is left" ;;
*"firewall"*) t_pass "and says the firewall rules it did not touch are the operator's now" ;;
*) t_fail "the summary should mention the firewall rules, got: $out" ;;
esac

# A step that fails is reported as failed, the rest still run, and the exit
# status says so. This used to print "Panel removed" over a namespace that was
# still there.
fake_uroot "$UR"
out=$(STUB_FAIL_NS=1 run_uninstall "$UR" --yes 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"Could not delete the namespace skifity-system"*"not completely removed"*) t_pass "a namespace that would not go is reported, and the exit status says so" ;;
*) t_fail "a failed delete should be reported and exit non-zero, got $status: $out" ;;
esac
case "$out" in
*"Panel removed"*) t_fail "it claimed the panel was removed when a step failed" ;;
*) t_pass "and it does not claim the panel was removed" ;;
esac
if grep -q "delete namespace skifity-builds" "$UR/calls.log"; then
  t_pass "the steps after the failed one still ran"
else
  t_fail "one failure stopped the rest of the removal"
fi
fake_uroot "$UR"
out=$(STUB_FAIL_NS=1 run_uninstall "$UR" --all --purge --yes 2>&1) && status=0 || status=$?
if [ "$status" = 1 ] && [ -e "$UR/bin/skifity-uninstall" ] && [ ! -e "$UR/etc/skifity" ]; then
  t_pass "after a failure the uninstaller stays, so running it again finishes the job"
else
  t_fail "a removal that failed should keep the uninstaller ($status)"
fi

# k3s that is not running cannot have the panel taken out of it, and saying
# nothing used to be the result.
fake_uroot "$UR"
out=$(STUB_CLUSTER_DOWN=1 run_uninstall "$UR" --yes 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"k3s is not answering"*"systemctl start k3s"*) t_pass "a k3s that is not answering is reported, with how to start it" ;;
*) t_fail "a stopped k3s should be reported, got $status: $out" ;;
esac
fake_uroot "$UR"
out=$(STUB_CLUSTER_DOWN=1 run_uninstall "$UR" --all --yes 2>&1) && status=0 || status=$?
if [ "$status" = 0 ] && grep -q k3s-uninstall "$UR/calls.log"; then
  t_pass "with --all a stopped k3s is simply removed"
else
  t_fail "--all should remove a stopped k3s ($status): $out"
fi

# k3s that its own installer did not put here has no script to remove it with.
fake_uroot "$UR"
rm -f "$UR/bin/k3s-uninstall.sh"
make_stub "$UR/stubbin" k3s <<'STUB'
exit 0
STUB
out=$(PATH="$UR/stubbin:$PATH"; export PATH; run_uninstall "$UR" --all --yes 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"was not installed with k3s's own installer"*) t_pass "a k3s with no uninstall script is not reported as removed" ;;
*) t_fail "a k3s that cannot be uninstalled should be reported, got $status: $out" ;;
esac

# The installer's lock: an install and an uninstall would undo each other.
fake_uroot "$UR"
mkdir -p "$UR/run/skifity-install.lock"
printf '%s\n' "$$" >"$UR/run/skifity-install.lock/pid"
out=$(run_uninstall "$UR" --yes 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"installer is running"*) t_pass "an uninstall is refused while the installer runs" ;;
*) t_fail "an uninstall under a running install should be refused, got $status: $out" ;;
esac
printf '99999999\n' >"$UR/run/skifity-install.lock/pid"
out=$(run_uninstall "$UR" --yes 2>&1) && status=0 || status=$?
if [ "$status" = 0 ]; then
  t_pass "a lock whose process is gone does not stop it"
else
  t_fail "a stale install lock stopped the uninstall ($status): $out"
fi

# rm -rf is never pointed at a place nobody means to delete.
for bad in / /var /etc "" relative/dir /var/../etc; do
  if (SKIFITY_UNINSTALLER_LIB=1 . "$ROOT/installer/uninstall.sh"; safe_dir "$bad") 2>/dev/null; then
    t_fail "safe_dir accepted \"$bad\""
  fi
done
t_pass "safe_dir refuses the root, a top-level directory, an empty or a relative path"
fake_uroot "$UR"
# shellcheck disable=SC2016
out=$(env PATH="$USTUBS:$PATH" STUB_LOG="$UR/calls.log" sh -c '
  SKIFITY_UNINSTALLER_LIB=1 . "$1/installer/uninstall.sh"
  R="$2"
  CONFIG_DIR="$R/etc/skifity"; DATA_DIR="/"; KUBECONFIG_PATH="$R/none"; REGISTRIES_PATH="$R/none"
  CLI_PATH="$R/bin/skifity"; UNINSTALLER_PATH="$R/bin/skifity-uninstall"; LOCK_DIR="$R/run/x"
  LOG_FILE="$R/uninstall.log"; TTY_DEV="$R/tty"
  main --purge --yes' run-uninstall "$ROOT" "$UR" 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"Refusing to delete \"/\""*) t_pass "a purge pointed at / is refused" ;;
*) t_fail "a purge pointed at / should be refused, got $status: $out" ;;
esac

# Not root: nothing, and the way to fix it.
out=$(STUB_UID=1000 run_uninstall "$UR" --yes 2>&1) && status=0 || status=$?
case "$status:$out" in
1:*"has to run as root"*) t_pass "without root it says so and does nothing" ;;
*) t_fail "an uninstall without root should refuse, got $status: $out" ;;
esac

printf '\n'
if [ "$FAILURES" -gt 0 ]; then
  printf '%s installer smoke check(s) failed.\n\n' "$FAILURES"
  exit 1
fi
printf 'All installer smoke checks passed.\n\n'
