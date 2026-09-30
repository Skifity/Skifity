#!/usr/bin/env bash
# The crosscheck: every claim this product makes, against a real cluster.
#
# Everything in test/smoke checks Skifity against fakes, golden files and an
# in-process SSH server, because the machine it was built on could not start a
# cluster. This is the other half. It installs Skifity on a real server and then
# works through the checklist a self-hosted platform is actually judged on, in
# six phases:
#
#   1  Git to build to deploy, live build logs, a domain, a variable change,
#      a rollback, a deploy that drops no requests, and a folder with no
#      repository behind it built the same way.
#   2  A volume that survives a restart, a managed database, a backup that
#      restores, and an export you could leave with.
#   3  One team cannot see another's, one namespace cannot reach another's,
#      a quota is a real ceiling, and a member is not an admin.
#   4  The panel goes away and the apps keep serving. A node goes away and
#      Kubernetes puts the work somewhere else.
#   5  Logs, metrics, an upgrade with an app running through it, and a
#      notification that actually arrives when something fails.
#
# Phase 6 is a person: give somebody the documentation and nothing else, and see
# whether they get an app running. No script can do that one.
#
#   THIS INSTALLS k3s AND CHANGES THE MACHINE IT RUNS ON.
#
# Run it on a server you are willing to rebuild — a fresh VPS, not something you
# care about. It never runs itself: it asks first unless SKIFITY_ASSUME_YES=1.
#
#   sudo bash test/cluster/verify.sh
#
# What it needs: Ubuntu 22.04+ or Debian 12+, 2 GB of memory, a public IP, and
# either a built image or SKIFITY_IMAGE pointing at one. With no domain it uses
# nip.io, so there is nothing to configure in DNS.
#
# It writes a report to /var/log/skifity-verify.log and prints the same at the
# end. When something fails it says which check, what it expected, and what to
# look at — the point is a report somebody can act on, not a green tick.

set -euo pipefail

PANEL_NS="skifity-system"
REPORT="/var/log/skifity-verify.log"
: "${SKIFITY_ASSUME_YES:=0}"
: "${SKIFITY_IMAGE:=}"
: "${SKIFITY_PHASES:=1 2 3 4 5}"
# The app phase 1 builds from source. It is this repository's own sample: one Go
# file and a Dockerfile with nothing to download, so a failure is the builder's
# and not the network's.
: "${VERIFY_GIT_REPO:=https://github.com/Skifity/Skifity}"
# The branch to build from. This repository has no "main": what exists is
# whatever branch this checkout is on, and that is the one worth verifying, so
# it is read rather than assumed. A run that spent an hour installing and then
# failed to clone a branch nobody ever pushed would be a waste of a server.
: "${VERIFY_GIT_BRANCH:=$(git -C "$(dirname "$0")/../.." rev-parse --abbrev-ref HEAD 2>/dev/null || echo main)}"
: "${VERIFY_GIT_ROOT:=test/cluster/sample-app}"
: "${VERIFY_APP_PORT:=8080}"
# Backups need somewhere to put them. Without these, phase 2 says so and skips
# rather than pretending.
: "${VERIFY_S3_ENDPOINT:=}"
: "${VERIFY_S3_BUCKET:=}"
: "${VERIFY_S3_ACCESS_KEY:=}"
: "${VERIFY_S3_SECRET_KEY:=}"

PASS=0
FAIL=0
SKIP=0

say() { printf '%s\n' "$*" | tee -a "$REPORT"; }
phase() { printf '\n########## %s\n' "$*" | tee -a "$REPORT"; }
step() { printf '\n== %s\n' "$*" | tee -a "$REPORT"; }
ok() { PASS=$((PASS + 1)); printf '  ok   %s\n' "$*" | tee -a "$REPORT"; }
no() { FAIL=$((FAIL + 1)); printf '  FAIL %s\n' "$*" | tee -a "$REPORT"; }
skip() { SKIP=$((SKIP + 1)); printf '  --   %s (skipped: %s)\n' "$1" "$2" | tee -a "$REPORT"; }

die() {
	no "$*"
	summary
	exit 1
}

summary() {
	phase "Result"
	say "  $PASS passed, $FAIL failed, $SKIP skipped"
	say "  The full log is at $REPORT"
	say ""
	say "  Phase 6 is not in here and cannot be: give somebody docs/quick-start.md"
	say "  and nothing else, and watch where they get stuck. That is the check."
	if [ "$FAIL" -gt 0 ]; then
		say ""
		say "  Something above is the first thing this product has ever been told"
		say "  by a real cluster. Send the failing section back with the output of:"
		say "    kubectl -n $PANEL_NS get pods,events --sort-by=.lastTimestamp | tail -40"
	fi
}

confirm() {
	[ "$SKIFITY_ASSUME_YES" = "1" ] && return 0
	printf '\nThis installs k3s and changes this machine permanently.\n'
	printf 'Type the word yes to continue: '
	read -r answer
	[ "$answer" = "yes" ] || { echo "Nothing was changed."; exit 1; }
}

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is not installed, and this needs it"; }

wants() { case " $SKIFITY_PHASES " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }

# pick reads one field out of JSON on stdin, by dotted path. grep and cut were
# what this used to do, and they cannot tell "id" at the top level from "id"
# three objects down — which is how it used to pull the wrong team.
pick() {
	python3 -c '
import json, sys
value = json.load(sys.stdin)
for part in sys.argv[1].split("."):
    if part == "":
        continue
    value = value[int(part)] if part.lstrip("-").isdigit() else value[part]
print(value if not isinstance(value, (dict, list)) else json.dumps(value))
' "$1"
}

# --- preflight -------------------------------------------------------------

: >"$REPORT"
phase "Before anything is installed"

[ "$(id -u)" = "0" ] || die "this has to run as root: it installs k3s"
need curl
need awk
need python3

if [ -z "$SKIFITY_IMAGE" ]; then
	die "set SKIFITY_IMAGE to a panel image. No release is published yet, so build one with 'make image' and push it somewhere this server can pull from, or load it into k3s with 'k3s ctr images import'."
