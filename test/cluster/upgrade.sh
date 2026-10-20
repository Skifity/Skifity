#!/usr/bin/env bash
# The upgrade check: the last release, installed the way a stranger installs
# it, upgraded to this checkout by running the installer again with an app
# serving through it, and taken back with the commands the installer prints.
#
# verify.sh checks a fresh install of this checkout. Most servers will not be
# one: an earlier release installed them, and what they get is a newer
# installer run over the top. This is that, on a real server:
#
#   1  The newest published release, installed by that release's own
#      installer, downloaded from the release the way the README says.
#   2  The panel set up, an app deployed from an image with a variable, and a
#      PostgreSQL database asked for.
#   3  This checkout's installer run again over it, with the app asked for an
#      answer twice a second throughout, and every answer counted.
#   4  The new version on the same database: the account signs in, the old API
#      token still works, the app and its variable are there, the database was
#      copied before anything changed, k3s has what a fresh install gives it,
#      and the new panel deploys the old panel's app.
#   5  The way back the installer printed, run as printed, with the app still
#      being asked: the old version comes back, on the copy.
#
#   THIS INSTALLS k3s AND CHANGES THE MACHINE IT RUNS ON.
#
#   sudo SKIFITY_IMAGE=skifity:local bash test/cluster/upgrade.sh
#
# SKIFITY_IMAGE is what this checkout's installer upgrades to, and has to be
# where k3s finds it: the Cluster workflow hands it to k3s before k3s exists.
# UPGRADE_FROM is the release to start from, the newest published one when it
# is not set. UPGRADE_APP_IMAGE is the app kept serving: the sample app,
# handed to k3s the same way as skifity-sample:upgrade.

set -euo pipefail

PANEL_NS="skifity-system"
REPORT="/var/log/skifity-upgrade.log"
DATA_DIR="/var/lib/skifity"
K3S_CONFIG_DIR="/etc/rancher/k3s"
: "${SKIFITY_ASSUME_YES:=0}"
: "${SKIFITY_IMAGE:=}"
: "${UPGRADE_FROM:=}"
: "${UPGRADE_APP_IMAGE:=skifity-sample:upgrade}"

HERE="$(cd "$(dirname "$0")/../.." && pwd)"
# The repository releases are published from, read from the installer's own
# line so it is written in one place. The dollar is the installer's text.
# shellcheck disable=SC2016
REPO=$(sed -n 's/^PROJECT_REPO="\${SKIFITY_REPO:-\(.*\)}"$/\1/p' "$HERE/installer/install.sh")

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
}

confirm() {
	[ "$SKIFITY_ASSUME_YES" = "1" ] && return 0
	printf '\nThis installs k3s and changes this machine permanently.\n'
	printf 'Type the word yes to continue: '
	read -r answer
	[ "$answer" = "yes" ] || { echo "Nothing was changed."; exit 1; }
}

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is not installed, and this needs it"; }

# pick reads one field out of JSON on stdin, by dotted path.
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

start_forward() {
	kubectl -n "$PANEL_NS" port-forward svc/skifity-panel 18080:80 >/dev/null 2>&1 &
	FORWARD=$!
	sleep 3
}
# Never `kill 0`: with no port-forward started, that is every process in this
# group, the CI runner's own step included.
stop_forward() {
	[ -z "${FORWARD:-}" ] || kill "$FORWARD" 2>/dev/null || true
	FORWARD=""
}
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

through_ingress() {
	curl -s -o /dev/null -w '%{http_code}' --max-time "${2:-5}" -H "Host: $1" "http://127.0.0.1/"
}

body_through_ingress() {
	curl -s --max-time 10 -H "Host: $1" "http://127.0.0.1/" || true
}

