#!/bin/sh
# Names the Go tests that failed, as error annotations on the run's summary.
#
#   ./scripts/ci-test-failures.sh test.log
#
# CI runs this after a test step fails (.github/workflows/ci.yml). A failing
# step said "exit code 1" and nothing else where most people look, and the job
# log is not something everybody who sees a red build can open. Each failure —
# a "--- FAIL" line, or a panic — becomes one annotation, with the indented
# lines go test printed under it, the test's own words, as its message.
#
# A data race is reported by the race detector in the middle of the log, above
# the "--- FAIL" line that only says "race detected during execution of test",
# and the report — who wrote, who read, from where — is the whole diagnosis. So
# each report becomes an annotation of its own, with the first forty lines of
# it. A race that cannot be reproduced locally has nothing else to go on.
#
# GitHub shows ten error annotations a step: up to three of them are races,
# the rest are failed tests, and the count says whether there were more.
set -eu

log="${1:?usage: ci-test-failures.sh LOG}"
[ -f "$log" ] || { echo "no test log at $log" >&2; exit 0; }

# A workflow command's message escapes %, CR and LF; the lines under a
# failure are joined with an escaped newline.
awk '
	function escape(text) { gsub(/%/, "%25", text); gsub(/\r/, "%0D", text); return text }
	/^--- FAIL|^panic: / {
		if (message != "") print message
		message = escape($0); kept = 0; next
	}
	message != "" && kept < 8 && /^    / { message = message "%0A" escape($0); kept++; next }
	message != "" && !/^    / { print message; message = "" }
	END { if (message != "") print message }
' "$log" >"${log}.failures"

awk '
	function escape(text) { gsub(/%/, "%25", text); gsub(/\r/, "%0D", text); return text }
	/^WARNING: DATA RACE/ { inrace = 1; message = escape($0); kept = 0; next }
	inrace && /^==================$/ { print message; inrace = 0; message = ""; next }
	inrace && kept < 40 { message = message "%0A" escape($0); kept++ }
	END { if (inrace) print message }
' "$log" >"${log}.races"

races=$(wc -l <"${log}.races" | tr -d ' ')
count=$(wc -l <"${log}.failures" | tr -d ' ')
head -n 3 "${log}.races" | while IFS= read -r race; do
	printf '::error title=data race::%s\n' "$race"
done
head -n $((10 - (races > 3 ? 3 : races))) "${log}.failures" | while IFS= read -r failure; do
	printf '::error title=go test::%s\n' "$failure"
done
echo "$races data race(s), $count failing test(s) or panic(s) in $log"
