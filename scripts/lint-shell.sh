#!/bin/sh
# Lint the shell this repository ships.
#
# The panel writes shell programs and runs them in other people's clusters: the
# installer, the crosschecks, the smoke tests. Nothing was checking any of them,
# and a build from a private repository was broken for months by a quoting
# mistake that shellcheck names in one line — SC2086, which the code even
# carried a directive to silence, for a linter that was never run.
#
# Shaped like scripts/lint-go.sh: locally it explains itself and lets the rest
# of `make check` run; with LINT_STRICT=1, as CI sets, it may not skip.
set -eu

if ! command -v shellcheck >/dev/null 2>&1; then
	if [ "${LINT_STRICT:-}" = "1" ]; then
		echo "shellcheck is not installed, and LINT_STRICT=1 says it has to be." >&2
		exit 1
	fi
	echo "shellcheck is not installed, so the shell was not linted."
	echo "Install it with: apt install shellcheck   (or: brew install shellcheck)"
	exit 0
fi

# Every shell file this repository ships. Found rather than listed, so a script
# written tomorrow is covered.
files=$(find installer scripts test -name '*.sh' -type f 2>/dev/null | sort)
if [ -z "$files" ]; then
	echo "No shell scripts found; this check is not reading the repository." >&2
	exit 1
fi

# Each file's own shebang decides which shell it is checked as. Forcing POSIX
# sh on everything was the first version of this script, and it reported
# `local` and `pipefail` in the cluster tests, which are bash and say so on
# their first line — noise that teaches people to ignore the linter.
#
# What is switched off, and why, is in .shellcheckrc at the root, which the
# linter reads by itself: the same settings hold for this check and for any
# scanner that runs it one file at a time, as the Codacy workflow does.
#
# Everything else is reported down to style, and that is not fussiness:
# SC2086, the unquoted expansion that broke every build from a private
# repository, is reported at "info". A threshold that sounds sensible lets
# through the one finding this check exists for.
# shellcheck disable=SC2086
shellcheck --severity=style $files

echo "shell: $(echo "$files" | wc -l | tr -d ' ') scripts, no findings."