# panel_ready waits for the panel's Deployment to have finished rolling and to
# answer its health check through a fresh port-forward.
panel_ready() {
	kubectl -n "$PANEL_NS" rollout status deploy/skifity-panel --timeout=300s >>"$REPORT" 2>&1 || return 1
	stop_forward
	start_forward
	for _ in $(seq 1 30); do
		api GET /api/health >/dev/null 2>&1 && return 0
		sleep 2
	done
	return 1
}

panel_image() {
	kubectl -n "$PANEL_NS" get deploy skifity-panel -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || true
}

# wait_deployment ID waits for one of the app's deployments to end, and prints
# how it ended.
wait_deployment() {
	local state=""
	for _ in $(seq 1 60); do
		state=$(api GET "/api/apps/$APP_ID/deployments/$1" | pick status 2>/dev/null || echo "")
		case "$state" in succeeded | failed) break ;; esac
		sleep 5
	done
	printf '%s' "$state"
}

# The app is asked for an answer twice a second, from hammer_start until
# hammer_stop, and every answer is written down with the time it came: a
# failure says when, which says which step of the upgrade it was.
hammer_start() {
	HAMMER_LOG=$(mktemp)
	HAMMER_STOP=$(mktemp -u)
	(
		while [ ! -e "$HAMMER_STOP" ]; do
			printf '%s %s\n' "$(date -u +%H:%M:%S)" "$(through_ingress "$APP_HOST" 5)"
			sleep 0.5
		done
	) >"$HAMMER_LOG" 2>/dev/null &
	HAMMER_PID=$!
}

# hammer_stop LABEL stops asking, and passes when every answer was a 200.
hammer_stop() {
	: >"$HAMMER_STOP"
	wait "$HAMMER_PID" 2>/dev/null || true
	rm -f "$HAMMER_STOP"
	local total missed
	total=$(wc -l <"$HAMMER_LOG" | tr -d ' ')
	missed=$(awk '$2 != "200"' "$HAMMER_LOG" | wc -l | tr -d ' ')
	if [ "$missed" -eq 0 ] && [ "$total" -gt 0 ]; then
		ok "the app answered all $total requests $1"
	else
		no "$missed of $total requests $1 did not answer 200; when, and with what:"
		awk '$2 != "200"' "$HAMMER_LOG" | head -40 | sed 's/^/      /' | tee -a "$REPORT"
	fi
	rm -f "$HAMMER_LOG"
}

trap 'stop_forward; [ -n "${HAMMER_STOP:-}" ] && : >"$HAMMER_STOP" 2>/dev/null || true' EXIT

# --- preflight -------------------------------------------------------------

: >"$REPORT"
phase "Before anything is installed"

[ "$(id -u)" = "0" ] || die "this has to run as root: it installs k3s"
need curl
need awk
need python3
[ -n "$SKIFITY_IMAGE" ] || die "set SKIFITY_IMAGE to the image this checkout's installer upgrades to, somewhere k3s will find it"
[ -n "$REPO" ] || die "could not read PROJECT_REPO from installer/install.sh"

if [ -z "$UPGRADE_FROM" ]; then
	UPGRADE_FROM=$(curl -fsS --max-time 20 "https://api.github.com/repos/$REPO/releases/latest" | pick tag_name 2>/dev/null || true)
fi
[ -n "$UPGRADE_FROM" ] || die "could not find the newest release of $REPO; set UPGRADE_FROM"
ok "upgrading $REPO from $UPGRADE_FROM to $SKIFITY_IMAGE"

PUBLIC_IP="${SKIFITY_PUBLIC_IP:-$(curl -fsS --max-time 10 https://api.ipify.org || true)}"
[ -n "$PUBLIC_IP" ] || die "could not work out this machine's public IP; set SKIFITY_PUBLIC_IP"
DOMAIN="${SKIFITY_DOMAIN:-${PUBLIC_IP}.nip.io}"
ok "public IP is $PUBLIC_IP, and the panel will answer on $DOMAIN"

confirm

##############################################################################
# 1 — the last release, installed the way the README says
##############################################################################

