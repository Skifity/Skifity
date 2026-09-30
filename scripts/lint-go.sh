#!/bin/sh
# Run golangci-lint, and say precisely what happened when it cannot be run.
#
# golangci-lint refuses a module whose `go` directive is newer than the Go it
# was built with, and the message it prints — "the Go language version (go1.25)
# used to build golangci-lint is lower than the targeted Go version (1.26.0)" —
# says nothing about the code. Before this script that took `make check` down
# with it: the command the README calls "what CI runs" could not be run at all
# on a machine whose golangci-lint was a release behind the toolchain, so the
# code went unlinted and nobody could tell.
#
# The fallback is `go run`, which builds the linter with this module's own
# toolchain and therefore can never be out of step with it. It costs a minute
# the first time and is cached afterwards. Skipping is the last resort, for a
# machine with no network, and LINT_STRICT=1 forbids even that — which is what
# CI sets, because a linter that quietly does not run is not a linter.
set -eu

LINTER=golangci-lint
FALLBACK=github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

skip() {
	if [ "${LINT_STRICT:-0}" = "1" ]; then
		echo "golangci-lint did not run, and LINT_STRICT=1." >&2
		echo "$1" >&2
		exit 1
	fi
	echo "Skipping golangci-lint. $1"
	echo "go vet ran, and CI runs this script with LINT_STRICT=1."
	exit 0
}

# usable reports whether the golangci-lint on PATH can read this module. It
# compares the Go it was built with against the Go this module targets, as
# version numbers rather than as strings: go1.9 is older than go1.26.
usable() {
	command -v "$LINTER" >/dev/null 2>&1 || return 1
	built=$("$LINTER" --version 2>/dev/null | sed -n 's/.*built with go\([0-9][0-9.]*\).*/\1/p')
	target=$(sed -n 's/^go \([0-9][0-9.]*\)$/\1/p' go.mod)
	[ -n "$built" ] && [ -n "$target" ] || return 0
	[ "$built" = "$target" ] && return 0
	[ "$(printf '%s\n%s\n' "$built" "$target" | sort -V | head -n1)" = "$target" ]
}

if usable; then
	exec "$LINTER" run
fi

command -v go >/dev/null 2>&1 || skip "There is no golangci-lint this module can use, and no go to build one."

echo "The installed golangci-lint cannot read a go$(sed -n 's/^go //p' go.mod) module."
echo "Building one with this module's own toolchain instead; the first run is slow."

# Built separately from the run, so "could not be built" and "found something"
# are told apart: both exit non-zero, and only the second is the code's fault.
if ! go run "$FALLBACK" --version >/dev/null 2>&1; then
	skip "It could not be built either, which usually means no network."
fi

exec go run "$FALLBACK" run
