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
#      a variable read from a secret manager, a rollback, a deploy that drops
#      no requests, and a folder with no repository behind it built the same
#      way.
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
# The branch to build from: whatever branch this checkout is on, because that
# is the code being verified, so it is read rather than assumed to be main. A
# run that spent an hour installing and then failed to clone a branch nobody
# ever pushed would be a waste of a server.
: "${VERIFY_GIT_BRANCH:=$(git -C "$(dirname "$0")/../.." rev-parse --abbrev-ref HEAD 2>/dev/null || echo main)}"
: "${VERIFY_GIT_ROOT:=test/cluster/sample-app}"
: "${VERIFY_APP_PORT:=8080}"
# Backups need somewhere to put them. Without these, phase 2 says so and skips
# rather than pretending.
: "${VERIFY_S3_ENDPOINT:=}"
: "${VERIFY_S3_BUCKET:=}"
: "${VERIFY_S3_ACCESS_KEY:=}"
: "${VERIFY_S3_SECRET_KEY:=}"
# A second server, for what one cannot show: an image built on one server and
# pulled on the other, the network policies between them, a server emptied and
# taken out. The panel adds it the way a person does, over SSH as root with the
# key in VERIFY_SECOND_SERVER_KEY, and every phase after that runs across both.
# Without it those checks skip and say so.
: "${VERIFY_SECOND_SERVER:=}"
: "${VERIFY_SECOND_SERVER_KEY:=}"
# A server that fails rather than being taken out. VERIFY_SECOND_SERVER_FAILS
# is a file holding the id of the process that is the second server, a
# virtual machine's; with it, that process is killed at the end, which to the
# cluster is a power cut, and the server is then removed the way somebody
# removes one that is gone, instead of being emptied and taken out.
: "${VERIFY_SECOND_SERVER_FAILS:=}"
# An ACME server, for a certificate: the free address is plain HTTP on purpose
# (ADR-0015), so a certificate needs a domain of the app's own, and nothing on
# the internet can reach a machine like a CI runner to validate one. An ACME
# server on the machine itself can, through the same ingress: Pebble, Let's
# Encrypt's own test server, at VERIFY_ACME_DIRECTORY, whose address is signed
# by the authority in VERIFY_ACME_CA, given to the panel the way an ACME server
# of one's own is. VERIFY_ACME_ADDRESS is this machine's address on the
# network the ACME server validates from, which the app's domain is made to
# resolve to; VERIFY_ACME_ROOT is where the server hands out the root it
# issues from. Without them the certificate check skips and says so.
: "${VERIFY_ACME_DIRECTORY:=}"
: "${VERIFY_ACME_CA:=}"
: "${VERIFY_ACME_ADDRESS:=}"
: "${VERIFY_ACME_ROOT:=}"
# Somewhere for notifications to arrive besides a webhook, each read back
# through its own API rather than taken on the panel's word. Email goes
# through the panel's Email settings to VERIFY_SMTP_SERVER, a host:port that
# takes mail without signing in, and is read back from the Mailpit at
# VERIFY_MAILPIT; a password reset goes the same way. ntfy is a server at
# VERIFY_NTFY_SERVER that refuses anybody without the token in
# VERIFY_NTFY_TOKEN. Gotify is a server at VERIFY_GOTIFY_SERVER, pushed to with
# the application token in VERIFY_GOTIFY_TOKEN and read with the client token
# in VERIFY_GOTIFY_CLIENT_TOKEN. Each one missing skips its own checks.
: "${VERIFY_SMTP_SERVER:=}"
: "${VERIFY_MAILPIT:=}"
: "${VERIFY_NTFY_SERVER:=}"
: "${VERIFY_NTFY_TOKEN:=}"
: "${VERIFY_GOTIFY_SERVER:=}"
: "${VERIFY_GOTIFY_TOKEN:=}"
: "${VERIFY_GOTIFY_CLIENT_TOKEN:=}"
# Mattermost is a server at VERIFY_MATTERMOST_API (its /api/v4), with an
# incoming webhook at VERIFY_MATTERMOST_HOOK that posts to the channel
# VERIFY_MATTERMOST_CHANNEL, read with the token in VERIFY_MATTERMOST_TOKEN.
: "${VERIFY_MATTERMOST_HOOK:=}"
: "${VERIFY_MATTERMOST_API:=}"
: "${VERIFY_MATTERMOST_TOKEN:=}"
: "${VERIFY_MATTERMOST_CHANNEL:=}"
# A Git host at a private address, as a Gitea at home is: VERIFY_GIT_HOST_REPO
# is an https:// repository on it holding the sample app at the root of its
# main branch, and VERIFY_GIT_HOST_CA the authority that signed the host's
# certificate. Without them the check skips.
: "${VERIFY_GIT_HOST_REPO:=}"
: "${VERIFY_GIT_HOST_CA:=}"
# VERIFY_GIT_HOST_TOKEN is a token of the repository's owner on that host, for
# the panel's connection to it and for the pushes and the pull request that
# check its webhooks. The host has to be a Gitea that delivers to loopback
# addresses ([security] ALLOWED_HOST_LIST = loopback; "*" is refused since
# Gitea 28), since the panel's address is pointed at this
# machine for it, and, unless the panel's certificate is one it trusts,
# without checking it (SKIP_TLS_VERIFY). Without the token those checks skip.
: "${VERIFY_GIT_HOST_TOKEN:=}"
# A Dex binary, for single sign-on through a real OpenID Connect provider:
# phase 3 writes its configuration and starts it on this machine's address.
# Without it the check skips.
: "${VERIFY_DEX:=}"
# A secret manager a variable is read from: a Vault, or an OpenBao, which
# speaks the same API, at VERIFY_VAULT_ADDRESS, with a KV version 2 mount at
# secret/ that the token in VERIFY_VAULT_TOKEN may write to. Without them the
# check skips.
: "${VERIFY_VAULT_ADDRESS:=}"
: "${VERIFY_VAULT_TOKEN:=}"

# An image with a shell, for what runs a command through one, and for an app
# with nothing on any server's disk: nginx as an unprivileged user, from
# GitHub's registry. The sample app's image has no shell.
SHELL_IMAGE="ghcr.io/nginxinc/nginx-unprivileged:1.27-alpine"

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
	die "set SKIFITY_IMAGE to a panel image: a release's, such as ghcr.io/skifity/skifity:v0.1.0, or one built from this checkout with 'make image' and pushed somewhere this server can pull from, or loaded into k3s with 'k3s ctr images import'."
fi
ok "the panel image to install is $SKIFITY_IMAGE"

MEM_MB=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
say "  this machine has ${MEM_MB} MB of memory"
# What the machine uses before anything is installed, so what the install adds
# can be told apart from what was already running on it.
BASE_USED_MB=$(free -m | awk '/^Mem:/ {print $3}')
# The README's minimum is 1 GB and its comfortable size 2 GB. Below the minimum
# is a machine the README says will not do; between the two is the run that
# finds out whether the minimum is true, which is worth having, not a failure.
if [ "$MEM_MB" -lt 900 ]; then
	no "under 1 GB, which is less than the minimum the README gives"
elif [ "$MEM_MB" -lt 1800 ]; then
	say "  that is the README's minimum rather than its comfortable 2 GB, so this run is the check of whether the minimum is true"
fi

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
# A forward started a moment after the panel is ready can fail to bind or find
# no endpoint yet and exit, which a run on 6c26148 met three seconds in; it is
# started again until the panel answers through it, for a minute and a half.
start_forward() {
	for _ in $(seq 1 12); do
		kubectl -n "$PANEL_NS" port-forward svc/skifity-panel 18080:80 >/dev/null 2>&1 &
		FORWARD=$!
		for _ in $(seq 1 10); do
			curl -fs -o /dev/null --max-time 2 http://127.0.0.1:18080/api/health && return 0
			kill -0 "$FORWARD" 2>/dev/null || break
			sleep 0.5
		done
		stop_forward
		sleep 2
	done
}
# Never `kill 0`: with no port-forward started, that is every process in this
# group, the CI runner's own step included.
stop_forward() {
	[ -z "${FORWARD:-}" ] || kill "$FORWARD" 2>/dev/null || true
	FORWARD=""
}
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

# api_any is api that hands back the answer whatever its status, for a step
# whose failure should say what the panel said.
api_any() {
	local method="$1" path="$2" body="${3:-}"
	curl -sS -X "$method" "$PANEL$path" -H 'Content-Type: application/json' \
		${TOKEN:+-H "Authorization: Bearer $TOKEN"} ${body:+-d "$body"}
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

# backup_field LIST ID FIELD reads one field of one backup in a list of them,
# found by its id rather than by its place in the list.
backup_field() {
	api GET "$1" 2>/dev/null | python3 -c '
import json, sys
for item in json.load(sys.stdin).get("items") or []:
    if item.get("id") == sys.argv[1]:
        print(item.get(sys.argv[2], ""))
' "$2" "$3" 2>/dev/null || true
}

# backup_ended LIST ID waits up to ten minutes for a backup to finish and
# prints how it ended; operation_ended ID does the same for a restore.
backup_ended() {
	local state=""
	for _ in $(seq 1 60); do
		state=$(backup_field "$1" "$2" status)
		case "$state" in succeeded | failed) break ;; esac
		sleep 10
	done
	printf '%s' "$state"
}
operation_ended() {
	local state=""
	for _ in $(seq 1 60); do
		state=$(api GET "/api/operations/$1" 2>/dev/null | pick status 2>/dev/null || echo "")
		case "$state" in succeeded | failed) break ;; esac
		sleep 10
	done
	printf '%s' "$state"
}

# read_through LIST ID asks the panel to read a backup through, as the Verify
# button does: downloaded from the bucket, its checksum compared, and opened
# when it is sealed. It prints nothing when that worked, and why not when not.
read_through() {
	local at=""
	api POST "$1/$2/verify" >/dev/null || { echo "the panel refused to read it"; return; }
	for _ in $(seq 1 60); do
		at=$(backup_field "$1" "$2" verified_at)
		[ -n "$at" ] && break
		sleep 5
	done
	if [ -z "$at" ]; then
		echo "the panel never finished reading it"
	else
		backup_field "$1" "$2" verify_error
	fi
}

# job_logs ID puts what a backup's or a restore's job said into the report.
job_logs() {
	kubectl -n "$NAMESPACE" logs -l "skifity.com/backup-id=$1" --all-containers --tail=40 >>"$REPORT" 2>&1 || true
}

# engine_client ENGINE STATEMENT runs one statement through an engine's own
# client, inside the database's own pod, as the app's user where the engine
# has one, and prints the answer: what is checked is the data, not anything
# the panel says about it. The statement travels as a variable and the
# password never leaves the pod, where the single quotes below are expanded.
# shellcheck disable=SC2016
engine_client() {
	local client=""
	case "$1" in
	mysql) client='MYSQL_PWD="$MYSQL_PASSWORD" mysql -N -B -u"$MYSQL_USER" "$MYSQL_DATABASE" -e "$STATEMENT"' ;;
	mariadb) client='MYSQL_PWD="$MARIADB_PASSWORD" mariadb -N -B -u"$MARIADB_USER" "$MARIADB_DATABASE" -e "$STATEMENT"' ;;
	mongodb) client='mongosh --quiet -u "$MONGO_INITDB_ROOT_USERNAME" -p "$MONGO_INITDB_ROOT_PASSWORD" --authenticationDatabase admin "$MONGO_INITDB_DATABASE" --eval "$STATEMENT"' ;;
	redis) client='REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli $STATEMENT' ;;
	valkey) client='REDISCLI_AUTH="$VALKEY_PASSWORD" valkey-cli $STATEMENT' ;;
	esac
	kubectl -n "$NAMESPACE" exec "verify-$1-0" -- env STATEMENT="$2" sh -c "$client" 2>>"$REPORT"
}

# engine_set ENGINE VALUE puts VALUE in the database, in a table, a
# collection or a key; engine_get ENGINE reads it back.
engine_set() {
	case "$1" in
	mysql | mariadb) engine_client "$1" "create table if not exists verify_restore (value varchar(32)); delete from verify_restore; insert into verify_restore values ('$2')" ;;
	mongodb) engine_client "$1" "db.verify_restore.deleteMany({}); db.verify_restore.insertOne({value: '$2'}); print('stored')" ;;
	redis | valkey) engine_client "$1" "set verify_restore $2" ;;
	esac
}
engine_get() {
	case "$1" in
	mysql | mariadb) engine_client "$1" "select value from verify_restore" ;;
	mongodb) engine_client "$1" "print((db.verify_restore.findOne() || {}).value)" ;;
	redis | valkey) engine_client "$1" "get verify_restore" ;;
	esac | tr -d '[:space:]'
}

# mail_text ADDRESS SUBJECT prints the text of the newest mail the Mailpit at
# VERIFY_MAILPIT holds for ADDRESS with SUBJECT in its subject, and nothing
# while it holds none; mail_has says whether there is one.
mail_text() {
	local id
	id=$(curl -fsS "$VERIFY_MAILPIT/api/v1/messages?limit=200" 2>/dev/null | python3 -c '
import json, sys
for message in json.load(sys.stdin).get("messages") or []:
    to = [address.get("Address") for address in message.get("To") or []]
    if sys.argv[1] in to and sys.argv[2] in message.get("Subject", ""):
        print(message["ID"])
        break
' "$1" "$2" 2>/dev/null || true)
	[ -n "$id" ] || return 0
	curl -fsS "$VERIFY_MAILPIT/api/v1/message/$id" 2>/dev/null | pick Text 2>/dev/null || true
}
mail_has() { [ -n "$(mail_text "$1" "$2")" ]; }

# wait_deployed APP waits up to five minutes for an app's newest deployment to
# end, and says whether it succeeded.
wait_deployed() {
	local state=""
	for _ in $(seq 1 60); do
		state=$(api GET "/api/apps/$1/deployments" 2>/dev/null | pick items.0.status 2>/dev/null || echo "")
		case "$state" in
		succeeded) return 0 ;;
		failed | cancelled | superseded) return 1 ;;
		esac
		sleep 5
	done
	return 1
}