phase "The last release, installed with its own installer"

FROM_INSTALLER=$(mktemp)
curl -fsSL --max-time 60 "https://github.com/$REPO/releases/download/$UPGRADE_FROM/install.sh" -o "$FROM_INSTALLER" \
	|| die "the installer published with $UPGRADE_FROM could not be downloaded"
ok "the installer published with $UPGRADE_FROM was downloaded"

START=$(date +%s)
if SKIFITY_ASSUME_YES=1 SKIFITY_ACME_STAGING=1 SKIFITY_DOMAIN="$DOMAIN" SKIFITY_PUBLIC_IP="$PUBLIC_IP" \
	sh "$FROM_INSTALLER" >>"$REPORT" 2>&1; then
	ok "$UPGRADE_FROM installed in $(($(date +%s) - START))s"
else
	die "the installer of $UPGRADE_FROM failed; the lines above in $REPORT say where"
fi

need kubectl
panel_ready || die "the panel $UPGRADE_FROM installed never answered"
FROM_IMAGE=$(panel_image)
ok "the panel runs $FROM_IMAGE"

##############################################################################
# 2 — something to keep
##############################################################################

phase "Something to keep"

step "An account, a token and a team"

SETUP_TOKEN=$(cat /etc/skifity/setup-token 2>/dev/null || true)
[ -n "$SETUP_TOKEN" ] || die "no setup token at /etc/skifity/setup-token"
PASSWORD="upgrade-$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')"
SETUP=$(api POST /api/setup "{\"token\":\"$SETUP_TOKEN\",\"email\":\"upgrade@example.test\",\"name\":\"Upgrade\",\"password\":\"$PASSWORD\",\"team_name\":\"Upgrade\"}") \
	|| die "first-run setup was refused"
TEAM_ID=$(printf '%s' "$SETUP" | pick team.id) || die "setup did not return a team"

sign_in() {
	COOKIE=$(mktemp)
	curl -fsS -c "$COOKIE" -X POST "$PANEL/api/auth/login" -H 'Content-Type: application/json' \
		-d "{\"email\":\"upgrade@example.test\",\"password\":\"$PASSWORD\"}" >/dev/null 2>&1
}
sign_in || die "signing in with the account just created was refused"
CSRF=$(awk '/skifity_csrf/ {print $7}' "$COOKIE")
TOKEN=$(curl -fsS -b "$COOKIE" -H "X-Skifity-CSRF: $CSRF" -H 'Content-Type: application/json' \
	-X POST "$PANEL/api/me/tokens" -d "{\"name\":\"upgrade\",\"team_id\":\"$TEAM_ID\",\"ttl_hours\":4}" \
	| pick secret) || true
[ -n "$TOKEN" ] || die "could not create an API token"
ok "an account, a team and an API token exist on $UPGRADE_FROM"

step "An app that answers through the ingress"

# The first cluster run found that a release before it could not work out a
# cloud server's public address on its own, and gave an app none. It is told
# here, so the app has an address to be asked at whichever release is running.
api PUT /api/settings "{\"values\":{\"domains.cluster_ip\":\"$PUBLIC_IP\"}}" >/dev/null \
	|| no "the cluster's public address was refused as a setting"

PROJECT_ID=$(api POST "/api/teams/$TEAM_ID/projects" '{"name":"Upgrade"}' | pick id) \
	|| die "a project could not be created"
ENV_ID=$(api GET "/api/projects/$PROJECT_ID/environments" | pick items.0.id) || die "the project has no environment"
NAMESPACE=$(api GET "/api/environments/$ENV_ID" | pick namespace) || die "the environment has no namespace"

