# Skifity

Self-hosted apps, powered by Kubernetes.

One binary is the panel, the CLI and an MCP server. Kubernetes runs underneath and
stays out of sight: the product talks about Apps, Instances, Servers, Domains and
Databases, and shows Kubernetes objects only under "Advanced".

Read `docs/progress.md` for where the work stands, `docs/decisions.md` for why
things are the way they are, and `docs/architecture.md` for how it fits together.

## Working rules

* **English only** in code, comments, commit messages, documentation and CLI
  output. The user interface is translated; the source is not.
* The panel ships in five languages (`en`, `id`, `hi`, `ru`, `zh-CN`). Every
  user-visible string is a key in `web/src/locales/*.json`, and
  `npm --prefix web run check:i18n` fails the build if any language is missing
  one. There are no hardcoded strings in the UI.
* The UI is built **only** with shadcn/ui components in `web/src/components/ui`.
  No second component library.
* Conventional Commits, small and often.
* Never run an installer, k3s, or anything system-level on a development
  machine. Infrastructure work happens inside a VM or a container.
* Never commit a secret. `.env.example` documents every setting; tests and
  fixtures use obviously fake credentials.

## Layout

```
cmd/skifity/        The single binary's entry point.
internal/
  api/              HTTP surface, authentication, authorization, SSE.
  auth/             Passwords (Argon2id), sessions, TOTP, API tokens.
  crypto/           Envelope encryption, the keyring, key rotation, recovery keys.
  store/            SQLite schema and repositories. Hand-written SQL.
  errdoc/           The error catalogue: cause, impact, fix, for every failure.
  kube/             Manifest generation, naming, namespaces, the cluster client.
  cluster/          What the panel asks a live cluster to do.
  provision/        Adding, promoting and removing a server over SSH.
  sshx/             The SSH client, plus a real in-process server for tests.
  builder/          Source detection and the build Job.
  deploy/           Deployments, rollbacks, scaling and the readiness checker.
  dbsvc/, backup/   Managed databases and their backups.
  cli/, mcpserver/  The CLI and the MCP server, both against the same API.
  manifests/        Renders the panel's own Kubernetes objects from deploy/.
  templates/        The one-click catalogue: catalogue/*.yaml, one per app.
  buildctx/         No code: the tests that check the image could be built.
  plugins/          The plugin standard: the manifest, permissions, events.
  pluginstore/      Reading and verifying a plugin store's signed index.
  edgerules/, geoip/, guard/
                    The firewall: the rule engine, the databases it reads, and
                    the process that enforces it in front of an app.
  cron/             The five-field parser backups and scheduled commands share.
  registry/         The in-cluster registry's HTTP API, for the sweep.
  serverapp/        Wiring: the HTTP server, the minute tick, startup recovery.
  docsite/          Serves docs/ in the panel and checks every link resolves.
  netguard/, shellsafe/, runsafe/
                    The three "do not let this be a hole" packages: an address
                    the panel dials, a string a shell runs, a panic in a
                    goroutine.
  settings/, notify/, gitsrc/, audit/, events/, logging/, config/
  metrics/, watch/, version/
web/                The frontend. Built into web/dist and embedded in the binary.
deploy/             The panel's own Kubernetes objects, with placeholders.
docs/               Also a Go package: the user-facing pages are embedded.
installer/          install.sh and uninstall.sh. POSIX shell, no bashisms.
test/smoke/         End-to-end shell tests that run a real panel.
```

## Commands

```
make build       Frontend and binary, in that order.
make dev-api     The panel in dev mode, proxying the UI to Vite.
make dev         The Vite dev server (a second terminal).
make check       What CI runs: every linter, the tests, then the vulnerability
                 scan. If it does not contain something CI fails on, it is a
                 gate with a hole in it — that happened, see docs/progress.md.
make smoke       Build and run the smoke tests against a real panel.
make verify      The end-to-end check against a real cluster. Installs k3s and
                 changes the machine, so it is never part of `check` and asks
                 before it starts. See test/cluster/README.md.
make e2e         The Playwright interface test, against the real binary.
make audit       Known vulnerabilities, Go and npm.
make image       The panel's container image.
make release     Static binaries for linux and darwin, amd64 and arm64.
```

`CGO_ENABLED=0` everywhere: the SQLite driver is pure Go, so a release binary is
static and needs no toolchain to run. The race detector is the one exception and
turns cgo back on for that run only.

## Conventions

* **Errors are documented, not printed.** A failure returns an `errdoc.Problem`
  with a cause, an impact and a fix. If a new failure has no entry in
  `internal/errdoc/catalogue.go`, add one rather than returning a bare error.
  A `WithDocs` link must resolve to a page the panel serves, at an anchor that
  exists; a test in `internal/docsite` enforces both.
* **Nothing is logged that a secret could be inside.** `internal/logging`
  redacts by key and by value pattern. A value that genuinely has to be logged
  is wrapped in `logging.Public`, and that should stay rare.
* **Authorization lives in `internal/api`**, in `authorizeTeam`, `authorizeApp`
  and friends. A handler that reaches into the store without one of those is a
  tenant-isolation bug.
* **Secrets are sealed with context.** `keyring.Seal(value, context)` binds the
  ciphertext to where it is stored, so a row copied elsewhere will not open.
* **A config change must not rebuild an app.** The build fingerprint
  (ADR-0007) separates what goes into the image from what the container reads at
  runtime. Keep that line clean.
* **Derive, do not synchronise.** In the frontend, state that follows other
  state is computed during render rather than copied into `useState` by an
  effect.

## Testing policy

Lean on purpose. Unit tests cover the logic that is expensive to get wrong:
encryption and key rotation, manifest generation, config parsing, permission
checks, preflight parsing, the scaling readiness checker. Two smoke tests run the
real binary rather than mocks: one takes the panel through first-run setup,
sign-in, tokens and the CLI, the other checks the installer's logic without
installing anything. Neither touches a cluster, and nothing else does either —
see ADR-0010. No snapshot tests, no coverage targets.

One Playwright test covers the interface: first-run setup, the recovery-key
gate, the shell, the theme, and all five languages. It runs against the real
binary serving the embedded frontend, not a dev server, so what is tested is
what ships. Set `CHROMIUM_PATH` to use a browser already on the machine.

The sandbox this was built in refuses privileged containers, so a real k3s
cluster could never be started here (ADR-0010). Where something has not been
executed, `docs/progress.md` says so plainly.