# panel_public_url prints the address the installer gave the panel, and points
# its name at this machine for what has to reach the panel by it — a Git
# host's webhook, a browser back from an identity provider — since the name
# resolves to a public address nothing on this machine can reach.
panel_public_url() {
	local url name
	url=$(kubectl -n "$PANEL_NS" get deploy skifity-panel \
		-o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SKIFITY_PUBLIC_URL")].value}' 2>/dev/null || true)
	name=$(python3 -c 'import sys, urllib.parse; print(urllib.parse.urlparse(sys.argv[1]).hostname or "")' "$url")
	[ -z "$name" ] || grep -q " $name\$" /etc/hosts || printf '127.0.0.1 %s\n' "$name" >>/etc/hosts
	printf '%s' "${url%/}"
}

# volume_node CLAIM prints the server a volume on a server's own disk is kept
# on, for a claim in the environment's namespace, and nothing for any other.
volume_node() {
	kubectl get pv -o jsonpath='{range .items[*]}{.spec.claimRef.namespace}{" "}{.spec.claimRef.name}{" "}{.spec.nodeAffinity.required.nodeSelectorTerms[0].matchExpressions[0].values[0]}{"\n"}{end}' \
		2>/dev/null | awk -v ns="$NAMESPACE" -v claim="$1" '$1 == ns && $2 == claim {print $3}' || true
}

# server_usage_known SERVER says whether the panel has the server's memory,
# processor and disk, which it reads every minute from the server's kubelet.
server_usage_known() {
	api GET "/api/servers/$1/usage?range=1h" 2>/dev/null | python3 -c '
import json, sys
usage = json.load(sys.stdin)
disk = usage.get("disk") or {}
points = [p for p in usage.get("points") or []
          if None not in (p.get("cpu_pct"), p.get("memory_pct"), p.get("disk_pct"))]
sys.exit(0 if points and 0 < disk.get("used_mb", 0) < disk.get("capacity_mb", 0) else 1)
'
}

# ntfy_has TOPIC TITLE, gotify_has TITLE and mattermost_has TITLE say whether
# the server holds a message with that title, read with the server's own
# credentials: what is checked is what arrived, not that the panel said it sent
# something.
ntfy_has() {
	curl -fsS -H "Authorization: Bearer $VERIFY_NTFY_TOKEN" "$VERIFY_NTFY_SERVER/$1/json?poll=1" 2>/dev/null |
		python3 -c '
import json, sys
titles = [json.loads(line).get("title") for line in sys.stdin if line.strip()]
sys.exit(0 if sys.argv[1] in titles else 1)
' "$2"
}
gotify_has() {
	curl -fsS -H "X-Gotify-Key: $VERIFY_GOTIFY_CLIENT_TOKEN" "$VERIFY_GOTIFY_SERVER/message?limit=200" 2>/dev/null |
		python3 -c '
import json, sys
titles = [message.get("title") for message in json.load(sys.stdin).get("messages") or []]
sys.exit(0 if sys.argv[1] in titles else 1)
' "$1"
}
mattermost_has() {
	curl -fsS -H "Authorization: Bearer $VERIFY_MATTERMOST_TOKEN" \
		"$VERIFY_MATTERMOST_API/channels/$VERIFY_MATTERMOST_CHANNEL/posts?per_page=200" 2>/dev/null |
		python3 -c '
import json, sys
posts = (json.load(sys.stdin).get("posts") or {}).values()
titles = [a.get("title") for p in posts for a in (p.get("props") or {}).get("attachments") or []]
sys.exit(0 if sys.argv[1] in titles else 1)
' "$1"
}

api GET /api/health >/dev/null || die "the panel's own health endpoint does not answer"
ok "/api/health answers"

# A fresh install, before anything is deployed or turned on: k3s, its own
# components, cert-manager and the panel. The end of the run measures the same
# again with everything installed. Half a minute first, so the install's own
# jobs have finished rather than being counted.
sleep 30
FRESH_K3S_MB=$(ps -o rss= -C k3s-server 2>/dev/null | awk '{s+=$1} END {print int(s/1024)}')
FRESH_USED_MB=$(free -m | awk '/^Mem:/ {print $3}')

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

SECOND_ID=""
SECOND_NODE=""
if [ -n "$VERIFY_SECOND_SERVER" ]; then
	step "A second server, added through the panel"

	[ -r "$VERIFY_SECOND_SERVER_KEY" ] || die "VERIFY_SECOND_SERVER_KEY is not a key file this can read"
	ADD=$(python3 -c '
import json, sys
print(json.dumps({"name": "second", "host": sys.argv[1], "ssh_user": "root", "ssh_port": 22,
                  "private_key": open(sys.argv[2]).read()}))
' "$VERIFY_SECOND_SERVER" "$VERIFY_SECOND_SERVER_KEY")
	SECOND_ID=$(api POST "/api/teams/$TEAM_ID/servers" "$ADD" | pick target_id 2>/dev/null || echo "")
	[ -n "$SECOND_ID" ] || die "the panel refused to add $VERIFY_SECOND_SERVER"
	SECOND_STATE=""
	for _ in $(seq 1 90); do
		SECOND_STATE=$(api GET "/api/servers/$SECOND_ID" | pick status 2>/dev/null || echo "")
		case "$SECOND_STATE" in ready | failed) break ;; esac
		sleep 10
	done
	if [ "$SECOND_STATE" != "ready" ]; then
		api GET "/api/servers/$SECOND_ID" >>"$REPORT" 2>&1 || true
		die "adding $VERIFY_SECOND_SERVER ended '${SECOND_STATE:-unknown}'; the operation's steps are above in $REPORT"
	fi
	ok "$VERIFY_SECOND_SERVER was added over SSH: checked, keyed, fenced and joined"
	SECOND_NODE=$(api GET "/api/servers/$SECOND_ID" | pick node_name 2>/dev/null || echo "")
	READY_NODES=$(kubectl get nodes --no-headers 2>/dev/null | awk '$2 == "Ready"' | wc -l)
	[ "$READY_NODES" -ge 2 ] \
		&& ok "the cluster has $READY_NODES servers, all ready, the new one as ${SECOND_NODE:-unknown}" \
		|| no "only $READY_NODES server(s) are ready after the second joined"
fi

##############################################################################
# Phase 1 — the basic path: Git, build, deploy, HTTPS, a variable, a rollback
##############################################################################

APP_ID=""
APP_HOST=""
# An app with nothing on any server's disk and an instance on each server,
# made in phase 4 for what a server going away should not be noticed by.
MOVER_HOST=""

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
	# The end of the build's own log, here as well as in the report: it is the
	# one thing that says why, and a run that dies here leaves nothing else.
	api GET "/api/apps/$APP_ID/deployments/$DEPLOY_ID/logs" 2>/dev/null | python3 -c '
import json, sys
for line in json.load(sys.stdin).get("lines", [])[-40:]:
    print("    " + line.get("line", ""))
' 2>/dev/null | tee -a "$REPORT" || true
	kubectl -n skifity-builds get pods -o wide 2>/dev/null | tee -a "$REPORT" || true
	die "the deployment ended as '${STATE:-unknown}'; the end of its log is above. This is checklist item 1, and nothing after it means much."
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
	elif [ -z "$VERIFY_ACME_DIRECTORY" ]; then
		skip "the HTTPS certificate" "the free address is on sslip.io, which is served over plain HTTP on purpose (ADR-0015). Set SKIFITY_DOMAIN to a domain you own, or VERIFY_ACME_DIRECTORY to an ACME server of your own, to check this."
	fi
fi

if [ -n "$APP_HOST" ] && [ -n "$VERIFY_ACME_DIRECTORY" ]; then
	step "A certificate for a domain of the app's own (checklist 3)"

	# The panel is pointed at the ACME server the way a person points it at
	# one of their own: the directory, and the authority its address is signed
	# by, under Settings. Saving them has to reach the issuer.
	ACME_SETTINGS=$(python3 -c '
import json, sys
print(json.dumps({"values": {"domains.acme_server": sys.argv[1], "domains.acme_ca_bundle": open(sys.argv[2]).read()}}))
' "$VERIFY_ACME_DIRECTORY" "$VERIFY_ACME_CA")
	api PUT /api/settings "$ACME_SETTINGS" >/dev/null || no "the ACME settings were refused"
	ISSUER_SERVER=$(kubectl get clusterissuer skifity-letsencrypt -o jsonpath='{.spec.acme.server}' 2>/dev/null || echo "")
	[ "$ISSUER_SERVER" = "$VERIFY_ACME_DIRECTORY" ] \
		&& ok "saving the ACME server in Settings changed the issuer the certificates come from" \
		|| no "the issuer still asks '${ISSUER_SERVER:-nothing}' after the panel was given $VERIFY_ACME_DIRECTORY"

	# A domain of the app's own that resolves to this machine for anybody
	# who asks, the ACME server included, and that is not on the free
	# address's name.
	SECURE_HOST="secure-hello.$(printf '%s' "$VERIFY_ACME_ADDRESS" | tr . -).sslip.io"
	api POST "/api/apps/$APP_ID/domains" "{\"hostname\":\"$SECURE_HOST\",\"tls\":true}" >/dev/null \
		|| no "$SECURE_HOST could not be added to the app"
	ISSUED=""
	for _ in $(seq 1 60); do
		[ "$(kubectl -n "$NAMESPACE" get certificate hello-tls -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)" = "True" ] \
			&& { ISSUED=1; break; }
		sleep 5
	done
	if [ -z "$ISSUED" ]; then
		no "no certificate for $SECURE_HOST after five minutes; what cert-manager said is below"
		{
			kubectl -n "$NAMESPACE" describe certificate,certificaterequest,order,challenge 2>&1 | tail -60
			kubectl -n "$NAMESPACE" get ingress,pods -o wide 2>&1
		} >>"$REPORT"
	else
		ok "cert-manager was issued a certificate for $SECURE_HOST, validated over HTTP through the ingress"
		ROOT_PEM=$(mktemp)
		curl -fsS --cacert "$VERIFY_ACME_CA" "$VERIFY_ACME_ROOT" -o "$ROOT_PEM" 2>>"$REPORT" || true
		# The certificate is Ready once its secret is written; Traefik reads
		# the secret a moment later, and until then serves its own. The run
		# on 11cfbfb asked within the same 20 ms and got that one.
		CODE=""
		for _ in $(seq 1 12); do
			CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 --cacert "$ROOT_PEM" \
				--resolve "$SECURE_HOST:443:127.0.0.1" "https://$SECURE_HOST/" 2>>"$REPORT" || true)
			[ "$CODE" = "200" ] && break
			sleep 5
		done
		[ "$CODE" = "200" ] \
			&& ok "https://$SECURE_HOST answers with that certificate, trusted from the ACME server's own root" \
			|| no "https://$SECURE_HOST answered '$CODE' when checked against the ACME server's root; the ingress is not serving the issued certificate"
		rm -f "$ROOT_PEM"
	fi
	REDIRECT=$(curl -s -o /dev/null -w '%{http_code} %{redirect_url}' --max-time 15 \
		--resolve "$SECURE_HOST:80:127.0.0.1" "http://$SECURE_HOST/path?q=1" 2>/dev/null || true)
	case "$REDIRECT" in
	30[18]" https://$SECURE_HOST/path?q=1") ok "plain HTTP on that domain is sent to HTTPS, path and all" ;;
	*) no "plain HTTP on $SECURE_HOST answered '$REDIRECT', not a redirect to https://$SECURE_HOST/path?q=1" ;;
	esac
	CODE=$(through_ingress "$APP_HOST" 15)
	[ "$CODE" = "200" ] \
		&& ok "the free address still answers over plain HTTP, and is not sent to an HTTPS it has no certificate for" \
		|| no "the free address answered $CODE once the app had a domain with a certificate; it is plain HTTP on purpose and must stay that way"
	if [ -n "$ISSUED" ]; then
		STATUS=""
		for _ in $(seq 1 18); do
			STATUS=$(api GET "/api/apps/$APP_ID/domains" | python3 -c '
import json, sys
for domain in json.load(sys.stdin).get("items", []):
    if domain.get("hostname") == sys.argv[1]:
        print(domain.get("status", ""))
' "$SECURE_HOST" 2>/dev/null || echo "")
			[ "$STATUS" = "active" ] && break
			sleep 10
		done
		[ "$STATUS" = "active" ] \
			&& ok "and the panel shows the domain as active" \
			|| no "the certificate was issued, but the panel shows the domain as '${STATUS:-nothing}'"
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

step "A variable read from a secret manager"

# The variable holds where the value is, not the value: the panel reads it
# when the app is rolled out, and a value changed in the manager reaches the
# app when its variables are refreshed, without a build. The next step sets
# VERSION to a value again, which drops the reference.
if [ -z "$VERIFY_VAULT_ADDRESS" ] || [ -z "$VERIFY_VAULT_TOKEN" ]; then
	skip "a variable read from a secret manager" "no VERIFY_VAULT_ADDRESS and VERIFY_VAULT_TOKEN"
else
	vault_put() {
		curl -fsS -o /dev/null -X POST "$VERIFY_VAULT_ADDRESS/v1/secret/data/verify/hello" \
			-H "X-Vault-Token: $VERIFY_VAULT_TOKEN" -H 'Content-Type: application/json' \
			-d "{\"data\":{\"version\":\"$1\"}}"
	}
	serving() {
		for _ in $(seq 1 36); do
			BODY=$(curl -s --max-time 10 -H "Host: $APP_HOST" "http://127.0.0.1/" || true)
			case "$BODY" in *"version=$1"*) return 0 ;; esac
			sleep 5
		done
		return 1
	}
	FIRST_SECRET="bao-first-$(date +%s)"
	SECOND_SECRET="bao-second-$(date +%s)"
	vault_put "$FIRST_SECRET" || no "the secret manager at $VERIFY_VAULT_ADDRESS would not take a secret to read back"
	CONNECTED=$(curl -sS -X POST "$PANEL/api/teams/$TEAM_ID/secret-managers" -H 'Content-Type: application/json' \
		-H "Authorization: Bearer $TOKEN" \
		-d "{\"name\":\"verify-bao\",\"kind\":\"vault\",\"settings\":{\"address\":\"$VERIFY_VAULT_ADDRESS\"},\"credentials\":{\"token\":\"$VERIFY_VAULT_TOKEN\"}}" || true)
	MANAGER=$(printf '%s' "$CONNECTED" | pick id 2>/dev/null || echo "")
	if [ -z "$MANAGER" ]; then
		no "connecting the secret manager at $VERIFY_VAULT_ADDRESS was refused: ${CONNECTED:-no answer}"
	else
		ok "the panel signed in to the secret manager before keeping the connection"
		IMAGE_BEFORE=$(api GET "/api/apps/$APP_ID/deployments" | pick items.0.image 2>/dev/null || echo "")
		SET=$(curl -sS -X PUT "$PANEL/api/apps/$APP_ID/variables" -H 'Content-Type: application/json' \
			-H "Authorization: Bearer $TOKEN" \
			-d '{"key":"VERSION","from":{"connection":"verify-bao","path":"verify/hello","key":"version"}}' || true)
		if ! printf '%s' "$SET" | pick variable.key >/dev/null 2>&1; then
			no "VERSION could not be read from the secret manager: ${SET:-no answer}"
		elif serving "$FIRST_SECRET"; then
			ok "the app runs with the value the secret manager holds"
		else
			no "the app answers '${BODY:-nothing}', not the value in the secret manager"
		fi
		vault_put "$SECOND_SECRET" || no "the secret manager would not take the changed secret"
		REFRESHED=$(api POST "/api/apps/$APP_ID/variables/refresh" || echo "")
		CHANGED=$(printf '%s' "$REFRESHED" | pick changed 2>/dev/null || echo "")
		case "$CHANGED" in
			*VERSION*) ok "a refresh found the value changed in the secret manager" ;;
			*) no "a refresh after the secret changed answered '${REFRESHED:-nothing}', without VERSION among what changed" ;;
		esac
		serving "$SECOND_SECRET" \
			&& ok "and rolled the app out with it" \
			|| no "after the refresh the app answers '${BODY:-nothing}', not the changed value"
		[ "$(api GET "/api/apps/$APP_ID/deployments" | pick items.0.image)" = "$IMAGE_BEFORE" ] \
			&& ok "with the image it had: a value from a secret manager is configuration, and builds nothing" \
			|| no "reading a variable from a secret manager produced a new image"
		LISTED=$(api GET "/api/apps/$APP_ID/variables" || echo "")
		case "$LISTED" in
			*"$FIRST_SECRET"* | *"$SECOND_SECRET"*) no "the app's variables, as the API lists them, show the value read from the secret manager" ;;
			*verify-bao*) ok "the app's variables say where VERSION is read from, and never what it is" ;;
			*) no "the app's variables do not say VERSION is read from the secret manager: $LISTED" ;;
		esac
		LEAKED=$(kubectl -n "$PANEL_NS" logs deploy/skifity-panel --tail=2000 2>/dev/null |
			grep -c -e "$FIRST_SECRET" -e "$SECOND_SECRET" -e "$VERIFY_VAULT_TOKEN" || true)
		[ "${LEAKED:-0}" = "0" ] \
			&& ok "the panel's own log holds neither the values it read nor the token it read them with" \
			|| no "the panel logged a value it read from the secret manager, or the token, $LEAKED time(s)"
	fi
fi

step "Deploying again, and rolling back (checklist 8)"

# What a rollback promises (docs/concepts.md, Deployments and rollback): the old
# version's image and the settings it ran with — instances, resources, port,
# health check, start command — and not its variables, which may be a key
# rotated since. The second deployment runs two instances and VERSION=3; going
# back to the first has to bring back one instance and leave VERSION alone.
#
# A variable and a size are applied without a deployment of their own (ADR-0007),
# which is why this was skipped on every run until it made a second one itself:
# a deploy of the same commit, which builds nothing.
api PUT "/api/apps/$APP_ID/variables" '{"key":"VERSION","value":"3"}' >/dev/null
api PUT "/api/apps/$APP_ID/scaling" '{"replicas":2,"autoscale":false}' >/dev/null
SECOND=$(api POST "/api/apps/$APP_ID/deploy" | pick id 2>/dev/null || echo "")
for _ in $(seq 1 60); do
	STATE=$(api GET "/api/apps/$APP_ID/deployments/$SECOND" | pick status 2>/dev/null || echo "")
	case "$STATE" in succeeded | failed) break ;; esac
	sleep 5
done
[ "$STATE" = "succeeded" ] || no "deploying the same commit again ended '${STATE:-unknown}'"
FIRST=$(api GET "/api/apps/$APP_ID/deployments" | pick items.1.id 2>/dev/null || echo "")
if [ -z "$FIRST" ]; then
	skip "the rollback" "there is only one deployment to roll back to"
else
	ROLLBACK=$(api POST "/api/apps/$APP_ID/rollback/$FIRST" | pick id 2>/dev/null || echo "")
	[ -n "$ROLLBACK" ] || no "the rollback was refused"
	for _ in $(seq 1 60); do
		STATE=$(api GET "/api/apps/$APP_ID/deployments/$ROLLBACK" | pick status 2>/dev/null || echo "")
		case "$STATE" in succeeded | failed) break ;; esac
		sleep 5
	done
	SIZE=$(kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "?")
	BODY=$(curl -s --max-time 10 -H "Host: $APP_HOST" "http://127.0.0.1/" || true)
	if [ "$STATE" = "succeeded" ] && [ "$SIZE" = "1" ]; then
		ok "one call rolled the app back to the version before, with the one instance it ran"
	else
		no "a rollback ended '${STATE:-unknown}' with ${SIZE} instance(s); the version it went back to ran one"
	fi
	case "$BODY" in
		*version=3*) ok "the rollback left the variables as they are now, as it says it will" ;;
		*) no "after a rollback the app answers '${BODY:-nothing}', not the VERSION it was last given; a rollback is not meant to take variables back" ;;
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

step "Switching autoscaling on keeps the instances there are"

# Phase 111 found Kubernetes dropping an app to one instance for a moment when
# autoscaling was switched on: the panel stops writing the size, and a field
# nobody owns defaults to one. The size is sampled while it is switched on, and
# the smallest it ever was is the answer.
(
	for _ in $(seq 1 60); do
		kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.spec.replicas}' 2>/dev/null || printf '?'
		echo
		sleep 0.5
	done
) >/tmp/skifity-autoscale-sizes.txt &
SAMPLER=$!
sleep 2
api PUT "/api/apps/$APP_ID/scaling" '{"autoscale":true,"min_replicas":2,"max_replicas":4,"cpu_target":80}' >/dev/null \
	|| no "autoscaling could not be switched on"
wait "$SAMPLER" || true
SMALLEST=$(grep -E '^[0-9]+$' /tmp/skifity-autoscale-sizes.txt | sort -n | head -1)
if [ "${SMALLEST:-0}" -ge 2 ] 2>/dev/null; then
	ok "the app kept its two instances while autoscaling took over (the smallest it was is $SMALLEST)"
else
	no "the app went down to ${SMALLEST:-nothing} instances when autoscaling was switched on. Sizes seen: $(sort /tmp/skifity-autoscale-sizes.txt | uniq -c | tr '\n' ' ')"
fi
if kubectl -n "$NAMESPACE" get hpa hello-hpa >/dev/null 2>&1; then
	ok "a HorizontalPodAutoscaler owns the app's size"
	CPU_NOW=""
	for _ in $(seq 1 24); do
		CPU_NOW=$(kubectl -n "$NAMESPACE" get hpa hello-hpa -o jsonpath='{.status.currentMetrics[0].resource.current.averageUtilization}' 2>/dev/null || true)
		[ -n "$CPU_NOW" ] && break
		sleep 5
	done
	[ -n "$CPU_NOW" ] \
		&& ok "the autoscaler reads the app's CPU use ($CPU_NOW% of what it asked for)" \
		|| no "the autoscaler has no reading of the app's CPU after two minutes; metrics-server is what supplies it"
else
	no "autoscaling was switched on and no HorizontalPodAutoscaler exists"
fi
# Back to a fixed size, so what follows starts from a known one.
api PUT "/api/apps/$APP_ID/scaling" '{"replicas":2,"autoscale":false}' >/dev/null
for _ in $(seq 1 30); do
	[ "$(kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.spec.replicas}' 2>/dev/null)" = "2" ] \
		&& ! kubectl -n "$NAMESPACE" get hpa hello-hpa >/dev/null 2>&1 && break
	sleep 2
done
kubectl -n "$NAMESPACE" get hpa hello-hpa >/dev/null 2>&1 \
	&& no "the autoscaler was left behind after autoscaling was switched off" \
	|| ok "switching autoscaling off removes the autoscaler and leaves the size the panel's"

if [ -n "$SECOND_NODE" ]; then
	step "One app on two servers"

	# The image was built into the registry on the first server. An instance
	# on the second pulls it through that server's own registry port, which
	# kube-proxy carries across to the registry, and the registry's policy has
	# to let a pull from another server in: none of that runs on one server.
	kubectl -n "$NAMESPACE" rollout status deploy/hello --timeout=300s >>"$REPORT" 2>&1 || true
	ON_SECOND=$(kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/name=hello \
		-o jsonpath='{range .items[*]}{.spec.nodeName}{" "}{.status.containerStatuses[0].ready}{"\n"}{end}' 2>/dev/null \
		| awk -v node="$SECOND_NODE" '$1 == node && $2 == "true"' | wc -l)
	if [ "${ON_SECOND:-0}" -ge 1 ]; then
		ok "an instance runs on the second server, from the image built on the first"
	else
		no "no instance of the app is ready on the second server; the spread put both elsewhere, or the pull failed"
		kubectl -n "$NAMESPACE" get pods -o wide >>"$REPORT" 2>&1 || true
	fi
	SECOND_CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -H "Host: $APP_HOST" "http://$VERIFY_SECOND_SERVER/")
	[ "$SECOND_CODE" = "200" ] \
		&& ok "the second server answers for the app too, at its own address" \
		|| no "the app's address answered $SECOND_CODE at the second server; every server runs the ingress"
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