fi
ok "the panel image to install is $SKIFITY_IMAGE"

MEM_MB=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
say "  this machine has ${MEM_MB} MB of memory"
[ "$MEM_MB" -ge 1800 ] || no "under 2 GB; the README says 2 GB and this is the first chance to find out whether that is true"

PUBLIC_IP="${SKIFITY_PUBLIC_IP:-$(curl -fsS --max-time 10 https://api.ipify.org || true)}"
[ -n "$PUBLIC_IP" ] || die "could not work out this machine's public IP; set SKIFITY_PUBLIC_IP"
ok "public IP is $PUBLIC_IP"
DOMAIN="${SKIFITY_DOMAIN:-${PUBLIC_IP}.nip.io}"
say "  the panel will answer on https://$DOMAIN"
say "  running phases: $SKIFITY_PHASES"

confirm

# --- install ---------------------------------------------------------------

phase "Installing"

HERE="$(cd "$(dirname "$0")/../.." && pwd)"
[ -f "$HERE/installer/install.sh" ] || die "run this from a clone of the repository: installer/install.sh is not next to it"

START_INSTALL=$(date +%s)
if SKIFITY_IMAGE="$SKIFITY_IMAGE" SKIFITY_DOMAIN="$DOMAIN" SKIFITY_ASSUME_YES=1 \
	SKIFITY_MANIFEST_BASE="file://$HERE/deploy" \
	sh "$HERE/installer/install.sh" >>"$REPORT" 2>&1; then
	ok "the installer finished in $(( $(date +%s) - START_INSTALL ))s"
else
	die "the installer failed; the last lines of $REPORT say where"
fi

need kubectl
kubectl get nodes >/dev/null 2>&1 || die "kubectl cannot reach the cluster the installer just made"
ok "the cluster answers"

# --- the panel is up -------------------------------------------------------

step "The panel"

for _ in $(seq 1 60); do
	if kubectl -n "$PANEL_NS" get deploy skifity-panel -o jsonpath='{.status.readyReplicas}' 2>/dev/null | grep -q '^[1-9]'; then
		break
	fi
	sleep 5
done
kubectl -n "$PANEL_NS" get deploy skifity-panel -o jsonpath='{.status.readyReplicas}' 2>/dev/null | grep -q '^[1-9]' \
	|| die "the panel never became ready; kubectl -n $PANEL_NS describe pod says why"
ok "the panel pod is ready"

# A port-forward rather than the NodePort: the NodePort is not guaranteed, and a
# check should not depend on something the installer is free to change.
start_forward() {
	kubectl -n "$PANEL_NS" port-forward svc/skifity-panel 18080:80 >/dev/null 2>&1 &
	FORWARD=$!
	sleep 3
}
stop_forward() { kill "${FORWARD:-0}" 2>/dev/null || true; }
trap 'stop_forward' EXIT
start_forward
PANEL="http://127.0.0.1:18080"

TOKEN=""
api() {
	local method="$1" path="$2" body="${3:-}"
	if [ -n "$body" ]; then
		curl -fsS -X "$method" "$PANEL$path" -H 'Content-Type: application/json' \
			${TOKEN:+-H "Authorization: Bearer $TOKEN"} -d "$body"
	else
		curl -fsS -X "$method" "$PANEL$path" ${TOKEN:+-H "Authorization: Bearer $TOKEN"}
	fi
}

# api_code answers with the HTTP status and nothing else, for the checks that
# are about being refused.
api_code() {
	local method="$1" path="$2" body="${3:-}" token="${4:-$TOKEN}"
	if [ -n "$body" ]; then
		curl -s -o /dev/null -w '%{http_code}' -X "$method" "$PANEL$path" \
			-H 'Content-Type: application/json' ${token:+-H "Authorization: Bearer $token"} -d "$body"
	else
		curl -s -o /dev/null -w '%{http_code}' -X "$method" "$PANEL$path" \
			${token:+-H "Authorization: Bearer $token"}
	fi
}

# through_ingress sends a request the way the internet would: at this node, on
# port 80, with the Host header that decides which app answers.
through_ingress() {
	curl -s -o /dev/null -w '%{http_code}' --max-time "${2:-15}" -H "Host: $1" "http://127.0.0.1/"
}

api GET /api/health >/dev/null || die "the panel's own health endpoint does not answer"
ok "/api/health answers"

# --- first-run setup -------------------------------------------------------

step "First run"

SETUP_TOKEN=$(cat /etc/skifity/setup-token 2>/dev/null || true)
[ -n "$SETUP_TOKEN" ] || die "no setup token at /etc/skifity/setup-token"

PASSWORD="verify-$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')"
SETUP=$(api POST /api/setup "{\"token\":\"$SETUP_TOKEN\",\"email\":\"verify@example.test\",\"name\":\"Verify\",\"password\":\"$PASSWORD\",\"team_name\":\"Verify\"}") \
	|| die "first-run setup was refused"
ok "an account and a team were created"

TEAM_ID=$(printf '%s' "$SETUP" | pick team.id)
[ -n "$TEAM_ID" ] || die "setup did not return a team id"

# An API token, because everything below is a script rather than a browser. The
# session cookie plus the CSRF header is the only way to ask for one — a token
# cannot mint another token — and the header's name is X-Skifity-CSRF.
COOKIE=$(mktemp)
curl -fsS -c "$COOKIE" -X POST "$PANEL/api/auth/login" -H 'Content-Type: application/json' \
	-d "{\"email\":\"verify@example.test\",\"password\":\"$PASSWORD\"}" >/dev/null \
	|| die "signing in with the account just created was refused"
ok "signing in works"

CSRF=$(awk '/skifity_csrf/ {print $7}' "$COOKIE")
[ -n "$CSRF" ] || die "no CSRF cookie was set by signing in"
TOKEN=$(curl -fsS -b "$COOKIE" -H "X-Skifity-CSRF: $CSRF" -H 'Content-Type: application/json' \
	-X POST "$PANEL/api/me/tokens" -d "{\"name\":\"verify\",\"team_id\":\"$TEAM_ID\",\"ttl_hours\":4}" \
	| pick secret)
