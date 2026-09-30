#!/bin/sh
# Smoke test: the panel starts, first-run setup works, sign-in works, an API
# token works, and the CLI can use it.
#
# It needs no cluster, so it runs anywhere, including in CI. Neither does
# installer.sh beside it, which checks the installer's logic without installing
# anything. Those two are the whole of test/smoke: there is no smoke test that
# exercises a real cluster, because none could ever have been run where this was
# built (ADR-0010). This header used to claim four of them.
set -eu

BINARY="${BINARY:-./bin/skifity}"
PORT="${PORT:-18099}"
WORKDIR="$(mktemp -d)"
BASE="http://127.0.0.1:${PORT}"
PASSWORD="a reasonable passphrase"

cleanup() {
  if [ -n "${SERVER_PID:-}" ]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$WORKDIR"
}
trap cleanup EXIT INT TERM

fail() { printf '\nFAILED: %s\n' "$1" >&2; [ -f "$WORKDIR/server.log" ] && tail -30 "$WORKDIR/server.log" >&2; exit 1; }
pass() { printf '  ok   %s\n' "$1"; }

printf '\n== %s smoke test: panel ==\n\n' "Skifity"

[ -x "$BINARY" ] || fail "$BINARY does not exist. Run: make build"

SKIFITY_LISTEN="127.0.0.1:${PORT}" \
SKIFITY_DATABASE_PATH="$WORKDIR/panel.db" \
SKIFITY_MASTER_KEY_PATH="$WORKDIR/master.key" \
SKIFITY_SETUP_TOKEN_PATH="$WORKDIR/setup-token" \
SKIFITY_PUBLIC_URL="$BASE" \
SKIFITY_DEV_MODE=true \
SKIFITY_LOG_FORMAT=text \
  "$BINARY" server > "$WORKDIR/server.log" 2>&1 &
SERVER_PID=$!

# Wait for it to answer rather than sleeping a fixed amount.
i=0
while [ $i -lt 50 ]; do
  if curl -fsS "$BASE/api/health" >/dev/null 2>&1; then break; fi
  kill -0 "$SERVER_PID" 2>/dev/null || fail "the panel exited during startup"
  i=$((i + 1))
  sleep 0.2
done
[ $i -lt 50 ] || fail "the panel did not answer within ten seconds"
pass "the panel started and answers /api/health"

curl -fsS "$BASE/api/health" | grep -q '"status":"ok"' || fail "health did not report ok"
pass "health reports ok"

curl -fsS "$BASE/api/setup/status" | grep -q '"needs_setup":true' || fail "a fresh panel did not ask for setup"
pass "a fresh panel asks for setup"

# An unauthenticated request must be refused.
code=$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/api/teams")
[ "$code" = "401" ] || fail "an unauthenticated request answered $code, want 401"
pass "unauthenticated requests are refused"

# A wrong setup token must be refused.
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/api/setup" \
  -H 'Content-Type: application/json' \
  -d '{"token":"wrong","email":"a@example.test","password":"'"$PASSWORD"'"}')
[ "$code" = "401" ] || fail "a wrong setup token answered $code, want 401"
pass "a wrong setup token is refused"

TOKEN="$(cat "$WORKDIR/setup-token")"
[ -n "$TOKEN" ] || fail "no setup token was written"

SETUP=$(curl -fsS -X POST "$BASE/api/setup" -H 'Content-Type: application/json' \
  -d '{"token":"'"$TOKEN"'","email":"owner@example.test","name":"Owner","password":"'"$PASSWORD"'","team_name":"Acme"}')
echo "$SETUP" | grep -q '"recovery_key":"SKIFITY-RECOVERY-v1-' || fail "setup did not return a recovery key"
pass "setup creates the first account and returns a recovery key"

# Setup must not be possible twice.
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/api/setup" \
  -H 'Content-Type: application/json' \
  -d '{"token":"'"$TOKEN"'","email":"second@example.test","password":"'"$PASSWORD"'"}')
[ "$code" = "409" ] || fail "setup ran a second time and answered $code"
pass "setup cannot be run twice"

[ ! -f "$WORKDIR/setup-token" ] || fail "the setup token file was not removed after use"
pass "the setup token file is removed once used"