step "A repository on a Git host on this machine's network"

# A Gitea at home or a GitLab on the office network is at an address builds are
# kept off, and was a repository no build could clone: the clone waited two
# minutes for a connection the policy dropped and gave up with a timeout that
# named nothing. The build is refused before it starts now, saying what an
# administrator can do; with the host listed and its authority given under
# Settings, the same build clones it and deploys.
if [ -z "$VERIFY_GIT_HOST_REPO" ] || [ -z "$VERIFY_GIT_HOST_CA" ]; then
	skip "a Git host on this network" "none was given. Set VERIFY_GIT_HOST_REPO to a repository with the sample app on a host at a private address, and VERIFY_GIT_HOST_CA to the authority that signed its certificate"
else
	HOME_APP=$(api POST "/api/environments/$ENV_ID/apps" "$(cat <<JSON
{"name":"homegit","source_type":"git","repo_url":"$VERIFY_GIT_HOST_REPO","branch":"main","builder":"dockerfile",
 "dockerfile_path":"Dockerfile","port":$VERIFY_APP_PORT,"health_path":"/healthz","deploy":true}
JSON
)" | pick app.id 2>/dev/null || echo "")
	if [ -z "$HOME_APP" ]; then
		no "an app could not be made from $VERIFY_GIT_HOST_REPO"
	else
		wait_deployed "$HOME_APP" || true
		REFUSED=$(api GET "/api/apps/$HOME_APP/deployments" | pick items.0.error_code 2>/dev/null || echo "")
		[ "$REFUSED" = "build.git_host_private" ] \
			&& ok "a build of a repository on a host at a private address was refused before it started, saying who can allow it" \
			|| no "the build from a private Git host nobody listed ended with '${REFUSED:-no reason}', not build.git_host_private"
		HOST_PORT=$(python3 -c 'import sys, urllib.parse; print(urllib.parse.urlparse(sys.argv[1]).netloc)' "$VERIFY_GIT_HOST_REPO")
		ALLOW=$(python3 -c '
import json, sys
print(json.dumps({"values": {"git.private_hosts": sys.argv[1], "git.ca_bundle": open(sys.argv[2]).read()}}))
' "$HOST_PORT" "$VERIFY_GIT_HOST_CA")
		if ! api PUT /api/settings "$ALLOW" >/dev/null; then
			no "the Git host and its authority were refused under Settings"
		else
			api POST "/api/apps/$HOME_APP/deploy" '{}' >/dev/null 2>&1 || true
			if wait_deployed "$HOME_APP"; then
				ok "with $HOST_PORT listed and its authority given, the same build cloned it and deployed"
				HOME_HOST=$(api GET "/api/apps/$HOME_APP/domains" | pick items.0.hostname 2>/dev/null || echo "")
				[ -n "$HOME_HOST" ] && [ "$(through_ingress "$HOME_HOST" 30)" = "200" ] \
					&& ok "the app built from the Git host on this network answers" \
					|| no "the app built from the Git host on this network does not answer at ${HOME_HOST:-no address}"
				LEFT=$(kubectl -n skifity-builds get networkpolicy -o name 2>/dev/null | grep -c -- '-source$' || true)
				[ "${LEFT:-0}" = "0" ] \
					&& ok "the build's way to the Git host was closed once it finished" \
					|| no "$LEFT policies letting builds reach a Git host were left after the build"
			else
				LAST=$(api GET "/api/apps/$HOME_APP/deployments" 2>/dev/null | pick items.0.id 2>/dev/null || echo "")
				[ -z "$LAST" ] || api GET "/api/apps/$HOME_APP/deployments/$LAST/logs" >>"$REPORT" 2>&1 || true
				no "with the host listed and its authority given, the build still failed; its log is in the report"
			fi
		fi
		api DELETE "/api/apps/$HOME_APP" >/dev/null 2>&1 || true
	fi
fi
fi

step "A push to that Git host deploys, and a pull request on it is previewed"

# The Git host tells the panel about a push and a pull request with a webhook
# the panel registered on it through the connection's token, signed with the
# connection's own secret. The panel's address is a name that resolves to
# this machine's public address, which nothing on this machine can reach, so
# for the Git host the name is pointed at this machine itself.
if [ -z "$VERIFY_GIT_HOST_REPO" ] || [ -z "$VERIFY_GIT_HOST_TOKEN" ] || [ -z "$VERIFY_GIT_HOST_CA" ]; then
	skip "webhooks from a Git host" "no VERIFY_GIT_HOST_TOKEN for the Git host above"
else
	GITEA_BASE=$(python3 -c 'import sys, urllib.parse; u = urllib.parse.urlparse(sys.argv[1]); print(f"{u.scheme}://{u.netloc}")' "$VERIFY_GIT_HOST_REPO")
	GITEA_REPO=$(python3 -c 'import sys, urllib.parse; print(urllib.parse.urlparse(sys.argv[1]).path.strip("/").removesuffix(".git"))' "$VERIFY_GIT_HOST_REPO")
	gitea() {
		curl -fsS --cacert "$VERIFY_GIT_HOST_CA" -X "$1" "$GITEA_BASE/api/v1$2" \
			-H "Authorization: token $VERIFY_GIT_HOST_TOKEN" -H 'Content-Type: application/json' ${3:+-d "$3"}
	}
	# commit_to BRANCH FILE pushes a commit adding FILE to BRANCH, made from
	# main, and prints its id.
	commit_to() {
		local work
		work=$(mktemp -d)
		GIT_SSL_CAINFO="$VERIFY_GIT_HOST_CA" git clone -q -b main \
			"${GITEA_BASE%%://*}://${GITEA_REPO%%/*}:$VERIFY_GIT_HOST_TOKEN@${GITEA_BASE#*://}/$GITEA_REPO.git" "$work" >/dev/null 2>&1 || return 1
		date >"$work/$2"
		git -C "$work" add "$2" &&
			git -C "$work" -c user.name=Crosscheck -c user.email=crosscheck@example.test commit -q -m "Add $2" &&
			GIT_SSL_CAINFO="$VERIFY_GIT_HOST_CA" git -C "$work" push -q origin "HEAD:refs/heads/$1" >/dev/null 2>&1 &&
			git -C "$work" rev-parse HEAD
		rm -rf "$work"
	}
	PANEL_URL=$(panel_public_url)
	CONNECTED=$(api_any POST "/api/teams/$TEAM_ID/git-sources" \
		"{\"kind\":\"gitea\",\"name\":\"home-gitea\",\"base_url\":\"$GITEA_BASE\",\"token\":\"$VERIFY_GIT_HOST_TOKEN\"}" || true)
	SOURCE=$(printf '%s' "$CONNECTED" | pick source.id 2>/dev/null || echo "")
	CREATED=""
	[ -z "$SOURCE" ] || CREATED=$(api_any POST "/api/environments/$ENV_ID/apps" "$(cat <<JSON
{"name":"fromgitea","source_type":"git","git_source_id":"$SOURCE","repo_url":"$VERIFY_GIT_HOST_REPO","branch":"main",
 "builder":"dockerfile","dockerfile_path":"Dockerfile","port":$VERIFY_APP_PORT,"health_path":"/healthz","deploy":true}
JSON
)" || true)
	PUSHED_APP=$(printf '%s' "$CREATED" | pick app.id 2>/dev/null || echo "")
	if [ -z "$SOURCE" ]; then
		no "the panel would not connect to the Git host: $(printf '%s' "${CONNECTED:-no answer}" | head -c 300)"
	elif [ -z "$PANEL_URL" ] || [ -z "$PUSHED_APP" ]; then
		no "an app could not be made through a connection to the Git host: $(printf '%s' "${CREATED:-no answer}" | head -c 300)"
	else
		[ "$(printf '%s' "$CREATED" | pick webhook.registered 2>/dev/null)" = "True" ] \
			&& ok "making the app registered a webhook on the Git host through the connection's token" \
			|| no "no webhook was registered on the Git host: $(printf '%s' "$CREATED" | pick webhook 2>/dev/null)"
		wait_deployed "$PUSHED_APP" || no "the app from the Git host did not deploy the first time"
		PUSHED=$(commit_to main pushed.txt || echo "")
		DEPLOYED=""
		for _ in $(seq 1 60); do
			DEPLOYED=$(api GET "/api/apps/$PUSHED_APP/deployments" 2>/dev/null | python3 -c '
import json, sys
for d in json.load(sys.stdin).get("items") or []:
    if d.get("commit_sha") == sys.argv[1]:
        print(d.get("status", ""), d.get("trigger", ""))
        break
' "$PUSHED" 2>/dev/null || echo "")
			case "$DEPLOYED" in succeeded* | failed*) break ;; esac
			sleep 5
		done
		case "$DEPLOYED" in
		"succeeded push") ok "a push to the Git host reached the panel by its webhook, and the commit pushed was built and deployed" ;;
		*) no "after a push of ${PUSHED:-nothing} the app's deployment of it is '${DEPLOYED:-not there}'; the Git host's delivery log has what the panel answered" ;;
		esac

		api PATCH "/api/apps/$PUSHED_APP" '{"preview_deploys":true}' >/dev/null || no "previews could not be switched on"
		BRANCHED=$(commit_to change-for-a-preview preview.txt || echo "")
		PULL=$(gitea POST "/repos/$GITEA_REPO/pulls" \
			'{"head":"change-for-a-preview","base":"main","title":"A change to preview"}' 2>/dev/null | pick number 2>/dev/null || echo "")
		# A pull request's preview is named for its number, pr-<number>-…
		PREVIEW_ENV=""
		for _ in $(seq 1 60); do
			[ -n "$PULL" ] || break
			PREVIEW_ENV=$(api GET "/api/projects/$PROJECT_ID/environments" 2>/dev/null | python3 -c '
import json, sys
for env in json.load(sys.stdin).get("items") or []:
    if env.get("kind") == "preview" and env.get("source_ref", "").startswith("pr-" + sys.argv[1] + "-"):
        print(env["id"])
        break
' "$PULL" 2>/dev/null || echo "")
			[ -n "$PREVIEW_ENV" ] && break
			sleep 5
		done
		if [ -z "$PULL" ] || [ -z "$BRANCHED" ] || [ -z "$PREVIEW_ENV" ]; then
			no "a pull request opened on the Git host made no preview (pull ${PULL:-not opened}, commit ${BRANCHED:-not pushed})"
		else
			PREVIEW_COPY=$(api GET "/api/environments/$PREVIEW_ENV/apps" | pick items.0.id 2>/dev/null || echo "")
			if [ -n "$PREVIEW_COPY" ] && wait_deployed "$PREVIEW_COPY"; then
				ok "a pull request opened on the Git host was previewed: the branch's commit built and deployed in an environment of its own"
			else
				no "the preview of the pull request did not deploy"
			fi
			PREVIEW_NS=$(api GET "/api/environments/$PREVIEW_ENV" | pick namespace 2>/dev/null || echo "")
			gitea PATCH "/repos/$GITEA_REPO/pulls/$PULL" '{"state":"closed"}' >/dev/null 2>&1 || true
			CLOSED=""
			for _ in $(seq 1 36); do
				[ -n "$PREVIEW_NS" ] && ! kubectl get namespace "$PREVIEW_NS" >/dev/null 2>&1 && { CLOSED=1; break; }
				sleep 5
			done
			[ -n "$CLOSED" ] \
				&& ok "closing the pull request on the Git host took its preview away" \
				|| no "the preview's namespace ${PREVIEW_NS:-unknown} was still there three minutes after its pull request was closed"
		fi
		api DELETE "/api/apps/$PUSHED_APP" >/dev/null 2>&1 || true
	fi
	[ -z "$SOURCE" ] || api DELETE "/api/teams/$TEAM_ID/git-sources/$SOURCE" >/dev/null 2>&1 || true
fi

##############################################################################
# Phase 2 — data: a volume, a database, a backup that restores, an export
##############################################################################

if wants 2; then
phase "Phase 2 — the data, and getting it back"

step "A volume that survives a restart (checklist 5)"

VOLUME_ID=""
if [ -z "$APP_ID" ]; then
	skip "the volume" "phase 1 did not run, so there is no app to attach one to"
else
	VOLUME_ID=$(api POST "/api/apps/$APP_ID/volumes" '{"name":"data","mount_path":"/data","size_gb":1}' | pick id 2>/dev/null || echo "")
	[ -n "$VOLUME_ID" ] || no "a volume could not be created"
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
		# The answer is {"database": {...}, "links": [...]}. This read a
		# "status" at the top, which is not there, for two runs: the database
		# was running and this said "unknown".
		DB_STATE=$(api GET "/api/databases/$DB_ID" | pick database.status 2>/dev/null || echo "")
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

# The other eight engines are a StatefulSet each, from the engine's own image,
# under the environment's restricted Pod Security profile and its quota, and
# none of that had ever run. Asked for at once and waited for together: each
# has to come up on its own, as PostgreSQL does, which is its own client
# answering the readiness check.
step "The other database engines (checklist 6)"

OTHER_DBS=""
UP_DBS=""
for engine in mysql mariadb mongodb redis valkey dragonfly clickhouse memcached; do
	id=$(api POST "/api/environments/$ENV_ID/databases" "{\"name\":\"verify-$engine\",\"engine\":\"$engine\",\"storage_gb\":1}" | pick id 2>/dev/null || echo "")
	if [ -n "$id" ]; then
		OTHER_DBS="$OTHER_DBS $engine=$id"
	else
		no "a $engine database could not be asked for"
	fi
done
PENDING_DBS="$OTHER_DBS"
for _ in $(seq 1 60); do
	[ -n "$PENDING_DBS" ] || break
	STILL=""
	for pair in $PENDING_DBS; do
		engine=${pair%%=*}
		id=${pair#*=}
		state=$(api GET "/api/databases/$id" | pick database.status 2>/dev/null || echo "")
		case "$state" in
		ready | running)
			ok "a $engine database came up on its own"
			UP_DBS="$UP_DBS $pair"
			;;
		failed)
			no "the $engine database failed: $(api GET "/api/databases/$id" | pick database.status_detail 2>/dev/null || echo "no reason given")"
			kubectl -n "$NAMESPACE" describe statefulset "verify-$engine" >>"$REPORT" 2>&1 || true
			;;
		*) STILL="$STILL $pair" ;;
		esac
	done
	PENDING_DBS="$STILL"
	[ -z "$PENDING_DBS" ] || sleep 10
done
# MongoDB 8 stops as it starts on Linux 6.19 and newer, which is the kernel
# Ubuntu 24.04 moves to, and some of GitHub's runners already have. On a
# cluster with such a server a new MongoDB gets 7.0, and 8.0 is refused with
# the reason rather than left in a crash loop.
KERNELS=$(kubectl get nodes -o jsonpath='{range .items[*]}{.status.nodeInfo.kernelVersion}{"\n"}{end}' 2>/dev/null || true)
NEW_KERNEL=$(printf '%s\n' "$KERNELS" | awk -F. '$1 > 6 || ($1 == 6 && $2 + 0 >= 19)' | head -n 1)
for pair in $OTHER_DBS; do
	[ "${pair%%=*}" = mongodb ] || continue
	MONGO_VERSION=$(api GET "/api/databases/${pair#*=}" | pick database.engine_version 2>/dev/null || echo "")
	if [ -n "$NEW_KERNEL" ]; then
		[ "$MONGO_VERSION" = "7.0" ] \
			&& ok "a server here runs Linux $NEW_KERNEL, which MongoDB 8 stops on, and the new MongoDB was given 7.0" \
			|| no "a server here runs Linux $NEW_KERNEL, which MongoDB 8 stops on, and the new MongoDB was given ${MONGO_VERSION:-nothing}"
		REFUSED=$(api_code POST "/api/environments/$ENV_ID/databases" '{"name":"verify-mongodb-8","engine":"mongodb","version":"8.0","storage_gb":1}')
		[ "$REFUSED" = "409" ] \
			&& ok "and asking for MongoDB 8.0 there was refused with the reason" \
			|| no "asking for MongoDB 8.0 on Linux $NEW_KERNEL answered $REFUSED, not a refusal"
	else
		say "  the servers here run Linux $(printf '%s' "$KERNELS" | tr '\n' ' '), and the new MongoDB was given ${MONGO_VERSION:-nothing}"
	fi
done
for pair in $PENDING_DBS; do
	engine=${pair%%=*}
	no "the $engine database is not ready after ten minutes; what the cluster says is below"
	{
		kubectl -n "$NAMESPACE" describe statefulset "verify-$engine" 2>&1 | tail -20
		kubectl -n "$NAMESPACE" describe pod "verify-$engine-0" 2>&1 | tail -30
		kubectl -n "$NAMESPACE" logs "verify-$engine-0" --all-containers --tail=30 2>&1
	} >>"$REPORT"
done
# Removed again, so what the rest of the run measures and drains is what it
# did before. The five the panel backs up stay until their backups have been
# taken and restored, below, when there is a bucket to take them to.
BACKUP_DBS=""
for pair in $OTHER_DBS; do
	case "${pair%%=*}" in mysql | mariadb | mongodb | redis | valkey) backed_up=1 ;; *) backed_up=0 ;; esac
	case " $UP_DBS " in *" $pair "*) up=1 ;; *) up=0 ;; esac
	if [ "$backed_up$up" = 11 ] && [ -n "$VERIFY_S3_BUCKET" ] && [ -n "$VERIFY_S3_ENDPOINT" ]; then
		BACKUP_DBS="$BACKUP_DBS $pair"
	else
		api DELETE "/api/databases/${pair#*=}" >/dev/null 2>&1 || true
	fi
done

step "A backup that restores (checklist 7)"

STORAGE_SET=""
if [ -z "$VERIFY_S3_BUCKET" ] || [ -z "$VERIFY_S3_ENDPOINT" ]; then
	skip "backup and restore" "no object storage is configured. Set VERIFY_S3_ENDPOINT, VERIFY_S3_BUCKET, VERIFY_S3_ACCESS_KEY and VERIFY_S3_SECRET_KEY — a backup with nowhere to go is not a backup, and this check refuses to pretend otherwise."