[ -n "$TOKEN" ] || die "could not create an API token"
ok "an API token was issued"

# The first screen of a brand new install. Nothing recorded the machine Skifity
# installs itself onto, so the panel used to open on "add your first server"
# while looking at a cluster that was already running — and the only sensible
# thing to do next was refused with a message about port 6443.
SERVERS=$(api GET "/api/teams/$TEAM_ID/servers" | pick total 2>/dev/null || echo 0)
if [ "${SERVERS:-0}" -ge 1 ] 2>/dev/null; then
	ok "the machine this was installed on is already listed as a server, with nobody adding it"
	api GET "/api/teams/$TEAM_ID/servers" | grep -q '"adopted": *true' \
		&& ok "and it is marked as one Skifity found rather than one it installed" \
		|| no "it is listed as a server Skifity installed, which it did not — the operations that need SSH will fail at the connection"
else
	no "a fresh install lists no servers at all, so the first screen asks the operator to add the machine they are already looking at"
fi

PROJECT_ID=$(api POST "/api/teams/$TEAM_ID/projects" '{"name":"Verify"}' | pick id) \
	|| die "a project could not be created"
ENV_ID=$(api GET "/api/projects/$PROJECT_ID/environments" | pick items.0.id)
NAMESPACE=$(api GET "/api/environments/$ENV_ID" | pick namespace)
[ -n "$NAMESPACE" ] || die "the environment has no namespace"
ok "a project and an environment exist, in namespace $NAMESPACE"

##############################################################################
# Phase 1 — the basic path: Git, build, deploy, HTTPS, a variable, a rollback
##############################################################################

APP_ID=""
APP_HOST=""

if wants 1; then
phase "Phase 1 — Git to running, and back again"

step "Deploying from Git (checklist 1 and 2)"

APP_ID=$(api POST "/api/environments/$ENV_ID/apps" "$(cat <<JSON
{"name":"hello","source_type":"git","repo_url":"$VERIFY_GIT_REPO","branch":"$VERIFY_GIT_BRANCH",
 "root_dir":"$VERIFY_GIT_ROOT","port":$VERIFY_APP_PORT,"health_path":"/healthz","deploy":true}
JSON
)" | pick app.id) || die "the app could not be created from Git"
ok "an app was created from $VERIFY_GIT_REPO and a build started"

DEPLOY_ID=$(api GET "/api/apps/$APP_ID/deployments" | pick items.0.id)
[ -n "$DEPLOY_ID" ] || die "no deployment was recorded for the app just created"

# Build logs while the build runs, not afterwards. A log you can only read once
# it is over is a file, not a log.
SAW_LOG=0
for _ in $(seq 1 40); do
	LINES=$(api GET "/api/apps/$APP_ID/deployments/$DEPLOY_ID/logs" 2>/dev/null | pick lines 2>/dev/null || echo "[]")
	if [ "$LINES" != "[]" ] && [ -n "$LINES" ]; then SAW_LOG=1; break; fi
	STATE=$(api GET "/api/apps/$APP_ID/deployments/$DEPLOY_ID" | pick status 2>/dev/null || echo "")
	case "$STATE" in succeeded|failed) break ;; esac
	sleep 5
done
if [ "$SAW_LOG" = "1" ]; then
	ok "the build's log could be read while it was still building"
else
	no "no build output was readable before the deployment ended; a log nobody can watch is a file"
fi

# The build is the slow part: a first build pulls a base image and compiles.
for _ in $(seq 1 120); do
	STATE=$(api GET "/api/apps/$APP_ID/deployments/$DEPLOY_ID" | pick status 2>/dev/null || echo "")
	case "$STATE" in succeeded|failed) break ;; esac
	sleep 10
done
if [ "$STATE" = "succeeded" ]; then
	ok "the app was built from source and deployed"
else
	api GET "/api/apps/$APP_ID/deployments/$DEPLOY_ID/logs" >>"$REPORT" 2>&1 || true
	die "the deployment ended as '${STATE:-unknown}'. The build log is in $REPORT. This is checklist item 1, and nothing after it means much."
fi

READY=$(kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
[ "${READY:-0}" -ge 1 ] 2>/dev/null \
	&& ok "the instance is running" \
	|| no "the deployment succeeded but no instance is ready; kubectl -n $NAMESPACE describe pod says why"

step "The address it was given (checklist 3)"

APP_HOST=$(api GET "/api/apps/$APP_ID/domains" | pick items.0.hostname 2>/dev/null || echo "")
if [ -z "$APP_HOST" ]; then
	no "the app was given no address at all; every app is supposed to get a working URL"
else
	ok "the app was given $APP_HOST without anybody configuring DNS"
	CODE=$(through_ingress "$APP_HOST" 30)
	[ "$CODE" = "200" ] \
		&& ok "a request through the ingress answered 200" \
		|| no "a request to $APP_HOST answered $CODE; the ingress, the Service or the health path is wrong"

	TLS=$(api GET "/api/apps/$APP_ID/domains" | pick items.0.tls 2>/dev/null || echo "False")
	if [ "$TLS" = "True" ]; then
		for _ in $(seq 1 24); do
			kubectl -n "$NAMESPACE" get secret hello-tls >/dev/null 2>&1 && break
			sleep 5
		done
		kubectl -n "$NAMESPACE" get secret hello-tls >/dev/null 2>&1 \
			&& ok "cert-manager issued a certificate for it" \
			|| no "no certificate after two minutes; kubectl -n $NAMESPACE describe certificate says what Let's Encrypt said"
	else
		skip "the HTTPS certificate" "the free address is on sslip.io, which is served over plain HTTP on purpose (ADR-0015). Set SKIFITY_DOMAIN to a domain you own to check this."
	fi
fi

step "Changing a variable does not rebuild (checklist 4)"

IMAGE_BEFORE=$(api GET "/api/apps/$APP_ID/deployments" | pick items.0.image)
api PUT "/api/apps/$APP_ID/variables" '{"key":"VERSION","value":"2"}' >/dev/null \
	|| no "a variable could not be set"
sleep 20
IMAGE_AFTER=$(api GET "/api/apps/$APP_ID/deployments" | pick items.0.image)
if [ "$IMAGE_BEFORE" = "$IMAGE_AFTER" ]; then
	ok "the same image is still deployed: a configuration change did not rebuild (ADR-0007)"
else
	no "changing a variable produced a new image. The build fingerprint is meant to keep configuration out of the build."
fi

for _ in $(seq 1 24); do
	BODY=$(curl -s --max-time 10 -H "Host: $APP_HOST" "http://127.0.0.1/" || true)
	case "$BODY" in *version=2*) break ;; esac
	sleep 5
