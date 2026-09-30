#!/bin/sh
# Smoke test: the crosscheck's own logic, without a server.
#
# test/cluster/verify.sh and its remote runner are the two scripts nothing else
# checks, because running them needs a machine somebody is willing to rebuild.
# The parts that can be checked here are the ones that would otherwise waste a
# server: a script that does not parse, a refusal that does not explain itself,
# a branch default that does not exist, or an image sent somewhere k3s will
# never look for it.
set -eu

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORKDIR="$(mktemp -d)"
FAILURES=0

cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT INT TERM

t_pass() { printf '  ok   %s\n' "$1"; }
t_fail() { printf '  FAIL %s\n' "$1" >&2; FAILURES=$((FAILURES + 1)); }

printf '\n== Skifity smoke test: the crosscheck ==\n\n'

# --- they parse -------------------------------------------------------------

for script in "$ROOT"/test/cluster/*.sh; do
  if bash -n "$script" 2>"$WORKDIR/syntax.err"; then
    t_pass "$(basename "$script") parses"
  else
    t_fail "$(basename "$script"): $(cat "$WORKDIR/syntax.err")"
  fi
done

# --- the remote runner refuses to guess -------------------------------------

out=$(HOST='' DRY_RUN=1 bash "$ROOT/test/cluster/verify-remote.sh" 2>&1 || true)
case "$out" in
*"Set HOST"*) t_pass "it refuses without a server to run on" ;;
*) t_fail "running it with no HOST should say what to set, got: $out" ;;
esac
case "$out" in
*"willing to rebuild"*) t_pass "it says what kind of server this needs" ;;
*) t_fail "the refusal should say the server is changed permanently" ;;
esac

# --- the dry run changes nothing and says what it would do ------------------

out=$(HOST=root@203.0.113.10 DRY_RUN=1 bash "$ROOT/test/cluster/verify-remote.sh" 2>&1)

case "$out" in
*"would run: make"*image*) t_pass "it builds the image before sending it" ;;
*) t_fail "the dry run should build an image" ;;
esac

# k3s imports every tarball in this directory when it starts. Sending it
# anywhere else means the panel's first pod cannot find its image, which
# surfaces as ImagePullBackOff twenty minutes into a run.
case "$out" in
*"/var/lib/rancher/k3s/agent/images"*) t_pass "the image lands where k3s imports from" ;;
*) t_fail "the image is not sent to the directory k3s imports from" ;;
esac

case "$out" in
*"docker save"*"ssh"*) t_pass "the image goes straight to the server, not to a registry" ;;
*) t_fail "the image should be streamed to the server, not pushed" ;;
esac

# The tag it sends has to be the tag the Makefile builds, or the server imports
# one image and the panel asks for another.
image=$(make -C "$ROOT" --no-print-directory -s print-image)
case "$out" in
*"$image"*) t_pass "it sends the image the Makefile builds" ;;
*) t_fail "the dry run does not name $image" ;;
esac

case "$out" in
*"dry run"*) t_pass "a dry run says it was one" ;;
*) t_fail "a dry run must not claim the checks passed" ;;
esac
case "$out" in
*"Every check passed"*) t_fail "a dry run must not say every check passed" ;;
*) t_pass "a dry run does not claim work it did not do" ;;
esac

[ -f "$ROOT/verify-report.log" ] &&
  t_fail "the dry run wrote a report; it must not touch anything" ||
  t_pass "the dry run wrote nothing"

# --- the branch it builds from exists ---------------------------------------

# This repository has no "main". A default nobody ever pushed means an hour of
# installing and then a clone that fails.
if grep -q 'VERIFY_GIT_BRANCH:=main}' "$ROOT/test/cluster/verify.sh"; then
  t_fail "verify.sh still defaults to a branch that does not exist"
else
  t_pass "the branch to build from is read from the checkout, not assumed"
fi

branch=$(bash -c 'git -C "$0/../.." rev-parse --abbrev-ref HEAD' "$ROOT/test/cluster" 2>/dev/null || echo "")
if [ -n "$branch" ] && git -C "$ROOT" rev-parse --verify "$branch" >/dev/null 2>&1; then
  t_pass "that branch resolves here ($branch)"
else
  t_fail "the branch default does not resolve in this checkout"
fi

printf '\n'
if [ "$FAILURES" -gt 0 ]; then
  printf '%s crosscheck smoke check(s) failed.\n\n' "$FAILURES"
  exit 1
fi
printf 'All crosscheck smoke checks passed.\n\n'