else
	api PUT /api/settings "$(cat <<JSON
{"values":{"storage.s3_endpoint":"$VERIFY_S3_ENDPOINT","storage.s3_bucket":"$VERIFY_S3_BUCKET",
 "storage.s3_access_key":"$VERIFY_S3_ACCESS_KEY","storage.s3_secret_key":"$VERIFY_S3_SECRET_KEY",
 "storage.s3_path_style":"true"}}
JSON
)" >/dev/null && STORAGE_SET=1 || no "the storage settings were refused"
fi

if [ -n "$STORAGE_SET" ] && [ -z "$DB_ID" ]; then
	skip "backup and restore" "there is no database to back up"
elif [ -n "$STORAGE_SET" ]; then
	# Something to lose, and something to get back: a row written before the
	# backup, changed after it, and expected back as it was once the backup is
	# restored. Through the database's own pod, as its superuser, so what is
	# checked is the data and not anything the panel says about it. The table
	# belongs to the database's owner, as one an app made would: the backup
	# connects as that user, and a table only the superuser could read failed
	# it with "permission denied", which no app's own table does.
	DB_POD=$(kubectl -n "$NAMESPACE" get pods -l cnpg.io/cluster=verifydb -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
	DB_NAME=$(kubectl -n "$NAMESPACE" get clusters.postgresql.cnpg.io verifydb -o jsonpath='{.spec.bootstrap.initdb.database}' 2>/dev/null || echo "")
	DB_OWNER=$(kubectl -n "$NAMESPACE" get clusters.postgresql.cnpg.io verifydb -o jsonpath='{.spec.bootstrap.initdb.owner}' 2>/dev/null || echo "")
	db_sql() { kubectl -n "$NAMESPACE" exec "$DB_POD" -c postgres -- psql -tAq -d "${DB_NAME:-postgres}" -c "$1" 2>>"$REPORT"; }
	db_sql "create table if not exists verify_restore (value text); alter table verify_restore owner to \"${DB_OWNER:-${DB_NAME:-app}}\"; delete from verify_restore; insert into verify_restore values ('before')" >/dev/null \
		|| no "a row could not be written to the database before the backup"

	BACKUP_ID=$(api POST "/api/databases/$DB_ID/backups" '{}' | pick id 2>/dev/null || echo "")
	if [ -z "$BACKUP_ID" ]; then
		no "a backup could not be started"
	else
		B_STATE=$(backup_ended "/api/databases/$DB_ID/backups" "$BACKUP_ID")
		if [ "$B_STATE" = "succeeded" ]; then
			ok "a backup ran and was uploaded to $VERIFY_S3_ENDPOINT"
			# The Verify button's check, which is the half of a restore that
			# needs no database: the panel downloads it and reads it through.
			WHY=$(read_through "/api/databases/$DB_ID/backups" "$BACKUP_ID")
			[ -z "$WHY" ] \
				&& ok "the panel downloaded the backup from the bucket and read it through" \
				|| no "the panel could not read the backup through: $WHY"
			db_sql "update verify_restore set value = 'after'" >/dev/null || true
			OP=$(api POST "/api/databases/$DB_ID/restore/$BACKUP_ID?overwrite=true" | pick id 2>/dev/null || echo "")
			if [ -z "$OP" ]; then
				no "the restore was refused"
			else
				R_STATE=$(operation_ended "$OP")
				[ "$R_STATE" = "succeeded" ] \
					&& ok "the backup restored over the live database without an error" \
					|| { no "the restore ended as '${R_STATE:-unknown}'. A backup that has never been restored is a file, not a backup."; job_logs "$BACKUP_ID"; }
				RESTORED=$(db_sql "select value from verify_restore" | tr -d '[:space:]')
				[ "$RESTORED" = "before" ] \
					&& ok "and what is in the database is what was there when the backup was taken" \
					|| no "after the restore the row reads '${RESTORED:-nothing}', not what the backup was taken of ('before')"
			fi
		else
			no "the backup ended as '${B_STATE:-unknown}'; the bucket, the keys, the path style, or the way from the backup job to the bucket is wrong"
			kubectl -n "$NAMESPACE" get networkpolicy -o wide >>"$REPORT" 2>&1 || true
			job_logs "$BACKUP_ID"
		fi
	fi
	say "  Restoring into a *different* cluster is the half a script cannot do on one"
	say "  machine. The backup is in your bucket: install Skifity elsewhere, point it"
	say "  at the same bucket, and restore. That is the drill worth running once."
fi

# The five other engines the panel backs up, each with its own tools: a
# mysqldump or a mariadb-dump, a MongoDB archive, and a Redis or Valkey
# snapshot, which goes back by the database replicating from a server the
# restore starts on it. None of it had ever run. All five at once, so the run
# waits for the slowest rather than for each in turn.
step "The other engines' backups restore (checklist 7)"

if [ -z "$BACKUP_DBS" ]; then
	skip "the other engines' backups" "no bucket is configured, or none of the engines it backs up came up"
else
	TAKEN=""
	for pair in $BACKUP_DBS; do
		engine=${pair%%=*}
		id=${pair#*=}
		# Read back before anything else, so a backup is only ever asked of a
		# database that is holding what the check expects of it.
		engine_set "$engine" before >/dev/null || true
		if [ "$(engine_get "$engine")" != before ]; then
			no "the $engine database did not keep what was written to it before its backup"
			continue
		fi
		backup=$(api POST "/api/databases/$id/backups" '{}' | pick id 2>/dev/null || echo "")
		if [ -n "$backup" ]; then
			TAKEN="$TAKEN $engine=$id=$backup"
		else
			no "a backup of the $engine database could not be started"
		fi
	done
	RESTORING=""
	for taken in $TAKEN; do
		IFS='=' read -r engine id backup <<<"$taken"
		state=$(backup_ended "/api/databases/$id/backups" "$backup")
		if [ "$state" != succeeded ]; then
			no "the $engine backup ended as '${state:-unknown}': $(backup_field "/api/databases/$id/backups" "$backup" error_message)"
			job_logs "$backup"
			continue
		fi
		ok "a $engine backup ran and was uploaded"
		engine_set "$engine" after >/dev/null || true
		op=$(api POST "/api/databases/$id/restore/$backup?overwrite=true" | pick id 2>/dev/null || echo "")
		if [ -n "$op" ]; then
			RESTORING="$RESTORING $engine=$id=$backup=$op"
		else
			no "the $engine restore was refused"
		fi
	done
	for restoring in $RESTORING; do
		IFS='=' read -r engine id backup op <<<"$restoring"
		state=$(operation_ended "$op")
		if [ "$state" != succeeded ]; then
			no "the $engine restore ended as '${state:-unknown}': $(api GET "/api/operations/$op" | pick error_message 2>/dev/null || echo "no reason given")"
			job_logs "$backup"
			continue
		fi
		value=$(engine_get "$engine")
		[ "$value" = before ] \
			&& ok "the $engine backup restored over the live database, which holds what it held when the backup was taken" \
			|| no "after the $engine restore the database holds '${value:-nothing}', not what the backup was taken of ('before')"
	done
fi
# Removed now, so what the rest of the run measures and drains is what it did
# before they were asked for.
for pair in $BACKUP_DBS; do
	api DELETE "/api/databases/${pair#*=}" >/dev/null 2>&1 || true
done

# An app's volume: its files archived beside the app and sent to the bucket,
# and a restore that stops the app, puts the archive back over what is there,
# and starts the app again as it was.
step "A volume's backup restores (checklist 7)"

if [ -z "$STORAGE_SET" ]; then
	skip "the volume's backup" "no bucket is configured"
elif [ -z "$VOLUME_ID" ] || [ -z "$APP_HOST" ]; then
	skip "the volume's backup" "there is no volume to back up"
else
	VOLUME_PATH="/api/apps/$APP_ID/volumes/$VOLUME_ID"
	curl -s --max-time 15 -H "Host: $APP_HOST" "http://127.0.0.1/write?written-before-the-backup" >/dev/null || true
	V_BACKUP=$(api POST "$VOLUME_PATH/backups" '{}' | pick id 2>/dev/null || echo "")
	if [ -z "$V_BACKUP" ]; then
		no "a backup of the volume could not be started"
	else
		state=$(backup_ended "$VOLUME_PATH/backups" "$V_BACKUP")
		if [ "$state" != succeeded ]; then
			no "the volume's backup ended as '${state:-unknown}': $(backup_field "$VOLUME_PATH/backups" "$V_BACKUP" error_message)"
			job_logs "$V_BACKUP"
		else
			ok "the volume was archived beside the app and uploaded"
			curl -s --max-time 15 -H "Host: $APP_HOST" "http://127.0.0.1/write?written-after-the-backup" >/dev/null || true
			op=$(api POST "$VOLUME_PATH/restore/$V_BACKUP?overwrite=true" | pick id 2>/dev/null || echo "")
			if [ -z "$op" ]; then
				no "the volume's restore was refused"
			else
				state=$(operation_ended "$op")
				if [ "$state" != succeeded ]; then
					no "the volume's restore ended as '${state:-unknown}': $(api GET "/api/operations/$op" | pick error_message 2>/dev/null || echo "no reason given")"
					job_logs "$V_BACKUP"
				else
					# The app is started again by the restore; the first
					# answers can be from before it is ready.
					AFTER=""
					for _ in $(seq 1 30); do
						AFTER=$(curl -s --max-time 15 -H "Host: $APP_HOST" "http://127.0.0.1/data" || true)
						case "$AFTER" in *written-*-the-backup*) break ;; esac
						sleep 5
					done
					case "$AFTER" in
					*written-before-the-backup*) ok "the app was started again, and reads its volume as it was when the backup was taken" ;;
					*) no "after the volume's restore the app reads '${AFTER:-nothing}', not what the backup was taken of" ;;
					esac
				fi
			fi
		fi
	fi
fi

# Sealed with a passphrase before it leaves the cluster, so whoever holds the
# bucket holds ciphertext: the panel's own image seals the dump inside the
# backup's job and opens it inside the restore's. Last of the databases'
# checks, because from here on every backup is sealed.
step "A backup sealed with a passphrase (checklist 7)"

if [ -z "$STORAGE_SET" ] || [ -z "$DB_ID" ]; then
	skip "a sealed backup" "no bucket is configured, or there is no database to back up"
else
	api PUT /api/settings '{"values":{"storage.backup_passphrase":"not-a-real-passphrase-for-the-crosscheck"}}' >/dev/null \
		|| no "the backup passphrase was refused"
	db_sql "update verify_restore set value = 'sealed'" >/dev/null || true
	S_BACKUP=$(api POST "/api/databases/$DB_ID/backups" '{}' | pick id 2>/dev/null || echo "")
	if [ -z "$S_BACKUP" ]; then
		no "a sealed backup could not be started"
	else
		state=$(backup_ended "/api/databases/$DB_ID/backups" "$S_BACKUP")
		if [ "$state" != succeeded ]; then
			no "the sealed backup ended as '${state:-unknown}': $(backup_field "/api/databases/$DB_ID/backups" "$S_BACKUP" error_message)"
			job_logs "$S_BACKUP"
		else
			[ "$(backup_field "/api/databases/$DB_ID/backups" "$S_BACKUP" encrypted)" = True ] \
				&& ok "a backup taken with a passphrase set is recorded as sealed" \
				|| no "a backup taken with a passphrase set is not recorded as sealed"
			# Read from the bucket directly, as its owner would, with the
			# bucket's own keys: the first bytes are the sealed format's, not
			# gzip's.
			LOCATION=$(backup_field "/api/databases/$DB_ID/backups" "$S_BACKUP" location)
			HEAD=$(curl -s --max-time 15 -r 0-7 --aws-sigv4 "aws:amz:us-east-1:s3" \
				--user "$VERIFY_S3_ACCESS_KEY:$VERIFY_S3_SECRET_KEY" \
				"$VERIFY_S3_ENDPOINT/$VERIFY_S3_BUCKET/$LOCATION" 2>>"$REPORT" | head -c 8 | tr -d '\0' || true)
			[ "$HEAD" = SKFSEAL1 ] \
				&& ok "what the bucket holds is sealed: it starts with SKFSEAL1, not with a dump" \
				|| no "what the bucket holds for the sealed backup starts with '${HEAD:-nothing}', not SKFSEAL1"
			WHY=$(read_through "/api/databases/$DB_ID/backups" "$S_BACKUP")
			[ -z "$WHY" ] \
				&& ok "the panel opened the sealed backup with the passphrase and read it through" \
				|| no "the panel could not read the sealed backup through: $WHY"
			db_sql "update verify_restore set value = 'after'" >/dev/null || true
			op=$(api POST "/api/databases/$DB_ID/restore/$S_BACKUP?overwrite=true" | pick id 2>/dev/null || echo "")
			if [ -z "$op" ]; then
				no "the sealed backup's restore was refused"
			else
				state=$(operation_ended "$op")
				RESTORED=$(db_sql "select value from verify_restore" | tr -d '[:space:]')
				if [ "$state" = succeeded ] && [ "$RESTORED" = sealed ]; then
					ok "the sealed backup was opened inside the restore and put back"
				else
					no "the sealed backup's restore ended as '${state:-unknown}' and the row reads '${RESTORED:-nothing}', not 'sealed'"
					job_logs "$S_BACKUP"
				fi
			fi
		fi
	fi
fi

# The panel's own database, copied to the same bucket: what `skifity
# restore-db` brings back after the server it ran on is gone. The master key
# is never in it, on purpose.
step "The panel's own database, in the bucket"

if [ -z "$STORAGE_SET" ]; then
	skip "the panel's own backup" "no bucket is configured"
else
	# Read whatever the answer is, so a refusal says why.
	P_OUT=$(curl -sS -X POST "$PANEL/api/panel/backups" ${TOKEN:+-H "Authorization: Bearer $TOKEN"} 2>&1 || true)
	P_BACKUP=$(printf '%s' "$P_OUT" | pick id 2>/dev/null || echo "")
	if [ -z "$P_BACKUP" ]; then
		no "the panel could not copy its own database to the bucket: $(printf '%s' "$P_OUT" | head -c 400)"
	else
		state=$(backup_field /api/panel/backups "$P_BACKUP" status)
		if [ "$state" = succeeded ]; then
			ok "the panel copied its own database to the bucket"
			WHY=$(read_through /api/panel/backups "$P_BACKUP")
			[ -z "$WHY" ] \
				&& ok "and read the copy back through" \
				|| no "the panel could not read its own copy back through: $WHY"
		else
			no "the panel's copy of its own database ended as '${state:-unknown}': $(backup_field /api/panel/backups "$P_BACKUP" error_message)"
		fi
	fi
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

# Made signed in, the way a person makes one: a token issued for one team is
# refused when it asks for another team to be created, on purpose, and that
# refusal is not what this check is about.
OTHER_TEAM=$(curl -fsS -b "$COOKIE" -H "X-Skifity-CSRF: $CSRF" -H 'Content-Type: application/json' \
	-X POST "$PANEL/api/teams" -d '{"name":"Other"}' | pick id 2>/dev/null || echo "")
CODE=$(api_code POST /api/teams '{"name":"By token"}')
[ "$CODE" = "403" ] \
	&& ok "a token issued for one team cannot create another" \
	|| no "a token issued for one team answered $CODE when asked to create a team; it should be 403"
if [ -z "$OTHER_TEAM" ]; then
	skip "the cross-team check" "a second team could not be created while signed in"
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

step "Single sign-on through an identity provider (checklist 11)"

# Dex, an OpenID Connect provider, with one connector that vouches for one
# person in one group, so a sign-in needs no form: the panel sends the browser
# to Dex, Dex sends it back with a code, and the panel checks the ID token,
# makes the account on its first sign-in, and puts it in the team its group
# names. Dex puts the groups in the ID token only when they are asked for.
if [ -z "$VERIFY_DEX" ] || [ ! -x "$VERIFY_DEX" ]; then
	skip "single sign-on" "no identity provider was given. Set VERIFY_DEX to a Dex binary"
else
	SSO_URL=$(panel_public_url)
	DEX_ADDRESS=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null || echo "")
	DEX_ISSUER="http://$DEX_ADDRESS:5556/dex"
	DEX_DIR=$(mktemp -d)
	cat >"$DEX_DIR/config.yaml" <<YAML
issuer: $DEX_ISSUER
storage:
  type: memory
web:
  http: 0.0.0.0:5556
oauth2:
  skipApprovalScreen: true
staticClients:
  - id: skifity
    name: Skifity
    secret: not-a-real-secret
    redirectURIs: ["$SSO_URL/api/auth/sso/callback"]
connectors:
  - type: mockCallback
    id: crosscheck
    name: Crosscheck