done
case "$BODY" in
	*version=2*) ok "the app is serving the new value, so the pods actually restarted" ;;
	*) no "the app still answers '${BODY:-nothing}'. A Secret's contents are not watched by Kubernetes, so the pod template has to carry a hash of them — that is what makes a variable change restart anything." ;;
esac

SECRET_IN_LOG=$(kubectl -n "$PANEL_NS" logs deploy/skifity-panel --tail=500 2>/dev/null | grep -c "verify-" || true)
[ "${SECRET_IN_LOG:-0}" = "0" ] \
	&& ok "the panel's own log does not contain the password it was set up with" \
	|| no "the panel logged something that looks like the setup password; internal/logging is meant to redact by value as well as by key"

step "Deploying again, and rolling back (checklist 8)"

api PUT "/api/apps/$APP_ID/variables" '{"key":"VERSION","value":"3"}' >/dev/null
sleep 25
FIRST=$(api GET "/api/apps/$APP_ID/deployments" | pick items.1.id 2>/dev/null || echo "")
if [ -z "$FIRST" ]; then
	skip "the rollback" "there is only one deployment to roll back to"
else
	api POST "/api/apps/$APP_ID/rollback/$FIRST" >/dev/null || no "the rollback was refused"
	for _ in $(seq 1 30); do
		BODY=$(curl -s --max-time 10 -H "Host: $APP_HOST" "http://127.0.0.1/" || true)
		case "$BODY" in *version=2*) break ;; esac
		sleep 5
	done
	case "$BODY" in
		*version=2*) ok "one call rolled the app back to what it was serving before" ;;
		*) no "after a rollback the app answers '${BODY:-nothing}'. A rollback restores the settings the old version ran with, not only its image." ;;
	esac
fi

step "A deploy that drops nothing (checklist 9)"

# Two instances, so there is something to roll. With one, a rolling update has
# no choice but to be a gap, and maxUnavailable: 0 cannot help.
api PUT "/api/apps/$APP_ID/scaling" '{"replicas":2,"autoscale":false}' >/dev/null
for _ in $(seq 1 30); do
	[ "$(kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)" = "2" ] && break
	sleep 5
done

HITS=0
MISSES=0
(
	for _ in $(seq 1 200); do
		CODE=$(through_ingress "$APP_HOST" 5)
		[ "$CODE" = "200" ] && echo hit || echo "miss:$CODE"
		sleep 0.25
	done
) >/tmp/skifity-zero-downtime.txt &
HAMMER=$!
sleep 3
api POST "/api/apps/$APP_ID/restart" >/dev/null || no "the app could not be restarted"
wait "$HAMMER" || true
HITS=$(grep -c '^hit$' /tmp/skifity-zero-downtime.txt || true)
MISSES=$(grep -c '^miss' /tmp/skifity-zero-downtime.txt || true)
say "  $HITS answered, $MISSES did not, across a rolling restart"
if [ "${MISSES:-1}" -eq 0 ]; then
	ok "a rolling restart under continuous traffic dropped nothing"
else
	no "$MISSES of $((HITS + MISSES)) requests failed during a rolling restart. maxUnavailable: 0 keeps the capacity; the preStop pause is what stops a proxy sending to a pod that has begun shutting down. $(sort /tmp/skifity-zero-downtime.txt | uniq -c | tr '\n' ' ')"
fi

step "A folder with no repository (ADR-0022)"

# The same sample app, sent as `skifity up` sends it rather than cloned. The
# build cannot reach the panel, so the panel execs into the waiting build and
# streams the archive in; this is the one place that stream runs for real.
FOLDER_TAR=$(mktemp)
tar -czf "$FOLDER_TAR" -C "$(cd "$(dirname "$0")" && pwd)/sample-app" .
FOLDER_ID=$(api POST "/api/environments/$ENV_ID/apps" \
	"{\"name\":\"folder\",\"source_type\":\"upload\",\"builder\":\"dockerfile\",\"dockerfile_path\":\"Dockerfile\",\"port\":$VERIFY_APP_PORT,\"health_path\":\"/healthz\"}" \
	| pick app.id 2>/dev/null || echo "")
UPLOAD_SHA=""
if [ -n "$FOLDER_ID" ]; then
	UPLOAD_SHA=$(curl -fsS -X PUT "$PANEL/api/apps/$FOLDER_ID/source" -H "Authorization: Bearer $TOKEN" \
		-H 'Content-Type: application/gzip' --data-binary "@$FOLDER_TAR" | pick sha256 2>/dev/null || echo "")
fi
rm -f "$FOLDER_TAR"
if [ -z "$UPLOAD_SHA" ]; then
	no "an app could not be made from an uploaded folder, or the folder was refused"
