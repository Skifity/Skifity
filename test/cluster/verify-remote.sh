#!/usr/bin/env bash
# Run the crosscheck on a server that is not this one.
#
# test/cluster/verify.sh installs k3s and changes the machine it runs on, which
# is why nobody runs it: it needs a throwaway server, a way to get an image onto
# that server, and the repository beside it. This does those three things and
# then runs it.
#
#   make verify-remote HOST=root@203.0.113.10
#
# What it does, in order: builds the panel image for the server's architecture,
# streams it into the directory k3s imports from before k3s exists, copies this
# checkout across, runs verify.sh, and brings the report back. Nothing is pushed
# to a registry and no credentials are needed — the image never leaves the two
# machines.
#
#   THE SERVER IS CHANGED PERMANENTLY. Use one you are willing to rebuild.
#
# DRY_RUN=1 prints every command instead of running it, which is how this is
# checked on a machine with no server and no Docker.
set -euo pipefail

HOST="${HOST:-}"
SSH_OPTS="${SSH_OPTS:--o StrictHostKeyChecking=accept-new}"
REMOTE_DIR="${REMOTE_DIR:-/root/skifity-verify}"
REPORT_OUT="${REPORT_OUT:-verify-report.log}"
DRY_RUN="${DRY_RUN:-0}"
: "${SKIFITY_PHASES:=1 2 3 4 5}"
: "${SKIFITY_DOMAIN:=}"
: "${SKIFITY_ASSUME_YES:=0}"

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

say() { printf '%s\n' "$*"; }
die() {
	printf '\n%s\n\n' "$*" >&2
	exit 1
}

# Every command that touches the network or the server goes through this, so
# DRY_RUN is a property of the script rather than something remembered at each
# call site.
run() {
	if [ "$DRY_RUN" = "1" ]; then
		printf '  would run: %s\n' "$*"
		return 0
	fi
	"$@"
}

# The same, for a pipeline that cannot be passed as an argument list.
run_sh() {
	if [ "$DRY_RUN" = "1" ]; then
		printf '  would run: %s\n' "$1"
		return 0
	fi
	sh -c "$1"
}

[ -n "$HOST" ] || die "Set HOST to the server to verify on.

  make verify-remote HOST=root@203.0.113.10

It has to be a server you are willing to rebuild: this installs k3s on it and
changes it permanently. Root, or a user who can sudo without a password."

# Under DRY_RUN nothing is run, so neither has to be here: the point of the dry
# run is to check this script on a machine that has no server and no Docker.
if [ "$DRY_RUN" != "1" ]; then
	command -v ssh >/dev/null 2>&1 || die "ssh is not installed, and this needs it."
	command -v docker >/dev/null 2>&1 || die "docker is not installed, and the image is built here before it is sent."
fi

# --- what is about to happen -----------------------------------------------

say ""
say "This will install k3s on ${HOST} and change it permanently."
say "Phases: ${SKIFITY_PHASES}"
say ""

if [ "$SKIFITY_ASSUME_YES" != "1" ] && [ "$DRY_RUN" != "1" ]; then
	printf 'Type yes to continue: '
	read -r answer
	[ "$answer" = "yes" ] || die "Stopped. Nothing was changed."
fi

# --- the server's architecture ---------------------------------------------

# An image built for the wrong architecture starts and immediately dies with
# "exec format error", which is a confusing way to spend an afternoon. Ask the
# server rather than assuming it matches this laptop.
if [ "$DRY_RUN" = "1" ]; then
	REMOTE_ARCH="x86_64"
else
	# SSH_OPTS holds several options and has to split into several words.
	# Quoting it would pass the lot as one argument, which ssh rejects.
	# shellcheck disable=SC2086
	REMOTE_ARCH="$(ssh $SSH_OPTS "$HOST" 'uname -m')"
fi
case "$REMOTE_ARCH" in
x86_64 | amd64) PLATFORM="linux/amd64" ;;
aarch64 | arm64) PLATFORM="linux/arm64" ;;
*) die "This does not know how to build for ${REMOTE_ARCH}." ;;
esac
say "The server is ${REMOTE_ARCH}, so the image is built for ${PLATFORM}."

IMAGE="$(make -C "$ROOT" --no-print-directory -s print-image 2>/dev/null || true)"
[ -n "$IMAGE" ] || die "Could not work out the image tag from the Makefile."
say "The image will be ${IMAGE}."

# --- build it, and put it where k3s will find it ---------------------------

say ""
say "Building the image"
run make -C "$ROOT" image PLATFORM="$PLATFORM"

# k3s imports every tarball in this directory when it starts, so writing it
# before k3s is installed means the panel's very first pod finds its image
# locally. No registry, no credentials, and no pull over the network.
say "Sending it to ${HOST}"
run_sh "docker save '$IMAGE' | gzip -1 | ssh $SSH_OPTS '$HOST' 'mkdir -p /var/lib/rancher/k3s/agent/images && gunzip > /var/lib/rancher/k3s/agent/images/skifity.tar'"

# --- the checkout -----------------------------------------------------------

# git archive rather than scp -r: it sends what is committed, so the run is of a
# state that exists in the repository rather than of whatever is lying around in
# the working tree.
say "Sending this checkout"
run_sh "git -C '$ROOT' archive --format=tar HEAD | ssh $SSH_OPTS '$HOST' 'rm -rf $REMOTE_DIR && mkdir -p $REMOTE_DIR && tar -x -C $REMOTE_DIR'"

# --- run it -----------------------------------------------------------------

BRANCH="$(git -C "$ROOT" rev-parse --abbrev-ref HEAD)"

say ""
say "Running the crosscheck. This takes a while and prints as it goes."
say ""
remote_command="cd $REMOTE_DIR && \
SKIFITY_IMAGE='$IMAGE' \
SKIFITY_PHASES='$SKIFITY_PHASES' \
SKIFITY_DOMAIN='$SKIFITY_DOMAIN' \
SKIFITY_ASSUME_YES=1 \
VERIFY_GIT_BRANCH='$BRANCH' \
bash test/cluster/verify.sh"

status=0
run_sh "ssh $SSH_OPTS -t '$HOST' \"$remote_command\"" || status=$?

# --- bring the report back --------------------------------------------------

say ""
say "Fetching the report"
run_sh "ssh $SSH_OPTS '$HOST' 'cat /var/log/skifity-verify.log' > '$REPORT_OUT'" || true
say "The report is in ${REPORT_OUT}."

if [ "$status" != "0" ]; then
	say ""
	say "Something failed. The report says which check, what it expected, and what"
	say "to look at. That is the point: it is the first thing this product has ever"
	say "been told by a real cluster."
	exit "$status"
fi

say ""
if [ "$DRY_RUN" = "1" ]; then
	say "That was a dry run: nothing was built, sent or installed. Run it without"
	say "DRY_RUN=1 against a server you are willing to rebuild."
else
	say "Every check passed. Move the rows it proved in docs/checklist.md, and say"
	say "which run proved them."
fi