YAML
	"$VERIFY_DEX" serve "$DEX_DIR/config.yaml" >"$DEX_DIR/dex.log" 2>&1 &
	DEX_PID=$!
	for _ in $(seq 1 30); do
		curl -fs -o /dev/null "$DEX_ISSUER/.well-known/openid-configuration" && break
		sleep 1
	done
	TEAM_SLUG=$(api GET "/api/teams/$TEAM_ID" | pick slug 2>/dev/null || echo "")
	SSO_SETTINGS=$(python3 -c '
import json, sys
print(json.dumps({"values": {
    "signin.oidc_issuer": sys.argv[1], "signin.oidc_client_id": "skifity",
    "signin.oidc_client_secret": "not-a-real-secret", "signin.oidc_allowed_domains": "kilgore.trout",
    "signin.oidc_auto_create": "true", "signin.oidc_group_roles": "authors = " + sys.argv[2] + ":member"}}))
' "$DEX_ISSUER" "$TEAM_SLUG")
	if [ -z "$SSO_URL" ] || ! curl -fs -o /dev/null "$DEX_ISSUER/.well-known/openid-configuration"; then
		no "Dex did not start at $DEX_ISSUER: $(tail -c 300 "$DEX_DIR/dex.log")"
	elif ! api PUT /api/settings "$SSO_SETTINGS" >/dev/null; then
		no "the single sign-on settings were refused"
	else
		# The panel's own address has a certificate from Let's Encrypt's
		# staging server, which cannot reach this machine to issue one, so
		# Traefik serves its own there; the browser here takes it, as a
		# person would click past it. Dex is plain HTTP on this machine.
		BROWSER=$(mktemp)
		LANDED=$(curl -s -k -L --max-redirs 10 -c "$BROWSER" -b "$BROWSER" -o /dev/null -w '%{url_effective}' \
			"$SSO_URL/api/auth/sso/start" || true)
		SIGNED_IN=$(curl -s -k -b "$BROWSER" "$SSO_URL/api/me" || true)
		case "$SIGNED_IN" in
		*kilgore@kilgore.trout*) ok "a sign-in through Dex made an account for the person it vouched for, and signed them in" ;;
		*)
			no "a sign-in through Dex ended at ${LANDED:-nowhere} with no session: ${SIGNED_IN:-no answer}; Dex's log is in the report"
			cat "$DEX_DIR/dex.log" >>"$REPORT" 2>/dev/null || true
			;;
		esac
		SSO_ROLE=$(api GET "/api/teams/$TEAM_ID/members" | python3 -c '
import json, sys
for member in json.load(sys.stdin).get("items") or []:
    if (member.get("user") or {}).get("email") == "kilgore@kilgore.trout":
        print(member.get("role", ""))
' 2>/dev/null || echo "")
		[ "$SSO_ROLE" = "member" ] \
			&& ok "the group the provider put them in made them a member of the team it names" \
			|| no "after signing in through Dex they are '${SSO_ROLE:-not in the team}', not the member their group gives"
	fi
	api PUT /api/settings '{"values":{"signin.oidc_issuer":"","signin.oidc_group_roles":""}}' >/dev/null 2>&1 || true
	kill "$DEX_PID" 2>/dev/null || true
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

step "An app cannot reach the machine it runs on, or the registry"

# What the egress policy is for: a tenant's pod reaching the Kubernetes API,
# the kubelet, the registry's NodePort on the node, or the registry itself,
# where every team's images are. The probe runs in the app's own namespace,
# under its policies and its pod security profile, and every one of these has
# to fail at the network: an HTTP answer of any kind, a 401 included, means the
# packet got there.
#
# The probe starts the way every pod the panel runs for a team does: behind the
# fence container, which holds the pod's own code back until the policy is in
# place. The second run on a real cluster, before there was one, saw the first
# target reached in the moment before the policy controller had written the
# pod's rules, and every other one refused after. So the app's own Deployment
# is checked for the fence first, and the probe borrows that very container.
EGRESS_NODE=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null || true)
EGRESS_FENCE=$(kubectl -n "$NAMESPACE" get deploy hello -o json 2>/dev/null | python3 -c '
import json, sys
try:
    init = json.load(sys.stdin)["spec"]["template"]["spec"].get("initContainers") or []
except Exception:
    init = []
print(json.dumps(init[0]) if init and init[0].get("name") == "fence" else "")
' 2>/dev/null || true)
if [ -n "$EGRESS_FENCE" ]; then
	ok "an app's pod starts behind the fence that waits for its network policy"
else
	no "the app's pods do not start with the fence container, so its code runs before its network policy is in place"
fi
if [ -z "$EGRESS_NODE" ]; then
	skip "the egress check" "the node reports no internal address to aim at"
else
	EGRESS_SCRIPT="for target in https://$EGRESS_NODE:6443/version https://$EGRESS_NODE:10250/pods http://$EGRESS_NODE:30500/v2/ http://skifity-registry.skifity-builds.svc.cluster.local:5000/v2/ https://kubernetes.default.svc/version; do if curl -sk -o /dev/null --max-time 6 \"\$target\"; then echo \"reached \$target\"; else echo \"refused \$target\"; fi; done"
	EGRESS_OVERRIDES=$(python3 - "$EGRESS_SCRIPT" "${EGRESS_FENCE:-}" <<'PYEOF'
import json, sys
fence = [json.loads(sys.argv[2])] if sys.argv[2] else []
print(json.dumps({"spec": {
    "securityContext": {"runAsNonRoot": True, "runAsUser": 100, "seccompProfile": {"type": "RuntimeDefault"}},
    "initContainers": fence,
    "containers": [{
        "name": "verify-egress",
        "image": "curlimages/curl:8.11.1",
        "command": ["sh", "-c", sys.argv[1]],
        # `kubectl run -i` attaches to the container's stdin, and the merge
        # replaces the container kubectl would have made with this one.
        "stdin": True, "stdinOnce": True,
        "securityContext": {"allowPrivilegeEscalation": False, "capabilities": {"drop": ["ALL"]}},
    }],
}}))
PYEOF
)
	EGRESS_OUT=$(kubectl -n "$NAMESPACE" run verify-egress --rm -i --quiet --restart=Never --timeout=180s \
		--image=curlimages/curl:8.11.1 --overrides="$EGRESS_OVERRIDES" 2>&1 || true)
	say "$(printf '%s\n' "$EGRESS_OUT" | sed 's/^/    /')"
	if ! printf '%s' "$EGRESS_OUT" | grep -q '^refused \|^reached '; then
		no "the probe did not run in the app's namespace, so the egress policy could not be checked: ${EGRESS_OUT:-no output}"
	elif printf '%s' "$EGRESS_OUT" | grep -q '^reached '; then
		no "a pod in an app's namespace reached $(printf '%s' "$EGRESS_OUT" | grep '^reached ' | cut -d' ' -f2 | tr '\n' ' '). The egress policy (internal/kube/egress.go) is what is supposed to stop it."
	else
		ok "an app's pod reaches neither the Kubernetes API, the kubelet, the registry's port on the node, nor the registry"
	fi
fi

step "A build cannot write over another team's image"

# Each build starts a BuildKit of its own, and the registry's guard lets a
# build write to its own app's repository with the token the panel gave it and
# to nothing else. Reading stays open: every server's container runtime pulls
# this way. The probe stands where a hostile build step would, in the build
# namespace beside the registry, with no token or a forged one.
if kubectl -n skifity-builds get deploy skifity-buildkit >/dev/null 2>&1; then
	no "the BuildKit every team once shared is still running in skifity-builds"
else
	ok "there is no builder shared between teams"
fi
BUILD_IMAGE=$(kubectl -n skifity-builds get jobs -l app.kubernetes.io/component=build \
	-o jsonpath='{.items[0].spec.template.spec.containers[0].image}' 2>/dev/null || true)
case "$BUILD_IMAGE" in
	*-rootless*) ok "a build runs its own rootless BuildKit ($(printf '%s' "$BUILD_IMAGE" | cut -d@ -f1))" ;;
	"") skip "the build's own builder" "no build Job is left to read; they are kept for an hour" ;;
	*) no "a build runs $BUILD_IMAGE, which is not a BuildKit of its own" ;;
esac
FORGED=$(printf 'skifity:v1.eyJyIjoiKiIsImUiOjQxMDI0NDQ4MDB9.forged' | base64 | tr -d '\n')
REGISTRY_URL="http://skifity-registry.skifity-builds.svc.cluster.local:5000"
# The probe starts the way a build does, behind the fence borrowed from a build
# Job, and reads until the registry answers. The third run had a bare pod ask
# the moment it started, before the policy controller had put its address in
# the set the registry lets in, and every request was refused in the first
# second — which says nothing about the guard. Its output is read from the log
# afterwards, which loses nothing, where attaching to it lost three lines.
REGISTRY_FENCE=$(kubectl -n skifity-builds get jobs -l app.kubernetes.io/component=build -o json 2>/dev/null | python3 -c '
import json, sys
try:
    jobs = json.load(sys.stdin)["items"]
    init = jobs[0]["spec"]["template"]["spec"].get("initContainers") or [] if jobs else []
except Exception:
    init = []
print(json.dumps(init[0]) if init and init[0].get("name") == "fence" else "")
' 2>/dev/null || true)
REGISTRY_SCRIPT="code() { label=\$1; shift; printf '%s %s\\n' \"\$label\" \"\$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 \"\$@\" 2>/dev/null)\"; }
for _ in \$(seq 1 30); do [ \"\$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 $REGISTRY_URL/v2/)\" = 200 ] && break; sleep 2; done
code read -X GET $REGISTRY_URL/v2/
code anonymous-upload -X POST $REGISTRY_URL/v2/$NAMESPACE/hello/blobs/uploads/
code forged-upload -X POST -H 'Authorization: Basic $FORGED' $REGISTRY_URL/v2/$NAMESPACE/hello/blobs/uploads/
code anonymous-manifest -X PUT -H 'Content-Type: application/vnd.oci.image.manifest.v1+json' --data '{}' $REGISTRY_URL/v2/$NAMESPACE/hello/manifests/latest
code anonymous-delete -X DELETE $REGISTRY_URL/v2/$NAMESPACE/hello/manifests/sha256:0000000000000000000000000000000000000000000000000000000000000000
code anonymous-catalog -X GET $REGISTRY_URL/v2/_catalog"
REGISTRY_OVERRIDES=$(python3 - "$REGISTRY_SCRIPT" "${REGISTRY_FENCE:-}" <<'PYEOF'
import json, sys
fence = [json.loads(sys.argv[2])] if sys.argv[2] else []
print(json.dumps({"spec": {
    "initContainers": fence,
    "containers": [{
        "name": "verify-registry",
        "image": "curlimages/curl:8.11.1",
        "command": ["sh", "-c", sys.argv[1]],
    }],
}}))
PYEOF
)
kubectl -n skifity-builds delete pod verify-registry --ignore-not-found >/dev/null 2>&1 || true
kubectl -n skifity-builds run verify-registry --restart=Never --image=curlimages/curl:8.11.1 \
	--overrides="$REGISTRY_OVERRIDES" >/dev/null 2>&1 || true
kubectl -n skifity-builds wait pod/verify-registry --for=jsonpath='{.status.phase}'=Succeeded --timeout=180s >/dev/null 2>&1 || true
REGISTRY_OUT=$(kubectl -n skifity-builds logs verify-registry -c verify-registry 2>&1 || true)
kubectl -n skifity-builds delete pod verify-registry --ignore-not-found >/dev/null 2>&1 || true
say "$(printf '%s\n' "$REGISTRY_OUT" | sed 's/^/    /')"
registry_code() { printf '%s\n' "$REGISTRY_OUT" | awk -v want="$1" '$1 == want {print $2}' | tail -n 1; }
if [ -z "$(registry_code read)" ]; then
	no "the probe did not run in the build namespace, so the registry's guard could not be checked: ${REGISTRY_OUT:-no output}"
else
	[ "$(registry_code read)" = "200" ] \
		&& ok "anybody in the build namespace can read the registry, as every server pulls" \
		|| no "reading the registry answered $(registry_code read); the servers could not pull what the builds push"
	REFUSED=1
	for attempt in anonymous-upload forged-upload anonymous-manifest anonymous-delete anonymous-catalog; do
		case "$(registry_code "$attempt")" in
			401 | 403) ;;
			*) REFUSED=0; no "the registry answered $(registry_code "$attempt") to $attempt; only a build's own token may write" ;;
		esac
	done
	[ "$REFUSED" = "1" ] \
		&& ok "a write with no token or a forged one, a delete and the list of every repository are all refused" \
		|| true
fi

step "A rate limit is a ceiling at the ingress"

# Through the real Traefik and the real guard: a rule that allows three requests
# a minute on one path, and the fourth answered 429 before it reaches the app.
if [ -z "$APP_HOST" ]; then
	skip "the rate limit" "phase 1 did not run or the app was given no address, so there is nothing to limit"
else
	api POST /api/components/firewall/install >/dev/null 2>&1 \
		|| no "the firewall component could not be installed"
	api PUT "/api/apps/$APP_ID/firewall" "$(cat <<'JSON'
{"enabled":true,"rules":{"default":"allow","rules":[{"id":"verify-limit","name":"Three a minute","action":"limit","enabled":true,
 "limit":{"requests":3,"period_seconds":60},
 "expr":{"test":{"field":"http.path","op":"starts_with","values":["/limited"]}}}]}}
JSON
)" >/dev/null || no "a rate-limit rule was refused"
	# The kubelet delivers a changed ConfigMap within a minute or so, and the
	# guard reads it within seconds of that; until then nothing is counted.
	CODES=""
	for _ in $(seq 1 30); do
		CODES=""
		for _ in 1 2 3 4 5; do
			CODES="$CODES $(curl -s -o /dev/null -w '%{http_code}' --max-time 10 -H "Host: $APP_HOST" "http://127.0.0.1/limited")"
		done
		case "$CODES" in *429*) break ;; esac
		sleep 5
	done
	case "$CODES" in
		*429*) ok "past the limit the ingress answered 429 (the last five answered$CODES)" ;;
		*) no "five requests in a row to a path limited to three a minute all got through:$CODES" ;;
	esac
	[ "$(through_ingress "$APP_HOST" 10)" = "200" ] \
		&& ok "a path the rule does not cover is not limited" \
		|| no "a path the rate-limit rule does not cover was refused too"
	# Off again, so what follows sees the app as it was.
	api PUT "/api/apps/$APP_ID/firewall" '{"enabled":false,"rules":{"default":"allow","rules":[]}}' >/dev/null || true
fi

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
# A pod the namespace's pod security profile admits, so that what turns it down
# is the quota and not the profile: a bare `kubectl create deployment` is
# refused by "restricted" before the quota is ever asked.
kubectl -n "$NAMESPACE" apply -f - >/dev/null 2>&1 <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: quota-probe
spec:
  replicas: 1
  selector:
    matchLabels: {app: quota-probe}
  template:
    metadata:
      labels: {app: quota-probe}
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        seccompProfile: {type: RuntimeDefault}
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.10
          resources:
            requests: {cpu: "64", memory: 64Gi}
            limits: {cpu: "64", memory: 64Gi}
          securityContext:
            allowPrivilegeEscalation: false
            capabilities: {drop: [ALL]}