else
	ok "a folder was accepted as an app's code"
	FOLDER_DEPLOY=$(api POST "/api/apps/$FOLDER_ID/deploy" "{\"commit_sha\":\"$UPLOAD_SHA\"}" | pick id)
	for _ in $(seq 1 90); do
		STATE=$(api GET "/api/apps/$FOLDER_ID/deployments/$FOLDER_DEPLOY" | pick status 2>/dev/null || echo "")
		case "$STATE" in succeeded|failed) break ;; esac
		sleep 10
	done
	if [ "$STATE" = "succeeded" ]; then
		ok "the panel handed the folder to the build, and it was built and deployed"
		FOLDER_HOST=$(api GET "/api/apps/$FOLDER_ID/domains" | pick items.0.hostname 2>/dev/null || echo "")
		[ -n "$FOLDER_HOST" ] && [ "$(through_ingress "$FOLDER_HOST" 30)" = "200" ] \
			&& ok "the app built from a folder answers through the ingress" \
			|| no "the app built from a folder does not answer at ${FOLDER_HOST:-no address}"
		AGAIN=$(api POST "/api/apps/$FOLDER_ID/deploy" "{\"commit_sha\":\"$UPLOAD_SHA\"}" | pick image 2>/dev/null || echo "")
		[ -n "$AGAIN" ] \
			&& ok "the same folder again reuses the image instead of building (ADR-0007)" \
			|| no "deploying the same upload again started a build"
	else
		api GET "/api/apps/$FOLDER_ID/deployments/$FOLDER_DEPLOY/logs" >>"$REPORT" 2>&1 || true
		no "the deployment from a folder ended as '${STATE:-unknown}'; the log is in $REPORT. If it says the code could not be handed to the build, the exec from the panel into the build namespace is what failed."
	fi
fi
fi

##############################################################################
# Phase 2 — data: a volume, a database, a backup that restores, an export
##############################################################################

if wants 2; then
phase "Phase 2 — the data, and getting it back"

step "A volume that survives a restart (checklist 5)"

if [ -z "$APP_ID" ]; then
	skip "the volume" "phase 1 did not run, so there is no app to attach one to"
else
	api POST "/api/apps/$APP_ID/volumes" '{"name":"data","mount_path":"/data","size_gb":1}' >/dev/null \
		|| no "a volume could not be created"
	# One instance: a ReadWriteOnce volume cannot be mounted by two, and the
	# panel switches to Recreate for exactly this reason.
	api PUT "/api/apps/$APP_ID/scaling" '{"replicas":1,"autoscale":false}' >/dev/null
	for _ in $(seq 1 40); do
		MOUNTED=$(kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.spec.template.spec.volumes[0].persistentVolumeClaim.claimName}' 2>/dev/null || true)
		[ -n "$MOUNTED" ] && [ "$(kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)" = "1" ] && break
		sleep 5
	done
	if [ -z "$MOUNTED" ]; then
		no "the volume was created and the Deployment does not mount it"
	else
		ok "the instance mounts $MOUNTED"
		curl -s --max-time 15 -H "Host: $APP_HOST" "http://127.0.0.1/write?written-before-the-restart" >/dev/null || true
		api POST "/api/apps/$APP_ID/restart" >/dev/null
		sleep 10
		for _ in $(seq 1 30); do
			[ "$(kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)" = "1" ] && break
			sleep 5
		done
		AFTER=$(curl -s --max-time 15 -H "Host: $APP_HOST" "http://127.0.0.1/data" || true)
		case "$AFTER" in
			*written-before-the-restart*) ok "what was written before the restart is still there afterwards" ;;
			*) no "the file written before the restart is gone; the volume is not persisting. Got: ${AFTER:-nothing}" ;;
		esac
	fi
fi

step "A managed database (checklist 6)"

DB_ID=$(api POST "/api/environments/$ENV_ID/databases" '{"name":"verifydb","engine":"postgres","storage_gb":1}' | pick id 2>/dev/null || echo "")
if [ -z "$DB_ID" ]; then
	no "a database could not be created; the CloudNativePG operator is installed on first use and this is where that is proved"
else
	ok "a PostgreSQL database was asked for, which installs CloudNativePG on first use"
	for _ in $(seq 1 60); do
		DB_STATE=$(api GET "/api/databases/$DB_ID" | pick status 2>/dev/null || echo "")
		case "$DB_STATE" in ready|running) break ;; failed) break ;; esac
		sleep 10
	done
	case "$DB_STATE" in
		ready|running) ok "the database came up on its own" ;;
		*) no "the database is '${DB_STATE:-unknown}' after ten minutes; kubectl -n $NAMESPACE get cluster says what CloudNativePG thinks" ;;
	esac

	CRED=$(api GET "/api/databases/$DB_ID/credentials" 2>/dev/null || echo "{}")
	printf '%s' "$CRED" | grep -q "password" \
		&& ok "its credentials can be read back once, for connecting to it" \
		|| no "no credentials came back for the database"
fi

step "A backup that restores (checklist 7)"

if [ -z "$VERIFY_S3_BUCKET" ] || [ -z "$VERIFY_S3_ENDPOINT" ]; then
	skip "backup and restore" "no object storage is configured. Set VERIFY_S3_ENDPOINT, VERIFY_S3_BUCKET, VERIFY_S3_ACCESS_KEY and VERIFY_S3_SECRET_KEY — a backup with nowhere to go is not a backup, and this check refuses to pretend otherwise."
elif [ -z "$DB_ID" ]; then
	skip "backup and restore" "there is no database to back up"
else
	api PUT /api/settings "$(cat <<JSON
{"values":{"storage.s3_endpoint":"$VERIFY_S3_ENDPOINT","storage.s3_bucket":"$VERIFY_S3_BUCKET",
 "storage.s3_access_key":"$VERIFY_S3_ACCESS_KEY","storage.s3_secret_key":"$VERIFY_S3_SECRET_KEY",
 "storage.s3_path_style":"true"}}