APP_ID=$(api POST "/api/environments/$ENV_ID/apps" "$(cat <<JSON
{"name":"steady","source_type":"image","image":"$UPGRADE_APP_IMAGE","port":8080,
 "health_path":"/healthz","deploy":true,"variables":{"VERSION":"before"}}
JSON
)" | pick app.id) || die "an app could not be created from $UPGRADE_APP_IMAGE"
FIRST_DEPLOY=$(api GET "/api/apps/$APP_ID/deployments" | pick items.0.id) || die "no deployment was recorded for the app"
STATE=$(wait_deployment "$FIRST_DEPLOY")
[ "$STATE" = "succeeded" ] || die "the app's first deployment ended as '${STATE:-unknown}'; kubectl -n $NAMESPACE describe pod says why"

APP_HOST=$(api GET "/api/apps/$APP_ID/domains" | pick items.0.hostname 2>/dev/null || echo "")
[ -n "$APP_HOST" ] || die "the app was given no address, so there is nothing to keep asking"
CODE=""
for _ in $(seq 1 24); do
	CODE=$(through_ingress "$APP_HOST" 10)
	[ "$CODE" = "200" ] && break
	sleep 5
done
[ "$CODE" = "200" ] || die "the app at $APP_HOST answered $CODE through the ingress"
case "$(body_through_ingress "$APP_HOST")" in
*version=before*) ok "the app answers at $APP_HOST, with the variable it was given" ;;
*) die "the app answers, but not with VERSION=before: the variable did not reach it" ;;
esac

step "A database"

DB_ID=$(api POST "/api/environments/$ENV_ID/databases" '{"name":"keepdb","engine":"postgres","storage_gb":1}' | pick id 2>/dev/null || echo "")
DB_BEFORE=""
if [ -z "$DB_ID" ]; then
	no "$UPGRADE_FROM refused to create a PostgreSQL database"
else
	for _ in $(seq 1 30); do
		DB_BEFORE=$(api GET "/api/databases/$DB_ID" | pick database.status 2>/dev/null || echo "")
		case "$DB_BEFORE" in ready | running | failed) break ;; esac
		sleep 10
	done
	say "  the database is '${DB_BEFORE:-unknown}' on $UPGRADE_FROM"
fi

##############################################################################
# 3 — the upgrade, with the app being asked throughout
##############################################################################

phase "Upgrading to this checkout by running the installer again"

UPGRADE_OUT=$(mktemp)
hammer_start
START=$(date +%s)
# What somebody upgrading runs is the newer installer with nothing else; the
# image and the manifests are this checkout's because that is what is being
# upgraded to. Staging, as before: nothing on the internet can reach this
# machine for a certificate.
if SKIFITY_IMAGE="$SKIFITY_IMAGE" SKIFITY_ASSUME_YES=1 SKIFITY_ACME_STAGING=1 \
	SKIFITY_PUBLIC_IP="$PUBLIC_IP" SKIFITY_MANIFEST_BASE="file://$HERE/deploy" \
	sh "$HERE/installer/install.sh" >"$UPGRADE_OUT" 2>&1; then
	ok "the installer upgraded the panel in $(($(date +%s) - START))s"
else
	cat "$UPGRADE_OUT" >>"$REPORT"
	hammer_stop "while the installer ran"
	die "the installer failed to upgrade $UPGRADE_FROM; its output is above in $REPORT"
fi
cat "$UPGRADE_OUT" >>"$REPORT"
# Some of what the installer changes finishes after it returns: k3s's own
# controller rolls Traefik when its configuration changed, and the new panel
# looks at every app once it starts.
sleep 120
hammer_stop "while the installer ran, and for two minutes after"

##############################################################################
# 4 — the new version, on the same database
##############################################################################

phase "The new version, on the same database"

[ "$(panel_image)" = "$SKIFITY_IMAGE" ] \
	&& ok "the panel runs $SKIFITY_IMAGE" \
	|| no "the panel runs $(panel_image), not $SKIFITY_IMAGE"
panel_ready && ok "the new panel answers" || die "the new panel never answered"