YAML
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
	skip "the panel-down check" "phase 1 did not run or the app was given no address, so there is no app to keep serving"
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
		# What a drain is meant to move without anybody noticing is an app
		# with nothing on any server's own disk, with an instance on each
		# server. The phase 1 app has kept a volume since phase 2, which ties
		# it to the server the volume was made on, and which server that is
		# the scheduler's choice: it is checked for what that choice means.
		MOVER=$(api POST "/api/environments/$ENV_ID/apps" \
			"{\"name\":\"mover\",\"source_type\":\"image\",\"image\":\"$SHELL_IMAGE\",\"port\":8080,\"deploy\":true}" \
			| pick app.id 2>/dev/null || echo "")
		if [ -n "$MOVER" ] && wait_deployed "$MOVER"; then
			api PUT "/api/apps/$MOVER/scaling" '{"replicas":2,"autoscale":false}' >/dev/null || true
			for _ in $(seq 1 36); do
				[ "$(kubectl -n "$NAMESPACE" get deploy mover -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)" = "2" ] && break
				sleep 5
			done
			MOVER_HOST=$(api GET "/api/apps/$MOVER/domains" | pick items.0.hostname 2>/dev/null || echo "")
		fi
		SERVERS=$(kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/name=mover \
			-o jsonpath='{range .items[*]}{.spec.nodeName}{"\n"}{end}' 2>/dev/null | sort -u | wc -l || true)
		if [ -z "$MOVER_HOST" ]; then
			no "an app from $SHELL_IMAGE with two instances did not come up to be moved"
		else
			[ "$SERVERS" = "2" ] \
				&& ok "an app with two instances has one on each server" \
				|| no "an app with two instances has them on $SERVERS server(s), not one on each"
		fi
		PINNED=$(volume_node hello-data)
		kubectl drain "$VICTIM" --ignore-daemonsets --delete-emptydir-data --force --timeout=180s >>"$REPORT" 2>&1 \
			&& ok "$VICTIM drained: the disruption budget let the instances move one at a time" \
			|| no "$VICTIM could not be drained inside three minutes; a PodDisruptionBudget that cannot be satisfied blocks a drain forever"
		if [ -n "$MOVER_HOST" ]; then
			DRAIN_FAILS=0
			for _ in $(seq 1 10); do
				[ "$(through_ingress "$MOVER_HOST" 10)" = "200" ] || DRAIN_FAILS=$((DRAIN_FAILS + 1))
				sleep 1
			done
			[ "$DRAIN_FAILS" = "0" ] \
				&& ok "the app kept answering while a server was emptied" \
				|| no "$DRAIN_FAILS of 10 requests failed while a node was drained"
		fi
		kubectl uncordon "$VICTIM" >/dev/null 2>&1 || true
		# An app whose volume is on the server's own disk waits for that
		# server, which is what the volume being there means, and comes back
		# with it. Longhorn, under Settings, is how a volume moves.
		if [ -n "$APP_HOST" ] && [ "$PINNED" = "${VICTIM#node/}" ]; then
			BACK=""
			for _ in $(seq 1 36); do
				[ "$(through_ingress "$APP_HOST" 5)" = "200" ] && { BACK=1; break; }
				sleep 5
			done
			[ -n "$BACK" ] \
				&& ok "the app whose volume is on ${VICTIM#node/}'s own disk waited for that server, and answered once it was back" \
				|| no "the app whose volume is on ${VICTIM#node/}'s own disk did not answer again once that server was back"
		fi
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
	# metrics-server reports a pod once it has scraped it at least once, which
	# is up to a minute after the pod started.
	KNOWN=no
	for _ in $(seq 1 18); do
		KNOWN=$(api GET "/api/apps/$APP_ID/status" 2>/dev/null | python3 -c '
import json, sys
try:
    status = json.load(sys.stdin)
except Exception:
    print("no"); sys.exit()
print("yes" if any(i.get("usage_known") for i in status.get("instances") or []) else "no")
' 2>/dev/null || echo no)
		[ "$KNOWN" = "yes" ] && break
		sleep 10
	done
	[ "$KNOWN" = "yes" ] \
		&& ok "each instance reports what it is using, not only what it reserved" \
		|| no "no instance of the app reports what it is using after three minutes; metrics-server may not be answering"
fi
# A server's own numbers, which the panel keeps a minute at a time: its memory
# and processor from metrics-server, and how full its disk is from the
# kubelet's summary, read through the API server's proxy.
FIRST_SERVER=$(api GET "/api/teams/$TEAM_ID/servers" 2>/dev/null | pick items.0.id 2>/dev/null || echo "")
if [ -n "$FIRST_SERVER" ]; then
	for _ in $(seq 1 12); do
		server_usage_known "$FIRST_SERVER" && break
		sleep 10
	done
	server_usage_known "$FIRST_SERVER" \
		&& ok "the server reports its memory, processor and disk, the disk read from its kubelet" \
		|| no "the server's usage has no disk or no full minute: $(api GET "/api/servers/$FIRST_SERVER/usage?range=1h" 2>/dev/null | head -c 300)"
fi
api GET /api/metrics 2>/dev/null | grep -q "^skifity_" \
	&& ok "the panel serves its own Prometheus metrics, behind the same authentication as everything else" \
	|| no "/api/metrics returned nothing that looks like Prometheus output"

step "Upgrading with an app running through it (checklist 15)"

UP=$(api GET /api/upgrade 2>/dev/null || echo "{}")
CURRENT=$(printf '%s' "$UP" | pick current_version 2>/dev/null || echo "")
say "  the panel reports version ${CURRENT:-unknown}"
if [ -z "$APP_HOST" ]; then
	skip "the upgrade check" "phase 1 did not run or the app was given no address, so there is no app to keep serving through it"
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
	fi

	# The services people read, each on a server of its own and read back
	# through that server's API. Each channel hears only of a failed deploy,
	# which is the one sent below.
	TEST_TITLE="Skifity test notification"
	NTFY_TOPIC="skifity-crosscheck"
	MAIL_CHANNEL="" NTFY_CHANNEL="" GOTIFY_CHANNEL="" MATTERMOST_CHANNEL=""
	if [ -z "$VERIFY_SMTP_SERVER" ] || [ -z "$VERIFY_MAILPIT" ]; then
		skip "email notifications" "no mail server was given. Set VERIFY_SMTP_SERVER and VERIFY_MAILPIT"
	elif ! api PUT /api/settings "$(cat <<JSON
{"values":{"email.smtp_host":"${VERIFY_SMTP_SERVER%:*}","email.smtp_port":"${VERIFY_SMTP_SERVER##*:}",
 "email.smtp_tls":"false","email.smtp_from":"skifity@example.test"}}
JSON
)" >/dev/null; then
		no "the panel's Email settings were refused"
	else
		MAIL_SET=1
		# A channel that names only who it goes to, which takes the server
		# from the panel's Email settings.
		MAIL_CHANNEL=$(api POST "/api/teams/$TEAM_ID/notifications" \
			'{"kind":"email","name":"verify-email","config":{"to":"ops@example.test"},"events":["deploy.failed"]}' \
			| pick id 2>/dev/null || echo "")
		if [ -z "$MAIL_CHANNEL" ]; then
			no "an email notification channel could not be created"
		else
			CODE=$(api_code POST "/api/teams/$TEAM_ID/notifications/$MAIL_CHANNEL/test")
			for _ in $(seq 1 15); do
				mail_has ops@example.test "$TEST_TITLE" && break
				sleep 2
			done
			mail_has ops@example.test "$TEST_TITLE" \
				&& ok "a test email arrived at the mail server under Settings → Email" \
				|| no "the test email never arrived (the panel answered $CODE); see the panel's log for what the mail server said"
		fi
	fi
	if [ -z "$VERIFY_NTFY_SERVER" ] || [ -z "$VERIFY_NTFY_TOKEN" ]; then
		skip "ntfy notifications" "no ntfy server was given. Set VERIFY_NTFY_SERVER and VERIFY_NTFY_TOKEN"
	else
		NTFY_CHANNEL=$(api POST "/api/teams/$TEAM_ID/notifications" "$(cat <<JSON
{"kind":"ntfy","name":"verify-ntfy","events":["deploy.failed"],
 "config":{"server":"$VERIFY_NTFY_SERVER","topic":"$NTFY_TOPIC","token":"$VERIFY_NTFY_TOKEN"}}
JSON
)" | pick id 2>/dev/null || echo "")
		if [ -z "$NTFY_CHANNEL" ]; then
			no "an ntfy notification channel could not be created"
		else
			CODE=$(api_code POST "/api/teams/$TEAM_ID/notifications/$NTFY_CHANNEL/test")
			ntfy_has "$NTFY_TOPIC" "$TEST_TITLE" \
				&& ok "a test notification arrived at an ntfy server that takes nobody without a token" \
				|| no "the test notification never arrived at ntfy (the panel answered $CODE)"
		fi
	fi
	if [ -z "$VERIFY_GOTIFY_SERVER" ] || [ -z "$VERIFY_GOTIFY_TOKEN" ] || [ -z "$VERIFY_GOTIFY_CLIENT_TOKEN" ]; then
		skip "Gotify notifications" "no Gotify server was given. Set VERIFY_GOTIFY_SERVER, VERIFY_GOTIFY_TOKEN and VERIFY_GOTIFY_CLIENT_TOKEN"
	else
		GOTIFY_CHANNEL=$(api POST "/api/teams/$TEAM_ID/notifications" "$(cat <<JSON
{"kind":"gotify","name":"verify-gotify","events":["deploy.failed"],
 "config":{"server":"$VERIFY_GOTIFY_SERVER","app_token":"$VERIFY_GOTIFY_TOKEN"}}
JSON
)" | pick id 2>/dev/null || echo "")
		if [ -z "$GOTIFY_CHANNEL" ]; then
			no "a Gotify notification channel could not be created"
		else
			CODE=$(api_code POST "/api/teams/$TEAM_ID/notifications/$GOTIFY_CHANNEL/test")
			gotify_has "$TEST_TITLE" \
				&& ok "a test notification arrived at Gotify" \
				|| no "the test notification never arrived at Gotify (the panel answered $CODE)"
		fi
	fi
	if [ -z "$VERIFY_MATTERMOST_HOOK" ] || [ -z "$VERIFY_MATTERMOST_API" ] || [ -z "$VERIFY_MATTERMOST_TOKEN" ] ||
		[ -z "$VERIFY_MATTERMOST_CHANNEL" ]; then
		skip "Mattermost notifications" "no Mattermost server was given. Set VERIFY_MATTERMOST_HOOK, VERIFY_MATTERMOST_API, VERIFY_MATTERMOST_TOKEN and VERIFY_MATTERMOST_CHANNEL"
	else
		# Mattermost takes Slack's payload, attachments and all, which is what
		# the panel sends to both.
		MATTERMOST_CHANNEL=$(api POST "/api/teams/$TEAM_ID/notifications" "$(cat <<JSON
{"kind":"mattermost","name":"verify-mattermost","events":["deploy.failed"],
 "config":{"webhook_url":"$VERIFY_MATTERMOST_HOOK"}}
JSON
)" | pick id 2>/dev/null || echo "")
		if [ -z "$MATTERMOST_CHANNEL" ]; then
			no "a Mattermost notification channel could not be created"
		else
			CODE=$(api_code POST "/api/teams/$TEAM_ID/notifications/$MATTERMOST_CHANNEL/test")
			for _ in $(seq 1 10); do
				mattermost_has "$TEST_TITLE" && break
				sleep 2
			done
			mattermost_has "$TEST_TITLE" \
				&& ok "a test notification arrived in a Mattermost channel, through its incoming webhook" \
				|| no "the test notification never arrived at Mattermost (the panel answered $CODE)"
		fi
	fi
	if [ -n "$NTFY_CHANNEL$GOTIFY_CHANNEL$MATTERMOST_CHANNEL" ]; then
		LISTED=$(api GET "/api/teams/$TEAM_ID/notifications" 2>/dev/null || echo "")
		LEAKED=""
		for secret in "$VERIFY_NTFY_TOKEN" "$VERIFY_GOTIFY_TOKEN" "${VERIFY_MATTERMOST_HOOK##*/}"; do
			case "$LISTED" in *"$secret"*) [ -z "$secret" ] || LEAKED=1 ;; esac
		done
		if [ -z "$LISTED" ]; then
			no "the notification channels could not be listed"
		elif [ -n "$LEAKED" ]; then
			no "listing the channels gave back a token stored in one; a secret is never returned once set"
		else
			ok "listing the channels gives back none of the tokens stored in them"
		fi
	fi

	# And a real failure, which is the one that matters, to every channel at
	# once.
	if [ -n "$CHANNEL$MAIL_CHANNEL$NTFY_CHANNEL$GOTIFY_CHANNEL$MATTERMOST_CHANNEL" ]; then
		: >"$HOOK_LOG"
		FAILED_TITLE="Deploying broken failed"
		BROKEN=$(api POST "/api/environments/$ENV_ID/apps" \
			'{"name":"broken","source_type":"image","image":"example.invalid/nothing:0","port":8080,"deploy":true}' \
			| pick app.id 2>/dev/null || echo "")
		if [ -n "$BROKEN" ]; then
			# Long enough for the deployment to be given up on: the first run
			# on a real cluster sent this one five seconds after a five-minute
			# wait had ended.
			for _ in $(seq 1 120); do
				{ [ -z "$CHANNEL" ] || [ -s "$HOOK_LOG" ]; } &&
					{ [ -z "$MAIL_CHANNEL" ] || mail_has ops@example.test "$FAILED_TITLE"; } &&
					{ [ -z "$NTFY_CHANNEL" ] || ntfy_has "$NTFY_TOPIC" "$FAILED_TITLE"; } &&
					{ [ -z "$GOTIFY_CHANNEL" ] || gotify_has "$FAILED_TITLE"; } &&
					{ [ -z "$MATTERMOST_CHANNEL" ] || mattermost_has "$FAILED_TITLE"; } &&
					break
				sleep 5
			done
			UNTOLD="a deployment failed and nothing was sent. A failure nobody is told about is an outage that starts whenever somebody next looks."
			if [ -n "$CHANNEL" ]; then
				[ -s "$HOOK_LOG" ] \
					&& ok "a deployment that could not pull its image sent a notification" \
					|| no "$UNTOLD"
			fi
			if [ -n "$MAIL_CHANNEL" ]; then
				mail_has ops@example.test "$FAILED_TITLE" \
					&& ok "the same failure arrived by email" \
					|| no "by email, $UNTOLD"
			fi
			if [ -n "$NTFY_CHANNEL" ]; then
				ntfy_has "$NTFY_TOPIC" "$FAILED_TITLE" \
					&& ok "the same failure arrived at ntfy" \
					|| no "at ntfy, $UNTOLD"
			fi
			if [ -n "$GOTIFY_CHANNEL" ]; then
				gotify_has "$FAILED_TITLE" \
					&& ok "the same failure arrived at Gotify" \
					|| no "at Gotify, $UNTOLD"
			fi
			if [ -n "$MATTERMOST_CHANNEL" ]; then
				mattermost_has "$FAILED_TITLE" \
					&& ok "the same failure arrived at Mattermost" \
					|| no "at Mattermost, $UNTOLD"
			fi
			api DELETE "/api/apps/$BROKEN" >/dev/null 2>&1 || true
		fi
	fi
	for id in $MAIL_CHANNEL $NTFY_CHANNEL $GOTIFY_CHANNEL $MATTERMOST_CHANNEL; do
		api DELETE "/api/teams/$TEAM_ID/notifications/$id" >/dev/null 2>&1 || true
	done
	kill "$LISTENER" 2>/dev/null || true
fi

step "A forgotten password, reset through the link the panel emails"

# The panel's own mail, which is not a channel: the reset link goes through the
# server under Settings → Email, and the link is the only way in it gives. The
# member from phase 3 forgets their password.
if [ -z "${MAIL_SET:-}" ]; then
	skip "the password reset" "the panel was given no mail server above, so it has nowhere to send a link"
elif [ -z "${MEMBER_TOKEN:-}" ]; then
	skip "the password reset" "phase 3 did not run, so there is no member whose password to forget"
else
	SESSION=$(curl -s -o /dev/null -w '%{http_code}' -b "$MEMBER_COOKIE" "$PANEL/api/me")
	CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$PANEL/api/auth/password-reset" \
		-H 'Content-Type: application/json' -d '{"email":"member@example.test"}')
	LINK=""
	for _ in $(seq 1 30); do
		LINK=$(mail_text member@example.test "Reset your password" |
			grep -oE '[^[:space:]]*/reset-password#token=[A-Za-z0-9_-]+' | head -1 || true)
		[ -n "$LINK" ] && break
		sleep 2
	done
	if [ -z "$LINK" ]; then
		no "no reset link arrived for member@example.test (asking for one answered $CODE)"
	else
		ok "asking for a reset link sent one to the member's address"
		RESET="{\"token\":\"${LINK##*#token=}\",\"password\":\"a new member passphrase, after a reset\"}"
		CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$PANEL/api/auth/password-reset/confirm" \
			-H 'Content-Type: application/json' -d "$RESET")
		[ "$CODE" = "200" ] \
			&& ok "the link set a new password" \
			|| no "setting a new password through the link answered $CODE"
		NEW=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$PANEL/api/auth/login" -H 'Content-Type: application/json' \
			-d '{"email":"member@example.test","password":"a new member passphrase, after a reset"}')
		OLD=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$PANEL/api/auth/login" -H 'Content-Type: application/json' \
			-d '{"email":"member@example.test","password":"a reasonable member passphrase"}')
		[ "$NEW" = "200" ] && [ "$OLD" = "401" ] \
			&& ok "the member signs in with the new password, and not with the old one" \
			|| no "after the reset, the new password answered $NEW and the old one $OLD; want 200 and 401"
		if [ "$SESSION" = "200" ]; then
			CODE=$(curl -s -o /dev/null -w '%{http_code}' -b "$MEMBER_COOKIE" "$PANEL/api/me")
			[ "$CODE" = "401" ] \
				&& ok "the session the member had before the reset is over" \
				|| no "the member's session from before the reset still answers $CODE; a reset ends every session, in case the password was what leaked"
		fi
		CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$PANEL/api/auth/password-reset/confirm" \
			-H 'Content-Type: application/json' -d "$RESET")
		[ "$CODE" != "200" ] \
			&& ok "the link works once" \
			|| no "the same reset link set a password a second time"
	fi
fi

step "Changed outside the panel, and put back"

# What the panel applied is compared with what is in the cluster, on the fields
# the panel sets and nowhere else: the autoscaler, cert-manager and Traefik
# write fields of their own beside it, and a check that took those for
# somebody's change would cry wolf on every app. Until here those controllers
# were fakes.
if [ -z "$APP_ID" ]; then
	skip "the drift check" "phase 1 did not run, so there is no app to compare"
else
	drift_report() { api GET "/api/apps/$APP_ID/drift" 2>/dev/null || echo "{}"; }
	# drift_settled prints the status once nothing is being applied.
	drift_settled() {
		local status=""
		for _ in $(seq 1 24); do
			status=$(drift_report | pick status 2>/dev/null || echo "")
			[ -n "$status" ] && [ "$status" != "applying" ] && break
			sleep 5
		done
		printf '%s' "$status"
	}
	limit_now() {
		kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.spec.template.spec.containers[0].resources.limits.memory}' 2>/dev/null || true
	}
	STATUS=$(drift_settled)
	[ "$STATUS" = "in_sync" ] \
		&& ok "an app nobody changed is in step with what the panel applied, with the cluster's controllers writing beside it" \
		|| no "an app nobody changed is reported as '${STATUS:-nothing}': $(drift_report | head -c 400)"
	LIMIT=$(limit_now)
	kubectl -n "$NAMESPACE" patch deploy hello --type=json \
		-p '[{"op":"replace","path":"/spec/template/spec/containers/0/resources/limits/memory","value":"300Mi"}]' >/dev/null 2>>"$REPORT" || true
	STATUS=$(drift_settled)
	BY=$(drift_report | python3 -c '
import json, sys
print(" ".join(sorted({item.get("changed_by", "") for item in json.load(sys.stdin).get("items") or []} - {""})))
' 2>/dev/null || echo "")
	[ "$STATUS" = "drifted" ] \
		&& ok "a limit changed with kubectl is reported as changed, by ${BY:-somebody it could not name}" \
		|| no "a memory limit changed with kubectl was reported as '${STATUS:-nothing}'"
	if api POST "/api/apps/$APP_ID/drift/repair" >/dev/null; then
		STATUS=$(drift_settled)
		[ "$STATUS" = "in_sync" ] && [ "$(limit_now)" = "$LIMIT" ] \
			&& ok "putting the app back restored the limit the panel set, $LIMIT" \
			|| no "after putting the app back it is '${STATUS:-nothing}' with a limit of $(limit_now), not $LIMIT"
	else
		no "putting the app back was refused"
	fi
	kubectl -n "$NAMESPACE" rollout status deploy/hello --timeout=180s >/dev/null 2>&1 || true
fi

step "A notice in front of an app, for maintenance"

# The guard answers for an app in maintenance. Traefik's forward-auth gives the
# visitor whatever the guard answers that is not a 2xx, headers and body: that
# was its documentation, and this is the run.
if [ -z "$APP_HOST" ]; then
	skip "maintenance" "phase 1 did not run or the app was given no address"
elif ! api PUT "/api/apps/$APP_ID/maintenance" '{"message":"Back soon\nThe crosscheck is moving things around."}' >/dev/null; then
	no "maintenance could not be started"
else
	HEADERS=$(mktemp)
	CODE=""
	PAGE=""
	# The kubelet delivers the guard's changed rules within a minute or so.
	for _ in $(seq 1 36); do
		PAGE=$(curl -s -D "$HEADERS" --max-time 10 -H "Host: $APP_HOST" "http://127.0.0.1/" 2>/dev/null || true)
		CODE=$(awk 'NR == 1 {print $2}' "$HEADERS")
		[ "$CODE" = "503" ] && break
		sleep 5
	done
	if [ "$CODE" = "503" ] && printf '%s' "$PAGE" | grep -q "The crosscheck is moving things around" \
		&& grep -qi '^retry-after: *300' "$HEADERS"; then
		ok "a visitor was shown the notice, with 503 and when to come back, through the real Traefik"
	else
		no "in maintenance the app answered ${CODE:-nothing}, not the notice"
	fi
	if api DELETE "/api/apps/$APP_ID/maintenance" >/dev/null; then
		for _ in $(seq 1 36); do
			[ "$(through_ingress "$APP_HOST" 10)" = "200" ] && break
			sleep 5
		done
		[ "$(through_ingress "$APP_HOST" 10)" = "200" ] \
			&& ok "the app answers again once maintenance ends" \
			|| no "maintenance ended and the app still does not answer"
	else
		no "maintenance could not be ended"
	fi
fi

step "A worker, a command run once, and one on a schedule"

# Each runs an app's image with a command of its own, through a shell, the way
# a Procfile's line is. The sample app's image has no shell, so this is an app
# whose image has one.
SHELL_APP=$(api POST "/api/environments/$ENV_ID/apps" \
	"{\"name\":\"shell\",\"source_type\":\"image\",\"image\":\"$SHELL_IMAGE\",\"port\":8080,\"deploy\":true}" \
	| pick app.id 2>/dev/null || echo "")
if [ -z "$SHELL_APP" ]; then
	no "an app could not be created from $SHELL_IMAGE"
elif ! wait_deployed "$SHELL_APP"; then
	no "the app from $SHELL_IMAGE did not deploy"
else
	ok "an app from $SHELL_IMAGE is running"

	# A worker is a Deployment of its own, from the same image, with no port
	# and behind no Service.
	if api PUT "/api/apps/$SHELL_APP/processes/worker" \
		'{"command":"while true; do echo worker-alive; sleep 5; done","instances":1}' >/dev/null; then
		LOGS=""
		for _ in $(seq 1 24); do
			LOGS=$(kubectl -n "$NAMESPACE" logs deploy/shell--worker --tail=5 2>/dev/null || true)
			case "$LOGS" in *worker-alive*) break ;; esac
			sleep 5
		done
		case "$LOGS" in
		*worker-alive*) ok "a worker runs from the app's image with its own command" ;;
		*) no "the worker never printed anything: $(kubectl -n "$NAMESPACE" get pods -o wide 2>/dev/null | grep shell | tr '\n' ' ')" ;;
		esac
		ENDPOINTS=$(kubectl -n "$NAMESPACE" get endpoints shell -o jsonpath='{.subsets[*].addresses[*].targetRef.name}' 2>/dev/null || true)
		case "$ENDPOINTS" in
		*worker*) no "the worker is behind the app's Service, so visitors reach it: $ENDPOINTS" ;;
		"") no "the app's Service has no instance behind it" ;;
		*) ok "visitors reach the app's instances and never the worker" ;;
		esac
		if api DELETE "/api/apps/$SHELL_APP/processes/worker" >/dev/null; then
			for _ in $(seq 1 12); do
				kubectl -n "$NAMESPACE" get deploy shell--worker >/dev/null 2>&1 || break
				sleep 5
			done
			kubectl -n "$NAMESPACE" get deploy shell--worker >/dev/null 2>&1 \
				&& no "the worker was removed and its Deployment is still there" \
				|| ok "removing the worker removed its Deployment"
		else
			no "the worker could not be removed"
		fi
	else
		no "a worker process was refused"
	fi

	# A command run once, whose output and exit code come back: a migration
	# that failed has to say so.
	RUN=$(api POST "/api/apps/$SHELL_APP/run" '{"command":"echo ran-once; exit 3"}' | pick run 2>/dev/null || echo "")
	if [ -z "$RUN" ]; then
		no "a command could not be run in the app"
	else
		RESULT=""
		for _ in $(seq 1 24); do
			RESULT=$(api GET "/api/apps/$SHELL_APP/runs/$RUN/logs?follow=true" 2>/dev/null || echo "")
			printf '%s' "$RESULT" | python3 -c 'import json, sys; sys.exit(0 if json.load(sys.stdin).get("finished") else 1)' \
				2>/dev/null && break
			sleep 5
		done
		printf '%s' "$RESULT" | python3 -c '