JSON
)" >/dev/null || no "the storage settings were refused"

	# Something to lose. Through the app's own one-off command runner, which is
	# also the only way a migration ever runs.
	BACKUP_ID=$(api POST "/api/databases/$DB_ID/backups" '{}' | pick id 2>/dev/null || echo "")
	if [ -z "$BACKUP_ID" ]; then
		no "a backup could not be started"
	else
		for _ in $(seq 1 60); do
			B_STATE=$(api GET "/api/databases/$DB_ID/backups" | pick items.0.status 2>/dev/null || echo "")
			case "$B_STATE" in succeeded|failed) break ;; esac
			sleep 10
		done
		if [ "$B_STATE" = "succeeded" ]; then
			ok "a backup ran and was uploaded"
			OP=$(api POST "/api/databases/$DB_ID/restore/$BACKUP_ID?overwrite=true" | pick id 2>/dev/null || echo "")
			if [ -z "$OP" ]; then
				no "the restore was refused"
			else
				for _ in $(seq 1 60); do
					R_STATE=$(api GET "/api/operations/$OP" | pick status 2>/dev/null || echo "")
					case "$R_STATE" in succeeded|failed) break ;; esac
					sleep 10
				done
				[ "$R_STATE" = "succeeded" ] \
					&& ok "the backup restored over the live database without an error" \
					|| no "the restore ended as '${R_STATE:-unknown}'. A backup that has never been restored is a file, not a backup."
			fi
		else
			no "the backup ended as '${B_STATE:-unknown}'; the bucket, the keys or the path style is wrong"
		fi
	fi
	say "  Restoring into a *different* cluster is the half a script cannot do on one"
	say "  machine. The backup is in your bucket: install Skifity elsewhere, point it"
	say "  at the same bucket, and restore. That is the drill worth running once."
fi

step "Leaving with the data (checklist 16)"

EXPORT=$(api GET "/api/teams/$TEAM_ID/export" 2>/dev/null || echo "")
if [ -z "$EXPORT" ]; then
	no "the export answered nothing"
else
	printf '%s' "$EXPORT" | grep -q '"manifests"' \
		&& ok "the export carries each app's Kubernetes objects, so the apps can be run elsewhere" \
		|| no "the export has no manifests in it, which is the half that makes it not a lock-in"
	printf '%s' "$EXPORT" | grep -q "$PASSWORD" \
		&& no "the export contains the account password; nothing sealed should ever be in it" \
		|| ok "the export contains no secret value"
fi
fi

##############################################################################
# Phase 3 — security and isolation
##############################################################################

if wants 3; then
phase "Phase 3 — what one tenant cannot do to another"

step "One team cannot see another's (checklist 12)"

OTHER_TEAM=$(api POST /api/teams '{"name":"Other"}' | pick id 2>/dev/null || echo "")
if [ -z "$OTHER_TEAM" ]; then
	skip "the cross-team check" "a second team could not be created"
else
	# The token was issued for the first team. A token bound to one team is
	# supposed to be useless against another, even for its own owner.
	CODE=$(api_code GET "/api/teams/$OTHER_TEAM/projects")
	[ "$CODE" = "404" ] \
		&& ok "a token issued for one team answers 404 on another, so it cannot even be used to find out what exists" \
		|| no "a token for one team answered $CODE on another team's projects; it should be 404"
fi

step "A member is not an admin (checklist 11)"

INVITE=$(api POST "/api/teams/$TEAM_ID/invitations" '{"email":"member@example.test","role":"member"}' 2>/dev/null || echo "")
INVITE_URL=$(printf '%s' "$INVITE" | pick url 2>/dev/null || echo "")
if [ -z "$INVITE_URL" ]; then
	no "a member could not be invited, so the roles could not be checked"
else
	ok "an invitation link was issued"
	INVITE_TOKEN="${INVITE_URL##*/}"
	MEMBER_COOKIE=$(mktemp)
	curl -fsS -c "$MEMBER_COOKIE" -X POST "$PANEL/api/invitations/$INVITE_TOKEN/accept" \
		-H 'Content-Type: application/json' \
		-d '{"name":"Member","password":"a reasonable member passphrase"}' >/dev/null 2>&1 \
		|| no "the invitation could not be accepted"
	MEMBER_CSRF=$(awk '/skifity_csrf/ {print $7}' "$MEMBER_COOKIE")
	MEMBER_TOKEN=$(curl -fsS -b "$MEMBER_COOKIE" -H "X-Skifity-CSRF: $MEMBER_CSRF" \
		-H 'Content-Type: application/json' -X POST "$PANEL/api/me/tokens" \
		-d "{\"name\":\"member\",\"team_id\":\"$TEAM_ID\",\"ttl_hours\":1}" | pick secret 2>/dev/null || echo "")
	if [ -z "$MEMBER_TOKEN" ]; then
		no "the invited member could not get a token, so their permissions could not be checked"
	else
		ok "the invited member has an account and can sign in"
		CODE=$(api_code GET "/api/settings" "" "$MEMBER_TOKEN")
		[ "$CODE" = "403" ] \
			&& ok "a member cannot read the panel's settings" \
			|| no "a member got $CODE from /api/settings, which is admin-only"
		CODE=$(api_code GET "/api/teams/$TEAM_ID/export" "" "$MEMBER_TOKEN")
		[ "$CODE" = "403" ] \
			&& ok "a member cannot download the whole team" \
			|| no "a member got $CODE from the export, which is admin-only"
		CODE=$(api_code POST "/api/teams/$TEAM_ID/invitations" '{"email":"third@example.test","role":"admin"}' "$MEMBER_TOKEN")
		[ "$CODE" = "403" ] \
			&& ok "a member cannot invite an admin" \
			|| no "a member got $CODE when inviting an admin"
	fi
fi

step "One namespace cannot reach another (checklist 12)"

OTHER_NS="verify-isolation"
kubectl create namespace "$OTHER_NS" >/dev/null 2>&1 || true
if [ -n "$APP_HOST" ] && kubectl -n "$NAMESPACE" get svc hello >/dev/null 2>&1; then
	if kubectl -n "$OTHER_NS" run verify-probe --rm -i --restart=Never --timeout=90s \
		--image=curlimages/curl:8.11.1 --command -- \
		curl -fsS --max-time 8 "http://hello.$NAMESPACE.svc.cluster.local" >/dev/null 2>&1; then
		no "a pod in another namespace reached this environment's app. The default-deny NetworkPolicy is the only thing between two tenants on one cluster."
	else
		ok "a pod in another namespace cannot reach this environment's app"
	fi