[ "$(api GET "/api/apps/$APP_ID" 2>/dev/null | pick id 2>/dev/null || echo "")" = "$APP_ID" ] \
	&& ok "the API token $UPGRADE_FROM issued still works, and finds the app" \
	|| no "the API token $UPGRADE_FROM issued no longer works, or the app is gone"
sign_in && ok "the account still signs in with its password" \
	|| no "the account no longer signs in after the upgrade"
api GET "/api/apps/$APP_ID/variables" 2>/dev/null | grep -q '"VERSION"' \
	&& ok "the app's variable is still there" \
	|| no "the app's variable is gone"
case "$(body_through_ingress "$APP_HOST")" in
*version=before*) ok "the app still answers with the value it was given" ;;
*) no "the app no longer answers with VERSION=before" ;;
esac

SNAPSHOT=$(find "$DATA_DIR" -maxdepth 1 -name 'panel.db.before-upgrade-*' -newer "$FROM_INSTALLER" 2>/dev/null | sort | tail -n 1)
if [ -n "$SNAPSHOT" ] && [ -s "$SNAPSHOT" ]; then
	ok "the database was copied before the upgrade, to $SNAPSHOT"
else
	no "no copy of the database was taken before the upgrade"
fi

# What a fresh install of this checkout gives k3s, an upgraded server has too.
if [ -s "$K3S_CONFIG_DIR/config.yaml.d/10-skifity-kubelet.yaml" ]; then
	CAPACITY=$(kubectl get nodes -o jsonpath='{.items[0].status.capacity.memory}' 2>/dev/null || echo "")
	ALLOCATABLE=$(kubectl get nodes -o jsonpath='{.items[0].status.allocatable.memory}' 2>/dev/null || echo "")
	if [ -n "$CAPACITY" ] && [ "$CAPACITY" != "$ALLOCATABLE" ]; then
		ok "the kubelet keeps memory back for k3s, as on a fresh install ($ALLOCATABLE of $CAPACITY is for pods)"
	else
		no "the kubelet's reservations are written but not in effect: allocatable is all of $CAPACITY"
	fi
else
	no "the upgrade did not give k3s the kubelet's reservations a fresh install has"
fi
if [ -d /var/lib/rancher/k3s/server/db/etcd ]; then
	[ -s "$K3S_CONFIG_DIR/config.yaml.d/20-skifity-etcd.yaml" ] \
		&& ok "and the etcd snapshot schedule" \
		|| no "the upgrade did not give k3s the etcd snapshot schedule a fresh install has"
fi

step "The new panel deploys the old panel's app"

hammer_start
api PUT "/api/apps/$APP_ID/variables" '{"key":"VERSION","value":"after"}' >/dev/null \
	|| no "a variable could not be changed on the new panel"
DEPLOY=$(api POST "/api/apps/$APP_ID/deploy" | pick id 2>/dev/null || echo "")
STATE=""
[ -n "$DEPLOY" ] && STATE=$(wait_deployment "$DEPLOY")
[ "$STATE" = "succeeded" ] \
	&& ok "a deployment of it succeeded" \
	|| no "a deployment of it on the new panel ended as '${STATE:-not started}'"
for _ in $(seq 1 24); do
	case "$(body_through_ingress "$APP_HOST")" in *version=after*) break ;; esac
	sleep 5
done
case "$(body_through_ingress "$APP_HOST")" in
*version=after*) ok "and it answers with the value changed on the new panel" ;;
*) no "the app does not answer with the value changed on the new panel" ;;
esac
hammer_stop "while the new panel deployed it"

step "The database asked for before the upgrade"