import json, sys
run = json.load(sys.stdin)
sys.exit(0 if run.get("exit_code") == 3 and "ran-once" in run.get("lines", []) else 1)
' 2>/dev/null \
			&& ok "a command run once gave back its output and its exit code" \
			|| no "a command run once did not give back its output and exit code 3: $(printf '%s' "$RESULT" | head -c 300)"
	fi

	# And on a schedule, which Kubernetes runs rather than the panel.
	JOB=$(api POST "/api/apps/$SHELL_APP/jobs" '{"name":"tick","schedule":"* * * * *","command":"echo ticked"}' \
		| pick id 2>/dev/null || echo "")
	if [ -z "$JOB" ]; then
		no "a scheduled command was refused"
	else
		LAST=""
		for _ in $(seq 1 30); do
			LAST=$(kubectl -n "$NAMESPACE" get cronjob shell-job-tick -o jsonpath='{.status.lastSuccessfulTime}' 2>/dev/null || true)
			[ -n "$LAST" ] && break
			sleep 5
		done
		[ -n "$LAST" ] \
			&& ok "a command scheduled for every minute ran, and succeeded, at $LAST" \
			|| no "a command scheduled for every minute did not succeed within two and a half minutes"
		api DELETE "/api/apps/$SHELL_APP/jobs/$JOB" >/dev/null 2>&1 || true
	fi
fi

step "Logs shipped to a drain of the team's own"

# A collector on every server reads the pods' log files and sends each team's
# lines to that team's drains, from a namespace whose policy keeps it off the
# cluster's own networks. The drain is a listener on this machine; the lines
# are the nginx app's access log, for a path nobody else asks for.
DRAIN_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null || echo "")
SHELL_HOST=""
[ -z "$SHELL_APP" ] || SHELL_HOST=$(api GET "/api/apps/$SHELL_APP/domains" | pick items.0.hostname 2>/dev/null || echo "")
if [ -z "$SHELL_HOST" ] || [ -z "$DRAIN_IP" ]; then
	skip "the log drain" "the nginx app above has no address, or this node has no internal one"
else
	DRAIN_LOG=$(mktemp)
	python3 - "$DRAIN_LOG" >/dev/null 2>&1 <<'PYEOF' &
import http.server, sys
path = sys.argv[1]
class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("content-length", 0))
        with open(path, "ab") as f:
            f.write(self.rfile.read(length) + b"\n")
        self.send_response(200)
        self.end_headers()
    def log_message(self, *_):
        pass
http.server.HTTPServer(("0.0.0.0", 19098), Handler).serve_forever()
PYEOF
	DRAIN_LISTENER=$!
	sleep 2
	DRAIN=$(api POST "/api/teams/$TEAM_ID/log-drains" \
		"{\"name\":\"verify\",\"kind\":\"http\",\"settings\":{\"url\":\"http://$DRAIN_IP:19098/\"}}" | pick id 2>/dev/null || echo "")
	if [ -z "$DRAIN" ]; then
		no "a drain to a listener on this machine was refused"
	else
		[ -s "$DRAIN_LOG" ] \
			&& ok "the drain's test line arrived at the listener" \
			|| no "the drain was kept without its test line arriving"
		kubectl -n skifity-logs rollout status ds/skifity-log-collector --timeout=180s >/dev/null 2>&1 || true
		MARK="drain-check-$(date +%s)"
		for _ in $(seq 1 30); do
			curl -s -o /dev/null --max-time 5 -H "Host: $SHELL_HOST" "http://127.0.0.1/$MARK" || true
			grep -q "$MARK" "$DRAIN_LOG" && break
			sleep 4
		done
		grep -q "$MARK" "$DRAIN_LOG" \
			&& ok "an app's own log lines reached the drain, read from the server's pod logs by the collector" \
			|| no "nothing the app logged reached the drain in two minutes; the collector's own log is in the report"
		grep -q "$MARK" "$DRAIN_LOG" || kubectl -n skifity-logs logs ds/skifity-log-collector --tail=40 >>"$REPORT" 2>&1 || true
		api DELETE "/api/teams/$TEAM_ID/log-drains/$DRAIN" >/dev/null 2>&1 || true
	fi
	kill "$DRAIN_LISTENER" 2>/dev/null || true
fi

step "A preview of a branch: the whole environment, seeded"

# A preview is a copy of the environment for one branch, in a namespace of its
# own: the app built from the branch and, with "Previews copy the whole
# environment" on, every other app at the image it runs. A seed runs once in a
# new preview, after its first deploy, for the data an empty preview needs; it
# runs through a shell, so here it is the nginx app's. The branch is the one
# this run builds, since the repository has no other: what is checked is the
# preview, not what is different in it.
if [ -z "$APP_ID" ] || [ -z "$SHELL_APP" ]; then
	skip "a preview" "phase 1's app or the nginx app above is not there to copy"
else
	SEED_MARK="seeded-by-the-crosscheck"
	api PATCH "/api/environments/$ENV_ID" '{"preview_stack":true}' >/dev/null \
		|| no "the environment would not copy itself whole for its previews"
	api PATCH "/api/apps/$SHELL_APP" "{\"preview_seed\":\"echo $SEED_MARK\"}" >/dev/null \
		|| no "the nginx app would not take a seed for its previews"
	STARTED=$(api POST "/api/apps/$APP_ID/previews" "{\"branch\":\"$VERIFY_GIT_BRANCH\"}" 2>/dev/null || echo "")
	PREVIEW_ENV=$(printf '%s' "$STARTED" | pick environment_id 2>/dev/null || echo "")
	PREVIEW_APP=$(printf '%s' "$STARTED" | pick app_id 2>/dev/null || echo "")
	if [ -z "$PREVIEW_ENV" ] || [ -z "$PREVIEW_APP" ]; then
		no "a preview of $VERIFY_GIT_BRANCH could not be started: ${STARTED:-no answer}"
	else
		PREVIEW_NS=$(api GET "/api/environments/$PREVIEW_ENV" | pick namespace 2>/dev/null || echo "")
		if wait_deployed "$PREVIEW_APP"; then
			PREVIEW_HOST=$(api GET "/api/apps/$PREVIEW_APP/domains" | pick items.0.hostname 2>/dev/null || echo "")
			[ -n "$PREVIEW_HOST" ] && [ "$(through_ingress "$PREVIEW_HOST" 30)" = "200" ] \
				&& ok "the preview of $VERIFY_GIT_BRANCH was built, deployed in a namespace of its own, and answers at $PREVIEW_HOST" \
				|| no "the preview deployed and does not answer at ${PREVIEW_HOST:-no address}"
		else
			no "the preview of $VERIFY_GIT_BRANCH did not deploy"
		fi
		COPY=$(api GET "/api/environments/$PREVIEW_ENV/apps" | python3 -c '
import json, sys
for app in json.load(sys.stdin).get("items") or []:
    if app.get("slug") == "shell":
        print(app["id"])
' 2>/dev/null || echo "")
		if [ -z "$COPY" ]; then
			no "the preview has no copy of the nginx app, with the whole environment asked for"
		else
			wait_deployed "$COPY" || true
			SEED_LOG=""
			for _ in $(seq 1 24); do
				DEPLOYED=$(api GET "/api/apps/$COPY/deployments" | pick items.0.id 2>/dev/null || echo "")
				SEED_LOG=$(api GET "/api/apps/$COPY/deployments/$DEPLOYED/logs" 2>/dev/null || echo "")
				case "$SEED_LOG" in *"The seed command finished."*) break ;; esac
				sleep 5
			done
			# The mark twice: once in the command the log says it runs, once in
			# what the command printed.
			MARKS=$(printf '%s' "$SEED_LOG" | grep -o "$SEED_MARK" | wc -l || true)
			case "$SEED_LOG" in
			*"The seed command finished."*)
				[ "$MARKS" -ge 2 ] \
					&& ok "the preview's copy of the nginx app ran at its image and was seeded once, after its first deploy, with the seed's output in its log" \
					|| no "the seed finished without its output in the deployment's log" ;;
			*) no "the preview's copy of the nginx app was not seeded: $(printf '%s' "$SEED_LOG" | tail -c 400)" ;;
			esac
		fi
		api POST "/api/apps/$APP_ID/previews/close" "{\"branch\":\"$VERIFY_GIT_BRANCH\"}" >/dev/null \
			|| no "the preview could not be closed"
		CLOSED=""
		for _ in $(seq 1 36); do
			[ -n "$PREVIEW_NS" ] && ! kubectl get namespace "$PREVIEW_NS" >/dev/null 2>&1 && { CLOSED=1; break; }
			sleep 5
		done
		[ -n "$CLOSED" ] \
			&& ok "closing the preview removed its namespace and everything in it" \
			|| no "the preview's namespace ${PREVIEW_NS:-unknown} was still there three minutes after it was closed"
	fi
	api PATCH "/api/environments/$ENV_ID" '{"preview_stack":false}' >/dev/null 2>&1 || true
fi
[ -z "$SHELL_APP" ] || api DELETE "/api/apps/$SHELL_APP" >/dev/null 2>&1 || true

step "A Compose file, as one stack"

# Two services: one published, as a port in Compose is, and one only the
# other reaches, by its name, the way it does in Compose.
STACK=$(api POST "/api/environments/$ENV_ID/stack" "$(cat <<JSON
{"deploy":true,"services":[
 {"name":"front","image":"$SHELL_IMAGE","ports":[8080],"depends_on":["back"]},
 {"name":"back","image":"$SHELL_IMAGE","expose":[8080]}]}
JSON
)" 2>/dev/null || echo "")
FRONT=$(printf '%s' "$STACK" | python3 -c '
import json, sys
for app in json.load(sys.stdin).get("apps") or []:
    print(app["id"], app["slug"])
' 2>/dev/null || true)
FRONT_ID=$(printf '%s\n' "$FRONT" | awk '$2 == "front" {print $1}')
BACK_ID=$(printf '%s\n' "$FRONT" | awk '$2 == "back" {print $1}')
if [ -z "$FRONT_ID" ] || [ -z "$BACK_ID" ]; then
	no "the stack was refused: $(printf '%s' "$STACK" | head -c 300)"
elif ! wait_deployed "$BACK_ID" || ! wait_deployed "$FRONT_ID"; then
	no "the stack's two apps did not both deploy"
else
	ok "a stack of two services was created and deployed together"
	REACHED=$(kubectl -n "$NAMESPACE" exec deploy/front -c front -- wget -qO- -T 10 http://back:8080/ 2>/dev/null || true)
	case "$REACHED" in
	*nginx*) ok "one service reaches the other by its name and port, as in Compose" ;;
	*) no "front could not reach back at http://back:8080/" ;;
	esac
	FRONT_HOST=$(api GET "/api/apps/$FRONT_ID/domains" | pick items.0.hostname 2>/dev/null || echo "")
	BACK_HOSTS=$(api GET "/api/apps/$BACK_ID/domains" | pick total 2>/dev/null || echo "")
	[ -n "$FRONT_HOST" ] && [ "$(through_ingress "$FRONT_HOST" 10)" = "200" ] \
		&& ok "the published service answers at its address" \
		|| no "the published service has no address that answers"
	[ "${BACK_HOSTS:-1}" = "0" ] \
		&& ok "the service nothing published has no address at all" \
		|| no "a service only the other reaches was given $BACK_HOSTS addresses"
fi
for id in $FRONT_ID $BACK_ID; do
	api DELETE "/api/apps/$id" >/dev/null 2>&1 || true
done

step "One click from the catalogue"

# Homer: a page and a disk, which it writes its example settings into on its
# first start, as whatever user its image runs as.
TEMPLATE=$(api POST /api/templates/homer/install "{\"environment_id\":\"$ENV_ID\"}" 2>/dev/null || echo "")
HOMER_ID=$(printf '%s' "$TEMPLATE" | pick apps.0.id 2>/dev/null || echo "")
if [ -z "$HOMER_ID" ]; then
	no "the Homer template could not be installed: $(printf '%s' "$TEMPLATE" | head -c 300)"
elif ! wait_deployed "$HOMER_ID"; then
	no "the app the Homer template made did not deploy"
else
	HOMER_HOST=$(api GET "/api/apps/$HOMER_ID/domains" | pick items.0.hostname 2>/dev/null || echo "")
	HOMER_PAGE=$(curl -s --max-time 10 -H "Host: $HOMER_HOST" "http://127.0.0.1/" 2>/dev/null || true)
	case "$HOMER_PAGE" in
	*[Hh]omer*) ok "Homer, installed from the catalogue, answers at its address" ;;
	*) no "Homer's address does not answer with Homer" ;;
	esac
	CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 -H "Host: $HOMER_HOST" "http://127.0.0.1/assets/config.yml")
	[ "$CODE" = "200" ] \
		&& ok "Homer wrote its settings to its disk, as the user its image runs as" \
		|| no "Homer's settings file is not on its disk ($CODE); the disk may not be writable by the image's user"
fi
[ -z "$HOMER_ID" ] || api DELETE "/api/apps/$HOMER_ID" >/dev/null 2>&1 || true

step "Promoting a version to another environment"