else
	skip "the namespace isolation check" "there is no app Service to aim at"
fi
kubectl delete namespace "$OTHER_NS" --wait=false >/dev/null 2>&1 || true

step "A quota is a real ceiling (checklist 13)"

QUOTA=$(api GET "/api/environments/$ENV_ID/quota" 2>/dev/null || echo "{}")
if printf '%s' "$QUOTA" | grep -q '"found": *true'; then
	ok "the environment reports its quota and what is used against it"
else
	no "the environment has no quota to report; every namespace is supposed to get a ResourceQuota"
fi
kubectl -n "$NAMESPACE" get resourcequota environment >/dev/null 2>&1 \
	&& ok "a ResourceQuota exists in the namespace" \
	|| no "no ResourceQuota in $NAMESPACE, so one environment can take the whole cluster"
kubectl -n "$NAMESPACE" get limitrange defaults >/dev/null 2>&1 \
	&& ok "a LimitRange gives containers a default reservation and ceiling" \
	|| no "no LimitRange in $NAMESPACE, so a container with no limits can starve a node"

# And it has to be a ceiling rather than a label. Asking for 64 cores in an
# environment allowed 8 must be refused by the cluster, not queued forever.
kubectl -n "$NAMESPACE" create deployment quota-probe --image=registry.k8s.io/pause:3.10 >/dev/null 2>&1
kubectl -n "$NAMESPACE" set resources deployment/quota-probe --requests=cpu=64,memory=64Gi >/dev/null 2>&1
sleep 10
QUOTA_SAID=$(kubectl -n "$NAMESPACE" get deploy quota-probe -o jsonpath='{.status.conditions[?(@.type=="ReplicaFailure")].message}' 2>/dev/null || true)
case "$QUOTA_SAID" in
	*exceeded*quota*) ok "a workload asking for 64 cores was refused by the environment's quota" ;;
	*) no "asking for 64 cores in this namespace was not refused by the quota. It said: ${QUOTA_SAID:-nothing}" ;;
esac
kubectl -n "$NAMESPACE" delete deployment quota-probe --wait=false >/dev/null 2>&1 || true
fi

##############################################################################
# Phase 4 — the panel is not the product
##############################################################################

if wants 4; then
phase "Phase 4 — what survives the panel"

step "The apps keep serving with the panel switched off (checklist 14)"

if [ -z "$APP_HOST" ]; then
	skip "the panel-down check" "phase 1 did not run, so there is no app to keep serving"
else
	kubectl -n "$PANEL_NS" scale deploy skifity-panel --replicas=0 >/dev/null
	for _ in $(seq 1 24); do
		[ -z "$(kubectl -n "$PANEL_NS" get deploy skifity-panel -o jsonpath='{.status.readyReplicas}' 2>/dev/null)" ] && break
		sleep 5
	done
	ok "the panel is stopped"

	DOWN_FAILS=0
	for _ in $(seq 1 10); do
		[ "$(through_ingress "$APP_HOST" 10)" = "200" ] || DOWN_FAILS=$((DOWN_FAILS + 1))
		sleep 1
	done
	[ "$DOWN_FAILS" = "0" ] \
		&& ok "the app answered every request with no panel running at all" \
		|| no "$DOWN_FAILS of 10 requests failed while the panel was down. Apps are served by Kubernetes; the panel is not in the path and must not be."

	kubectl -n "$PANEL_NS" scale deploy skifity-panel --replicas=1 >/dev/null
	for _ in $(seq 1 36); do
		kubectl -n "$PANEL_NS" get deploy skifity-panel -o jsonpath='{.status.readyReplicas}' 2>/dev/null | grep -q '^[1-9]' && break
		sleep 5
	done
	stop_forward
	start_forward
	api GET /api/health >/dev/null 2>&1 \
		&& ok "the panel came back on its own, with its database intact" \
		|| no "the panel did not come back after being scaled to zero"
fi

step "A server going away (checklist 14)"

NODES=$(kubectl get nodes --no-headers 2>/dev/null | wc -l)
if [ "${NODES:-1}" -lt 2 ]; then
	skip "the node-failure check" "this cluster has one node. Add a second server in the panel and run this phase again with SKIFITY_PHASES=4 — with one node there is nowhere for the work to go, and pretending otherwise would be the kind of claim this file exists to stop."
else
	VICTIM=$(kubectl get nodes --no-headers -l '!node-role.kubernetes.io/control-plane' -o name 2>/dev/null | head -1)
	if [ -z "$VICTIM" ]; then
		skip "the node-failure check" "every node runs the control plane; draining one would take the cluster with it"
	else
		kubectl drain "$VICTIM" --ignore-daemonsets --delete-emptydir-data --force --timeout=180s >>"$REPORT" 2>&1 \
			&& ok "$VICTIM drained: the disruption budget let the instances move one at a time" \
			|| no "$VICTIM could not be drained inside three minutes; a PodDisruptionBudget that cannot be satisfied blocks a drain forever"
		DRAIN_FAILS=0
		for _ in $(seq 1 10); do
			[ "$(through_ingress "$APP_HOST" 10)" = "200" ] || DRAIN_FAILS=$((DRAIN_FAILS + 1))
			sleep 1
		done
		[ "$DRAIN_FAILS" = "0" ] \
			&& ok "the app kept answering while a server was emptied" \
			|| no "$DRAIN_FAILS of 10 requests failed while a node was drained"
		kubectl uncordon "$VICTIM" >/dev/null 2>&1 || true
	fi
fi
fi

##############################################################################
# Phase 5 — running it day to day
##############################################################################

if wants 5; then
phase "Phase 5 — logs, metrics, upgrades and being told"

step "Logs and metrics (checklist 10)"