# Sign in.
LOGIN=$(curl -fsS -c "$WORKDIR/cookies" -X POST "$BASE/api/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"email":"owner@example.test","password":"'"$PASSWORD"'"}')
echo "$LOGIN" | grep -q '"csrf_token"' || fail "sign-in did not return a CSRF token"
pass "sign-in works"

CSRF=$(echo "$LOGIN" | sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p')

# A wrong password must be refused.
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/api/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"email":"owner@example.test","password":"wrong password entirely"}')
[ "$code" = "401" ] || fail "a wrong password answered $code, want 401"
pass "a wrong password is refused"

# A state-changing request without the CSRF header must be refused.
TEAM_ID=$(curl -fsS -b "$WORKDIR/cookies" "$BASE/api/teams" | sed -n 's/.*"id":"\(team_[^"]*\)".*/\1/p')
[ -n "$TEAM_ID" ] || fail "no team was returned"
code=$(curl -sS -b "$WORKDIR/cookies" -o /dev/null -w '%{http_code}' -X POST "$BASE/api/me/tokens" \
  -H 'Content-Type: application/json' -d '{"name":"x","team_id":"'"$TEAM_ID"'"}')
[ "$code" = "403" ] || fail "a request without a CSRF token answered $code, want 403"
pass "cross-site requests are blocked"

# A CSRF token that agrees with its own cookie but is not this session's must
# be refused. Double-submit compares two things the caller supplies, and this
# panel hosts applications on subdomains of the domain it answers on, so "a
# page on another site" can be an app somebody deployed here this morning.
code=$(curl -sS -b "$WORKDIR/cookies" -o /dev/null -w '%{http_code}' -X POST "$BASE/api/me/tokens" \
  -H 'Content-Type: application/json' \
  -H "Cookie: skifity_csrf=forged-by-an-app" -H "X-Skifity-CSRF: forged-by-an-app" \
  -d '{"name":"x","team_id":"'"$TEAM_ID"'"}')
[ "$code" = "403" ] || fail "a CSRF token that only agrees with its own cookie answered $code, want 403"
pass "the CSRF token is checked against the session, not against a cookie"

# Create an API token with the CSRF header.
TOKEN_RESPONSE=$(curl -fsS -b "$WORKDIR/cookies" -X POST "$BASE/api/me/tokens" \
  -H 'Content-Type: application/json' -H "X-Skifity-CSRF: $CSRF" \
  -d '{"name":"smoke test","team_id":"'"$TEAM_ID"'"}')
API_TOKEN=$(echo "$TOKEN_RESPONSE" | sed -n 's/.*"secret":"\([^"]*\)".*/\1/p')
[ -n "$API_TOKEN" ] || fail "no API token was returned"
pass "an API token can be created"

# The token must work without a cookie, and without CSRF.
curl -fsS -H "Authorization: Bearer $API_TOKEN" "$BASE/api/me" | grep -q 'owner@example.test' \
  || fail "the API token does not authenticate"
pass "the API token authenticates"

# An API token cannot turn two-factor authentication off, or mint another
# token. There is nobody at the keyboard for it to ask, and a token that could
# do either would be a way around the thing two-factor protects.
for route in "DELETE /api/me/totp" "POST /api/me/tokens" "POST /api/me/reauth"; do
  method=${route%% *}
  path=${route#* }
  code=$(curl -sS -H "Authorization: Bearer $API_TOKEN" -o "$WORKDIR/needs-person.json" \
    -w '%{http_code}' -X "$method" "$BASE$path" \
    -H 'Content-Type: application/json' -d '{"name":"x"}')
  [ "$code" = "403" ] || fail "$method $path with a bearer token answered $code, want 403"
  grep -q 'auth.needs_person' "$WORKDIR/needs-person.json" ||
    fail "$method $path did not say why an API token cannot do it"
done
pass "an API token cannot change two-factor or mint another token"

# Stepping up needs the right password, and asks for it before the three
# actions that turn a borrowed session into access somebody keeps.
code=$(curl -sS -b "$WORKDIR/cookies" -o /dev/null -w '%{http_code}' -X POST "$BASE/api/me/reauth" \
  -H 'Content-Type: application/json' -H "X-Skifity-CSRF: $CSRF" \
  -d '{"password":"not the password"}')
[ "$code" = "401" ] || fail "stepping up with the wrong password answered $code, want 401"
code=$(curl -sS -b "$WORKDIR/cookies" -o /dev/null -w '%{http_code}' -X POST "$BASE/api/me/reauth" \
  -H 'Content-Type: application/json' -H "X-Skifity-CSRF: $CSRF" \
  -d '{"password":"'"$PASSWORD"'"}')
[ "$code" = "200" ] || fail "stepping up with the right password answered $code, want 200"
pass "proving who you are again works, and the wrong password does not"

# A token is issued for one team, and that binding was stored and never read,
# so a token made for one team worked on every team its owner belonged to.
SECOND_TEAM=$(curl -fsS -b "$WORKDIR/cookies" -X POST "$BASE/api/teams" \
  -H 'Content-Type: application/json' -H "X-Skifity-CSRF: $CSRF" \
  -d '{"name":"Other"}')
SECOND_TEAM_ID=$(echo "$SECOND_TEAM" | sed -n 's/.*"id":"\(team_[^"]*\)".*/\1/p')
[ -n "$SECOND_TEAM_ID" ] || fail "a second team was not created"

curl -fsS -b "$WORKDIR/cookies" "$BASE/api/teams/$SECOND_TEAM_ID/projects" >/dev/null \
  || fail "the owner cannot reach their own second team"
code=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $API_TOKEN" \
  "$BASE/api/teams/$SECOND_TEAM_ID/projects")
[ "$code" = "404" ] || fail "a token bound to one team reached another and answered $code, want 404"
pass "a token cannot be used on a team it was not issued for"

# The bound token must also not be told the other team exists, or the CLI
# would pick a team every later request refuses.
curl -fsS -H "Authorization: Bearer $API_TOKEN" "$BASE/api/me" | grep -q "$SECOND_TEAM_ID" \
  && fail "a bound token was shown a team it cannot use"
pass "a bound token is only shown its own team"

# A token's scopes were stored and never consulted, so a read-only token could
# do everything its owner could, including issue a token with no scopes.
READONLY=$(curl -fsS -b "$WORKDIR/cookies" -X POST "$BASE/api/me/tokens" \
  -H 'Content-Type: application/json' -H "X-Skifity-CSRF: $CSRF" \
  -d '{"name":"read only","team_id":"'"$TEAM_ID"'","scopes":"read"}')
READONLY_TOKEN=$(echo "$READONLY" | sed -n 's/.*"secret":"\([^"]*\)".*/\1/p')
[ -n "$READONLY_TOKEN" ] || fail "no read-only token was returned"

curl -fsS -H "Authorization: Bearer $READONLY_TOKEN" "$BASE/api/teams" >/dev/null \
  || fail "a read-only token could not read"
code=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $READONLY_TOKEN" \
  -X POST "$BASE/api/me/tokens" -H 'Content-Type: application/json' \
  -d '{"name":"escalated","team_id":"'"$TEAM_ID"'"}')
[ "$code" = "403" ] || fail "a read-only token wrote and answered $code, want 403"
pass "a read-only token can read and cannot write"

# A scope nothing enforces must be refused, not stored and ignored.
code=$(curl -sS -b "$WORKDIR/cookies" -o /dev/null -w '%{http_code}' -X POST "$BASE/api/me/tokens" \
  -H 'Content-Type: application/json' -H "X-Skifity-CSRF: $CSRF" \
  -d '{"name":"bogus","team_id":"'"$TEAM_ID"'","scopes":"admin"}')
[ "$code" = "400" ] || fail "an unenforced scope answered $code, want 400"
pass "a scope the panel does not enforce is refused"

# A made-up token must not.
code=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer skf_not_a_real_token" "$BASE/api/me")
[ "$code" = "401" ] || fail "an invalid token answered $code, want 401"
pass "an invalid token is refused"

# The CLI, using that token.
export SKIFITY_CONFIG="$WORKDIR/cli-config.json"
"$BINARY" login --url "$BASE" --token "$API_TOKEN" >/dev/null || fail "skifity login failed"
pass "skifity login stores the token"

"$BINARY" whoami | grep -q 'owner@example.test' || fail "skifity whoami did not show the user"
pass "skifity whoami works"

"$BINARY" apps --json >/dev/null || fail "skifity apps failed"
pass "skifity apps works"

"$BINARY" servers --json | grep -q '\[' || fail "skifity servers did not return a list"
pass "skifity servers works"

# --- a folder with no repository ---------------------------------------------
#
# `skifity up` is the way in for somebody whose app an assistant wrote: no
# repository, no settings. There is no cluster here, so the build itself cannot
# run; everything before it can, and that is what this checks — the app is
# made, the folder is sent without its .env or node_modules, the .env's values
# become variables, and sending the same folder again is the same upload.
APPDIR="$WORKDIR/My Notes"
mkdir -p "$APPDIR/node_modules/left-out" "$APPDIR/src"
printf '{"name":"notes","scripts":{"start":"node src/index.js"}}\n' > "$APPDIR/package.json"
printf 'console.log("hi")\n' > "$APPDIR/src/index.js"
printf 'STRIPE_SECRET_KEY=sk_test_smokevalue\n' > "$APPDIR/.env"
printf 'STRIPE_SECRET_KEY=\n' > "$APPDIR/.env.example"
printf 'x\n' > "$APPDIR/node_modules/left-out/index.js"

# The binary's path is relative to the repository, and these run elsewhere.
BINARY_PATH=$(cd "$(dirname "$BINARY")" && pwd)/$(basename "$BINARY")
UP=$(cd "$APPDIR" && "$BINARY_PATH" up --dotenv --follow=false 2>&1) || fail "skifity up failed: $UP"
[ -f "$APPDIR/skifity.toml" ] || fail "skifity up did not link the folder to its app"
UP_APP=$(sed -n 's/^app = "\(app_[^"]*\)"/\1/p' "$APPDIR/skifity.toml")
curl -fsS -H "Authorization: Bearer $API_TOKEN" "$BASE/api/apps/$UP_APP" | grep -q '"source_type":"upload"' ||
  fail "skifity up did not make an app that deploys uploaded code"
curl -fsS -H "Authorization: Bearer $API_TOKEN" "$BASE/api/apps/$UP_APP" | grep -q '"name":"my-notes"' ||
  fail "the app was not named after the folder"
pass "skifity up makes an app from a folder, named after it, and links the folder"

UPLOADS="$WORKDIR/uploads/$UP_APP"
uploads_kept() { find "$UPLOADS" -name '*.tar.gz' | wc -l; }
[ "$(uploads_kept)" -eq 1 ] || fail "skifity up did not leave exactly one upload, but $(uploads_kept)"
LISTING=$(tar -tzf "$UPLOADS"/*.tar.gz)
echo "$LISTING" | grep -q '^src/index.js$' || fail "the upload is missing the code: $LISTING"
echo "$LISTING" | grep -q '^\.env$' && fail "the .env file was uploaded"
echo "$LISTING" | grep -q 'node_modules' && fail "node_modules was uploaded"
echo "$LISTING" | grep -q '^\.env\.example$' || fail "the .env.example template was left out"
pass "the folder is sent without its .env or node_modules"

VARS=$(curl -fsS -H "Authorization: Bearer $API_TOKEN" "$BASE/api/apps/$UP_APP/variables")
echo "$VARS" | grep -q '"STRIPE_SECRET_KEY"' || fail "the .env's value did not become a variable: $VARS"
echo "$VARS" | grep -q 'sk_test_smokevalue' && fail "a secret from .env came back from the API"
pass "the .env's values become variables, and a secret one stays secret"

UP2=$(cd "$APPDIR/src" && "$BINARY_PATH" up --follow=false 2>&1) || fail "a second skifity up failed: $UP2"
[ "$(uploads_kept)" -eq 1 ] || fail "the same folder sent again made a second upload"
pass "skifity up from a subfolder sends the whole app, and the same code is the same upload"

# Create an app through the API and check the CLI sees it.
PROJECT_ID=$(curl -fsS -H "Authorization: Bearer $API_TOKEN" "$BASE/api/teams/$TEAM_ID/projects" \
  | sed -n 's/.*"id":"\(prj_[^"]*\)".*/\1/p')
ENV_ID=$(curl -fsS -H "Authorization: Bearer $API_TOKEN" "$BASE/api/projects/$PROJECT_ID/environments" \
  | sed -n 's/.*"id":"\(env_[^"]*\)".*/\1/p')
[ -n "$ENV_ID" ] || fail "setup did not create an environment"
pass "setup created a project and an environment"

APP=$(curl -fsS -H "Authorization: Bearer $API_TOKEN" -X POST "$BASE/api/environments/$ENV_ID/apps" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Web","source_type":"image","image":"nginx:1.27-alpine","port":80}')
APP_ID=$(echo "$APP" | sed -n 's/.*"id":"\(app_[^"]*\)".*/\1/p')
[ -n "$APP_ID" ] || fail "the app was not created"
pass "an app can be created"

"$BINARY" apps --json | grep -q "$APP_ID" || fail "the CLI does not list the new app"
pass "the CLI lists the new app"

# A one-off command is how a migration runs. Without a cluster it cannot
# actually start one, but the route, the authorization and the validation are
# real, and an empty command has to be refused rather than queued.
code=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $API_TOKEN" \
  -X POST "$BASE/api/apps/$APP_ID/run" -H 'Content-Type: application/json' -d '{"command":"  "}')
[ "$code" = "400" ] || fail "an empty command answered $code, want 400"
pass "a one-off command with nothing in it is refused"

# A scheduled command is a cron expression the panel has to understand before
# Kubernetes sees it: an object rejected by the API server is a failure the
# panel would have to explain afterwards.
code=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $API_TOKEN" \
  -X POST "$BASE/api/apps/$APP_ID/jobs" -H 'Content-Type: application/json' \
  -d '{"name":"nightly","schedule":"every night","command":"npm run digest"}')
[ "$code" = "400" ] || fail "a nonsense schedule answered $code, want 400"
pass "a schedule the panel cannot read is refused"

JOB=$(curl -fsS -H "Authorization: Bearer $API_TOKEN" -X POST "$BASE/api/apps/$APP_ID/jobs" \
  -H 'Content-Type: application/json' \
  -d '{"name":"nightly report","schedule":"0 3 * * *","command":"npm run digest"}')
JOB_ID=$(echo "$JOB" | sed -n 's/.*"id":"\(job_[^"]*\)".*/\1/p')
[ -n "$JOB_ID" ] || fail "the scheduled command was not created"
curl -fsS -H "Authorization: Bearer $API_TOKEN" "$BASE/api/apps/$APP_ID/jobs" |
  grep -q 'nightly report' || fail "the scheduled command is not listed"
curl -fsS -H "Authorization: Bearer $API_TOKEN" -X DELETE "$BASE/api/apps/$APP_ID/jobs/$JOB_ID" >/dev/null ||
  fail "the scheduled command could not be removed"
pass "a command can be scheduled, listed and removed"

# The release command is stored on the app and comes back on it.
curl -fsS -H "Authorization: Bearer $API_TOKEN" -X PATCH "$BASE/api/apps/$APP_ID" \
  -H 'Content-Type: application/json' -d '{"release_command":"npm run migrate"}' >/dev/null ||
  fail "the release command could not be set"
curl -fsS -H "Authorization: Bearer $API_TOKEN" "$BASE/api/apps/$APP_ID" |
  grep -q '"release_command":"npm run migrate"' || fail "the release command was not stored"
pass "an app can be given a release command"

# An app created without a port used to get zero, which renders no Service, no
# Ingress and no URL: a deploy that succeeds and cannot be reached.
PORTLESS=$(curl -fsS -H "Authorization: Bearer $API_TOKEN" -X POST "$BASE/api/environments/$ENV_ID/apps" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Portless","source_type":"image","image":"nginx:1.27-alpine"}')
echo "$PORTLESS" | grep -q '"port":8080' || fail "an app created without a port did not get the default one"
pass "an app created without a port still gets one"

# Variables: a runtime one must not require a rebuild.
"$BINARY" env set --app "$APP_ID" LOG_LEVEL=debug | grep -q 'No rebuild was needed' \
  || fail "setting a runtime variable reported a rebuild"
pass "a runtime variable is rolled out without rebuilding"

"$BINARY" env set --app "$APP_ID" --build API_URL=https://api.example.test | grep -q 'rebuild' \
  || fail "setting a build-time variable did not warn about a rebuild"
pass "a build-time variable says it will rebuild"

"$BINARY" env --app "$APP_ID" list | grep -q 'LOG_LEVEL' || fail "the variable is not listed"
pass "variables are listed"

# A secret must never be readable again.
"$BINARY" env set --app "$APP_ID" --secret DB_PASSWORD=verysecret >/dev/null
"$BINARY" env --app "$APP_ID" list | grep -q 'verysecret' && fail "a secret's value was shown"
"$BINARY" env --app "$APP_ID" list | grep -q '(secret)' || fail "the secret is not listed at all"
pass "a secret is stored but never shown again"

# The scaling readiness checker must notice a real problem.
"$BINARY" env set --app "$APP_ID" DATABASE_URL=sqlite:///data/app.db >/dev/null
READINESS=$(curl -fsS -H "Authorization: Bearer $API_TOKEN" "$BASE/api/apps/$APP_ID/scaling/readiness")
echo "$READINESS" | grep -q 'sqlite' || fail "the readiness checker missed SQLite"
pass "the scaling readiness checker spots SQLite"

# Audit: the actions above must be recorded.
AUDIT=$(curl -fsS -H "Authorization: Bearer $API_TOKEN" "$BASE/api/teams/$TEAM_ID/audit")
echo "$AUDIT" | grep -q 'app.created' || fail "the audit log has no record of the app being created"
echo "$AUDIT" | grep -q 'verysecret' && fail "a secret's value reached the audit log"
pass "actions are audited, without secret values"


# A CI job, a container or an AI assistant never runs `skifity login`, so the
# CLI has to work from the environment alone, with no stored configuration and
# no team ever chosen.
ENV_OUT=$(SKIFITY_CONFIG="$WORKDIR/does-not-exist.json" \
  SKIFITY_URL="$BASE" SKIFITY_TOKEN="$API_TOKEN" "$BINARY" whoami 2>&1) ||
  fail "the CLI could not authenticate from the environment: $ENV_OUT"
echo "$ENV_OUT" | grep -q "owner@example.test" || fail "whoami from the environment did not report the user"
pass "the CLI authenticates from SKIFITY_URL and SKIFITY_TOKEN"

SKIFITY_CONFIG="$WORKDIR/does-not-exist.json" \
  SKIFITY_URL="$BASE" SKIFITY_TOKEN="$API_TOKEN" "$BINARY" apps --json >/dev/null ||
  fail "the CLI could not find the team from the token alone"
pass "the CLI finds the only team without being told"

# With nothing at all, the error has to name the way out rather than just
# refusing.
NO_AUTH=$(SKIFITY_CONFIG="$WORKDIR/does-not-exist.json" "$BINARY" whoami 2>&1 || true)
echo "$NO_AUTH" | grep -q 'SKIFITY_URL' ||
  fail "signing in with nothing set should mention SKIFITY_URL, got: $NO_AUTH"
pass "an unauthenticated CLI says how to authenticate"

# Connecting a Git account: the token must be stored and never come back, and
# the webhook address the user has to paste in must be returned once.
GIT_SOURCE=$(curl -fsS -b "$WORKDIR/cookies" -X POST "$BASE/api/teams/$TEAM_ID/git-sources" \
  -H 'Content-Type: application/json' -H "X-Skifity-CSRF: $CSRF" \
  -d '{"kind":"gitea","name":"Self-hosted","token":"verysecrettoken","base_url":"https://git.example.test"}')
echo "$GIT_SOURCE" | grep -q '"webhook_url"' || fail "connecting a Git account returned no webhook address"
pass "a Git account can be connected"

SOURCES=$(curl -fsS -b "$WORKDIR/cookies" "$BASE/api/teams/$TEAM_ID/git-sources")
echo "$SOURCES" | grep -q 'verysecrettoken' && fail "a Git token was returned by the API"
echo "$SOURCES" | grep -q 'Self-hosted' || fail "the connected Git account was not listed"
pass "a Git token is stored but never returned"

# A notification channel with a bad address must be refused before it is stored,
# while the person is still looking at the form.
code=$(curl -sS -b "$WORKDIR/cookies" -o "$WORKDIR/channel.json" -w '%{http_code}' \
  -X POST "$BASE/api/teams/$TEAM_ID/notifications" \
  -H 'Content-Type: application/json' -H "X-Skifity-CSRF: $CSRF" \
  -d '{"kind":"discord","name":"Bad","config":{"webhook_url":"https://example.com/nope"},"events":[]}')
[ "$code" = "400" ] || fail "a bad Discord webhook was accepted, answered $code"
grep -q 'does not look like a Discord webhook' "$WORKDIR/channel.json" ||
  fail "the rejection did not say what was wrong"
pass "a notification channel is validated before it is stored"

curl -fsS -b "$WORKDIR/cookies" -X POST "$BASE/api/teams/$TEAM_ID/notifications" \
  -H 'Content-Type: application/json' -H "X-Skifity-CSRF: $CSRF" \
  -d '{"kind":"discord","name":"Ops","config":{"webhook_url":"https://discord.com/api/webhooks/1/verysecrethook"},"events":["deploy.failed"]}' >/dev/null ||
  fail "a valid notification channel was refused"
CHANNELS=$(curl -fsS -b "$WORKDIR/cookies" "$BASE/api/teams/$TEAM_ID/notifications")
echo "$CHANNELS" | grep -q 'verysecrethook' && fail "a channel's webhook URL was returned by the API"
pass "a notification channel is stored without its address coming back"

# The MCP server is how an assistant uses the panel. Every tool that does
# anything needs a team, and a token set up the documented way — SKIFITY_URL
# and SKIFITY_TOKEN, nothing else — used to have none, so every one of them
# answered "this token is not tied to a team".
MCP_OUT="$WORKDIR/mcp.jsonl"
# The subshell holds the pipe open after writing: the server answers over the
# same stream, and an immediate EOF ends it before it has said anything.
(
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_projects","arguments":{}}}'
  sleep 5
) | env -u SKIFITY_CONFIG SKIFITY_URL="$BASE" SKIFITY_TOKEN="$API_TOKEN" \
  "$BINARY" mcp >"$MCP_OUT" 2>"$WORKDIR/mcp.err" || true

grep -q '"name":"create_app"' "$MCP_OUT" || fail "the MCP server offers no way to create an app"
pass "the MCP server can create an app, not only change one"

grep -q 'not tied to a team' "$MCP_OUT" && fail "the MCP server could not work out which team to act on"
grep -q 'prj_' "$MCP_OUT" || fail "the MCP server listed no projects"
pass "the MCP server finds the team from the token alone"

# Two-factor recovery codes are the only way back in on a panel with no email
# reset by design, so they have to be handed out and then actually remembered.
TOTP=$(curl -fsS -b "$WORKDIR/cookies" -H "X-Skifity-CSRF: $CSRF" -X POST "$BASE/api/me/totp")
echo "$TOTP" | grep -q '"recovery_codes":\[' || fail "two-factor setup returned no recovery codes"
echo "$TOTP" | grep -qE '"[A-Z2-7]{4}-[A-Z2-7]{4}-[A-Z2-7]{4}"' \
  || fail "the recovery codes are not in the shape the account page prints"
pass "two-factor setup hands out recovery codes"

curl -fsS -b "$WORKDIR/cookies" "$BASE/api/me" | grep -q '"recovery_codes_left":8' \
  || fail "the recovery codes were printed and not stored"
pass "the recovery codes are stored, not just printed"

# The panel's own database has to be copyable, and "cp panel.db" is not it: in
# WAL mode a committed change can still be sitting in panel.db-wal.
"$BINARY" admin backup-db --database "$WORKDIR/panel.db" "$WORKDIR/panel-copy.db" >/dev/null ||
  fail "admin backup-db failed"
[ -s "$WORKDIR/panel-copy.db" ] || fail "the database copy is empty"
"$BINARY" admin list-users --database "$WORKDIR/panel-copy.db" | grep -q 'owner@example.test' ||
  fail "the copy does not hold the account that was there when it was taken"
pass "the panel's database can be copied consistently while it is running"

# Nobody can sign in is the one situation the API cannot fix, so the recovery
# path has to work: it reads the database directly, on the server.
"$BINARY" admin list-users --database "$WORKDIR/panel.db" | grep -q 'owner@example.test' ||
  fail "admin list-users did not find the account"
pass "an administrator can list the accounts from the server"

"$BINARY" admin reset-password --database "$WORKDIR/panel.db" \
  --password 'an entirely different passphrase' owner@example.test >/dev/null ||
  fail "admin reset-password failed"

code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/api/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"email":"owner@example.test","password":"'"$PASSWORD"'"}')
[ "$code" = "401" ] || fail "the old password still worked after a reset, answered $code"

code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/api/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"email":"owner@example.test","password":"an entirely different passphrase"}')
[ "$code" = "200" ] || fail "the new password did not work after a reset, answered $code"
pass "a password can be reset from the server, and the old one stops working"

# Nothing sensitive may reach the panel's own log. This runs last on purpose:
# every secret above starts with "verysecret", and a leak check that runs
# before the secrets are sent proves nothing.
grep -q 'verysecret' "$WORKDIR/server.log" && fail "a secret reached the panel's log"
grep -q "$PASSWORD" "$WORKDIR/server.log" && fail "the password reached the panel's log"
pass "no secrets reached the panel's log"

# The metrics page: behind authentication, in the format Prometheus reads, and
# carrying the request this very check makes.
code=$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/api/metrics")
[ "$code" = "401" ] || fail "the metrics page answered $code without credentials, want 401"
pass "the metrics page is not open to anyone who can reach the port"

METRICS=$(curl -fsS -H "Authorization: Bearer $API_TOKEN" "$BASE/api/metrics")
printf '%s' "$METRICS" | grep -q '^# TYPE skifity_http_requests_total counter$' \
  || fail "the metrics page is not in the exposition format"
printf '%s' "$METRICS" | grep -q 'skifity_http_requests_total{.*status="200".*} ' \
  || fail "requests are not being counted"
printf '%s' "$METRICS" | grep -q '^skifity_goroutines ' \
  || fail "the runtime is not reported"
printf '%s' "$METRICS" | grep -q '^skifity_database_bytes ' \
  || fail "the database size is not reported"
printf '%s' "$METRICS" | grep -q 'skifity_http_request_duration_seconds_bucket{.*le="+Inf"}' \
  || fail "the duration histogram has no +Inf bucket"
pass "the panel reports on itself, in the format every monitoring system reads"

# A label per app id is how a metrics endpoint becomes a memory leak, so the
# route pattern is the label and the id is not in it.
printf '%s' "$METRICS" | grep -q "$TEAM_ID" && fail "an id leaked into a metric label"
pass "metric labels are route patterns, not paths with ids in them"

# --- getting a second person in, and letting them take the data out ---------
#
# These two exist here as well as in Go because test/cluster/verify.sh drives
# them over HTTP, and a field renamed in the API would leave that script quietly
# checking nothing on the one machine nobody can reach from CI.

INVITE=$(curl -fsS -H "Authorization: Bearer $API_TOKEN" -X POST \
  "$BASE/api/teams/$TEAM_ID/invitations" -H 'Content-Type: application/json' \
  -d '{"email":"colleague@example.test","role":"member"}')
INVITE_URL=$(echo "$INVITE" | sed -n 's/.*"url":"\([^"]*\)".*/\1/p')
[ -n "$INVITE_URL" ] || fail "inviting somebody returned no link to send them"
INVITE_TOKEN="${INVITE_URL##*/}"
pass "an invitation returns a one-time link"

curl -fsS "$BASE/api/invitations/$INVITE_TOKEN" | grep -q 'colleague@example.test' \
  || fail "the invitation lookup does not say who it is for"
pass "opening the link says which team and which address, before any password is typed"

curl -fsS -c "$WORKDIR/member-cookies" -X POST "$BASE/api/invitations/$INVITE_TOKEN/accept" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Colleague","password":"a reasonable member passphrase"}' >/dev/null \
  || fail "the invitation could not be accepted"
pass "accepting the link creates the account and signs them in"

code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/api/invitations/$INVITE_TOKEN/accept" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Again","password":"a reasonable member passphrase"}')
[ "$code" = "404" ] || fail "an invitation link worked twice, answering $code"
pass "the link is spent once it is used"

MEMBER_CSRF=$(awk '/skifity_csrf/ {print $7}' "$WORKDIR/member-cookies")
[ -n "$MEMBER_CSRF" ] || fail "no CSRF cookie was set for the new member"
MEMBER_TOKEN=$(curl -fsS -b "$WORKDIR/member-cookies" -X POST "$BASE/api/me/tokens" \
  -H 'Content-Type: application/json' -H "X-Skifity-CSRF: $MEMBER_CSRF" \
  -d '{"name":"member","team_id":"'"$TEAM_ID"'"}' | sed -n 's/.*"secret":"\([^"]*\)".*/\1/p')
[ -n "$MEMBER_TOKEN" ] || fail "the new member could not create a token"

EXPORT=$(curl -fsS -H "Authorization: Bearer $API_TOKEN" "$BASE/api/teams/$TEAM_ID/export")
echo "$EXPORT" | grep -q '"manifests"\|"manifest_error"' \
  || fail "the export says nothing about each app's Kubernetes objects"
echo "$EXPORT" | grep -q 'verysecret' && fail "the export contains a secret value"
pass "the export carries the apps and none of the secrets"

code=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $MEMBER_TOKEN" \
  "$BASE/api/teams/$TEAM_ID/export")
[ "$code" = "403" ] || fail "a member downloaded the whole team, answering $code"
code=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $MEMBER_TOKEN" \
  "$BASE/api/settings")
[ "$code" = "403" ] || fail "a member read the panel's settings, answering $code"
pass "a member can use the panel and cannot run it"

# --- the panel hands out its own binary -------------------------------------
#
# One file is the panel, the CLI and the MCP server, so the panel can serve the
# thing somebody wants on their PATH. The installer fetches it from here rather
# than from a releases page that does not exist, which is why this checks that
# what comes back actually runs and is the same version.

code=$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/api/cli/download")
[ "$code" = "200" ] || fail "downloading the CLI answered $code before signing in, and the installer has no account yet"
pass "the CLI can be downloaded before there is anybody to authenticate"

curl -fsS "$BASE/api/cli/download" -o "$WORKDIR/skifity-downloaded" || fail "the CLI could not be downloaded"
chmod +x "$WORKDIR/skifity-downloaded"
DOWNLOADED=$("$WORKDIR/skifity-downloaded" version 2>/dev/null || true)
RUNNING=$("$BINARY" version)
[ -n "$DOWNLOADED" ] || fail "what the panel served does not run"
[ "$DOWNLOADED" = "$RUNNING" ] || fail "the panel served $DOWNLOADED while running $RUNNING"
pass "what it serves runs, and is the version the panel is running"

curl -fsS "$BASE/api/meta" | grep -q '"cli_platform":"' \
  || fail "the panel does not say which platform its binary is for"
pass "the panel says which platform that binary is for"

# Logging out must end the session.
"$BINARY" logout >/dev/null
printf '\nAll panel smoke checks passed.\n\n'