# The version phase 1 built, run in a second environment of the same project
# without building it again: the image another namespace pulls.
STAGING_ID=$(api POST "/api/projects/$PROJECT_ID/environments" '{"name":"staging"}' | pick id 2>/dev/null || echo "")
SOURCE_DEPLOY=$(api GET "/api/apps/$APP_ID/deployments" 2>/dev/null | python3 -c '
import json, sys
for deployment in json.load(sys.stdin).get("items") or []:
    if deployment.get("status") == "succeeded" and deployment.get("image"):
        print(deployment["id"], deployment["image"])
        break
' 2>/dev/null || true)
if [ -z "$APP_ID" ] || [ -z "$STAGING_ID" ] || [ -z "$SOURCE_DEPLOY" ]; then
	no "there is no second environment or no version to promote (environment ${STAGING_ID:-missing})"
else
	PROMOTED=$(api POST "/api/environments/$STAGING_ID/apps" "$(cat <<JSON
{"name":"hello","source_type":"git","repo_url":"$VERIFY_GIT_REPO","branch":"$VERIFY_GIT_BRANCH",
 "root_dir":"$VERIFY_GIT_ROOT","port":$VERIFY_APP_PORT,"health_path":"/healthz","deploy":false}
JSON
)" | pick app.id 2>/dev/null || echo "")
	if [ -z "$PROMOTED" ]; then
		no "the app could not be made in the second environment"
	elif ! api POST "/api/apps/$PROMOTED/promote" "{\"deployment_id\":\"${SOURCE_DEPLOY%% *}\"}" >/dev/null; then
		no "promoting phase 1's version was refused"
	elif ! wait_deployed "$PROMOTED"; then
		no "the promoted version did not deploy in the second environment"
	else
		IMAGE=$(api GET "/api/apps/$PROMOTED/deployments" | pick items.0.image 2>/dev/null || echo "")
		[ "$IMAGE" = "${SOURCE_DEPLOY#* }" ] \
			&& ok "the second environment runs the very image phase 1 built, with nothing built again" \
			|| no "the promoted version runs $IMAGE, not ${SOURCE_DEPLOY#* }"
		PROMOTED_HOST=$(api GET "/api/apps/$PROMOTED/domains" | pick items.0.hostname 2>/dev/null || echo "")
		[ -n "$PROMOTED_HOST" ] && [ "$(through_ingress "$PROMOTED_HOST" 10)" = "200" ] \
			&& ok "it answers there, from another namespace" \
			|| no "the promoted version does not answer at its address"
	fi
	[ -z "$PROMOTED" ] || api DELETE "/api/apps/$PROMOTED" >/dev/null 2>&1 || true
fi
[ -z "$STAGING_ID" ] || api DELETE "/api/environments/$STAGING_ID" >/dev/null 2>&1 || true

step "A database reached from this machine, through the panel"

# skifity db connect asks the panel to upgrade a request into a stream to the
# database. Traefik passes an upgrade of any protocol through, which was its
# documentation; this goes through it, and then the way a person does it.
if [ -z "${DB_ID:-}" ]; then
	skip "the database tunnel" "phase 2 did not run, so there is no database"
else
	# PostgreSQL's SSLRequest, answered with S or N by PostgreSQL alone.
	python3 - "$DOMAIN" "/api/databases/$DB_ID/tunnel" "$TOKEN" >>"$REPORT" 2>&1 <<'PYEOF' \
		&& ok "a tunnel opened through Traefik in front of the panel, and PostgreSQL answered at its end" \
		|| no "no tunnel opened through Traefik to the database; the report has what came back"
import socket, ssl, sys
host, path, token = sys.argv[1:4]

def tunnel(port):
    conn = socket.create_connection(("127.0.0.1", port), timeout=20)
    if port == 443:
        # Traefik's own certificate, or one from Let's Encrypt's staging
        # server: what is checked here is the route, not the certificate.
        context = ssl.create_default_context()
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE
        conn = context.wrap_socket(conn, server_hostname=host)
    conn.sendall((f"POST {path} HTTP/1.1\r\nHost: {host}\r\nAuthorization: Bearer {token}\r\n"
                  "Connection: Upgrade\r\nUpgrade: skifity-db-tunnel\r\nContent-Length: 0\r\n\r\n").encode())
    head = b""
    while b"\r\n\r\n" not in head:
        chunk = conn.recv(1)
        if not chunk:
            break
        head += chunk
    status = head.split(b"\r\n", 1)[0]
    print(f"  the tunnel through port {port} answered:", status.decode(errors="replace"))
    if b" 101 " not in status:
        return False
    conn.sendall(bytes([0, 0, 0, 8, 4, 210, 22, 47]))
    answer = conn.recv(1)
    print("  PostgreSQL answered:", answer)
    return answer in (b"S", b"N")

def tried(port):
    try:
        return tunnel(port)
    except OSError as err:
        print(f"  the tunnel through port {port} failed: {err}")
        return False

sys.exit(0 if tried(80) or tried(443) else 1)
PYEOF
	if [ ! -x /usr/local/bin/skifity ]; then
		skip "skifity db connect" "the installer put no CLI at /usr/local/bin/skifity"
	else
		TUNNEL_OUT=$(mktemp)
		SKIFITY_URL="$PANEL" SKIFITY_TOKEN="$TOKEN" /usr/local/bin/skifity db connect verifydb \
			--env "$ENV_ID" --port 15432 --json >"$TUNNEL_OUT" 2>&1 &
		TUNNEL_PID=$!
		for _ in $(seq 1 20); do
			grep -q '"url"' "$TUNNEL_OUT" 2>/dev/null && break
			sleep 1
		done
		DB_URL=$(pick url <"$TUNNEL_OUT" 2>/dev/null || echo "")
		if [ -z "$DB_URL" ]; then
			no "skifity db connect did not open a port: $(head -c 300 "$TUNNEL_OUT")"
		elif command -v psql >/dev/null 2>&1; then
			ANSWER=$(psql "$DB_URL" -tAc 'select 6 * 7' 2>>"$REPORT" || true)
			[ "$ANSWER" = "42" ] \
				&& ok "skifity db connect gave psql on this machine the database, and it answered" \
				|| no "psql through skifity db connect answered '${ANSWER}', not 42"
		else
			python3 -c '
import socket, sys
conn = socket.create_connection(("127.0.0.1", 15432), timeout=20)
conn.sendall(bytes([0, 0, 0, 8, 4, 210, 22, 47]))
sys.exit(0 if conn.recv(1) in (b"S", b"N") else 1)
' && ok "skifity db connect opened a port on this machine that PostgreSQL answers" \
				|| no "the port skifity db connect opened does not reach PostgreSQL"
		fi
		kill "$TUNNEL_PID" 2>/dev/null || true
	fi
fi

step "An app that sleeps wakes for a visitor and for its own deploy"

# Scale to zero turns an app down to no instances when nobody uses it, and KEDA's
# interceptor wakes it for the next request. A rollout of no instances finishes
# the moment it starts, so every deploy of a sleeping app used to succeed,
# whatever it deployed; a deploy now wakes it first. The app is put to sleep by
# hand rather than by waiting out the five quiet minutes: KEDA leaves an app at
# no instances until a request asks for it, whoever put it there.
if [ -z "$APP_HOST" ]; then
	skip "scale to zero" "phase 1 did not run or the app was given no address, and a sleeping app is woken by its address"
elif ! api POST /api/components/keda/install >/dev/null 2>&1; then
	no "the scale-to-zero component could not be installed"
else
	ok "the scale-to-zero component installed"
	api PUT "/api/apps/$APP_ID/scaling" '{"scale_to_zero":true,"autoscale":false,"replicas":1}' >/dev/null \
		|| no "scale to zero could not be switched on"
	# Asked again until it stays asleep: for as long as the request that woke
	# it is inside KEDA's window, a minute, KEDA counts the app as in use and
	# puts its instance straight back, which is what it is for. The second
	# time, right after a visitor, that was every attempt for two minutes on a
	# fast run. Still at none after one of KEDA's polls, half a minute, it
	# stays there.
	sleep_app() {
		for _ in $(seq 1 10); do
			kubectl -n "$NAMESPACE" scale deploy hello --replicas=0 >/dev/null 2>&1 || return 1
			for _ in $(seq 1 15); do
				[ -z "$(kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/name=hello -o name 2>/dev/null)" ] && break
				sleep 2
			done
			sleep 35
			[ "$(kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.spec.replicas}' 2>/dev/null)" = "0" ] &&
				[ -z "$(kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/name=hello -o name 2>/dev/null)" ] &&
				return 0
		done
		return 1
	}
	for _ in $(seq 1 30); do
		kubectl -n "$NAMESPACE" get httpscaledobject hello >/dev/null 2>&1 && break
		sleep 2
	done
	if ! sleep_app; then
		no "the app could not be put to sleep to try waking it"
	else
		WOKE=""
		for _ in $(seq 1 12); do
			[ "$(through_ingress "$APP_HOST" 20)" = "200" ] && { WOKE=1; break; }
			sleep 5
		done
		[ -n "$WOKE" ] \
			&& ok "a visitor's request woke the sleeping app and was answered" \
			|| no "a request to a sleeping app's address was never answered; the interceptor did not wake it"
	fi
	if ! sleep_app; then
		no "the app could not be put to sleep again to deploy it"
	else
		SLEPT_DEPLOY=$(api POST "/api/apps/$APP_ID/deploy" | pick id 2>/dev/null || echo "")
		STATE=""
		for _ in $(seq 1 60); do
			STATE=$(api GET "/api/apps/$APP_ID/deployments/$SLEPT_DEPLOY" | pick status 2>/dev/null || echo "")
			case "$STATE" in succeeded | failed) break ;; esac
			sleep 10
		done
		SLEPT_LOG=$(api GET "/api/apps/$APP_ID/deployments/$SLEPT_DEPLOY/logs" 2>/dev/null || echo "")
		READY=$(kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
		if [ "$STATE" = "succeeded" ] && printf '%s' "$SLEPT_LOG" | grep -q "is asleep, so it is woken" && [ "${READY:-0}" -ge 1 ] 2>/dev/null; then
			ok "a deploy of the sleeping app woke it, and succeeded once the new version was ready"
		else
			no "a deploy of the sleeping app ended '${STATE:-unknown}' with ${READY:-0} instance(s) ready, and its log $(printf '%s' "$SLEPT_LOG" | grep -q 'is asleep' && echo says || echo does not say) it was woken"
		fi
	fi
	# And switched off while asleep, which is when it matters: the app has to
	# be given its instance back, and answer without anything to wake it. The
	# add-on, on its way out, used to scale it to none after the panel had
	# given it its size, and nothing scaled it up again.
	if ! sleep_app; then
		no "the app could not be put to sleep before switching scale to zero off"
	else
		api PUT "/api/apps/$APP_ID/scaling" '{"scale_to_zero":false,"autoscale":false,"replicas":1}' >/dev/null \
			|| no "scale to zero could not be switched off"
		BACK=""
		for _ in $(seq 1 24); do
			if [ "$(kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.status.readyReplicas}' 2>/dev/null)" = "1" ] &&
				[ "$(through_ingress "$APP_HOST" 10)" = "200" ]; then
				BACK=1
				break
			fi
			sleep 5
		done
		[ -n "$BACK" ] \
			&& ok "switching scale to zero off gave the sleeping app its instance back, and it answers" \
			|| no "two minutes after scale to zero was switched off the app has $(kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.spec.replicas}' 2>/dev/null || echo '?') instance(s) and does not answer"
	fi
fi
fi

if [ -n "$SECOND_ID" ] && [ -n "$VERIFY_SECOND_SERVER_FAILS" ]; then
	step "A server that fails without warning (checklist 14)"

	# Two instances, so the app has one left when a server goes; the spread
	# puts them on both servers when it can.
	if [ -n "$APP_ID" ]; then
		api PUT "/api/apps/$APP_ID/scaling" '{"replicas":2,"autoscale":false,"scale_to_zero":false}' >/dev/null 2>&1 \
			|| no "the app could not be given two instances"
		on_second() {
			kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/name=hello \
				-o jsonpath='{range .items[*]}{.spec.nodeName}{"\n"}{end}' 2>/dev/null | grep -cx "$SECOND_NODE" || true
		}
		two_ready() {
			for _ in $(seq 1 36); do
				[ "$(kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)" = "2" ] && return 0
				sleep 5
			done
			return 1
		}
		two_ready || true
		# Phase 4 emptied the second server, so both are likely on the first.
		# A restart is a rollout, and a rollout has to leave the new version's
		# instances on both servers: counted together with the old version's,
		# they were spread against instances about to go, and a run once put
		# both on the server that then failed.
		api POST "/api/apps/$APP_ID/restart" >/dev/null 2>&1 || true
		sleep 5
		kubectl -n "$NAMESPACE" rollout status deploy/hello --timeout=180s >/dev/null 2>&1 || true
		two_ready || true
		ON_SECOND=$(on_second)
		[ "$ON_SECOND" = "1" ] \
			&& ok "a restart left one of the app's two instances on each server" \
			|| no "a restart left $ON_SECOND of the app's two instances on the second server, where one was wanted"
	fi

	kill -9 "$(cat "$VERIFY_SECOND_SERVER_FAILS")" 2>>"$REPORT" \
		|| die "the second server's process could not be killed; VERIFY_SECOND_SERVER_FAILS has to name it"
	FAILED_AT=$(date +%s)
	ok "the second server's machine was switched off without warning"

	# The cluster finds out when the server stops reporting: the node is
	# Unknown, its instances stop being given visitors, and until then some
	# requests go to an instance that is not there any more.
	ASKED=0
	MISSED=0
	NOTICED=""
	while [ $(($(date +%s) - FAILED_AT)) -lt 300 ]; do
		if [ -n "$APP_HOST" ]; then
			ASKED=$((ASKED + 1))
			[ "$(through_ingress "$APP_HOST" 5)" = "200" ] || MISSED=$((MISSED + 1))
		fi
		if [ "$(kubectl get node "$SECOND_NODE" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)" = "Unknown" ]; then
			NOTICED=$(($(date +%s) - FAILED_AT))
			break
		fi
		sleep 1
	done
	if [ -n "$NOTICED" ]; then
		ok "the cluster knew the server was gone after ${NOTICED}s"
	else
		no "five minutes after the second server stopped, the cluster still did not say so"
	fi
	[ -z "$APP_HOST" ] || say "  until then, $MISSED of $ASKED requests through the first server went unanswered"

	# Once it knows, the instance that was there is out of the app's
	# addresses, and every request is answered by the one that is left. A
	# few seconds for the ingress to hear it.
	if [ -n "$APP_HOST" ] && [ -n "$NOTICED" ]; then
		sleep 10
		AFTER_FAILS=0
		for _ in $(seq 1 20); do
			[ "$(through_ingress "$APP_HOST" 5)" = "200" ] || AFTER_FAILS=$((AFTER_FAILS + 1))
			sleep 1
		done
		[ "$AFTER_FAILS" = "0" ] \
			&& ok "from then on the app answered every request, from the instance it had left" \
			|| no "$AFTER_FAILS of 20 requests failed after the cluster knew the server was gone"
	fi

	# The panel's own watch, which is what sends "stopped answering".
	SERVER_STATE=""
	for _ in $(seq 1 36); do
		SERVER_STATE=$(api GET "/api/servers/$SECOND_ID" | pick status 2>/dev/null || echo "")
		[ "$SERVER_STATE" = "not_ready" ] && break
		sleep 5
	done
	[ "$SERVER_STATE" = "not_ready" ] \
		&& ok "the panel marked the server as not responding, $(($(date +%s) - FAILED_AT))s after it stopped" \
		|| no "the panel still shows the second server as '${SERVER_STATE:-unknown}' three minutes after it stopped"

	# And taken out, the way somebody takes out a server that is gone: it
	# cannot be emptied, so it is not waited on, and what was on it starts on
	# the server that is left.
	REMOVE_STARTED=$(date +%s)
	api DELETE "/api/servers/$SECOND_ID" >/dev/null 2>&1 || no "the panel refused to remove the server that had gone"
	GONE=""
	for _ in $(seq 1 60); do
		[ "$(api_code GET "/api/servers/$SECOND_ID")" = "404" ] && { GONE=1; break; }
		[ "$(api GET "/api/servers/$SECOND_ID" 2>/dev/null | pick status 2>/dev/null || echo "")" = "failed" ] && break
		sleep 5
	done
	if [ -n "$GONE" ]; then
		ok "the panel removed the server that had gone, in $(($(date +%s) - REMOVE_STARTED))s"
	else
		api GET "/api/servers/$SECOND_ID" >>"$REPORT" 2>&1 || true
		no "the server that had gone could not be removed; the operation's steps are above in $REPORT"
	fi
	kubectl get node "$SECOND_NODE" >/dev/null 2>&1 \
		&& no "the cluster still lists $SECOND_NODE after the server was removed" \
		|| ok "and the cluster no longer lists it"

	if [ -n "$APP_ID" ]; then
		READY=""
		for _ in $(seq 1 36); do
			READY=$(kubectl -n "$NAMESPACE" get deploy hello -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
			[ "$READY" = "2" ] && break
			sleep 5
		done
		[ "$READY" = "2" ] \
			&& ok "the app is back to two instances, on the server that is left, $(($(date +%s) - FAILED_AT))s after the other one stopped" \
			|| no "the app has ${READY:-0} of its two instances ready three minutes after the server was removed"
	fi
elif [ -n "$SECOND_ID" ]; then
	step "Taking the second server out, through the panel"

	# A server whose own disk holds a volume is refused, naming it: removing
	# the server does not move the data. Which server a volume is made on is
	# the scheduler's choice, so whether the second one holds any by now
	# depends on the run. When it does, the refusal is checked, and the
	# server is then removed the way the refusal says to, accepting the loss.
	# What is asked for an answer meanwhile is an app that can move: the
	# phase 4 app with an instance on each server where there is one, or the
	# phase 1 app where its volume is not on the server going away.
	PROBE=$MOVER_HOST
	[ -n "$PROBE" ] || [ "$(volume_node hello-data)" = "$SECOND_NODE" ] || PROBE=$APP_HOST
	# Asked twice a second from before the removal is asked for until after
	# it has finished, each answer written down with its time, so a failure
	# says when it happened and what it was.
	PROBE_LOG=$(mktemp)
	PROBING=""
	if [ -n "$PROBE" ]; then
		(
			while :; do
				printf '%s %s\n' "$(date +%T.%N | cut -c1-12)" "$(through_ingress "$PROBE" 5)"
				sleep 0.5
			done
		) >"$PROBE_LOG" 2>&1 &
		PROBING=$!
		sleep 2
	fi
	# What was on the server as it went, for the report.
	kubectl get pods -A -o wide --field-selector "spec.nodeName=$SECOND_NODE" >>"$REPORT" 2>&1 || true
	REFUSAL=$(mktemp)
	CODE=$(curl -s -o "$REFUSAL" -w '%{http_code}' -X DELETE "$PANEL/api/servers/$SECOND_ID" -H "Authorization: Bearer $TOKEN" || true)
	if [ "$CODE" = "409" ] && grep -q server.has_local_volumes "$REFUSAL"; then
		HELD=$(pick error.context.volumes <"$REFUSAL" 2>/dev/null || echo "")
		[ -n "$HELD" ] \
			&& ok "the panel refused to remove a server whose own disk holds data, and named it: $HELD" \
			|| no "the panel refused to remove a server that holds data without saying which: $(head -c 300 "$REFUSAL")"
		CODE=$(api_code DELETE "/api/servers/$SECOND_ID?accept_data_loss=true")
	fi
	case "$CODE" in
	2??) ;;
	*) no "the panel refused to remove the second server ($CODE): $(head -c 300 "$REFUSAL")" ;;
	esac
	GONE=""
	for _ in $(seq 1 60); do
		CODE=$(api_code GET "/api/servers/$SECOND_ID")
		[ "$CODE" = "404" ] && { GONE=1; break; }
		[ "$(api GET "/api/servers/$SECOND_ID" 2>/dev/null | pick status 2>/dev/null || echo "")" = "failed" ] && break
		sleep 5
	done
	if [ -n "$GONE" ]; then
		ok "the second server was drained and removed"
	else
		api GET "/api/servers/$SECOND_ID" >>"$REPORT" 2>&1 || true
		no "the second server was not removed; the operation's steps are above in $REPORT"
	fi
	kubectl get node "$SECOND_NODE" >/dev/null 2>&1 \
		&& no "the cluster still lists $SECOND_NODE after the server was removed" \
		|| ok "and the cluster no longer lists it"
	if [ -n "$PROBING" ]; then
		sleep 10
		kill "$PROBING" 2>/dev/null || true
		wait "$PROBING" 2>/dev/null || true
		ASKED=$(wc -l <"$PROBE_LOG" | tr -d ' ')
		MISSED=$(awk '$2 != "200"' "$PROBE_LOG")
		if [ -z "$MISSED" ]; then
			ok "an app with nothing on that server's disk answered all $ASKED requests while the server was taken away"
		else
			no "$(printf '%s\n' "$MISSED" | wc -l | tr -d ' ') of $ASKED requests failed while the second server was taken away: $(printf '%s' "$MISSED" | tr '\n' ' ')"
		fi
	fi
fi

# --- what it costs ---------------------------------------------------------

phase "What it costs, measured"

sleep 20
K3S_MB=$(ps -o rss= -C k3s-server 2>/dev/null | awk '{s+=$1} END {print int(s/1024)}')
# The panel's own labels. A selector that matches nothing leaves the name
# empty, and `kubectl top pod ""` lists every pod in the namespace: the first
# run reported the panel as "2" and "16" on two lines, which were an ACME
# solver's memory and the panel's.
PANEL_POD=$(kubectl -n "$PANEL_NS" get pod -l app.kubernetes.io/name=skifity,app.kubernetes.io/component=panel \
	-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
PANEL_MB=""
[ -n "$PANEL_POD" ] && PANEL_MB=$(kubectl -n "$PANEL_NS" top pod "$PANEL_POD" --no-headers 2>/dev/null | awk 'NR == 1 {print $3}' | tr -d 'Mi')
USED_MB=$(free -m | awk '/^Mem:/ {print $3}')

say "  before anything was installed:"
say "    the whole machine  : ${BASE_USED_MB:-unknown} MB of ${MEM_MB} MB in use"
say "  just after the install, with nothing deployed:"
say "    k3s server process : ${FRESH_K3S_MB:-unknown} MB"
say "    the whole machine  : ${FRESH_USED_MB:-unknown} MB in use"
say "  at the end, with everything this run turned on still running:"
say "    k3s server process : ${K3S_MB:-unknown} MB"
say "    the panel pod      : ${PANEL_MB:-unknown} MB"
say "    the whole machine  : ${USED_MB:-unknown} MB in use"
say ""
say "  All in MiB. docs/performance.md has these from earlier runs; if they"
say "  disagree, the document is what changes."

summary
[ "$FAIL" -eq 0 ]