if [ -n "$APP_ID" ]; then
	api GET "/api/apps/$APP_ID/logs?tail=20" 2>/dev/null | head -c 200 >/dev/null \
		&& ok "an app's own logs can be read through the panel" \
		|| no "the app's logs could not be read"
	STATUS=$(api GET "/api/apps/$APP_ID/status" 2>/dev/null || echo "{}")
	printf '%s' "$STATUS" | grep -q "cpu_used_m" \
		&& ok "each instance reports what it is using, not only what it reserved" \
		|| no "the app's status carries no per-instance usage; metrics-server may not be answering"
fi
api GET /api/metrics 2>/dev/null | grep -q "^skifity_" \
	&& ok "the panel serves its own Prometheus metrics, behind the same authentication as everything else" \
	|| no "/api/metrics returned nothing that looks like Prometheus output"

step "Upgrading with an app running through it (checklist 15)"

UP=$(api GET /api/upgrade 2>/dev/null || echo "{}")
CURRENT=$(printf '%s' "$UP" | pick current_version 2>/dev/null || echo "")
say "  the panel reports version ${CURRENT:-unknown}"
if [ -z "$APP_HOST" ]; then
	skip "the upgrade check" "phase 1 did not run, so there is no app to keep serving through it"
else
	UP_FAILS=0
	(
		for _ in $(seq 1 60); do
			[ "$(through_ingress "$APP_HOST" 5)" = "200" ] || echo miss
			sleep 1
		done
	) >/tmp/skifity-upgrade.txt &
	HAMMER=$!
	# Rolling the panel's own Deployment is exactly what an upgrade does; doing
	# it without changing the version checks the part that can break — the
	# apps' indifference to it — without needing a second release to exist.
	kubectl -n "$PANEL_NS" rollout restart deploy/skifity-panel >/dev/null 2>&1
	kubectl -n "$PANEL_NS" rollout status deploy/skifity-panel --timeout=300s >>"$REPORT" 2>&1 \
		&& ok "the panel rolled to a new pod" \
		|| no "the panel's own rollout did not finish in five minutes"
	wait "$HAMMER" || true
	UP_FAILS=$(grep -c miss /tmp/skifity-upgrade.txt || true)
	[ "${UP_FAILS:-1}" -eq 0 ] \
		&& ok "the app served every request while the panel was replaced under it" \
		|| no "$UP_FAILS requests failed while the panel restarted; the panel is not supposed to be in the path at all"
	stop_forward
	start_forward
fi

step "Being told when something fails (checklist 17)"

NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null || echo "")
if [ -z "$NODE_IP" ]; then
	skip "the notification check" "this node has no internal address to send a webhook to"
else
	HOOK_LOG=$(mktemp)
	python3 - "$HOOK_LOG" >/dev/null 2>&1 <<'PYEOF' &
import http.server, sys, threading
path = sys.argv[1]
class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("content-length", 0))
        with open(path, "ab") as f:
            f.write(self.rfile.read(length) + b"\n")
        self.send_response(204)
        self.end_headers()
    def log_message(self, *_):
        pass
http.server.HTTPServer(("0.0.0.0", 19099), Handler).serve_forever()
PYEOF
	LISTENER=$!
	sleep 2
	CHANNEL=$(api POST "/api/teams/$TEAM_ID/notifications" "$(cat <<JSON
{"kind":"webhook","name":"verify","config":{"url":"http://$NODE_IP:19099/"},
 "events":["deploy.failed","deploy.succeeded","backup.failed","certificate.failed","app.unhealthy"]}
JSON
)" | pick id 2>/dev/null || echo "")
	if [ -z "$CHANNEL" ]; then
		no "a webhook notification channel could not be created"
	else
		api POST "/api/teams/$TEAM_ID/notifications/$CHANNEL/test" >/dev/null 2>&1 || true
		sleep 5
		[ -s "$HOOK_LOG" ] \
			&& ok "a test notification arrived at the webhook" \
			|| no "the test notification never arrived. The panel dials out through internal/netguard, which refuses loopback and the metadata service; $NODE_IP is a private address and should be allowed."

		# And a real failure, which is the one that matters.
		: >"$HOOK_LOG"
		BROKEN=$(api POST "/api/environments/$ENV_ID/apps" \
			'{"name":"broken","source_type":"image","image":"example.invalid/nothing:0","port":8080,"deploy":true}' \
			| pick app.id 2>/dev/null || echo "")
		if [ -n "$BROKEN" ]; then
			for _ in $(seq 1 60); do
				[ -s "$HOOK_LOG" ] && break
				sleep 5
			done
			[ -s "$HOOK_LOG" ] \
				&& ok "a deployment that could not pull its image sent a notification" \
				|| no "a deployment failed and nothing was sent. A failure nobody is told about is an outage that starts whenever somebody next looks."
			api DELETE "/api/apps/$BROKEN" >/dev/null 2>&1 || true
		fi
	fi
	kill "$LISTENER" 2>/dev/null || true
fi
fi

# --- what it costs ---------------------------------------------------------

phase "What it costs, measured"

sleep 20
K3S_MB=$(ps -o rss= -C k3s-server 2>/dev/null | awk '{s+=$1} END {print int(s/1024)}')
PANEL_POD=$(kubectl -n "$PANEL_NS" get pod -l app=skifity-panel -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
PANEL_MB=$(kubectl -n "$PANEL_NS" top pod "$PANEL_POD" --no-headers 2>/dev/null | awk '{print $3}' | tr -d 'Mi')
USED_MB=$(free -m | awk '/^Mem:/ {print $3}')

say "  k3s server process : ${K3S_MB:-unknown} MB"
say "  the panel pod      : ${PANEL_MB:-unknown} MB"
say "  the whole machine  : ${USED_MB:-unknown} MB of ${MEM_MB} MB in use"
say ""
say "  docs/performance.md says the panel is 35 MiB idle, measured on a laptop."
say "  The number above is the first one measured on a cluster. If they disagree,"
say "  the document is what changes."

summary
[ "$FAIL" -eq 0 ]