if [ -n "$DB_ID" ]; then
	DB_NOW=""
	for _ in $(seq 1 18); do
		DB_NOW=$(api GET "/api/databases/$DB_ID" | pick database.status 2>/dev/null || echo "")
		case "$DB_NOW" in ready | running) break ;; esac
		sleep 10
	done
	case "$DB_BEFORE:$DB_NOW" in
	ready:ready | running:running | ready:running | running:ready)
		ok "it was ready on $UPGRADE_FROM and is ready now" ;;
	*:ready | *:running)
		ok "it was '${DB_BEFORE:-unknown}' on $UPGRADE_FROM and came up after the upgrade" ;;
	*)
		# The advice for a database an earlier release could not start: delete
		# it and ask again. That advice is checked here.
		say "  it was '${DB_BEFORE:-unknown}' on $UPGRADE_FROM and is '${DB_NOW:-unknown}' now: deleting it and asking again"
		curl -fsS -X DELETE "$PANEL/api/databases/$DB_ID" -H "Authorization: Bearer $TOKEN" >/dev/null 2>&1 \
			|| no "the database $UPGRADE_FROM could not start could not be deleted"
		GONE=""
		for _ in $(seq 1 30); do
			CODE=$(curl -s -o /dev/null -w '%{http_code}' "$PANEL/api/databases/$DB_ID" -H "Authorization: Bearer $TOKEN")
			[ "$CODE" = "404" ] && { GONE=1; break; }
			sleep 5
		done
		[ -n "$GONE" ] && ok "the database $UPGRADE_FROM could not start was deleted" \
			|| no "the database $UPGRADE_FROM could not start is still there after being deleted"
		NEW_DB=$(api POST "/api/environments/$ENV_ID/databases" '{"name":"keepdb2","engine":"postgres","storage_gb":1}' | pick id 2>/dev/null || echo "")
		DB_NEW=""
		for _ in $(seq 1 60); do
			[ -n "$NEW_DB" ] || break
			DB_NEW=$(api GET "/api/databases/$NEW_DB" | pick database.status 2>/dev/null || echo "")
			case "$DB_NEW" in ready | running | failed) break ;; esac
			sleep 10
		done
		case "$DB_NEW" in
		ready | running) ok "a database asked for again after the upgrade came up" ;;
		*) no "a database asked for again after the upgrade is '${DB_NEW:-not created}'" ;;
		esac
		;;
	esac
else
	skip "the database after the upgrade" "$UPGRADE_FROM made none"
fi

##############################################################################
# 5 — the way back, as the installer printed it
##############################################################################

phase "Going back, with the commands the installer printed"

# The four lines after "If the new version does not come up", as printed.
WAY_BACK=$(awk '/If the new version does not come up/ {found = 1; next} found && /^    (kubectl|skifity) / {print; n++} n == 4 {exit}' "$UPGRADE_OUT" | sed 's/^ *//')
if [ "$(printf '%s\n' "$WAY_BACK" | grep -c .)" != "4" ]; then
	no "the installer did not print the four commands that go back"
	printf '%s\n' "$WAY_BACK" | sed 's/^/      /' | tee -a "$REPORT"
else
	ok "the installer printed the way back"
	hammer_start
	stop_forward
	while IFS= read -r command; do
		say "  \$ $command"
		sh -c "$command" >>"$REPORT" 2>&1 || no "'$command' failed"
	done <<EOF
$WAY_BACK
EOF
	if panel_ready; then
		ok "the panel answers after going back"
	else
		no "the panel did not come back"
	fi
	[ "$(panel_image)" = "$FROM_IMAGE" ] \
		&& ok "it runs $FROM_IMAGE again" \
		|| no "it runs $(panel_image) after going back, not $FROM_IMAGE"
	sign_in && ok "the account signs in on the copy taken before the upgrade" \
		|| no "the account does not sign in after going back"
	[ "$(api GET "/api/apps/$APP_ID" 2>/dev/null | pick id 2>/dev/null || echo "")" = "$APP_ID" ] \
		&& ok "and the app is there" \
		|| no "the app is not there after going back"
	sleep 30
	hammer_stop "while the panel went back"
fi

summary
[ "$FAIL" -eq 0 ]
