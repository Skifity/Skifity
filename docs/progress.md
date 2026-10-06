# Progress

Read this file, `CLAUDE.md` and `docs/decisions.md` first if you are picking this
project up cold.

## Sandbox constraints (important)

The environment this was built in has Docker but **refuses privileged containers
and host-network relays**, so k3d/kind clusters and VMs could never be started.
See ADR-0010. Nothing below is called verified unless it was actually executed.
Where a cluster is required, the code is written and unit-tested against a fake
clientset and golden manifests, and that is said plainly rather than glossed over.

## Phase status

| Phase | Title | Status |
|---|---|---|
| 0 | Research and key decisions | done |
| 1 | Repository foundation | done |
| 2 | One-command installer | done, not run on a real server |
| 3 | Panel core: accounts, security, encryption | code complete, tested |
| 4 | Server and cluster management | code complete, cluster not exercised |
| 5 | App deployment | code complete, builds not exercised |
| 6 | Scaling | code complete, cluster not exercised |
| 7 | Databases, storage, backups | code complete, cluster not exercised |
| 8 | Developer experience and vibe coding | done |
| 9 | Differentiating features | done |
| 10 | Hardening and release | done, except a run on real hardware |

## What is done

### Phase 0 — done

* `docs/research/competitors.md` — 13 products compared, weaknesses we design against.
* `docs/research/stack.md` — component versions verified 2026-09-16, reconciled
  with the code 2026-09-17.
* `docs/architecture.md` — layers, deploy path, add-server state machine, data model, isolation.
* `docs/decisions.md` — ADR-0001 onwards; there are twenty-one.

### Phase 1 — done

* Go module, package layout, `Makefile` with build/dev/test/lint/smoke/release.
* `golangci-lint` configured and clean; `go vet` and `gofmt` clean.
* ESLint and Prettier configured; `npm run lint` clean with zero warnings.
* Frontend: Vite 6, React 19, Tailwind v4, shadcn/ui only, app shell with
  sidebar, header, command palette, dark/light theme applied before first paint.
* Five languages complete (446 keys each), with a checker that fails the build on
  a missing key, an extra key, an empty string, a dropped placeholder, or a
  plural form the language does not have. Deliberately broken translations were
  used to confirm it actually fails.
* The built frontend is embedded in the binary; a deep link reloads correctly and
  fingerprinted assets are cached for a year while `index.html` is not.
* GitHub Actions CI: Go vet, format check, golangci-lint, tests, race tests;
  frontend i18n check, lint, type-check and build; then the smoke test.

**Verified by running it:** `make build` produces one 55 MB binary that serves
the localized UI and the health API; `/apps/app_x` falls back to `index.html`;
`test/smoke/panel.sh` passes all 26 checks against that binary.

### Phase 2 — done, not run on a real server

* `installer/install.sh` — POSIX shell, no bashisms, checks the server before it
  changes anything, installs k3s with embedded etcd and WireGuard between nodes,
  prepares the panel's directories, generates the setup token, works out a
  hostname, applies the panel and prints a URL and a token. Safe to run again:
  every step checks what is already there.
* `installer/uninstall.sh` — removes the panel by default, k3s with `--all`, and
  the data only with `--purge` and a typed confirmation. `--dry-run` prints what
  it would do and changes nothing.
* `deploy/` — the panel's own objects, also usable with `kubectl apply -f`.
* `Dockerfile` — three stages down to a distroless image holding one static
  binary, running as a non-root user.

**Verified:** `test/smoke/installer.sh` runs 26 checks — both scripts parse under
`sh` and `dash`, failures carry a cause and a fix, every manifest renders with
nothing left to substitute, the plain-HTTP route does not claim a certificate it
does not have, and nothing is installed on the machine running the test. A Go
test parses the rendered Deployment and asserts it runs as non-root with a
read-only root filesystem, no capabilities, no CPU limit, the Recreate strategy
and the node pinning. Both were confirmed to fail when the manifests were
deliberately broken.

**Not verified:** the installer has never been run on a real server. It needs
root, systemd and a kernel k3s can use, none of which this sandbox allows.

### Phase 3 — code complete, tested

Accounts, Argon2id passwords, sessions, TOTP implemented in-house, API tokens,
CSRF double-submit, security headers, an audit log, and envelope encryption with
per-secret DEKs, context binding, master-key rotation and a printable recovery
key. Unit tests cover the envelope format against bit-flips and non-canonical
encodings, and rotation across every encrypted column.

### Phases 4–7 — code complete, cluster not exercised

Adding a server over SSH (seven idempotent steps), preflight checks, firewall
rules written per source address, k3s install and join, promotion and removal
with drain; manifest generation with probes, limits, spread and network policies;
builds through Railpack and BuildKit; deployments, rollback, autoscaling and the
scaling readiness checker; managed PostgreSQL, Redis and MariaDB; backups to S3
through presigned URLs so storage credentials never enter a tenant namespace.

Exercised against a fake clientset, golden manifests, a real in-process SSH
server that records the commands it receives, and `sh -n` on every generated
script. Not exercised against a real cluster.

### Phases 8–9 — done

The CLI and the MCP server are built on the same API as the panel, and both
render errors with their cause, impact and fix. The panel has the project canvas,
one-step templates, the error catalogue, and the build fingerprint that keeps a
configuration change from rebuilding an image.

Added since:

* `llms.txt`, describing the whole product on one page, served by the panel at
  `/llms.txt`.
* `SKIFITY_URL` and `SKIFITY_TOKEN`, so the CLI and the MCP server work with no
  interactive sign-in and no stored configuration — which is how CI, a
  container and an assistant's sandbox actually use them. The team is worked
  out from the token's account when there is only one.
* `skifity admin reset-password` and `skifity admin list-users`, which read the
  panel's database directly on the server. Nobody being able to sign in is the
  one thing the API cannot fix, and a self-hosted panel has no mail server it
  can trust to send a reset link.
* Connecting a Git account and choosing where to be notified, both of which had
  a complete API and no way to reach it from the panel. Connecting a Git account
  shows the webhook address once, with a copy button, because a self-hosted
  Gitea whose token cannot register a webhook needs it pasted in by hand.
* `docs/configuration.md` and `docs/backups.md`.
* The documentation is served by the panel itself, from inside the binary, so
  the roughly twenty links in the error catalogue resolve on a server with no
  outbound network — which is when they matter. A test walks every `WithDocs`
  link and every link between pages, and fails on a missing page or a missing
  anchor. Both failure modes were confirmed by breaking a link on purpose.

### Phase 10 — done, except a run on real hardware

* `govulncheck` and `npm audit` are clean and run in CI. The one advisory left
  is `golang.org/x/crypto/openpgp` being unmaintained, in a package this code
  never imports; govulncheck reports zero reachable vulnerabilities.
* A Playwright interface test covering first-run setup, the recovery-key gate,
  the shell, the theme, and all five languages. It runs against the real binary
  serving the embedded frontend, so what is tested is what ships.
* GoReleaser: static binaries for Linux and macOS on both architectures, and a
  multi-architecture distroless image, on a tag.
* The documentation set: quick start, concepts, adding servers, the CLI and AI
  assistants, troubleshooting, questions — plus a README with real screenshots
  taken by the test suite, so an image can never show a screen that no longer
  exists.
* Apache 2.0.

**The interface test found a real bug the completeness checker could not:**
Simplified Chinese resolved to English at runtime. `nonExplicitSupportedLngs`
makes i18next check the *language part* of a code against `supportedLngs`, so
asking for `zh-CN` looked up `zh`, did not find it, and fell back. The locale
file was complete the whole time. Fixed by registering the bundle under `zh` as
well, which also gets `zh-TW` and `zh-HK` a Simplified page rather than an
English one.

### After an adversarial audit

The whole product was read back against what it claims, one dimension at a time,
looking for the class of defect a sandbox with no cluster cannot catch: code that
compiles, tests that pass, and a feature that was never once executed. What came
out of it:

* **Notifications were configured and never sent.** `notify.Send` was called from
  exactly one place in the codebase — the "send a test message" button. No
  deployment failure, backup failure, lost server or certificate failure ever
  produced one, although the panel let an operator subscribe to all of them.
  There is a dispatcher now, and the producers call it.
* **Nothing watched the cluster between user actions.** A node that stopped
  answering at three in the morning, an app whose last instance crashed, a
  certificate cert-manager had given up on: all three were visible only to
  somebody already looking at the right page. `internal/watch` compares the
  cluster with the database once a minute. It also fills in two columns the
  panel had written a query for and never called: a server's last seen time, and
  a domain's certificate status, which said "waiting for DNS" forever.
* **No app ever got a URL.** `kube.AutoHostname` was written, tested and called
  by nothing, so the free address the product promises on every page existed
  only as a function. A deploy now gives an app its automatic domain.
* **An app created from the panel had no port**, because the form does not ask,
  and a port of zero renders no Service, no Ingress and no URL.
* **A build could send a team's Git token to any host**, because the clone
  rewrote the URL to include the token whatever host was typed into the form.
* **An API token's team and scopes were stored and never read**, so a token
  issued for one team worked on every team, and a read-only token could do
  anything its owner could.
* **Two-factor recovery codes were generated, returned by the API, and dropped**
  by both sides; the account page never even rendered them.
* **Deleting a volume** authorized the app in the URL and then deleted whatever
  volume id came after it.
* **A backup job could not have run**: rejected by Pod Security for naming no
  seccomp profile and no user, then killed by its own package-install line, then
  refused by S3 for uploading from a pipe with no Content-Length.
* **The builder could never start**: rootless BuildKit needs a seccomp profile
  the panel's namespace refuses, and its readiness probe looked for a socket
  that is not there. Builds moved to their own namespace (ADR-0016).
* **Built images could never be pulled**, because they are tagged with a Service
  name the host's containerd cannot resolve (ADR-0017).
* **Every build was a cold build**: the cache was exported inline and imported
  from a tag nothing ever wrote.
* **Adding a server built a second cluster.** The panel decided a server was
  the first one by looking for a control-plane row in its own database, and a
  panel installed by `install.sh` has none. The first server anyone added was
  given `--cluster-init`, and its token could never have matched the real one
  anyway.
* **A second control-plane node joined with a different network backend** than
  the first, so the two never exchanged a packet.
* **A fork's pull request was handed the project's secrets**, because a preview
  environment copied every variable into a container built from the pull
  request's own code.
* **Scale to zero installed KEDA and changed nothing**: the flag was carried
  into the app spec and read by no manifest.
* **The MCP server's every tool answered "this token is not tied to a team"**
  for a token set up the documented way, and it had no way to create anything.
* **The logs tab could not show why a crash-looping app crashed**, because the
  container that printed it had already been replaced.
* **A superseded deployment kept building** and could roll out an older version
  after the newer one.
* **The event hub never forgot a topic**, so a panel up for a month held the
  build output of every deploy since it started.
* **Half the audit log was invisible**: every panel-wide event — password
  changes, settings, key rotation — was recorded with no team and the list
  filtered on the team alone.
* **Preview environments were never reclaimed** except by a webhook that had to
  arrive.
* **An instance's CPU and memory were always an em-dash**, and the panel's own
  database had no way to be copied that did not silently lose data.

### What was missing rather than broken

Three things the audit named repeatedly as gaps rather than defects, now built:

* **A release command and a one-off command.** There was no way to run anything
  in an app's environment, so a migration had nowhere to go. An app's release
  command runs on every deployment between the build and the rollout; a one-off
  command runs once on request. Both run in the app's own image with its own
  variables, as a Job rather than an exec, because the moment you need this
  most is when the app will not start.
* **Scheduled commands.** Cron existed and ran the panel's own backups; an app
  could not have one. Kubernetes does the scheduling now, so a panel restarting
  at three in the morning is not a reason for a job to be skipped.
* **Honest components.** "Full monitoring" had an Install button that always
  failed. It says how to install it instead.

### A second pass over the cluster, the backend and the frontend

The first audit asked "what does this claim that it never does". This one asked
a narrower question of the same three layers: what do the pieces do to each
other. Everything below was found by reading the rendered objects and the
selectors against one another, and each fix carries a test that was confirmed to
fail against the old behaviour.

* **Every followed log ended after thirty seconds.** `rest.Config`'s Timeout is
  the HTTP client's, so it bounds the response body as well as the request. A
  log stream is a request that succeeds at once and is then read for as long as
  somebody watches it, so one deadline for both kinds of call cut it off. The
  browser reconnected and replayed its tail, so live logs repeated themselves
  every half minute and a build log stopped part way through a build that was
  still running. There are two clients from one connection now.
* **A quiet log stream was dropped by whatever sat in front of it**, because it
  had no heartbeat and could not have written one while the handler was waiting
  on the pod.
* **An autoscaling app that could also sleep had two controllers.** It got an
  HorizontalPodAutoscaler and an HTTPScaledObject, and KEDA creates an
  HorizontalPodAutoscaler of its own for the second. Two of them pointed at one
  Deployment do not divide the work: each overwrites the other's replica count
  on every reconcile. KEDA owns the scaling when scale to zero is on, and the
  scaling tab says so rather than leaving two fields on screen that no longer
  decide anything.
* **The disruption budget deadlocked a node drain.** `minAvailable: 1` permits
  two of three instances to go at once and none at all when an autoscaled app is
  sitting at its minimum of one — so `kubectl drain` waited forever for an
  eviction that could never be allowed. `maxUnavailable: 1` is the promise that
  was meant: one at a time, at every instance count.
* **A migration's pod counted as an instance of the app.** It carried the app's
  own selector labels, so it appeared on the instances tab as though it were
  serving traffic, its CPU was averaged into the autoscaler's decision, and it
  could be picked as the pod to read the app's logs from — which is how somebody
  opens the logs tab during a nightly job and reads the job instead.
* **A deleted app kept running.** Deleting it removed the Deployment, the
  Service, the Ingress and the Secret, and left the scheduled commands: the
  nightly job went on firing every night forever against an image nothing would
  pull. The wake Service and the HTTPScaledObject were left too.
* **A scheduled command's history was empty by morning**, because it inherited
  the one-off's hour-long TTL, and the morning is exactly when somebody looks for
  the run that failed.
* **The nixpacks builder could never have built anything.** Its buildctl line
  read `.nixpacks/Dockerfile` and no step ever wrote one. It is reachable from
  the API and the CLI; the panel's own form only offers auto and Dockerfile,
  which is why nobody had hit it.
* **An app and a database could take each other's address.** They are unique
  among themselves, share a namespace, and both render a Service under their
  slug — so a Redis called "web" next to an app called "web" took the app's
  Service over while its Ingress went on pointing at the name. Both creation
  paths now refuse the collision and say what holds it.
* **A render that threw blanked the whole panel.** React unmounts the tree when
  nothing catches it, and what is left is a white page and a console message. On
  a self-hosted panel that is the worst failure there is: the cluster is fine,
  the apps are serving, and the operator cannot tell. There are two error
  boundaries now, and the one inside the shell leaves the navigation working so
  the person can simply go somewhere else.

**What this pass did not find:** the store, the authorization layer and the
event hub came out clean. Every team-scoped handler goes through `authorizeTeam`
or `authorizeApp`, every write is serialised behind one mutex and every raw
statement runs inside `db.Tx`, and the SSE handler already had its heartbeat,
its replay and `X-Accel-Buffering`.

### The roadmap, worked through

`docs/roadmap.md` set out what was left, largest first: the things that decide
whether a panel survives its second month, then the questions people actually
ask it, then two features that were gaps rather than defects. All of it is
built.

* **The registry collects its garbage.** Every build pushed an image and nothing
  ever removed one — the first open issue on this page since the day it was
  written, and the one that ends with a full disk and every pod on the node
  stopping at once. The panel untags what nothing can reach and a Job runs the
  registry's own collector beside the files. Builds are held while it runs,
  because a push concurrent with a collection is the one case the registry's
  documentation says corrupts an image, and the panel is the only thing that
  starts builds.
* **The panel's own database stops growing.** Deployment records, audit entries
  and finished operations were written and never removed. A daily pass, with the
  window in settings; the activity log keeps a year by default, because a log
  that forgets is most of the way to not having one.
* **A pending instance says why.** It used to say "waiting for a server with
  enough free CPU and memory" whatever the reason, which is right about a third
  of the time and otherwise sends somebody to add a server for a quota, a
  volume, or a control-plane node that does not take apps. Kubernetes writes the
  answer onto the pod; the panel now reads it. A quota refusal never reaches a
  pod at all, so that one is read off the Deployment.
* **An environment's limits are visible before they are hit**, once something is
  above sixty per cent.
* **The panel can be monitored.** `GET /api/metrics`, in the Prometheus format,
  behind the same authentication as everything else, written by hand rather than
  pulled in.
* **A repository is read before it is built.** `builder.Detect` had been written,
  tested and called by nothing. The panel reads the tree through the provider's
  API — two requests, no clone — and says what it found while the form is still
  open, as a guess presented as a guess.
* **A volume can be backed up**, mounted read-only, as the app's own user, on
  the node that holds it.
* **The frontend is split by what changes**, and the labels only a screen reader
  hears are translated — "Close", "Loading", "Toggle Sidebar" were all hardcoded
  English in a panel shipping five languages, and the i18n check now fails the
  build on the next one.

### A second pass over tenant isolation

The roadmap put this next because it is the one class of bug where being wrong
is not recoverable. Every route was traced to its authorization helper, every
second id in a URL checked against the first, and the CLI, the MCP server and
the Git webhook followed back to the same checks — the first two go through the
HTTP API with the caller's own token, so there is no second surface to get
wrong, and the webhook skips any app whose team is not the connection's.

Three things came out of it.

* **The panel could be pointed at the cloud metadata service.** A Git
  connection's base URL and a notification channel's webhook are both settings
  holding an address the panel's own process requests. The webhook path echoed
  part of the response back in its error, which turns a test message into a
  read. `internal/netguard` refuses link-local, loopback, the unspecified
  address and multicast at connection time, on the resolved address rather than
  the hostname. Private ranges stay allowed: a self-hosted Gitea on 10.0.0.5 is
  the ordinary case here, not the attack.
* **Unlinking a database authorized the database and not the app.** Linking
  checks both and says so in a comment; unlinking checked one. It removed no
  variable it did not own, and did re-apply that app's configuration to the
  cluster.
* **The api package had no tests.** "Authorization lives in one place" was true
  and unenforced. Six now run against the real router with a real database and
  keyring: every shape of id one team can ask another for, the token's team
  binding, read-only scopes, what is open without credentials, what is
  administrator-only.

What did not turn anything up: the authorize helpers themselves, the SSE topic
authorization, the encryption contexts, secret disclosure through the variables
and credentials endpoints, and the webhook's team scoping.

### A pass over the code that runs as root on somebody's server

The provisioning path had never been read this session and is where a defect
does the most damage: it runs shell as root on a machine the user owns, and it
can delete the cluster. Two things came out of it, and the second explains why
the first had survived.

* **`%q` is not shell quoting, and every generated script used it.** Go's `%q`
  produces a Go double-quoted literal; a shell reading a double-quoted string
  still expands `$` and a backtick, and `%q` escapes neither. So
  `fmt.Sprintf("TARGET=%q", "1.2.3.4$(id)")` produces `TARGET="1.2.3.4$(id)"`
  and the shell runs `id`. The code reads as though it is defended and is not.
  Every script this panel generates used it — the ones that provision a server
  over SSH and the ones that build an image. `internal/shellsafe` quotes with
  single quotes, the only quoting a POSIX shell does not look inside, and a test
  puts both forms through a real `sh` and shows the difference rather than
  asserting it. The one reachable path was the SSH account name, which was never
  validated; it is now checked against the shape a distribution produces.
* **The guard against destroying the cluster counted the wrong thing.** Removing
  a control plane node is refused when too few would be left, and it counted
  rows in one team's table rather than the nodes that actually run the cluster.
  A panel installed by `install.sh` has no row for the node it runs on, and a
  panel with more than one team splits the rest between them, so the number was
  low by at least one and scoped to the wrong thing. It refused removals from a
  healthy four-node control plane, and at zero it permitted the one removal that
  deletes Kubernetes, every app, and the panel answering the request.

**And the pattern underneath both.** Three handlers checked "is this feature
configured" before the check that mattered — before validating the request,
before authorizing the second resource, before the quorum guard. Beyond the
small leak of telling somebody who may not touch an app whether databases are
configured, it means a safety check only runs on a configured panel, so nothing
can test it without a cluster. That is why this class kept surviving review. The
checks run first now, and the tests for them run against a panel with no cluster
at all.

## Phase 19 — the research, read back against the code

The three research pages were written before the code and never checked against
it afterwards. Reading them back found two promises the product did not keep and
a page that had drifted.

* **The WireGuard fallback did not exist.** The preflight told an operator that
  a kernel without the module was fine and that "traffic between your servers
  will use vxlan". Nothing did that: the backend was hardcoded to
  `wireguard-native` in the panel's install scripts and in `install.sh`, so the
  server joined a cluster it could not exchange a packet with, and neither k3s
  nor the panel reported anything wrong. The pod network is now one choice for
  the whole cluster, stored in `cluster.flannel_backend`. The installer picks it
  on the first node — where the fallback is real, because there is nothing yet
  to disagree with — and hands its choice to the panel, which records it and
  installs every later server the same way. A server whose kernel cannot run the
  cluster's backend is refused, with the two fixes that work.
* **Compose was announced, not implemented.** Detection reported "Docker
  Compose" and said services become separate apps and their links become
  variables. `ConvertCompose` was written, tested, and called by nothing; the
  Compose file's contents were never fetched; and `compose` was a source an app
  could be created with, which the API accepted, stored, and then deployed as a
  Git app with no repository. The reader is wired up now: the file is fetched
  and parsed, the services are listed with what did not carry over named against
  the service it came from, and picking one fills the form in — name, root
  directory, port, image or build, and the service's variables, which the create
  endpoint now seals before the first deploy. Skifity still runs one service per
  app, and the panel says so rather than implying an import.
* **A Dockerfile's `EXPOSE` line was never read.** Same cause: the file was
  found in the tree and then read back as an empty string, because it was not in
  the list of files fetched. The port detection it fed had never once produced
  an answer.
* **`docs/research/stack.md` had drifted** — `--write-kubeconfig-mode=0644`
  against 0600 in the code, a node label the code never sets, `--secrets-encryption`
  missing, self-hosted fonts the frontend deliberately does not use, and a link
  to a page that does not exist. It is reconciled, and a test now reads the
  flags out of the page and fails when no generated script passes one.

## Phase 20 — the tooling, and the catalogue

* **`make check` could not be run.** The README calls it "what CI runs" and it
  failed twice on tooling rather than code: golangci-lint refuses a module whose
  `go` directive is newer than the Go it was built with, and took the whole
  suite down with it, so nothing here had ever been linted; and
  `npm --prefix web exec -- tsc -b` keeps the directory it was called from, so
  tsc looked for a tsconfig.json at the repository root. `scripts/lint-go.sh`
  now builds the linter with this module's own toolchain, which cannot be out of
  step with it, and CI runs the same script with `LINT_STRICT=1` so it can never
  skip there. With the linter finally running it found eight things, all fixed,
  including two doc comments left behind by a rename.
* **The template catalogue had no tests, and a wrong template is silent.** A
  database whose `link_to` names no service was created and then never linked,
  so the app came up without the one variable it cannot run without and
  crash-looped with nothing on screen saying why. That now fails the install.
  The catalogue is checked for it, and for engines Skifity does not provision,
  variable names a container could not carry, services that ask for more memory
  than they are allowed, and templates nothing can open.
* **Four templates ran `latest`.** n8n, Vaultwarden, Umami and MinIO. A floating
  tag is not a version: two deploys of the same app run different software, a
  rollback restores a tag rather than the thing that worked, and an upstream
  release arrives on a restart nobody asked for — in a product whose whole point
  is that a rollback works. All four are pinned, the card shows the version, and
  a test refuses anything ending in `latest`.
* **`Icon` was a field the UI never read.** It is gone, and the card shows what
  the template installs instead.
* **Templates had no documentation page.** `docs/templates.md`, served by the
  panel like the rest.
* **The configuration file had three ways to be wrong quietly.** The struct's
  toml tag said `kubeconfig_path` and the parser looked for `kubeconfig`, so a
  file written from the struct was read, accepted and ignored. A `#` anywhere in
  a value cut the value short, whether or not it was inside quotes, so a path
  with a hash in it became a shorter path and what failed was whatever used it.
  A quote that was never closed was accepted, taking the comment somebody
  thought they were writing along with it. `internal/config` had no tests at
  all; it has them now, and one of them derives the list of settings from the
  struct and fails when any of them is missing from `docs/configuration.md` —
  which two of them already were.

### The claims, read back one by one

Every checkable claim in `README.md`, `llms.txt` and the documentation was put
against the code. Most held — the seven provisioning steps, the password that is
never stored and the test that scans every column for it, all fourteen MCP
tools, all thirty-two documented API routes, the three scaling risks named by
name, the copy-for-AI button, the placeholder check in the i18n script. These
did not:

* **"It does not contact any server but yours"**, and in the FAQ, "it does not
  contact any server at all". Skifity reaches Let's Encrypt, GitHub for the
  component manifests, the user's Git provider and S3 bucket — and the preflight
  asks a public-IP service for the address of a server being added, which is the
  one call a privacy-minded reader would actually want named. "It never phones
  home" is true and stays; the absolute around it is gone.
* **"Every command takes `--json`."** `open`, `logout`, `admin reset-password`
  and `admin backup-db` did not. That claim is aimed at assistants, who read it
  literally and get "flag provided but not defined". All four have it now, and a
  test reads the package and fails when a command is added without it.
* **The measured figures had drifted.** Idle memory is 35 MiB, not 34. The
  frontend is 314 KiB gzipped across nine files, not 278 across five — the chunk
  split in Phase 15 changed both numbers and neither was re-measured.
* **"Any server can be promoted."** True of the code, and that was the bug — see
  below.
* **Every published install path points at nothing.** See the open issues.

The API route table is now checked against the router itself: llms.txt is what
an assistant reads before calling anything, and a route that moved does not read
as a documentation mistake, it reads as the product being broken.

### Promotion wiped the node before checking it

The panel refuses to add a control plane server below 2 GB and 20 GB. It would
promote one without looking. Promotion drains the node, removes it from the
cluster and uninstalls Kubernetes before reinstalling it as a control plane
member, so the first thing to notice a machine too small for etcd was the
install at the very end — with the apps already moved off and a working worker
turned into a machine with nothing on it. The check runs first now, before
anything is touched.

### One panic used to end the panel

The HTTP handlers have recovered from a panic since the beginning. Nothing else
did. Every deployment, every server being added, every database, backup,
restore, health pass, scheduler tick and notification runs in a goroutine of its
own, and a panic in any of them took the process down — on a self-hosted install
that is the panel you would use to find out why, so the deployment that crashed
it also removed the way to diagnose it.

`internal/runsafe` recovers, logs the stack, and hands the failure to whatever
was running so it is marked failed rather than left at "running" forever. It is
wired into the two choke points every deployment and every provisioning
operation already pass through, into the backup, restore, volume-backup and
database goroutines beside the `fail` each of them already had, and into the
dispatcher. The three long-running loops recover per iteration rather than
around the loop: a panel that is alive with no scheduler is worse than one that
restarted, because nothing says so and the backups simply stop.

The test for it does not report a failure when it regresses. It takes the test
binary down, which is the point.

### The guards that stopped guarding

Four safety checks were written as `if err == nil && <the dangerous
condition>`, which reads as caution and means the opposite: the moment the query
behind the check fails, the check disappears and the destructive path runs. This
is the same shape as the capability-before-check pattern from Phase 18 — a
safety check that only runs when everything else is already well — and one of
these four was that pattern as well.

* **Deleting a database** skipped the "apps still use this" check when the links
  could not be read, and ran that check after the cluster capability check, so
  nothing could test it without a cluster. Both fixed; a test drops the
  `database_links` table and asks for the delete.
* **Restoring a backup** over a live database did the same thing with the same
  query. A failure to read the links is not an empty list of them.
* **Demoting the last owner** skipped the last-owner check when the membership
  or the owner count could not be read, leaving a team nobody can administer.
  Not being a member yet is the ordinary case and still passes; anything else
  now refuses. A test drops the `memberships` table.
* **Removing an owner** allowed it when the actor's own role could not be read.
  Nothing can reach that branch — `authorizeTeam` refuses a caller with no
  readable membership first — so there is no test for it, only the change. The
  ordinary guard, that an admin may not remove an owner, had no test either and
  has one now.

Two more in the same handler. Deleting a database asked which team it belonged
to *after* the row was gone, so the deletion was recorded with no team — filed
with the panel-wide events, which belong to no team by design, and shown in the
audit log of every team that person is in rather than in the one whose database
it was. And `dbsvc.Delete` removed the apps' database variables inside an
`if err == nil`, so a query failure left apps holding a connection string to
something that no longer exists, silently; it now says so.

`internal/errdoc` also had no tests. The catalogue is the product's central
promise — cause, impact and fix on every failure — and an entry missing one of
those still compiles and still renders. A test now reads the file as source and
checks every entry for all three, for a code shaped like a code, and for codes
that do not collide, since the UI picks a translation by code.

## Phase 21 — the competitors, researched properly

`docs/research/competitors.md` was written before the code and never checked
against the market again. Re-researched against live 2026 sources, it changed
the conclusions rather than confirming them.

* **Coolify disclosed eleven critical CVEs on 8 January 2026, five at CVSS
  10.0**, all authenticated command injection ending in root, plus a readable
  root SSH key and later a cross-team authorization bypass. Censys counted
  52,890 publicly reachable dashboards. The published cause is a class, not a
  mistake: user input reaching a shell in many independent code paths — which is
  exactly what `internal/shellsafe` exists for and exactly the bug found in this
  repository in September. ADR-0002a now records why there is one place for it.
* **Railpack is no longer a differentiator.** Dokploy ships it already, along
  with SSO/SAML that Skifity does not have.
* **aaPanel is not a competitor.** It replaces cPanel — websites, PHP, mail, a
  WordPress toolkit — and is sold to agencies running hundreds of client
  servers. It was researched and removed from the comparison rather than left in
  as a name.
* **Kubernetes is a category mismatch as much as an advantage.** Comparison
  sites exclude k3s from "self-hosted PaaS" as a different abstraction layer
  entirely, and one of the most-read 2026 guides is titled "Best Self-Hosted
  PaaS to Replace Heroku (No Kubernetes)". The audience is selecting away from
  what this is built on, which makes hiding it well the whole bet rather than a
  nice touch.
* **The footprint gap is the clearest measurable win.** 35 MiB against Coolify's
  500 MB–1.2 GB and Dokploy's ~350 MB.
* **Eight templates against Coolify's 280+** is the single most-cited reason
  people choose Coolify, and it is a content problem rather than an engineering
  one.

## Phase 22 — the catalogue, closed

Eight templates against Coolify's 342 was the largest gap we had, and the
research said it was a content problem rather than an engineering one. It was
both: a Go literal is fine for eight and impossible for three hundred, which is
why Coolify's catalogue is files and grew by contribution.

* **The catalogue is data now.** `internal/templates/catalogue`, one YAML file
  per template, embedded at build time. Adding one touches no Go, and the
  directory's README has the format and the two rules that are not obvious.
* **212 templates**, up from eight. 204 were converted from Coolify's catalogue
  by `hack/import_templates.py`, which is kept so the next batch is a re-run.
* **Every image was resolved to a version and verified.** `hack/resolve_tags.py`
  asks each registry for the tags, prefers a series tag that takes patches over
  an exact pin, and then fetches the manifest to prove the tag exists. 225 of
  269 single-service templates resolved; the 44 that did not were dropped rather
  than shipped on `latest`. More than half of Coolify's own entries ship
  `latest`.
* **The database wiring was taken out, and that was a real bug.** A Compose file
  points an app at a sibling container — `DB_HOST=mariadb` — and here the
  database is a managed one that arrives as a connection string. The first
  import carried those over, which would have produced apps that start, fail to
  resolve a hostname nobody recognises, and crash-loop. Bookstack, GLPI,
  Metabase, Redmine and Keycloak were all affected. A test now refuses any
  variable that names a datastore and ends in an address.
* **Nothing was shipped half-filled.** Templates with several application
  services (72), no port (15), or no verifiable image (46) were dropped.
* **The panel did not get heavier**: 34 MiB idle and 39.4 MiB of binary, against
  35 MiB and 39.3 MiB before.

What this does not mean: none of these has been deployed, because nothing in
this product has. What is checked is that each template is structurally sound
and that its image exists.

## Phase 23 — CI had been red on every commit

Twenty-three runs, all of them red, back to the first. Nobody looked, including
whoever wrote `make check`.

The failure was one step: `govulncheck`. `go.mod` said `go 1.26.0`, CI's
setup-go installs exactly what `go.mod` asks for, and Go 1.26.0 shipped with 21
known standard-library vulnerabilities that 1.26.1 fixed — one of them reachable
from `internal/notify`, which dials TLS to send mail. Locally the same command
passed, because `go run …@latest` quietly switches to a newer toolchain and the
scan then reports the newer standard library. The local answer and the CI answer
were about two different Go versions.

Two things were wrong, and the second is worse:

* **The `go` directive named a version with known holes.** It now names a patch
  release, so anyone building this gets a fixed standard library rather than
  whichever one they happen to have.
* **`make check` did not run the step that was failing.** It is documented as
  "what CI runs" and it was not: no `audit`. A gate with a hole in it looks
  exactly like a gate. `check` now includes it.

And the consequence nobody would have guessed from the summary line: because
`govulncheck` runs before them, **the race-detector run, both smoke tests and
the Playwright interface test had never executed on CI**. With the scan fixed
they ran, and all of them passed — except one step that had never run anywhere,
on any machine, and was broken in two ways.

### `make image` had never worked

Building the image needs a Docker daemon, and nothing here has one, so the whole
target was unverified for as long as it existed. The first CI run that ever
reached it failed twice over:

* The frontend stage builds from `web/` alone, and the frontend build copies
  `llms.txt` in from the repository root so the panel can serve it. The
  Dockerfile never copied that file, so the build stopped on a missing file that
  is right there in the repository.
* `.dockerignore` excluded `docs`, which is a Go package — `docs/embed.go`
  embeds the pages into the binary — so even past the first failure the backend
  stage would have failed on a missing import.

Both are the same shape as everything else in this log: a path nothing executes,
so nothing contradicts it. `internal/buildctx` now holds two tests that read the
Dockerfile and the ignore file rather than running Docker: every `//go:embed`
path must survive the build context, and anything the frontend build reads from
outside `web/` must be copied in.

This is also the path the quick start tells somebody to use, since nothing is
published — clone, `make image`, run the installer from inside the clone. It
would not have worked for them either.

## Phase 24 — the multi-service templates, and what they were worth

Seventy-two templates in Coolify's catalogue install more than one application
service. Installing several apps from one template was never the problem —
`installTemplate` already creates an app per service — so this was a converter
problem, and the converter kept being confidently wrong.

The first attempt produced 21 templates. They looked fine and were not: a
Sidekiq worker marked public on the web app's port, Elasticsearch given Kibana's
port, n8n given Postgres's, HeyForm given Redis's. Every heuristic fix revealed
another wrong guess, because a Compose file encodes a topology and the converter
was inferring one.

So it stopped inferring. A service's port now comes from the source saying so —
its own `SERVICE_FQDN` marker, or `expose`, or `ports` — or from a short table of
ports that are documented facts about an image, and that table only answers when
the image appears once in the stack (seaweedfs runs a master and an admin from
one image, and 8333 was wrong for both). Anything else is dropped as ambiguous.

That leaves **seven**, and all seven are right. It also produced three real
changes to the product rather than the converter:

* **`link_to` is a list.** A web app and its worker share one database, and
  linking only the first left the worker starting without the variable it cannot
  run without — the same crash loop as a link that names nothing.
* **A worker is `port: 0`**, which the manifest builder already handled: no
  Service, no probes, no ingress. The catalogue tests now allow it, and refuse a
  service that is public or has a health path with no port to check it on.
* **A shared volume does not carry over**, and that is said rather than
  discovered. A Compose volume is shared between services; a Skifity volume
  belongs to one app and is read-write-once, so Chatwoot's web app and its
  Sidekiq get two different `/app/storage` directories. The template says so and
  points at object storage.

Of the 65 that did not convert: 8 need the Docker socket, which cannot run under
a restricted pod security policy at all; 8 are stacks of five to twenty-three
services where getting startup order and shared state right without ever running
them is not a bet worth making; the rest have an image whose version could not be
verified or a service nothing says the port of.

## Phase 25 — the rest of the catalogue, and five resolver bugs

Seven multi-service stacks out of seventy-two, and 44 single-service templates
dropped for an unverifiable image, both looked like the source being awkward.
Most of it was this code being wrong, and each bug read in the log as somebody
else's fault.

**A tag can contain a colon.** `${IMMICH_VERSION:-release}` does, so splitting
the reference on the last colon produced the repository name
`ghcr.io/immich-app/immich-server:${IMMICH_VERSION`, and every registry answered
403. In the log that is the registry refusing us. It was eleven images, Immich,
Outline, Ente and Campfire among them.

**A registry's token comes from its own challenge.** ghcr.io and codeberg.org
were hardcoded and everything else — flipt, rocket.chat, weaviate, gcr.io,
outline, the Docker Hub mirrors — read as "unauthorized", which is what a
registry says when nobody asked it for a token. Reading `WWW-Authenticate` and
asking the realm it names works everywhere.

**A 429 is not "no".** Several vanity registries are pull-through caches in front
of Docker Hub and share its anonymous rate limit. Treating the rate limit as "the
image does not exist" dropped templates whose images were fine. It now backs off
and retries, and a question the registry never answered is recorded as
unverified rather than as absent.

**A tags listing is paginated.** Reading the first page picked
immich-machine-learning v1.132.3's server beside a v1.106.4 model runner — both
tags exist, the stack does not work, and upstream requires the two to match.
`Link` is followed now, and where a Compose file uses one version variable for
several images, the resolved tags are aligned and re-verified before any of them
is used.

**Not every project ships semver.** GitLab ships `19.1.8-ce.0`, SearXNG and
Excalidraw ship dates, DokuWiki ships `version-2026-07-14c`. Each names a build
exactly; insisting on semver threw all of them away. The fallback only applies
where the listing is newest-first, which the Hub API promises and a v2
`tags/list` does not, and it refuses an architecture (`linux-arm-v7`), a runtime
(`php8.3-apache`), a branch build (`…-chore-dependabot-security-36cd703`), a
toolchain variant (`3.8-python3.14-conda`) and anything longer than four parts.
Cockpit publishes `core-` and `pro-` from one repository, so the original tag's
prefix is kept as well.

And the converter stopped needing a port to be in `ports:` to count as written
down:

* **A `SERVICE_FQDN` marker does not have to match the service name.** Coolify
  writes `SERVICE_FQDN_CWA_8083` on a service called `calibre-web-automated`.
  Requiring the names to match was really defending against a shared environment
  block — a web app and its Sidekiq declared with one YAML anchor carry the same
  marker — and that case is visible in the data, so it is counted instead.
* **A healthcheck against localhost states a port.** `curl -fs
  http://localhost:8083` only makes sense if 8083 is open.
* **A peer states a port.** `PLAYWRIGHT_DRIVER_URL=ws://browser-sockpuppet-chrome:3000`
  and `ELASTICSEARCH_HOSTS=http://elasticsearch:9200` each name a service and the
  port it answers on. Matching the host against the service's own name is what
  keeps this from being the environment-scanning that once gave n8n Postgres's
  port: a bare `DB_PORT=5432` names nothing and is ignored.

A peer beats a healthcheck, because the peer's port is the one other apps have to
reach: ZooKeeper's healthcheck talks to its admin server on 8080, which answers
`ruok` and nothing ClickHouse wants.

**282 templates**, up from 230 — 38 of them multi-service, up from seven. Three
more fixes to the output rather than the converter:

* **A worker keeps no port it did not declare.** A healthcheck that shells out,
  or a peer reference naming the web app, is not a worker declaring a port, and
  `openpanel-worker` was public on 3000 before this. A test now refuses any
  service named for a worker that is public.
* **A datastore is not linked to the database.** ClickHouse, MinIO, Meilisearch
  and a headless Chrome do not read a `DATABASE_URL`, and handing one to
  ClickHouse says the analytics store depends on the Postgres, which is not true.
* **A UDP port is not an ingress, and 80000 is not a port.** Palworld publishes
  `8211/udp` and Coolify's metadata says healthchecks listens on 80000. Both
  would have produced a domain that never answers.

## Phase 26 — the unverified images, and what the catalogue says about itself

Forty-three images could not be verified, which sounded like forty-three
upstreams being careless. Four more bugs, one of them shipping:

* **A reference with no tag has no tag.** `busybox` rpartitions to `("", "",
  "busybox")`, and taking that as the tag made the resolver answer "already
  pinned" for the most floating reference there is. Nothing reached the
  catalogue — the writer's own gate refuses an image with no tag — but the
  resolver was telling itself the opposite of the truth.
* **A pinned tag still has to exist.** Coolify's Mealie template names
  `3.17.0`; Mealie publishes `v3.17.0`. Trusting "already pinned" dropped a
  template whose current release was one lookup away.
* **The registry and the Hub API have different limits.** A manifest fetch
  counts against Docker Hub's anonymous pull limit and an API call does not, so
  a vanity host in front of Hub — docker.flipt.io, registry.rocket.chat,
  cr.weaviate.io — could answer 429 forever while the tag was plainly there.
  Asking the Hub API instead recovered Rocket.Chat, Weaviate and Flipt without
  pulling anything.
* **Nothing ever asked whether the catalogue was still true.** A tag that
  existed at import can be deleted afterwards, and MinIO did exactly that to its
  old RELEASE tags on Docker Hub. `hack/verify_catalogue.py` now asks every
  registry about every image in the directory: 290 of 291 present, 0 gone, 1
  rate-limited. It is not in `make check` — CI has no business depending on
  Docker Hub being up, and the anonymous limit would make it flaky — so it is a
  thing somebody runs before a release.

**282 templates**, 38 of them multi-service.

What stays dropped: 8 stacks need the Docker socket, which cannot run under a
restricted pod security policy; 4 are five to twenty-three services; 36 images
have no tag this could verify — `tiredofit/freescout` and `ghcr.io/ente-io/web`
are gone or private, Prefect publishes only toolchain variants, and Excalidraw,
Fizzy and label-studio publish nothing but branch builds. Shipping any of them
means guessing, and the whole point of the previous phase was that guessing is
worse than dropping.

### The number in the prose was wrong, and nothing failed

The README said 219 while the directory held 279. It had been wrong for two
phases. Every count this repository states about the catalogue — in the README
and in `docs/templates.md` — is now read back from the catalogue by a test, and
it caught the next drift on the same afternoon.

### Three things the panel did not say

Thirty-eight multi-service templates made three gaps visible that seven had hidden:

* **A card said `3210 · 6791 · 26.2.4.23 · postgres`.** Joining every service's
  tag with a dot says nothing and looks like a fault. One app still shows its
  version; a stack shows how many apps it is.
* **Installing four apps ended on `/projects`**, with no sign of where they
  went. It now lands on the app when there is one and on its project when there
  are several, and says how many were made.
* **A template's notes were shown before installing and never again.** They are
  the steps Skifity cannot do for you — a bucket to create, a migration to run,
  a shared directory that is two directories here — and the moment they matter
  is after the install, not before it. The comment on the field said they
  "appear after installation"; they did not. They do now, on the page the
  install lands on, until dismissed.

## Phase 27 — reliability and security, looked at properly

Three real defects, one of them a panic with a trigger anybody could hit by
accident, and two spot checks turned into complete ones.

### A closed browser tab could end a deployment

`Hub.Publish` collected its subscribers under the lock, released it, and then
sent. Between those two steps a client whose request context had just ended had
its channel closed by the goroutine watching that context — and a send on a
closed channel is a panic, in whatever happened to be publishing. A build emits
thousands of log events; closing the tab during one is all it takes. The panic
is contained by `internal/runsafe`, so the panel survives, but the deployment
that was publishing does not.

Reproduced in about three hundred iterations of subscribe-and-cancel, and it is
now a test that does exactly that. The sends happen under the lock, which is
safe because every one of them is non-blocking: the default arm drops the event
rather than waiting, so the section never sleeps.

### The third setting that becomes a request, and did not go through netguard

`internal/netguard`'s own comment said "two settings hold an address the panel
then makes a request to". There are three. The URL a cluster component's
manifest is downloaded from is a setting, `ApplyManifestURL` fetched it with
`http.DefaultClient`, and **what comes back is applied to the cluster as
Kubernetes objects** — which makes it the worst of the three to have been able
to point at `169.254.169.254`. It goes through the guarded client now, a scheme
that is not http or https is refused before anything is dialled, and a test asks
for the metadata service and for loopback and requires a `netguard.Blocked`.

### Two things a restart left in progress forever

A deployment interrupted by a panel restart is marked failed at startup, and so
is a provisioning operation. A **backup** written as `running` and a **database**
written as `creating` were not, and they are the same shape: a row only the
goroutine holding it ever finishes. The panel is a Deployment with a self-upgrade
endpoint, so a restart is routine rather than exotic.

The database is the worse of the two. One stuck at `creating` cannot be backed
up either — the backup manager refuses a target that is not running — so it is
not merely wrong on screen, it is unusable, and the only way out was to delete it
and start again.

### A key id longer than the header could hold

The envelope header carries the key id's length in one byte, and the id comes out
of the master key file, which an operator edits by hand. Nothing checked it: a
longer id would have been written truncated, every secret sealed afterwards would
have been unopenable, and the first sign of it would have been a decryption
failure on rows that were written correctly weeks earlier. The keyring refuses
one now, at both boundaries, and `encode` refuses to truncate rather than doing
it quietly.

### Two spot checks became complete ones

`TestNothingIsReachableWithoutCredentials` named seven paths out of a hundred and
twenty-three. `TestOneTeamCannotReachAnother` named thirty-three. The route that
matters is always the one nobody thought to add, so both now walk the router:

* **112 routes** refuse an anonymous request; 8 are open on purpose, each with a
  reason written next to it, and a second test fails if one of those 8 stops
  being a route this panel serves.
* **84 team-scoped routes** answer 404 for another team's team, project,
  environment, app, database or server — not 403, which would confirm the thing
  exists.

Neither found a hole. That is the point of running them: "authorization lives in
one place" was a claim about a hundred and twenty-three routes checked at seven.

### What was looked at and was already right

Argon2id at the OWASP parameters, sign-in lockout per account *and* per address
with the correct password refused while locked, constant-time comparisons,
unknown accounts indistinguishable from wrong passwords, CSRF double-submit with
bearer tokens exempt, `HttpOnly`/`Secure`/`SameSite` on the session cookie, a
strict CSP, request body limits, HMAC-verified webhooks, SQLite on WAL with a
busy timeout and every write serialised through one mutex, and `runsafe` on every
background goroutine including the four launched as method calls that a grep for
`go func` misses.

Three things gosec reports here are not findings: SHA-1 in `internal/auth/totp.go`
is what RFC 6238 specifies, the CSRF cookie is readable by the frontend because
that is how double-submit works, and `skifity.toml` is 0644 because it is meant
to be committed.

## Phase 28 — the one thing autoscaling needed and did not check

Scaling on a CPU or memory target reads the metrics API. Without it the
HorizontalPodAutoscaler sits at `<unknown>/70%`: the app never scales up under
load, never scales back down, and neither Kubernetes nor the panel says why.
k3s ships metrics-server by default and this install does not disable it, so
the common case is fine — but an operator who brought their own cluster,
disabled it, or is watching it crash-loop on a small node would have had
autoscaling that looked configured and did nothing.

That is the exact failure the scaling readiness checker exists for, and it was
the one thing the checker did not look at. It checks the eight ways an app
breaks when it is scaled — a read-write-once volume, SQLite, in-memory
sessions, a local-disk cache, local uploads, in-app cron, no health path, a
single instance — and not whether the cluster can supply the number it is meant
to scale on. It does now, as an error rather than a warning, with what to do
about it. Scale-to-zero is exempt: KEDA counts requests, not CPU.

## Phase 29 — single sign-on, and what building it found

**Dokploy has SSO and this did not.** It was the only feature on the competitor
list that was a straight absence rather than a trade-off, so it is built:
OpenID Connect, authorization code with PKCE, against whatever an operator
points it at — Okta, Entra, Authentik, Keycloak, Zitadel, Google.

SAML is deliberately not here. It is a second protocol, a second XML parser and
a second class of signature bug, and every provider a self-hosted panel is
likely to meet speaks OIDC.

Three things about it are load-bearing, because each is an auth bypass when it
is wrong, and none of them is code written here:

* The ID token's signature, issuer, audience and expiry are verified by
  `oidc.IDTokenVerifier`. A hand-rolled JWT check is the usual way to end up
  accepting `alg: none` or a token minted for somebody else's client.
* The nonce is generated per sign-in and has to come back inside the ID token,
  which is what makes a replay of an old one fail.
* The state is generated per sign-in, kept in a short-lived HttpOnly cookie, and
  **spent before the code is exchanged** — so resending the same callback cannot
  replay it.

The issuer is a setting, which makes it the **fourth** address an administrator
types that the panel's own process then connects to. It dials through
`internal/netguard` like the other three. The redirect URI comes from the Panel
URL setting rather than from the request's Host, which a caller controls: that
is the difference between a fixed redirect target and one somebody can register
under their own hostname.

### What building it found in the code that was already there

An account created by single sign-on has no password hash. Signing in with a
password was correctly refused — and refused with *"stored password hash is not
in the expected argon2id format"*, which tells an anonymous caller which
addresses are provider-only accounts. That is the exact enumeration
`TestUnknownAccountLooksLikeAWrongPassword` exists to prevent, arriving through
a new door. An empty hash now costs the same Argon2 hash, the same lockout
entry and the same `ErrInvalidCredentials` as any other wrong password.

`TestEveryRouteRefusesAnAnonymousRequest` caught the two new routes on the first
run, which is what a complete check is for: they are open on purpose, and now
say so with a reason next to each.

### And the sandbox claim that was never checked

ADR-0010 has said for months that this environment "refuses privileged
containers", so no cluster could ever run here. Checked today for the first
time: the container is root with nearly every capability, `docker` and `k3d` are
both installed, `/dev/kmsg` and `/dev/net/tun` are there. What actually blocks
it is the session's permission layer refusing to start a Docker daemon — a fact
with a remedy, rather than a wall. The ADR now says so. No cluster has been run
either way, so nothing else changes; what changes is that the reason written
down was not the real one.

## Phase 30 — the release pointed somewhere nobody owns

"Tag a release" was on the roadmap as *the image does not exist until the first
tag*. It was worse than that.

The release published to **`ghcr.io/skifity/skifity`**, and the README two pages
away says plainly that there is no such repository — this one lives at
`TegarTheGreat/Skifity`. So a tag would either fail, or succeed into a namespace
nobody here owns and tell every installer to pull from a name **somebody else
could register**. That is not a missing artefact; it is a supply-chain hazard
written into the release configuration, sitting next to a document that already
knew.

The release now publishes to the repository it is cut from: the workflow derives
`IMAGE_REPO` from `GITHUB_REPOSITORY`, lowercased for ghcr.io, and GoReleaser
uses that for the image, the upgrade command in the release notes, and the
`image.source` label. Right wherever it is released from, and no decision taken
here.

One thing is still a decision rather than a fix, and it is flagged rather than
guessed: `installer/install.sh` carries a literal default, and a shell script a
stranger downloads cannot derive one. That line has to name wherever the project
actually publishes, which is choosing the project's public home.

### And the half of "a second pair of eyes" that can be arranged

The roadmap has asked since Phase 17 for somebody who did not write any of this
to look at the security model. **CodeQL** now runs on every push and weekly with
`security-extended` — a different analyser with a different model of the code,
reading taint from source to sink across packages, reporting into the Security
tab where a finding cannot be quietly forgotten. **Dependabot** opens the update
before `make audit` has to report it, grouped so a Kubernetes bump is one pull
request rather than twelve.

Neither is a person. A tool that agrees with the author is not evidence that the
author was right, and the roadmap still says so.

**And it was switched off.** The workflow said `branches: ["main"]`, and this
repository has no `main` — the work is on a branch, and Dependabot opens its
pull requests against that branch. So the scan never ran once. The first green
pull request is what made it visible: eight checks passed and CodeQL was not one
of them. It runs on every branch now, like CI does.

That is the same shape as `make check` not running `audit`, as the four cluster
smoke tests that were named and never written, as `make image` never having
worked: **a gate with a hole in it looks exactly like a gate**, and this one was
added in the same session that wrote that sentence down again.

## Phase 31 — the check that settles it, written

The largest open item in this repository is that nothing has ever run against a
real cluster. It cannot be closed from here — the environment refuses to start a
Docker daemon, and the server offered for it is not reachable from this
container, which serves HTTPS through a proxy and has no SSH client at all.

What can be written is the check itself, so that closing it is one command
somebody runs rather than an afternoon of improvisation. `test/cluster/verify.sh`
installs Skifity on a real server, deploys a real application, and then settles
the three things only a cluster can:

1. **An app comes up and answers.** Not that the manifest renders — that the pod
   starts, the Service routes, and an HTTP request gets a reply.
2. **Autoscaling has numbers to scale on.** It waits a minute for the HPA's
   first sample and fails if it is still `<unknown>`. That is the silent failure
   Phase 28 added a warning for; this is what proves the warning is right.
3. **Scale to zero is wired the way it is drawn.** KEDA running, an
   HTTPScaledObject for the app, and the app's own HPA *gone* — two autoscalers
   on one Deployment fight, and this is where that stops being a claim.

It also measures what the thing costs. `docs/performance.md` says 35 MiB idle,
measured on a laptop; this prints the first number measured on a cluster, and if
they disagree the document is what changes.

It is not in `make check` and never will be: it needs a machine to destroy,
several minutes and the internet, and it asks for a typed `yes` before touching
anything. `make verify` runs it. The release is not tagged until it passes.

## Phase 32 — the screenshots, and the page that had never been opened

A request for pictures of every screen. Taking them found that one of the
screens did not work.

### The Templates page threw on every install

`"databases": null`. A nil slice in Go marshals as `null`, and **157 of the 282
templates have no database**. The panel iterated it, threw
`TypeError: t.databases is not iterable`, and the error boundary rendered "This
page stopped working" where the catalogue should be.

It had been that way since the catalogue grew past the eight hand-written
entries — all eight of which happened to have a database. So the single
most-cited reason people choose a panel in this category, the thing three phases
of work went into, **has been a crash the whole time.**

Nothing caught it, and the reasons are worth writing down because they are all
the same reason:

* The structural tests in `internal/templates` read the Go value. The difference
  was in the JSON.
* The interface test checked the shell and the empty states. It had never opened
  the page.
* The screenshot capture is skipped by default, so the only thing that would
  have looked was switched off.

Fixed at both ends: the loader normalises nil slices to empty ones, because the
API's own type says these are arrays, and the page defaults them anyway — a
client that falls over on a shape it did not expect is the other half of the
same bug. A test now marshals every template and fails on a `null` list.

**And the guard that should have existed from the start:** the interface test
opens every page in the navigation and fails on an uncaught error or on the
error boundary being on screen. It deliberately asserts nothing about what each
page contains — that would be a second copy of the panel, out of date within a
week. It asserts only that the page renders, which is the thing that was not
true.

### The theme flash, in production only

The same run showed `Refused to execute inline script` in the console. `index.html`
carries one inline script: it reads the stored theme and adds the dark class
before the first paint, so a dark-mode user never sees a white flash. The policy
is `script-src 'self'` with no `'unsafe-inline'`, so **it never ran** — and the
white flash it exists to prevent happened on every load.

In production only. The policy is not set in dev mode, which is why it was
invisible to everybody who was looking.

The hash is now computed from the embedded `index.html` at startup and put in
the policy, so the two cannot drift: a hash written down beside a script goes
stale the first time somebody edits the script and does not think about the
policy. A test fetches the page and checks that the policy names every inline
script that ships, and that `script-src` still has no `'unsafe-inline'`.

### Two smaller things the pictures made obvious

* The template categories were shown as their slugs — `ai`, `cms`, `other`.
  Translated now, in all five languages, and sorted by the name somebody reads
  rather than by the slug underneath it.
* The autoscaling switch's description repeated its own label: "Scale
  automatically / Scale automatically".

**32 screenshots**, in `docs/tour.md`, captured by a test against the real
binary — so a picture can never show a screen that no longer exists.

## Phase 33 — the panel at 375px, and a team that could not be joined

A layout test at 375, 768 and 1440 (`web/tests/responsive.spec.ts`), and four
faults, none of them in hard code: the inset had no `min-w-0`, so the command
palette button's own width made every page on a tablet scroll 7px sideways; a
`Card` had none either, so a repository URL made the app page scroll 372px on a
phone with a `truncate` that could never apply; shadcn fixes a tab strip at one
row, so Settings' seven tabs wrapped out of the pill and onto the panel below;
and the switch, the checkbox and the breadcrumb links were all under the 24px
WCAG 2.2 (AA, 2.5.8) minimum. The test seeds a project, an app and a long secret
variable first, because an empty install has none of the shapes that break.

Separately, and larger: **there was no way to add anybody to a team.** Members
could be listed and their role changed; an account could only be made by
first-run setup, which happens once. An invitation is now a one-time link —
stored as a SHA-256, spent on use, expiring in seven days, carrying the address
it was issued for so an accept cannot be pointed at somebody else's account.

Also: a missing file was served `index.html` with a 200, which is how a browser
holding a cached page ends up parsing `<!doctype html>` as JavaScript; and
`Cross-Origin-Resource-Policy` was missing from an otherwise complete set of
headers.

## Phase 34 — what autoscaling did after it worked

Three faults on the scaling path, found by reading it against KEDA's own
manifests rather than against itself. All three are the same shape: an object
that renders correctly and a system that then behaves differently.

### Every apply undid the autoscaler

Objects are applied with server-side apply and `Force`, and the Deployment
carried `spec.replicas`. An apply happens on a deploy, a rollback, a variable
change, a domain change and a scaling change — so each of those reasserted the
panel's number over the autoscaler's. An app the HPA had taken to six under load
dropped to its minimum because somebody edited a variable, then climbed back
over the next few minutes. With scale to zero it was the mirror image: a
sleeping app forced awake and billed for it.

The field is now omitted whenever an autoscaler owns it, which is what
Kubernetes documents for this case. The test that existed asserted the old
behaviour — "the Deployment must start at the minimum" — which is why nothing
ever failed.

### A sleeping app could not be woken

An app that scales to zero is reached through an ExternalName Service aliasing
KEDA's interceptor. That alias said `port: 80` with `targetPort: 8080`, and the
Ingress asked for port 80.

`targetPort` is not applied to an ExternalName Service: no kube-proxy rule is
made for one, so the ingress controller dials whatever number it settles on —
Traefik the Service's `port`, nginx the number in the Ingress backend. Both
would have dialled port 80 of a proxy that listens on 8080 and nothing else.
Every request to an app that could sleep would have been a 502, and the app
would never have started. KEDA's own example points an Ingress at 8080 and gives
the alias no ports at all; this now says 8080 in all three places, which is the
only value that is right under every reading.

### A percentage target of a number nobody chose

A CPU or memory target is a percentage of what the app *reserves*. A new app
reserves 50m and 128Mi, so a 70% target fires at 35m and 90Mi — under what most
frameworks use while idle. Autoscaling would therefore "work" by going straight
to the ceiling and staying there. The readiness checker now does that arithmetic
and says the numbers out loud, and `docs/concepts.md` explains it.

`test/cluster/verify.sh` gained the two checks that would have caught the first
two: it scales an app to three, changes a variable and fails if the count moves;
and it puts an app to sleep, sends one request through the real ingress and
fails unless the app answers and comes back. The second used to be a sentence
telling the operator to try it by hand.

### The other half of a zero-downtime deploy

`maxUnavailable: 0` keeps the capacity, and the test that checks it said that is
"what makes a deploy zero-downtime". It is half of it. A pod is removed from its
Service and told to stop at the same moment, and the ingress controller learns
about the removal through a watch — so for a fraction of a second it is still
sending requests to a process that has begun shutting down. Every rolling update
therefore dropped a handful of requests, which is the kind of thing nobody can
reproduce afterwards.

There is now a five-second `preStop` pause before SIGTERM, out of the same
thirty-second grace period. A sleep action rather than a shell command, because
a distroless image has no shell; and only for an app that serves HTTP, because a
worker has no endpoint for anybody to notice disappearing.

### The front door, said out loud

Every server runs the ingress, so an app answers on every server's address. DNS
names one. If an app has three instances across three servers and the server the
domain points at goes down, the app is running and the name is dead — Kubernetes
moved the work, it cannot move a DNS record.

Nothing in the documentation said this, and `docs/research/competitors.md`
criticised Coolify for the same thing without admitting it. Both now say it, and
`docs/adding-servers.md` gives the three ways out: round-robin DNS, a floating
IP, or a provider's load balancer, with the address going in
**Settings → Domains → Cluster public IP**.

## Phase 35 — the checklist, crosschecked

Eighteen things a self-hosted platform is judged on, put against the code one at
a time. `docs/checklist.md` is the result and the record: three statuses, and
only three — **Works** means a test that runs on every push, **Written** means
the code and its unit tests exist and it has never touched a cluster, **Missing**
means missing. Nothing is Works because it looks right.

Two of the eighteen are Works end to end. Most are Written. That ratio is the
honest state of this product and it does not change until `verify.sh` has run.

### The one that was Missing

Taking the data out. "Can I take my data with me" was answered by "copy
`panel.db` and `master.key`", which is a Skifity-shaped blob and a promise
rather than an export. `skifity export` now writes a directory that needs none
of this product to read: the whole team as JSON, and each app as the Kubernetes
objects it would be applied as. Secret values are deliberately not in it — the
promise that a stored secret is never shown again is worth more than the
convenience, and it costs nothing, because those values are already in the
reader's own cluster as ordinary Kubernetes Secrets.

### And the crosscheck itself, made runnable

`test/cluster/verify.sh` was a script that had never been run, and reading it
against the code showed why that matters: it asked for an API token with the
wrong CSRF header name, without the team id the endpoint requires, and then read
the secret out of a field that does not exist. It would have died four checks
in, and everything after it would never have run.

It is now organised as the five phases a person can actually work through, and
covers what it claimed to and more: a build from Git rather than a prebuilt
image, a build log read while it is still building, an address that answers, a
variable change that restarts without rebuilding, a rollback, two hundred
requests held across a rolling restart, a volume that survives one, a database,
a backup restored, the export, a token refused on another team, a member refused
the settings, one namespace refused another's app, sixty-four cores refused by
the quota, the panel scaled to zero while the app keeps serving, a node drained,
the panel replaced under a running app, and a deployment that cannot pull its
image sending a real notification to a real listener.

`test/cluster/sample-app` is what phase 1 builds: one Go file and a two-stage
Dockerfile with nothing to download, so a build failure is the builder's and not
the network's.

The parts of that script that only talk to the panel are now also covered by
`make smoke` against the real binary — the invitation flow end to end over HTTP,
what a member is refused, and the export. A field renamed in the API would
otherwise leave the crosscheck quietly checking nothing, on the one machine
nobody can run from CI.

## Phase 36 — the first five minutes

Three things a person who has never seen this would hit, found by walking the
path rather than by reading the code that implements it.

### The first screen told you to do what you had just done

`CreateServer` was called from exactly one place — the SSH provisioner — so the
machine Skifity installs itself onto was never recorded anywhere. A brand new
install opened on

> No servers yet — Add your first server

while looking at a panel served by a cluster that was already running on that
very machine. The only sensible thing to do next was to type its own address
into the form, which the preflight refused with a message about something
listening on port 6443. Correct, and no use at all as an answer.

The cluster's nodes are now adopted into the team when it is created: listed
like any other server and not managed like one. The row carries `adopted`,
because the panel has no key to that machine and put nothing on it — so Remove,
Retry and Promote refuse with a reason (`server.not_ours`) rather than failing
at the SSH connection with what reads like a network problem, and the panel does
not offer the buttons at all.

### "New app" was a button on a page nobody was standing on

Creating an app only existed inside a project's page, two lists deep. The quick
start said *"Press New app"* in step 3, describing a button that is not on the
screen it had just walked the reader to. It is now on the overview and in the
command palette, pointing at the first project's first environment — the one
setup creates.

### Five settings that nothing read

Settings → Git offered a GitHub App ID, a slug, a client id, a client secret and
a private key. Nothing in the repository read any of them: there is no JWT
signed with that key and no installation token exchanged for it. An operator
could paste a private key and have nothing happen, and the client id's help text
promised signing in with GitHub and a list of repositories to choose from,
neither of which exists.

They are gone, along with `github_app` as a connection kind and the webhook
branch that verified pushes for a kind of connection the panel could not create.
A personal access token is the path that works, for GitHub, GitLab and Gitea,
and it is the one the panel offers. Same shape as Compose in phase 19 and the
WireGuard fallback before it: the feature was the settings page.

`verify.sh` now checks the first of these where it means something — a fresh
install must list the machine it is running on, and must mark it adopted.

## Phase 37 — the first command, and the first click

### The link the installer prints now does the copying

Every self-hosted install ends the same way: a URL, a forty-character token, and
a minute of moving one into the other. The installer prints a link with the
token in it instead. In the `#fragment`, deliberately — a fragment is never sent
to a server, so opening it cannot put the token into an access log, a proxy or a
`Referer` header on the way somewhere else. The page reads it during render and
clears the address bar, so a bookmark or a screen share does not keep it. The
plain URL and the token are still printed underneath.

**Writing the test for that found an older bug.** The installer has always
printed `<url>/setup`, and `/setup` is not a route: the setup screen is a gate
in front of the router, so once an account exists the gate is gone and the path
has nothing behind it. Everybody who followed the printed link finished first-run
setup and landed on "Not found". `/setup` and `/login` now redirect to the
overview.

### The panel hands out its own binary

One file is the panel, the CLI and the MCP server, so the file answering a
request is the file somebody wants on their PATH. `GET /api/cli/download`
streams it.

The installer used to fetch the CLI from `github.com/skifity/skifity/releases`,
which does not exist, so every install ended with "could not download the
command line tool" and a link to nothing. It now asks the panel it has just
started: always present, always the matching version — a CLI one release behind
its panel is a confusing afternoon — and no internet needed at all.
`SKIFITY_CLI_URL` remains for an air-gapped mirror. `make smoke` downloads it,
runs it, and fails if the version differs from the panel's.

### The domain nobody pointed yet

`SKIFITY_DOMAIN` used to be taken at its word. A record that does not point at
this server means Let's Encrypt cannot answer the challenge, and the operator
learns that ten minutes later from a cert-manager log, as a browser warning on
a page they cannot open. One `getent` lookup at install time says it now, with
the address the record should have. A warning rather than a stop: installing
first and pointing DNS afterwards is entirely reasonable.

### One click meant one click and a decision

The template install dialog opened with the environment picker empty and the
button greyed out. Almost every panel has exactly one environment — setup makes
it — so the one-click catalogue began with a choice that had a single possible
answer. It is preselected when there is exactly one, derived during render
rather than copied into state, and left empty when there are several, because
installing into the wrong environment is not a mistake anybody notices
straight away. The label said "Environments"; it now says where it is going.

## Phase 38 — which systems, asked properly

"Does it work on every operating system" turns out to be four questions, and
three of them had an honest answer already. The fourth did not.

### Alpine was on the list of distributions that work, and could never work

`knownWorkingDistros` said AlmaLinux, Rocky, RHEL, CentOS, Fedora, openSUSE,
SLES, Alpine, Arch. Four lines below it, a missing systemd is a **fatal**
problem — and Alpine runs OpenRC. So the file promised a distribution and
refused it in the same breath. k3s itself supports OpenRC; Skifity does not,
because it manages the unit with `systemctl`.

Alpine is off the list, the refusal now names it and OpenRC by name, and a test
walks every remaining entry and fails if one of them is refused on an otherwise
healthy server. The same shape as the Compose claim and the WireGuard fallback:
a list is a promise.

### There was no CLI for Windows, for no reason

`goos: [linux, darwin]`. The binary is pure Go with cgo off, `os.UserConfigDir`
finds `%AppData%` by itself, and `GOOS=windows go build ./...` compiles clean on
the first try — it had simply never been asked for. Somebody deploying from a
Windows laptop needs the CLI as much as anybody, and the CLI, the MCP server and
the panel are one file. Six binaries now: linux, darwin and windows, amd64 and
arm64, with `.exe` spelled out in the release's name template rather than left
to a default.

The panel half is still Linux only, which is not a gap: what it installs is
Linux.

### `make image` built for whatever machine ran it

The release builds `linux/amd64` and `linux/arm64` through buildx. `make image`
— the path everybody uses, since no release exists — passed no platform at all,
so an image built on an amd64 laptop for an arm64 VPS starts with "exec format
error". `PLATFORM=linux/arm64 make image` now does the obvious thing.

### And the answer, written down

`docs/quick-start.md` has the table: tested, expected-to-work, will-not-work,
and which architectures, for servers and for the CLI separately. `docs/faq.md`
has the short version. It was knowledge somebody had to read three Go files to
assemble.

## Phase 39 — what the research said, against what the code did

"Is every Linux distribution covered" was answered from memory in phase 38 and
checked against k3s's own documentation afterwards. Two of the answers were
wrong, and one of them was wrong in a way that would have taken a whole tier of
supported distributions down.

### firewalld did not exist anywhere in this repository

The firewall step knew two firewalls: ufw, and iptables as a fallback. firewalld
is the default on **AlmaLinux, Rocky, RHEL, CentOS and Fedora** — every one of
which this product lists as expected-to-work — and it is active out of the box
on their cloud images.

What happened on such a server: no ufw, so the fallback put rules in with
`iptables -I INPUT`. firewalld discards those on its next reload, and there is no
`netfilter-persistent` on those systems to survive a reboot either. The cluster's
ports were open until something touched the firewall, and then were not — which
is the worst shape a bug can have, because it works when you test it.

There are now three branches, each using its own tool the way its own users
would, decided once: ufw, firewalld with `--permanent` rich rules and a
`--reload`, iptables otherwise. And all three trust the pod and service networks
by CIDR, which is what k3s's documentation asks for and what the ufw branch only
half did with interface rules.

### The memory cgroup, which the kubelet cannot start without

k3s's requirements name it for Raspberry Pi OS, which ships with it off. Skifity
builds for arm64, so that is a path people take — and what k3s says on the way
out is about cgroups rather than about the one line in `cmdline.txt` to change.
Both preflights check it now, on cgroup v1 and v2, and the refusal carries the
line to add. Unknown is not treated as no: a server whose cgroups cannot be read
is not refused on a guess.

### And what the research confirmed rather than changed

* **k3s's install script does support OpenRC**, so k3s on Alpine works. Skifity
  does not, because it manages the service with `systemctl` — phase 38's
  reasoning was right and the wording now says whose limitation it is.
* The ports Skifity opens are exactly k3s's documented inbound list: 6443,
  10250, 8472, 51820, 51821, and 2379–2380 on control plane servers.
* `nm-cloud-setup` on RHEL was a real problem and its last affected release
  reached end of life in May 2023; not worth a check.
* armhf is supported by k3s and not by Skifity, which builds only 64-bit — and
  the preflight already refuses it.

## Phase 40 — the catalogue had no pictures

Three hundred cards, each with a grey square and one letter in it. Every product
in this category shows logos, and the catalogue is the single most-cited reason
people choose one.

**211 of the 282 templates now have one**, from
[homarr-labs/dashboard-icons](https://github.com/homarr-labs/dashboard-icons) —
the collection Homarr, Homepage and Dashy all draw on, and CC0-1.0, which is
what makes vendoring it possible at all. The remaining 71 show the letter, which
is what the fallback was always for.

**They are committed, not fetched.** The panel's own policy says
`img-src 'self'`, and pointing at a CDN would mean widening it, telling that CDN
which self-hosted apps each user is browsing, leaving an offline install without
pictures, and making somebody else's uptime a thing that makes this product look
broken. 2.5 MB inside a 40 MB binary buys all four back. SVG where the
collection has one and WebP where it does not: a 260 KB PNG drawn at 40 pixels
is a waste nobody sees and everybody carries.

Nothing in the YAML changed. An icon is a file named after the template it
belongs to, so adding one is adding a file; the loader looks and fills in
`Icon`, and `hack/fetch_icons.py --report` names the ones still missing.

### And a measurement that lied, again

The first check counted images that were `complete && naturalWidth > 0` and
reported **200 of 211 broken**. They were not: `loading="lazy"` means an image
below the fold has not been fetched, and counting that as failure is the same
mistake as guessing at a layout instead of measuring it. Asking the endpoint for
all 211 directly gives 0 failures, and that is what the interface test does now —
it also checks that a template *without* a logo answers 404 rather than putting
a broken image on every card.

## Phase 41 — the front door, answered properly

`docs/adding-servers.md` listed three ways to keep one address alive when a
server goes down: round-robin DNS, a floating IP, a provider's load balancer.
Researched against how each actually behaves, that list was wrong in two ways.

**It was missing the best answer for this audience.** Cloudflare Tunnel:
`cloudflared` in the cluster with two or three replicas, each making outbound
connections to Cloudflare. Kubernetes already spreads those across servers, so
failover needs no configuration and no health check — and it needs **no public
IP and no inbound ports at all**, which makes a server behind NAT or on a home
connection work. Free to 25 replicas. The trades are written down too: traffic
goes through Cloudflare, replicas are steered by geography rather than round
robin, and the free plan caps an upload at 100 MB.

**And it did not warn about the thing people try first.** kube-vip and MetalLB
in layer-2 mode hold a virtual IP by answering ARP, and ARP does not cross a
router. Every node has to be on one segment *and* the provider has to route that
extra address to you — which on cloud VPS is exactly what a floating IP is, sold
as a product. Their BGP modes work, and need a provider that speaks BGP to you.
On bare metal in one rack they are the right answer; between providers they are
not, and somebody was going to spend an evening finding that out.

Round-robin DNS is also described for what it is now: a failover for a server
that is **off**, not one that is **sick**. A machine that accepts a connection
and then answers nothing is one DNS keeps handing out.

## Phase 42 — the front door, built

Phase 41 wrote down that Cloudflare Tunnel was the right answer for this
audience and left the reader to install it. That is the shape every dead
integration in this repository started as: a paragraph of documentation and five
settings nothing read.

So it is a component now. **Settings → Components → Cloudflare tunnel** renders
a Secret and a Deployment into the panel's own namespace: two replicas, spread
across hosts with `ScheduleAnyway` so a one-server cluster still gets both,
`maxUnavailable: 0` so a rollout never takes a connector away before its
replacement is connected, `/ready` as the readiness and liveness probe because
"connected to Cloudflare" is not the same as "the process is running", no
service account token, and the token itself only ever in a Secret.

Three things make it a component rather than a page:

* **It refuses without a token.** `cloudflared` with no token starts, fails to
  authenticate, and restarts for ever while the panel says installed. Installing
  with nothing in the box returns `tunnel.no_token` with the four clicks that
  produce one.
* **The setting is validated where it is typed.** The dashboard shows the token
  inside a `cloudflared service install <token>` command line, and the tunnel's
  UUID is in the address bar above it. Both get pasted. Both are refused in the
  text box, each with the sentence that says which one this is.
* **Saving a new token changes what is running.** The token reaches the
  container as an environment variable, and an environment variable is read once
  at startup — so applying a new Secret under a running pod changes nothing at
  all. A fingerprint of the token is an annotation on the pod template, which
  makes a new token a new template and rolls the connectors the ordinary way.
  Clearing the token stops them and puts the component back to not installed,
  which is how every other integration in Settings is disconnected.

Nothing is needed per app. `cloudflared` forwards the Host header untouched and
the ingress routes on exactly that, so one wildcard public hostname pointed at
`traefik.kube-system.svc.cluster.local:80` covers every app that exists and
every app that ever will. That address is a constant in `internal/settings`
rather than a string in two places, because the operator has to type it into
Cloudflare and the help text must not drift from the code.

What is still manual, and is written down where somebody looking for it will
find it: creating the tunnel and adding that hostname. Doing those from the
panel needs a Cloudflare API token with Zero Trust permissions, which is a
second credential and a second integration — and this one has never been pointed
at a real Cloudflare account, so it is not the moment to add a third thing that
cannot be run here either.

## Phase 70 — thirteen settings nobody read

A form field is a promise: somebody types an answer and the panel keeps it.
Thirteen of forty-five settings were answers nothing ever read.

### An external registry broke a deploy at both ends

The worst of them. `registry.url`, `registry.username` and `registry.password`
are on the settings page, sealed and stored. The build Job mounted a Secret
called `skifity-registry-auth` — and **nothing created it**, so the build pod
could not start at all. Even if it had, the app's own pod had no
`imagePullSecret`, so it could not pull what was pushed.

Configuring your own registry did not quietly do nothing. It broke every
deploy, twice, with a Kubernetes error about a missing Secret and no way to
connect that to the settings page.

Both ends now get the same `dockerconfigjson` Secret, applied on every deploy
rather than when the setting is saved — namespaces are created later, and
credentials change. Docker Hub's short name is rewritten to the URL a docker
config is actually filed under, because a credential filed under `docker.io` is
one the kubelet never finds.

### An Email page that configured no email

Six SMTP fields — server, port, user, password, from, TLS — under a heading
that says Email, read by nothing. Every email channel carried its own copy
instead, so three recipients meant typing the same SMTP password three times.

A channel falls back to the panel's settings for anything it does not say
itself, and still wins where it does: somebody who pointed one alert at a
different server meant it.

### Three fields for a feature that does not exist

`dns.provider`, `dns.zone` and `dns.api_token`: "Lets Skifity create DNS
records for you when you add a domain." Nothing anywhere creates a DNS record.
The third one asked for an API token — a real credential, pasted in, sealed,
stored, and used for nothing, which is worse than a field that does nothing.

Removed, along with the settings tab that held only them. The same answer this
repository gave to the GitHub App kind and to Compose support: a feature that
does not exist should not have a form.

### And a default nobody defaulted to

`general.default_builder` — "which builder to use when a repository has no
Dockerfile" — was ignored by `chooseBuilder`, which always answered Railpack.
An operator who chose Nixpacks had to set it on every app. It is read now, and
a Dockerfile in the repository still wins over it, because that is the
repository author's decision rather than the panel's.

### The gate

`TestEverySettingIsReadBySomething` walks every `Key` constant with go/ast and
fails when nothing outside the settings package names it. Forty-one settings,
one exemption: the telemetry switch, which exists so the absence of telemetry
is visible and says exactly that in its own help text.

Proven by deleting one mapping. The other two of this shape — the seven
notification events and the ten plugin events — were checked at the same time
and the notification ones were already complete.

`make check` exits 0, `make smoke` exits 0.

## Phase 69 — the webhook the form promised to register

Two questions: does a push to GitHub deploy on its own, and does anything go
down. The first was half true and the second was true with one exception
nobody was told about.

### Half automatic

Once a webhook exists, everything after it works: the delivery is verified
against the connection's secret, matched to apps by repository, checked
against the app's own team, skipped unless deploy on push is on, matched
against the app's branch, and a pull request gets a preview of its own. That
part is good.

Nothing created the webhook. The panel printed a URL and a secret and left
somebody to paste them into the Git host, once per repository. Miss it and
deploy on push silently never happens: nothing is broken, the panel is simply
never told.

The connect form even says so — "a token is needed so Skifity can read the
repository **and register a webhook**" — and only the first half of that
sentence was ever true.

`gitsrc.EnsureWebhook` registers it, on GitHub, GitLab and Gitea, at the moment
an app is created, because a hook belongs to a repository and a token covers
many. Idempotent by the address it delivers to, so a second app from the same
repository does not mean two hooks and two deploys per push. Never fatal: a
read-only token is the right token for somebody who deploys by hand, so a
refusal comes back as "here is the URL, add it yourself" in the panel and on
the CLI.

### And a response shape that would have broken the CLI quietly

Adding the webhook's status to the reply exposed something older. Creating an
app answered two different shapes — the app on its own, or an object holding
it when a deploy started — and three things decode that reply. The panel and
the MCP server handled both. The CLI decoded straight into an `App`, so it
would have written a project file naming no app at all, with no error, and
`skifity deploy` in that directory would have had nothing to deploy.

One shape now, always, with a test that says so. The interface test's own
fixture broke on the change, loudly, which is the difference between a
consumer that is tested and one that is not.

### The one thing that does go down

A deploy starts the new instance, waits for its readiness check, moves traffic,
and only then stops the old one — which also pauses five seconds first, so
every proxy has seen it leave.

**Unless the app has a disk.** One disk, one writer, so Kubernetes is told to
stop the old instance before starting the new one: `Recreate`. That is correct
and deliberate, and the first place it was written down was `docs/checklist.md`
— an internal document. Somebody who adds a disk for uploads gets a gap in
every deploy from then on and finds out in production.

The Storage tab says it now, as soon as there is a disk, and `docs/concepts.md`
has a section on deploys and downtime that names all three cases: a disk,
restoring a volume backup, and scale to zero.

`make check` exits 0, `make smoke` exits 0, 15 interface tests pass.

## Phase 68 — eight promises to somebody else's code

The plugin standard declares ten events. Two of them were ever sent.

A plugin subscribes in its manifest. The manifest is validated against the
list, so `backup.failed` is accepted. The install screen shows what the plugin
will be told about. The documentation has a table of them. And then
`app.created`, `app.deleted`, `deploy.failed`, `backup.completed`,
`backup.failed`, `server.added`, `server.removed` and `database.created` never
happened, because only the deployer held a dispatcher and only the deployer's
two calls existed.

This is worse than the same shape of bug found twice before — seven hub events
nobody listened for, a build setting dropped before the build — because it is a
promise to code somebody else wrote. Their plugin is not broken. It is waiting.
There is no error, no log line, nothing to debug: a backup fails, and the
plugin that exists to open a ticket about it sits there. The only way to find
out is to make a backup fail and watch nothing happen.

The documentation even said so, in a sentence that was half true: "Subscribing
to an event the panel does not send is refused at install time, not discovered
six months later when you notice your plugin has never run." The check was
against the list. The list was right. What the list described did not happen.

### What it took

One dispatcher, built once and handed to everything that has news, rather than
one the deployer happened to own. The API server, the backup manager and the
provisioner each hold one now; a zero value sends nothing, so nothing has to be
configured for a panel with no plugins.

Then the eight calls, at the four places a backup finishes — a database and a
volume, each succeeding and failing — the two places a server joins or leaves,
the two an app is created or deleted, the one a database is created, and the
deploy failure path that had a notification and a hub event and no plugin
event.

`tellPlugins` names its event at the call rather than picking it into a
variable, which reads better and is also what the gate below can see. A rule
that forces a call site to be readable is worth keeping.

### The gate

`TestEveryDeclaredEventIsActuallySent` reads the standard's `Events` and every
`Notify`/`Ask` call across eight packages with go/ast, and fails when the panel
declares an event it never sends. It named all eight on the first run, which is
how they were found. Proven again afterwards by deleting one call.

### Also

The documentation now says what each event carries — nine rows of payload
fields. An event standard with no payload documentation is one a plugin author
reverse-engineers from a log, if they are lucky enough to have made the event
fire.

And `docs/progress.md` claimed plugins had "no store and no interface". The
interface has been there for a while: installed plugins, a store catalogue,
inspect before install, settings, uninstall. It is the store that does not
exist, because nothing is published at `plugins.skifity.com`.

`make check` exits 0, `make smoke` exits 0.

## Phase 67 — a backup nobody could restore

Maturity rather than coverage: the parts that are built up to the last step and
then stop. Volumes had two.

### The restore that existed and was never called

`VolumeJobSpec.Restore` inverts the direction, downloads the archive and
unpacks it into the claim. It has been there since volume backups were added.
Nothing ever called it: no manager method, no route, no button. So a volume
backup could be taken, listed, shown with its size and its age, and never put
back. A backup you cannot restore is a file somebody is paying to store, and
the checklist row it belongs to is called "Backup and restore, **proven**".

`RestoreVolume` does it properly, which mostly means handling the part the job
cannot. The volume is ReadWriteOnce and the app is holding it: unpacking a tar
underneath a process with files open on the same disk turns one bad day into
two. So the app is scaled to zero, the pods are waited for — scaling returns as
soon as the API server has the number, not when the pod is gone — the archive
is unpacked, and the app goes back to exactly the size it was. That last part
is a `defer`, because the case that matters is the restore that fails: leaving
the app at zero would be an outage caused by the thing that was meant to end
one.

It refuses without an explicit confirmation, and says what it will do: replace
everything on the disk, and stop the app while it does.

### The schedule the scheduler could already run

`RunScheduledAt` reads every enabled policy and calls `Run(policy.TargetType,
…)`, and `Run` has handled `"volume"` since volume backups existed. There was
no route to create such a policy, so the answer to "back up my uploads every
night" was to press a button every night. Three routes, and the validation
those routes share with the database side rather than a second copy that can
drift.

### Two things the gates caught on the way

**A bad schedule answered 500.** Typing "every night please" into a database's
backup schedule produced a correct, well-written problem — cause, impact, fix —
with no status on it, and an `errdoc.Problem` with no status is a 500. The
interface renders that as "something went wrong" rather than as the thing the
person just typed, the access log files it at ERROR where it drowns the real
ones, and a client deciding whether to retry gets the wrong answer.
`TestEveryProblemThisPackageAnswersWithSaysItsStatus` walks every `errdoc.New`
in `internal/api` with go/ast and fails on one that does not say. 48 problems,
one exemption: the panic handler, which by definition is nobody's input.

**An event nobody was listening for.** The new restore published `"succeeded"`
on the operation topic; every other finished operation publishes `"operation"`
with the operation itself, which is what the interface hears. Phase 61's wiring
gate caught it before it shipped, which is the first time one of these gates
has caught a defect on the way in rather than on the way out.

`make check` exits 0, `make smoke` exits 0, 15 interface tests pass.

## Phase 66 — the builder, and the front end it never built

The question was whether the builder is mature and whether it handles every
stack. The answer to the second is yes in principle — Railpack does its own
detection inside the build and a Dockerfile takes anything it cannot — and the
answer to the first was no, because the most common front end in the world
deployed a blank page.

### A Vite project was detected, and then not built

`detectNode` maps a repository with Vite and a build script to the static
builder with `StaticDir: "dist"`. The static builder generates a Dockerfile
that copies a directory into Caddy. `dist` does not exist in a Vite
repository — producing it is the entire point of the build step — and nothing
ran `npm run build`. So the image held `index.html` and `src/main.tsx`, the
page was blank, and the deploy reported success.

It is worse than a missing directory, because of the second half: the detected
`StaticDir` **never reached the build at all**. `Detection` had the field,
`JobSpec` had the field, and `internal/deploy/build.go` never assigned it, so
the directory was always the default. Every static build was `COPY . /srv` —
the whole checkout, served.

A front end is now built before it is served: a Node stage installs with
whichever package manager the repository locks to, runs the build command, and
only what that produced is copied into Caddy. Create React App and Angular are
detected too, with the directories they actually write to. Both fields are on
the app, in the form and in Settings, so a framework the detection does not
know is two boxes rather than a Dockerfile.

### And `.git` was being published

`COPY . /srv` included the repository's own `.git`: every commit of a private
repository, served at a path anybody can guess.

With, underneath it, the reason that is worse than a source leak. The clone
step built the URL as `https://x-access-token:${TOKEN}@host/...` and ran `git
remote add origin` with it — and git writes the remote to `.git/config`. So a
private repository deployed as a static site published **its own access token**
on the internet. The token reaches git through a scoped `http.<origin>.extraHeader`
now, so it is never written to disk, and the remote is the address as given.
The header is scoped to the repository's own host on purpose: an unscoped one
is sent wherever a submodule points.

### The gate

The shape of this bug is the one this repository keeps finding: a value worked
out, stored, shown in the interface, and dropped one layer before it is used.
`TestEveryBuildSettingOnTheAppReachesTheBuild` reads `store.App` and
`builder.JobSpec` with go/ast and fails when a field on both is never assigned
where the build is put together. Three fields are exempt and each says why —
the app's CPU and memory are its runtime limits, not the build pod's, and a
128 MB app can need 3 GB to build.

Proven by removing each thing: the assignment, the build stage, the `.git`
removal.

### What is still true about all of this

None of it has been run. A generated Dockerfile is a string this repository
asserts on, and whether `pnpm install --frozen-lockfile` works in
`node:22-alpine` against a real repository is a question only a cluster
answers. That is ADR-0010 again and it is the same answer as everywhere else:
`make verify-remote` is one command, and nobody has run it.

`make check` exits 0, `make smoke` exits 0.

## Phase 65 — signing in, taken apart

The panel's own front door, read line by line against what it would take to get
through it. Most of it held: Argon2id at the OWASP parameters, a single error
for every kind of failure so an unknown address cannot be told from a wrong
password, an unknown account hashed anyway so the timing does not say either, a
TOTP code spent when it is used, two separate lockouts, and an OIDC flow whose
ID token is verified by the library with a per-sign-in nonce and a single-use
state. Five things did not.

### A cookie is not isolated by origin, and this panel hosts other people's apps

The largest one, and it is specific to what this product is. Any page on a
sibling name can write a cookie scoped to the parent domain, and the server
cannot tell it from its own. The common configuration here — a wildcard app
domain with the panel on the same domain — gives **every application deployed on
this panel** a way to write the panel's session cookie. That is session
fixation: the victim silently signed in as the attacker, typing their own
secrets into an account somebody else can read. The same trick rewrites the SSO
state cookie, which carries the state, the nonce and the PKCE verifier.

Every cookie the panel sets now carries the `__Host-` prefix, which a browser
refuses to store if the cookie names a Domain at all, and only that name is
read. Accepting the bare name as a fallback would hand it all back — the
attacker would write that one instead — so it is not accepted. ADR-0019.

### CSRF compared two things the caller supplied

Double-submit is a cookie checked against a header, and it rests entirely on the
assumption above. The session now carries the hash of its own CSRF token and the
header is checked against that; the cookie is how the token reaches the
frontend and nothing else. The test that proves it forges both halves the way
an app on a sibling subdomain would, and a smoke check does the same against the
real binary.

### A cookie that was Secure on a panel that is not

Found while writing those tests, and it predates them: `secureCookies` was
`!DevMode`, and the default install is **plain HTTP** on an sslip.io address
(ADR-0015). A Secure cookie is never stored over http, so the default install
would have set a session cookie no browser would keep, and nothing in the panel
could have said why. It follows `SKIFITY_PUBLIC_URL` now — the address people
actually open — and an unset one gets the safe answer plus a warning at startup.
Nobody had ever signed into this panel over http from a browser, which is how it
survived.

### A session that renewed forever

The sliding expiry answers "has this person been away", and never "how long has
this cookie been valid". A session used once a day renewed indefinitely, so a
token stolen in January was still good in December. Thirty days from when it was
created, whatever the activity.

### A borrowed session could keep itself

Turning two-factor off needed nothing but a session. Neither did reading the
recovery codes or minting an API token that outlives the session it came from.
Each turns a minute at an unlocked laptop into access somebody keeps. All three
now require that the password — and the second factor, when there is one — was
given in the last five minutes. Signing in counts, so in practice it is one
dialog. ADR-0020.

An API token is refused outright rather than waved through: it has nobody to
ask, and a token that could disable two-factor would be a way around the thing
two-factor protects. The step-up is rate limited exactly like signing in,
because otherwise it is an unmetered password oracle for an account whose
session has already been taken — which is the case it exists for.

The frontend never learns any of this happened. The API client sees the panel's
refusal, opens the dialog, and repeats the request, so an action added later
cannot forget to ask.

### And a memory amplifier

Argon2 costs 19 MiB by design, every sign-in attempt starts one, and attempts
for accounts that do not exist have to hash anyway so that timing says nothing.
Nothing capped how many ran at once. On the 1 GB server this product is sold on,
fifty in flight is the machine. Four at a time now, with the rest waiting rather
than refused, so a real sign-in still works while a flood is in progress.

### What was also added

**Sign out everywhere else**, because the moment somebody wants it — a laptop
left on a train — is not the moment to work through a list deciding which row is
which device.

Six properties, six tests, each proven by removing the thing it checks and
watching it fail: the prefix, the name that is not accepted, the CSRF check, the
ceiling, the step-up, and its rate limit. Three more run against the real binary
in `make smoke`. `make check` exits 0, `make smoke` 119 checks, 15 interface
tests pass.

## Phase 64 — the three things between here and an MVP

`docs/checklist.md` named three. Two of them turned out to be work; the third
cannot be done by anything that types.

### Where this project publishes

It publishes where it lives: `TegarTheGreat/Skifity`, so
`ghcr.io/tegarthegreat/skifity` for the image and that repository's raw URL for
the installer and the manifests. That was never a hard decision — it was an
undecided one, and undecided meant the installer carried a name nobody owned.

It is now one `PROJECT_REPO` line in `installer/install.sh` and one in the
`Makefile`. The image tag, the manifest URL, the clone command in every error
message and the tag `make image` builds all derive from those two. Moving to an
organisation of its own later is those two lines.
`scripts/check-home.sh` — `make check` runs it — fails the build if a name this
project does not own reappears anywhere, and four test fixtures that had been
using the old one now say `example/`.

### The manifests follow the release, not a branch

`fetch_manifest` read from `.../main/deploy`. Two things were wrong with that.
The small one: **this repository has no `main`.** There is no default branch at
all — the only branch is the one being worked on — so that URL was a 404 waiting
for somebody to install from `curl | sh`.

The large one is that a branch is the wrong thing to read from. An install that
pulled v1's image and `main`'s objects would apply a Deployment that image had
never seen. The manifests now come from
`raw.githubusercontent.com/<repo>/<version>/deploy`, the same ref the installer
itself was fetched from, so the objects always match the image — and no default
branch has to exist for an install to work.

### The installer knows which release it is

It has to, because it runs as one file with no repository around it.
`RELEASED_VERSION` is a line in its own source, empty until a release sets it,
and `check_release` refuses before k3s is installed while it is empty. A second
refusal covers the other half: an image named with no version and no manifests
on disk, which would have been a 404 twenty minutes into an install.

The release workflow reads that line out of the script and **fails the release
when it disagrees with the tag**. Getting it wrong would publish an installer
that pulls the wrong image or refuses to install at all, and it would be found
by a stranger rather than by CI. `docs/releasing.md` is the procedure.

No tag was cut. Everything a tag needs is in place; what it waits on is the
cluster run, because a release is an invitation to install and the first person
to accept it should not be the first person to find out whether any of this
works on a cluster.

### The cluster run, from a laptop

`test/cluster/verify.sh` has existed and never been run, and part of the reason
is that running it meant a server, an image on that server, and the repository
beside it. `make verify-remote HOST=root@…` does those three: it builds the
image for the server's architecture — asked, not assumed, because the wrong one
dies with "exec format error" — streams it into
`/var/lib/rancher/k3s/agent/images` **before k3s exists**, so the panel's first
pod finds it locally with no registry and no credentials, sends the committed
tree with `git archive`, runs the phases, and brings the report back.
`DRY_RUN=1` prints every command instead of running it, which is how it is
checked here, where there is no server and no Docker.

One thing it found before it ever ran: `verify.sh` defaulted
`VERIFY_GIT_BRANCH` to `main`, which does not exist. An hour of installing,
then a clone that fails. It reads the branch from the checkout now.

`test/smoke/verify.sh` is thirteen checks on the two crosscheck scripts —
including that the image lands in the directory k3s imports from, that the tag
sent is the tag the Makefile builds, and that a dry run never claims the checks
passed. Proven by pointing the image somewhere else.

### The person

Nothing here can be that person, and `docs/walkthrough.md` does not pretend
otherwise: it is the sheet somebody fills in while watching one. The rule it
puts first is not to help, and the thing it says to watch hardest for is the
stop nobody reports — where the reader worked it out and carried on, which feels
like success from the inside and is a bug for every reader after them.

Walking `docs/quick-start.md` as literally as possible did find one real defect.
Step 3 said the first app "has a working address with HTTPS", and the README
said the same in its opening paragraph. It does not: an sslip.io address
deliberately gets no certificate, for the rate-limit reason in ADR-0015, and
two tests assert that it does not. The pages say what actually happens now, and
say why. `llms.txt` had it right all along, which is its own small lesson about
which page gets re-read.

The interface's own deep link caught the fix: renaming that heading broke
`/docs/quick-start#4-add-your-own-domain`, and `TestEveryDocsLinkInTheInterface
Resolves` failed the build. The heading stayed; the sentence moved into the
body.

`make check` exits 0, `make smoke` exits 0 across 116 checks.

## Phase 63 — the default that would have installed a stranger's image

### What the readiness check found

The rubric is `docs/checklist.md`: eighteen things a self-hosted platform is
judged on. Re-measured against the code rather than against the last time
somebody wrote a number down, four of its numbers had drifted — 126 routes
refuse an anonymous request rather than 113, 91 team-scoped routes refuse
another team rather than 85, thirteen pages are served rather than ten,
thirty-eight screenshots rather than thirty-two. All four were understatements,
which is the harmless direction and still wrong.

The blocker was in the installer. `IMAGE` defaulted to
`ghcr.io/skifity/skifity:latest` — a namespace this project does not own and has
never pushed to — and nothing checked it. A run with no `SKIFITY_IMAGE` would
install k3s, change the firewall and write to `/etc`, and only then fail pulling
an image that does not exist, leaving a half-built cluster on somebody's server.
The worse half is the day somebody registers that name: the install would
succeed, as root, with an image nobody here published.

### The refusal

`check_image` is its own function so it can be tested, and `preflight` calls it
immediately after the log line and before anything on the machine changes. It
names the image it will not use, gives the two commands that build a local one
and pass it, and says the server has not been touched — which at that point is
true.

`test/smoke/installer.sh` asks for all four: that the default is refused, that
the refusal names the image, that it says how to build one, and that an image
the operator names is accepted. Two more check the ordering — `check_image` is
called from inside `preflight`, and `preflight` runs before `install_k3s`.
Proven by deleting the call: one check fails and the rest pass, which is the
right shape.

The installer's own header and its root-check message pointed at
`curl -fsSL https://get.skifity.com | sudo sh`, a command that cannot work. The
header now says so and gives the clone-and-build path; the root message names
`sudo sh installer/install.sh`, which is the command somebody running it from a
clone actually needs.

### Where that leaves the MVP

`docs/checklist.md` has the answer in a section of its own. Three things stand
between here and something a stranger can use: somewhere to publish and a tag,
one run of `test/cluster/verify.sh` on real hardware, and Phase 6 with a person
who has not seen this before. Thirteen of the eighteen rows are Written, which
means the code and its unit tests exist and no cluster has ever seen them.
Nothing on the roadmap is larger than any of those three.

`make check` exits 0, `make smoke` exits 0.

## Phase 62 — six bins that asked nothing, and two checks that checked nothing

### The inconsistency was the bug

The panel asks before it deletes an app, a project, a database, a server, a
disk, a domain and a variable. Six other things went straight from a bin icon —
the same size, the same colour, in the same kind of table row:

| | What it costs |
|---|---|
| Revoke an API token | Cannot be got back. Whatever used it — a script, a pipeline, an assistant — fails until it is given a new one |
| Remove a team member | Their access to the team and everything in it |
| Unlink a database from an app | The app loses the variable and cannot reach the database from its next deployment |
| Delete a notification channel | The webhook address and token, typed in by hand |
| Revoke a session | That device is signed out |
| Cancel an invitation | The link already sent to somebody stops working |

Somebody who has learned that this panel asks is exactly the person who clicks
without reading. Each of them asks now, and says what it costs — the token one
spells out the consequence, because that is the one that cannot be undone.

The current session was already safe: its row has no bin at all, so signing
yourself out by mis-click was never possible.

### The gate

`web/scripts/check-destructive.mjs` finds every mutation whose request is a
`DELETE` and every place it is fired from, and fails unless something asked
first. An exception has to be named in the script with a reason; there is one,
and the reason is that it already asks through a dialog of its own. `make check`
runs it. Proven by reverting one of the six.

### Two checks that were not checking

Both were mine, and both were found by using them properly rather than by
reading them:

* **`npx tsc --noEmit` in `web/` passes on any input.** `tsconfig.json` has
  `"files": []` and only project references, so plain `tsc` compiles an empty
  program. `tsc -b` is the real one, and is what `npm run typecheck` and
  `make check` have always run. I had been using the hand-run form all session;
  the first time I ran `tsc -b` it found four genuine errors in Phase 61's own
  work.
* **Grepping `make check`'s output hides failures.** A `nilerr` finding in
  Phase 59's own test — `internal/errdoc/i18n_test.go` returning nil after a
  parse error — sat there through several "green" runs because the filter I was
  using to read the output did not match the line golangci-lint printed. The
  answer is the exit code, not a grep. The finding is fixed and the intent is
  now explicit: a file the walk cannot parse is deliberately skipped.

1556 keys, five languages. `make check` exits 0, 15 interface tests pass.

## Phase 61 — the detail nobody saw, and seven events nobody heard

The bug Phase 60 recorded rather than fixed, and what an audit of the realtime
path found around it.

### The step detail

`{step.status === "failed" && step.detail && ...}`. Three things are written
against steps that *succeed* — the preflight warnings, the server's SSH host
key, the fingerprint of the key the panel installed — and all three were
collected, stored, and rendered to nobody.

Showing them was four lines. Showing them *without putting English back on the
screen* was the rest: the sentences were a single joined blob in a free-text
column, so they are structured now, the way everything else was in Phase 60.
Migration 0013 adds `notes`, a JSON array of `{text, key, args}`; `detail` stays
for the one thing that genuinely is not structured, the rendered problem of a
failed step. A preflight warning reuses the catalogue entry its fatal twin
already has, so the sentence is written once and read from both places.

A failure opens on its own, because that is what somebody is looking at.
Anything else waits behind a line they can click.

### Seven events published, nobody listening

The hub is good: it never blocks a publisher, keeps per-topic history with a TTL
and LRU eviction, replays under the same lock that registers a subscriber, and
the stream handler authorizes topics once, resumes from `Last-Event-ID`, sets
`retry: 3000`, heartbeats every 25 seconds and clears the write deadline. None
of that was the problem.

The problem was the wiring at the other end. The server publishes fourteen kinds
of event; the interface handled seven of them.

| Published | Who should have cared |
|---|---|
| `audit` | **Activity** — the audit timeline, listening to operations and deployments as a proxy |
| `server` | Servers list, server detail — the watcher noticing a server stop answering |
| `app` | App detail — an app that fell over between deployments |
| `domain` | The Domains tab, which is exactly where somebody sits waiting for a certificate |
| `database`, `backups` | The databases list, which had **neither a subscription nor a poll** |
| `project.created`, `project.deleted`, `app.created`, `app.deleted` | The projects list and the project page |

And one in the other direction: the add-server page handled `step`, which
nothing has ever published. The steps arrive on `operation`, which carries the
whole operation.

### The desync signal that was computed and never sent

`Subscription.Dropped()` counts what a slow client missed, and its own doc
comment says "The UI uses it to decide it must reload rather than trust its
state." Nothing called it. A browser asleep with a build log open came back to a
page that had quietly stopped being true.

The stream handler now checks it on every turn of the loop and emits
`event: desync`; `useEvents` adds that handler to every subscriber and
invalidates everything, because a page that missed an event cannot know what it
missed.

### The typecheck that checked nothing

`npx tsc --noEmit` in `web/` passes on any input: `tsconfig.json` has
`"files": []` and only project references, so plain `tsc` compiles an empty
program. The real check is `tsc -b`, which is what `npm run typecheck` and
`make check` have always run — so CI was never fooled, only the hand-run
command was. It caught four genuine errors in this phase's own work the moment
it was used properly.

### The gate

`internal/events` reads every `hub.Publish` call's event name out of the server
and every handler key out of every `useEvents` block in the interface, and
requires the two sets to match — in both directions. Proven by renaming one
handler, which failed it twice: once for the event nobody listens to, once for
the handler nothing publishes.

1549 keys, five languages. `make check` green, 15 interface tests pass.

## Phase 60 — the scaling findings, the preflight report, and the line under every step

The two pieces named at the end of Phase 59, and a third that turned up while
counting them.

### Three surfaces, one mechanism

`errdoc.Sprintf` is the whole of it: format the sentence, and hand back the
values that went into it, each rendered by the verb that was going to print it.
The English stays as it was — the fallback, what the CLI prints, what an
assistant reads — and the locale writes `{{0}}` where the Go wrote `%s`. The
same trade as Phase 59, applied three more times:

* **Twelve scaling findings.** The readiness checker is the panel's best
  advice — the volume only one instance can use, sessions in memory, SQLite, a
  CPU target that fires while the app is idle — three sentences each, under
  `scaling.finding.<code>`.
* **Seventeen preflight problems.** A fatal one becomes an `errdoc` Problem, so
  these live in the error catalogue as `preflight.<code>` and needed no new
  frontend code at all. `PreflightFailed` sets `Cause` and `Fix` on the fields
  rather than through `WithCause`: running an already-rendered sentence back
  through `"%s"` would make the whole English string the one argument, and the
  locale entry could then only be `{{0}}` — the English again, in every
  language.
* **Twenty-five step messages**, under `servers.stepMessage.<key>`.

### The step names were translated. The line under them was not.

`servers.steps.*` has had all fifteen step names in five languages since the
panel shipped — "Checking the server", "Installing Kubernetes". The sentence
underneath each one was whatever Go wrote: *Connected to 203.0.113.10*,
*Ubuntu 24.04, 4 cores, 8192 MB memory, 40 GB free*, *Moved 3 instance(s) to the
other servers*. Somebody adding a server in Indonesian read a translated heading
over an English line, fifteen times in a row.

A step is stored, so this needed migration 0012: `message_key` and
`message_args` beside the `message` that was already there. The English column
stays — it is the fallback, and a step recorded before the migration has only
that. `store.StepNote` carries the three together, which is what the call sites
now pass; a note with no key is shown as its English, which is exactly what the
old rows do.

A failed step shows its problem's title, and that is already in the error
catalogue, so the note's key is `problem:<code>` and the panel looks there
instead. One prefix, one branch, no second copy of ninety-odd sentences.

### Two codes that were not codes

`provision.Problem` had a `Check`, and it is a grouping, not an identity: three
problems say `os`, three say `conflict`, three say `udp_port`. Each has a
`Code` now.

The memory check spliced `"a worker"` or `"a control plane server"` into the
middle of its sentence. No locale can reassemble that — a noun phrase from
another language cannot be dropped where its own grammar needs one — so it is
two codes with two whole sentences.

### Three gates, and one bug found on the way

`internal/deploy` reads every `api.ScalingFinding`'s `Code` out of `scaling.go`;
`internal/provision` reads every preflight `Code` and every `store.StepNote`'s
`Key` out of its own package **and out of `internal/backup`**, which writes step
notes too — the restore flow fills the same list on the same screen. Both refuse
a locale key nothing can produce. The error catalogue's gate hands the
`preflight_*` keys to the provision one, because `"preflight." + code` is the
one code built at run time and cannot be read out of the source where it is
used.

While counting: **a step's `detail` is only rendered when the step failed.**
`{step.status === "failed" && step.detail && ...}`. So the preflight warnings,
the host key and the key fingerprint — all written against steps that
*succeeded* — are collected, stored, and shown to nobody. That is not fixed
here and is not a translation problem; it is recorded in Open issues.

1547 keys, five languages. `make check` green, 15 interface tests pass.

## Phase 59 — the error catalogue, in five languages

The last part of the interface that was English in every language. Ninety-odd
`errdoc` problems — a title, a cause, an impact and a fix each, the best writing
in this repository — reached a Russian or Indonesian operator exactly as a Go
file wrote them.

### Why it was not just another locale file

The settings page, in Phase 55, was a table of static strings: label and help,
looked up by the setting's key. An error is not static. Two thirds of them
interpolate a value — the hostname, the exit code, how many instances were
ready — through `fmt.Sprintf`, and a translated sentence needs the same values
in the same places.

Naming every argument would have been a hundred and seventeen call-site edits
for no gain, so they stay positional. `WithCause`, `WithImpact` and `WithFix`
now record each argument alongside the sentence they built, **rendered by the
verb that was going to print it** — `%q` keeps its quotes, `%d` stays a number —
and the locale writes `{{0}}` and `{{1}}` where the English writes `%s` and `%d`,
in the same order. The server's English is unchanged: it is still the fallback,
still what the CLI prints, and still what the "copy for AI" button copies.

A literal `%%` consumes no argument and must not shift the rest along; a verb
with a width or a flag is read whole. Both have a test.

### Four errors that were invisible to everything

`New("resource.not_found", "That "+kind+" does not exist")` builds its title
from a variable. The catalogue's own test skipped those calls, the extractor
written for this phase skipped them, and so the list of what was missing did not
contain them either — they were missing from the translation *and* from the
report of what was missing. Four of them: `resource.not_found`,
`config.missing`, `component.external` and `firewall.no_geo_database`.

There is a `Newf` now, which takes the title as a format string and records its
values like the three sentences do, and both tests read it. That is 116 codes,
not the 112 the first pass found.

### The gate

The codes are not in a list anywhere — they are the first argument to a hundred
and seventeen `errdoc.New` calls across thirty files. So the test parses the
source with `go/ast`, collects every code with the format strings chained onto
it, and requires each to have `errors.catalogue.<code>` in all five locales,
with a `title`, and a `cause`, `impact` and `fix` wherever the Go has one. It
refuses a locale key no Go file can raise, which is what a code renamed in Go
leaves behind. Proven by deleting one field and renaming one key.

A second proof, in the browser, because a complete locale file does not mean the
running panel resolves it: the interface test switches to Russian, opens an app
id that cannot exist, and checks that the 404 comes out as the Russian sentence
with the server's own value in it — and that the English it replaced is nowhere
on the page. Proven by putting `problem.title` back and watching it fail.

### What is left in English, said plainly

* **The CLI**, on purpose. It has one language, and `CLAUDE.md` says so.
* **The catalogue's `Context` map** — the host, the exit code, the command —
  which is data, not prose.
* **The template catalogue's names and descriptions**, which are mostly
  upstream product copy; the categories are translated.
* **A plugin's own manifest**, which belongs to whoever wrote it.
* **Scaling findings and provisioning step names**, which are the next two of
  these and are much smaller: 14 and 40 strings against this one's 463.

1418 keys, five languages. `make check` green, 15 interface tests pass.

## Phase 58 — what the reference screens actually show, read at full size

Phase 56 and 57 were written from Mobbin's inline previews, which are low
resolution and meant for reading titles. Downloading the full-size images and
reading them changed two decisions and added three details, one of which
reverses something shipped hours earlier.

### The DNS instruction was a sentence. Nobody types a sentence.

"Create an A record for blog.example.com pointing to 203.0.113.10" is how
somebody who already knows DNS would say it out loud. It is not the shape of
what they are copying into: every registrar's form has three boxes — type,
name, value.

[Okta](https://mobbin.com/screens/4330008e-3784-48d2-b750-dd5109549b80),
[Tally](https://mobbin.com/screens/8d7bf746-7f7b-4ac8-a3ee-4ee4cbb3fcfe),
[Klaviyo](https://mobbin.com/screens/f6dcd542-ad3a-4101-abff-404742f1d19b),
[AutoSend](https://mobbin.com/screens/bb3e5124-d099-46c2-881a-2e0037cef8ac) and
[Loops](https://mobbin.com/screens/455556f9-d2d3-4058-8b2d-3cc2315bdd1d) all lay
it out as those three columns with a copy control on **each cell**, not only on
the value. So does this now.

Read at full size, Okta's page carries three sentences the preview could not
resolve, and each of them is a thing this panel was not saying:

* **"The host format may vary by registrar."** That is the mistake people
  actually make. Half the registrars want the whole hostname in the Name box
  and half want only the label in front of the domain. The panel cannot work
  out which part is the zone without the public suffix list — `.co.uk` breaks
  the naive split — so it says so instead of guessing.
* **"It may take a few minutes for the DNS changes to be available globally."**
  Waiting is the normal case and reads as a failure without a line saying so.
* **"After the DNS records are updated, return here to verify them."** There is
  a **Check again** button now, next to a link to the panel's own quick start.

### Masking the database password: the reversal

Phase 57 deleted a `secret` prop from the credentials row on the grounds that
it chose between `type="text"` and `type="text"` and had never masked anything,
and that the card is already behind a **Show credentials** gate — so why hide
twice?

Because that is not what the gate is for. Every product in the reference set
keeps the mask *after* the gate:
[Cloudflare](https://mobbin.com/screens/31f33e60-deaa-4d06-9a91-b70ab9ceb153)
and [Retool](https://mobbin.com/screens/fac20c45-0981-40f5-b6f6-a6ed3c589146)
put an eye toggle on the field,
[Laravel Cloud](https://mobbin.com/screens/35bdc533-361d-45c3-b23c-d8feb58ccac7)
masks the whole block behind one, and
[PlanetScale](https://mobbin.com/screens/cfddc224-b1bd-498d-a548-ad145a60c20f)
does not show the password again **at all** — "Cannot be displayed after
creation", with an offer to make a new one.

The gate is consent to fetch the secret. The mask is so that fetching the
*host* does not leave the password on a screen somebody is sharing. The prop is
real now: `type="password"` with a per-field eye, on the password and on the
connection string that carries it inside. The copy button hands over the value
without putting it on screen.

PlanetScale's version is stronger still and is not available here: this panel
can decrypt the password, so "cannot be displayed" would be a lie.

### A delete dialog says what the safety net is

[Adobe](https://mobbin.com/screens/0e4a6af0-e652-41bf-8602-ffdf96e0bec4) lists
the items that will be "gone for good",
[Render](https://mobbin.com/screens/e62178b2-645c-4dc2-befe-196618fa3dee) says
to move the services out first if you want to keep them, and
[Laravel Cloud](https://mobbin.com/screens/7a7a42b5-04a3-4a56-9410-7a8c9484c5a5)
says it will take a final backup before archiving. The dialog is where somebody
finds out whether they can afford to press the button.

The volume row already knew when the disk was last copied — it is a column on
that table. The backup query moved up into the row so the dialog can use it
too, and it now reads either "The last working backup of this disk was three
days ago. Anything written since then goes with it." or, in the case that
matters, **"This disk has never been backed up. Everything on it is gone for
good."** A failed backup does not count as one.

### The gate for the link

Three `/docs/` links are written straight into `.tsx` files — the sidebar, the
dashboard and now the Domains tab. The catalogue's `WithDocs` links have been
checked page-and-anchor since Phase 16; these were checked by nothing, and a
page renamed in `docs/` would have broken all three in silence. A test in
`internal/docsite` now reads every `href="/docs/…"` in the frontend and serves
it, anchor included. Proven by breaking the anchor.

954 keys, five languages. `make check` green, 14 interface tests pass.

## Phase 57 — the DNS record that said "Unknown", and the disk one click could destroy

A walk of every journey, start to finish, asking one question at each screen:
can somebody who has never seen this finish what they came to do? Three screens
said no.

### "Create an A record for blog.example.com pointing to Unknown."

That is what the Domains tab said. Literally: it interpolated
`t("common.unknown")` where the address goes, on the one screen in the panel
whose entire job is to answer where to point a domain. `docs/quick-start.md`
step 4 says "The panel shows the DNS record to create."

The address was never missing. `domains.cluster_ip` has been a setting since
there were settings, its own help reads "The address your domains should point
at", and `Cluster.clusterAddress` resolves it — setting first, then a
control-plane server's external address — every time it hands an app its
automatic subdomain. The Domains tab simply never asked.

It asks now. `PublicAddress` is on the Cluster port, `handleListDomains` fills
a `dns_target` per domain, and the instruction carries the real value with a
copy button beside it. An address that is a name rather than a number is a
CNAME, so the sentence follows the value; an install where nobody has set it
and the panel cannot see one says so, and says where an administrator sets it,
rather than printing a word where an address belongs.

### Storage was the roughest tab in the panel

Four things on one screen:

* The column showing `/data` was headed **Storage**, and so was the form field
  where you type it. Both now say **Mount path**.
* The empty state's description was `scaling.spreadHelp` — the sentence from
  the scaling tab about spreading instances across servers, on a screen about
  disks. It now describes what a disk is for.
* The size field said **Size**, in a number box with no unit, over the help
  text for a *database's* disk. It says **Size (GB)** over a sentence about a
  disk that outlives a deployment.
* **The bin icon deleted the disk immediately.** The panel makes you type the
  name before it deletes an app, a project, a database or a server — and none
  of those destroy anything a redeploy cannot bring back. This one does. It
  asks now, with the name typed out and the consequence spelled out.

Two more actions that took effect on one click and should not have: removing a
domain, which takes a live address off the internet, and deleting a variable,
whose value is sealed and cannot be read back.

### The gate caught one more of its own

`placeholder={host || "Frankfurt 1"}` — the example server name, in English for
everybody, written as a fallback rather than a value, which is how it slipped
past the literal check added in Phase 56. The check now reads the brace form
too, for prose only: a code sample, a product name and an interpolation
argument are written the same way and are not translatable.

946 keys, five languages. `make check` green, 14 interface tests pass.

### The conclusion, and what it rests on

The interface is done. What is left is the half of the panel that is written in
Go: **90 `errdoc` problems, 14 scaling findings, 40 provisioning steps** and a
scattering of status details reach the screen as English sentences the locale
never sees. They are the panel's best writing and none of it is translated. It
is one mechanism — the one `internal/settings` already uses — applied to more
places, and it is the largest piece of work left in the interface.

## Phase 56 — a live version nobody could point at, and an app asleep that read as down

Four screens from Mobbin — Vercel, Render, Railway, Laravel Cloud, Cloudflare —
against the same four screens here. Most of what they do, this panel already
does. Four things it did not, and three of them were wrong rather than missing.

### The deployments list never said which version was serving

Every row looked the same. A rollback here is a new deployment carrying an old
image, so the list is a straight line and the newest succeeded row is always the
one in production — but nothing said so, and the row a reader assumes is live
(the highest number) is a superseded build the moment anybody rolls back.
Cloudflare puts "Active deployment" at the top of the list; this puts a **Live**
badge on the row itself, derived during render from the list already on screen.

The live row also stopped offering **Rollback**. Rolling back to the version
already running is a deployment that changes nothing, and a button offering it
invites the question of what it would do.

### A rollback said "a person" deployed it, in English

`Trigger` was written as the sentence `rollback to #3`. The panel ships in five
languages, so that sentence went to the user untranslated; and the list matched
the trigger against `"rollback"`, missed, and fell through to the default, which
says a person deployed it by hand. The same default swallowed `create`,
`template` and `preview`: four of the six triggers the panel stores rendered as
the wrong one.

The number is data, so it is stored as data — `rollback_of`, migration 0011 —
and the sentence is built in the interface, where it has five translations.
`deploy.trigger.*` now has a key per trigger and the list uses all of them.

### An app asleep read as an app somebody stopped

Laravel Cloud puts "Hibernating 2h" in an app's header. Scale to zero has been
in this panel since Phase 7, and when it took the last instance away the app
page said **Stopped**, with the grey badge and the pause icon — the same thing
it says about an app a person deliberately took down. The cluster is not wrong:
it can only see that the instances are gone. The panel knows the user asked for
exactly that and that the next request brings the app back.

So the status handler rewrites that one case to `sleeping`, with a sentence
saying the next request starts it again, and the badge has a word for it in all
five languages. A test covers both readings of zero instances: with the setting
on it says asleep, with it off it says stopped.

The status sentence under an app's name came from the cluster in English. Three
phases have a sentence that never varies — sleeping, stopped, not deployed — and
those are translated now; the rest carry a number or a message from Kubernetes
and still fall back to what the panel was told.

### Copying anything was three implementations and a dead prop

Render puts a copy control beside every id and address. This had three
hand-written ones that did not agree — one toasted, one did not, one put the
word "Copy" in a button wide enough to push the value off the row — and the
app's own address, the thing people came to the page for, had none: it could be
clicked and not copied. One `CopyButton` now, with a tick where the icon was,
used on the address and on every database credential.

The credentials rows carried a `secret` prop that chose between `type="text"`
and `type="text"`. The whole card is behind "Show credentials", so nothing there
needs hiding twice; the prop that pretended otherwise is gone.

### Three English words on a translated page, and the gate that missed them

`label="Host"`, `label="Port"`, `label="User"` — beside a database name that was
translated — plus `label="Architecture"` on the server page. No check saw them:
they are not in an `sr-only` block, they carry no `aria-label`, and they are not
keys that could go missing. `check:i18n` now refuses a capitalised literal in
`label`, `title`, `description`, `confirmLabel` or `placeholder` anywhere
outside the vendored `components/ui`, with an allowlist for names that are the
same word everywhere (Kubelet, PostgreSQL, Docker) and an exemption for a
placeholder that is code rather than prose — `DATABASE_URL` is an instruction to
type that exact string.

It found two more on the way in: the example server location was `Frankfurt` and
the example team name was `Acme` for everybody. They are examples, so they are
translated like everything else — an Indonesian operator is offered Jakarta.

A second gate, in Go: every phase either `summarisePhase` or the status handler
can return must have `apps.phase.*` in all five locales. Proven by deleting
`sleeping` from `ru.json`, which failed it.

931 keys, five languages. `make check` green, 14 interface tests pass.

## Phase 55 — the half of the panel that was never translated

"There is still a lot of i18n missing." There was, and not where the checker was
looking: every key existed in all five languages — 825 of them — and the largest
page in the panel was still entirely English, because its words do not come from
the locale at all. They come from the server.

### Settings, in English, in every language

`internal/settings` carries a label and a help paragraph for each of the
forty-three settings, and the panel showed them as they are. With the interface
in Indonesian the chrome read Pengaturan, Umum, Klaster — and every field under
it read "Panel URL", "Which builder to use when a repository has no
Dockerfile…". Six and a half thousand characters of English on the page an
operator spends the most time on. The screenshot of it is in `docs/images`,
before and after.

The server keeps its English: it has one language, and the API, the CLI and an
assistant all read those strings. The panel looks them up by the setting's own
key — `settings.field.<key>.label` — with the server's text as the fallback for
a setting added before anybody has translated it. Eighty-six strings, five
languages.

`TestEverySettingHasItsWordsInTheInterface` checks that every definition has
words in the interface **and that the English matches character for character**.
Two copies of the same sentence drift, and the one that drifts is the one nobody
reads.

### Two screen readers' worth of English, and a button nobody has pressed

The mobile sidebar's drawer announced itself as "Sidebar. Displays the mobile
sidebar." in all five languages: text inside an `sr-only` block rather than on
the tag carrying the class, which is the shape the checker did not look for. It
looks for it now, and the pattern found the other one — a `Close` button in the
dialog footer, hardcoded, behind a flag nothing passes today. A hardcoded
English button waiting for the first person to turn it on.

### Two deploy buttons, and the answer changing on a second look

Asked about, answered "that is the pattern", asked again, and looked again —
this time the answer is different, because the second look was at the screen
rather than at the other pages.

The pattern is real: Databases, Servers and Projects all repeat their primary
action in the empty state. On those pages the empty state **is** the page, so
the two buttons are obviously the same thing, because there is nothing else they
could be. The app page is not that: the empty state sits in a card titled
"Instances", among other cards, with "Deploy now" already in the header. A
second "Deploy now" inside a card about instances invites the reading that it
starts an instance without deploying — which is not a thing this panel does.

So the app page is the one place that does not repeat the action. The sentence
does the work instead, and names the button by its own label rather than in
English: it reads "Deploy sekarang" in Indonesian and "立即部署" in Chinese,
which is what the button at the top of that page actually says. The empty state
also has a title now rather than the logs tab's full sentence with a full stop
in it.

### What is still English, said plainly

* **Every error.** Ninety `errdoc.New` sites, each with a title, a cause, an
  impact and a fix — the panel's best writing, and none of it translated. It is
  the next piece, and it is larger than this one was.
* **The template catalogue.** Two hundred and eighty-two names and descriptions,
  which are mostly upstream product copy; the categories are translated.
* **A plugin's own manifest**, which belongs to whoever wrote it.

## Phase 54 — a page of snake_case, an app with no address, and a trail to nowhere

The project page, the app page and Activity, read the way somebody meets them.

### Activity was a column of machine codes

The page that answers "what happened" printed `app.scaling_changed`,
`variable.set`, `setup.completed` — the strings the database stores. On a panel
whose whole premise is that Kubernetes stays out of sight, the human page was
showing identifiers.

All seventy-two audit actions have words now, in five languages: "Scaling
changed", "Variable set", "Panel set up". The code stays on the hover, because
this is also the page somebody reads with a log open beside them, and a code
with no phrase falls back to itself — which is what every row used to be. The
audit tab in Settings shows the same phrases, so one thing is not called two
names in one product.

`TestEveryAuditActionHasWordsForIt` reads the actions out of this package's own
source and checks each one against the locale. Adding an action without a phrase
fails with the file it is in and the key to add.

The page also borrowed the audit tab's sentence — "Who did what, when, and from
where" — while deliberately not showing the address, and then repeated that same
sentence in its empty state.

### An app page that could not say where it was

The breadcrumb read "Overview › Apps", the title was the app's name, and nothing
anywhere named the project or the environment it belongs to. The only way back
to its project was the browser's back button.

It says "Storefront · Production" under the name now, with the project as a
link. Both queries are keyed so they come from the cache when anything else has
already asked.

Two more on the same page: the stat card was labelled "Instances" above a card
titled "Instances", and read "0 / 0" without saying which number was which — it
is "Instances ready" now. And "The panel is not connected to a cluster" was a
grey sentence floating between cards, while the same condition is an Alert on
the Overview page; it is an Alert here too, with a tone that follows the phase,
so a cluster that cannot be reached reads as a problem and "waiting for the new
instances" does not.

### A trail that pointed at the not-found page

The breadcrumb drops the ids — `/apps/app_06gb…` is noise — and rebuilds the
path from what is left. On the new-app page, `/environments/env_x/apps/new`,
that produced links to `/environments` and `/environments/apps`. Neither is a
page. Both were links.

A crumb is a link only when the path it would point at is one of the panel's
pages now, and a Playwright test walks every breadcrumb on every seeded page and
follows it, failing if it lands on the not-found page.

### The test that was testing an older build

`make e2e` depended on `backend`, which builds the binary against whatever is in
`web/dist`. Change a page, run the interface test, and it tests the build from
an hour ago — which is exactly what happened here: a tap target that was too
small kept failing after it had been fixed. There is a `ui` target now, and both
`e2e` and `screenshots` depend on it.

**Verified by looking:** the screenshots are recaptured, and the one that made
this pass worth doing is Activity — eight rows that used to be code.

## Phase 53 — the braces on the page, cron in a text box, and four sentences said twice

An interface pass, looking for what a person meets rather than what a test
covers: placeholders, flows, repeated words, and the controls that ask somebody
to know a syntax.

### `{{product}}`, on the page, in five languages

The app's Settings tab read "Most frameworks read it from the PORT variable,
which **{{product}}** sets for you". The same string on the New app page reads
correctly, because that call passes the value and this one did not. i18next has
nothing to say about a missing interpolation: it renders the braces and carries
on.

So `check:i18n` reads the call sites now. Every `t("…")` for a string with a
placeholder has to name each one, and the check is deliberately one-sided —
passing a value a string does not use is harmless; leaving one out is what
shipped.

### A schedule that asked you to know cron

A backup schedule and a scheduled command were both a text box containing
`0 3 * * *`. That asks every user to know five fields in the right order, and to
find out they were wrong at three in the morning when the backup they thought
they had did not happen.

Four presets cover what people pick — hourly, daily, weekly, monthly — and cron
stays behind "Custom" for the times they do not. The times say UTC, because a
schedule that quietly meant the server's idea of local time is a different
failure in every timezone. The list of scheduled commands shows the words rather
than the expression: a row reading `0 3 * * *` asks whoever is looking at it to
parse cron in their head.

One piece of local state survives the "derive, do not synchronise" rule, and it
is written down: which mode the control is in is derived from the value, except
for the moment somebody picks "Custom" while the box still holds a preset, which
is an intent no value can carry.

### The same sentence, twice on one screen

Four screens introduced themselves and then said it again:

* **Databases** used the apps' empty-state help as the page's own description,
  so the subtitle and the empty state were the same sentence.
* **Projects** and **Servers** did the same. Servers went further: its page
  description was the list of what a server needs — Ubuntu, a gigabyte, SSH as
  root — which belongs on the page where you add one, and already is there.
* The **Console** tab showed "Run a command" as a field label and again as a
  panel title, with its help text repeated word for word sixty pixels below.
  What that panel is for is output, so it says so now.
* The backup card labelled three different controls "Automatic backups": the
  card, the switch and the schedule. And "Keep the last" was a number with no
  unit.

### Two controls for one action

On Databases with more than one environment, the header button asks which one
and the empty state picked the first — and with no environments at all it called
`setCreating(null)`, which is a button that does nothing. Both are the same
control now.

### Advanced, two ways

Add a server opens its advanced section with a bordered row and a chevron. New
app had a bare ghost button that said "Show advanced" and then "Hide advanced" —
a different affordance for the same idea, two pages apart in one flow. It is the
same control now, and the dead `advanced` state is gone with it.

### The rest of what the pass found

New app described itself with the apps' empty-state text, which ends "or start
from a template" — a third path the form does not offer. The sentence says what
the page does, and the template path is a button beside it. The Dockerfile path
field was labelled "Dockerfile", which is the name of the builder option above
it. The sidebar had one group, headed "Overview", above an item called
"Overview". Documentation is the one link that leaves the panel and now says so.

**Verified by looking:** the screenshots are recaptured from the real binary,
and the console tab is in them now — the scheduled commands card had never been
photographed.

## Phase 52 — a command nobody waited for, an error cached forever, and a path the panel chose

An audit of `internal/mcpserver`, `internal/cli` and `internal/templates`.

### "It waits for the command to finish" — it did not

`skifity run -- npm run migrate` starts a Job and then reads its log. The read
did not pass `follow`, so the panel returned whatever the container had printed
by the time the pod was first seen running — which, for anything slower than the
two-second poll, is nothing. The CLI printed "Running: npm run migrate", a blank
line, and exited zero. Both it and the MCP tool said otherwise: the tool's
description reads "It waits for the command to finish and returns its output",
and the CLI's own comment said "the output comes back when the command is done".

An assistant reading an empty output as a successful migration is the worst
answer available, so this is the one that mattered most.

Both follow now. That needs a client without the ordinary one-minute deadline,
so `DoLong` exists beside `Do`: the wait belongs to the migration, not to the
network. The CLI has no cap and says that Ctrl-C stops the waiting rather than
the command; the MCP tool caps at ten minutes, because what is on the other end
is an assistant waiting on a tool call, and past the cap it says the command is
still running and where its output will be.

### An error that outlived its cause

The MCP server resolved the team once, with a `sync.Once` — which caches the
failure as happily as the success. An assistant that opened its editor while the
panel was restarting got the same error from every tool for the rest of the
session, and the only cure was restarting something nobody would think to
restart. Only success is kept now.

### The panel chose where the CLI wrote

`skifity export` writes `manifests/<namespace>/<app>.yaml` under the directory
the user named, and both the namespace and the slug come from the panel's own
JSON. `filepath.Join` cleans as it goes, so a namespace of `../../.ssh` becomes
a path beside the export rather than inside it, and the result looks perfectly
ordinary. It is the panel the user signed in to, so this is unlikely — and the
check is one line, while what it prevents is a file written over somewhere
nobody looked. `underneath` refuses anything that climbs out, with a test for
the cases that actually climb (one `..` is absorbed by the `manifests` element
and lands back inside; two are not).

### The tool table nobody checked

`llms.txt` prints the MCP tools, and that page is what an assistant is pointed
at. The API routes in the same document have been checked against the router
since Phase 22; the tool table was checked against nothing. It is now, in both
directions — a documented tool that does not exist, and an existing tool nobody
documented, both fail. Adding a row for a tool that is not there fails with its
name in the message.

`ReadIcon` sliced a file name at its last dot without checking there was one, in
the function whose comment says it checks the name anyway "because the one that
is not checked is the one that changes later". Now it does.

### What was checked and found sound

The catalogue is the best-gated part of this codebase: thirteen tests, including
that every image names a version rather than `latest`, that every database
reaches the service it is for, that every list is an array in JSON rather than
`null`, that every icon belongs to a template and can actually be served, and
that what the README says ships is what ships. The CLI's config file is written
0600 and a test reads the mode back; `SKIFITY_URL` and `SKIFITY_TOKEN` override
it for a CI job or an assistant; every command takes `--json` and a test walks
the package to prove it. The MCP tools go through the same API with the same
scoped token as the CLI, so an assistant can do what the token's owner can do
and nothing more.

## Phase 51 — the keyring under load, a code used twice, and the secrets rotation stepped over

An audit of `internal/api`, `internal/auth`, `internal/store` and
`internal/crypto`. Four real defects, three of them in the parts that are only
exercised on the day they matter.

### One keyring, no lock

The `Keyring` is shared by everything in the panel — the API, the deployer, the
backup manager, the cluster adapter — and rotation writes to its map while all
of them read it. There was no mutex. A Go map read during a map write is not a
race the program survives: the runtime **throws**, and a throw is not a panic,
so `runsafe.Recover` never sees it. Rotating the master key on a busy panel
could take the panel down, which is also the one thing that makes the cluster
unreachable.

The lock is held for every read of the map as well as every write, because
`DropKey` zeroes a key's bytes and a slice read after that is a key of zeros.
`BeginRotation` now picks the id, adds the key and promotes it under one lock:
two rotations started together would otherwise choose the same id, and the
second would fail after the first had already moved the active key.

The test runs four goroutines sealing and opening while five rotations run
through them. Under `-race` it fails on the old code and passes on the new, and
`make check` runs the race detector.

### The rotation stepped over the plugin secrets

`ListSealedSecrets` is the list master key rotation walks. It named eight
columns. The database has ten: `plugins.hmac_sealed` and the sealed rows in
`plugin_settings` were both missing — added by the plugin work three commits
earlier, and not added here.

The consequence is silent and permanent. Rotation drops the retired keys once
every secret it knows about has been rewrapped, so the two it did not know about
stay wrapped in a key that no longer exists. Nothing reports it. The first sign
would be plugins that quietly stop receiving events, because the panel can no
longer read the secret it signs them with.

Both are listed now, `SealedRef` carries a second key column for a row
identified by two, and `TestEverySealedColumnIsRotated` walks the schema the
panel actually creates: a column named `*_enc` or `*_sealed`, or a `value`
beside an `encrypted` flag, has to be in the list. Removing one line from the
list fails the test with the column's name in it.

### A two-factor code that worked twice

RFC 6238 says a one-time password is used once. The panel accepted a code for
the current thirty-second step and one either side, and recorded nothing, so a
code read over a shoulder, off a screen share or out of a proxy log stayed valid
for up to ninety seconds.

`VerifyTOTP` now returns the step it matched, and the step is spent in a single
statement — `UPDATE … WHERE totp_last_counter < ?` — so two sign-ins arriving
with the same code in the same instant cannot both win. The code that switches
two-factor on is spent as well, so it cannot be the code that gets past it a
moment later.

### A plugin heard about every team

A plugin is installed panel-wide by an owner, and the dispatcher sent every
subscribed plugin every event. On a panel with more than one team that means an
owner of one team could install a plugin and have it watch another team's
deploys — and, with a blocking hook, refuse them.

A plugin's token already belongs to whoever installed it, so it can read what
that person can read. The events draw the same line now: a plugin hears about a
team only when its installer is a member. An event that carries no team reaches
nobody, which is the safe direction, and both refusals are logged rather than
silent, because a plugin that receives nothing looks exactly like a plugin that
is broken.

### Two smaller things

Rotation ran on the request's context, so closing the tab halfway through
cancelled it: the secrets already rewrapped were fine, the rest kept the old key,
and nobody was told which was which. It runs on its own context now.
`SaveKeyring` wrote and renamed without syncing the directory, so a crash
seconds later could lose the rename — on the one file whose loss cannot be
undone.

### What was checked and found sound

Envelope encryption: per-secret data keys, AES-256-GCM both levels, the wrapped
key bound to its key id and the ciphertext bound to where it is stored, a strict
base64 decoder so an envelope has exactly one textual form, and bounds checked
before every slice. Passwords: Argon2id at the OWASP parameters, parameters read
back from the hash so old ones keep working, an unknown account costing the same
hash as a known one, and a lockout per account and per address. Sessions and API
tokens: SHA-256 at rest, expiry checked after the read, a disabled account cut
off immediately rather than at expiry. CSRF: double-submit, enforced only where a
cookie could carry the request, exempt for bearer tokens. The store: one
interpolated statement, and its table and column names come from a fixed list.
The API's authorization is gated by tests that walk the router rather than a
list somebody maintains — every route refuses an anonymous request, every route
that takes an id refuses another team's.

## Phase 50 — the schedules, the sweep, and six documents that were wrong

An audit of the builder, the deploy path, the cluster adapter and cron, and a
read of every document against the code.

### The settings that had nowhere to be set

Single sign-on is built — OIDC, PKCE, a verified ID token, a nonce, a spent
state. Six settings, a section of the documentation, and **no way to set them in
the panel**: the frontend kept its own list of setting groups, and `signin` was
not in it, so the group rendered as nothing at all. Plugins had been the same
thing a commit earlier.

The list is now an *ordering*, not the list. The groups come from the settings
the server sends; anything this build has not heard of is shown at the end under
its own key. Untranslated is worse than translated and a great deal better than
invisible, and a hand-kept copy of somebody else's list is a list that is wrong
the day it changes.

### A backup schedule nothing could parse

`PUT /api/databases/{id}/backup-policy` verified that backup storage worked —
"rather than at three in the morning", as the comment says — and did not verify
the schedule. Anything at all was accepted, stored, and then never matched, so
the backups simply did not happen. Nothing could report it: by then it is a row
that is never due. It is parsed now, and refused with the same message the
scheduled-command path uses.

### "5/15" meant five

In cron, a step after a plain number means "from here to the end of the field,
every n". The parser cut the step off, parsed the number, and dropped the step on
the floor — so `0 5/6 * * *`, four times a day, ran once. The schedule parsed.
Nothing was logged. It is the exact failure the package comment says it exists to
prevent, written in the package itself.

`Describe` is gone rather than fixed. It rendered a schedule in words and would
say "every day at 03:00 UTC" for `0 3 * 1 *`, which runs in January — and it had
no callers, because a sentence built in Go cannot be shown in an interface where
every string is a translation key.

### The minute tick that could stop for half an hour

Scheduled backups run on the panel's own minute tick. The registry sweep ran
inside that tick: it takes the build lock, which waits for every build in flight
— a build is allowed forty minutes — and then waits up to thirty more for its
Job. For that whole window no backup was evaluated, and a nightly backup due
inside it never ran. Maintenance now runs beside the tick rather than in it.

Ticks are dropped by Go when the receiver is late, so the tick also catches up:
it evaluates every minute since the last one it looked at, capped at five —
Kubernetes' own starting deadline for a CronJob that could not start on time.
Long enough for a slow minute or a restart, short enough that a panel switched on
after a week off does not fire a week of backups at once. A policy that matches
several caught-up minutes still runs once.

### Applying onto a corpse

`Delete` returns when the API server accepts the request. With foreground
propagation the object stays — deletion timestamp, finalizer — until its
dependents are collected. Two places deleted a Job and applied the same name
immediately: the build, and the registry sweep, whose Job has the same name every
single time. The apply is accepted, the object is collected a moment later, and
the wait that follows waits for something that is not coming: a build that never
starts, or a sweep that reports it did not finish while the disk fills. There is
a `DeleteAndWait` now, with a test that an object held by a finalizer produces a
refusal rather than an apply.

### What the documents claimed

* `CLAUDE.md`'s package layout was missing fourteen packages — plugins, the
  firewall, cron, the registry client, the three guard packages — and listed a
  doc-site directory under the frontend that does not exist. The doc site is
  `internal/docsite`, and the check in it caught this paragraph naming the path
  that is gone, which is the check working.
* `docs/configuration.md` described a **Git** settings group that is empty: the
  GitHub App settings were removed when it turned out nothing read them, and the
  table still offered them. It was missing **Cluster**, **Sign-in** (six
  settings, already documented in the same file) and **Plugins**, and described
  Domains without the tunnel token, the trusted proxies or the geo databases.
* `docs/backups.md` said a volume backup could be put on a schedule "the same way
  as for a database". There is no volume policy: no route, no handler, no
  control. Databases have schedules; volumes are taken when somebody asks.
* `llms.txt` — the page the product hands an AI assistant — had no firewall and
  no plugins in it at all, in either the endpoint list or the notes.
* `docs/progress.md` said `docs/decisions.md` holds ADR-0001 to ADR-0017. It
  holds nineteen.

### What was checked and found sound

The deploy path handles the things that usually go wrong: a deployment
interrupted by a restart is marked failed at startup rather than left "building"
forever, a superseded build is stopped rather than left to roll out an older
version behind a newer one, a panic in one deployment is caught without taking
the panel with it, the fingerprint match only ever reuses an image from a
deployment that succeeded, and a rollback outside the registry's keep window is
refused with a reason instead of sitting in ImagePullBackOff. Scheduled commands
are Kubernetes CronJobs and not the panel's business: "a panel that is restarting
at 03:00 should not be the reason a nightly job did not run."

## Phase 49 — the settings nobody could open, and a domain nobody owned

Two settings and a rename.

**The plugin settings had nowhere to be set.** The panel defined them, the
documentation told people to open Settings, then Plugins — and the frontend's
group list did not carry `plugins`, so the group rendered as nothing at all. A
setting that exists on the server and not on the page is a setting that only
looks configurable.

They have a tab now, with a button that reads the catalogue and says which of
the three answers came back: signed by the key set here, readable and vouched
for by nobody, or a failure that names itself. An address and a key are a pair
you otherwise discover is wrong on the day you wanted a plugin. It reads on
request rather than on mount, because an address somebody is halfway through
typing should not be fetched and a store that is down should not make the
settings page look broken.

**`skifity.io` was never ours.** `skifity.com` is. It was the vendor domain on
every Kubernetes label and annotation the panel writes — `skifity.io/app-id`,
`skifity.io/managed` — the plugin standard's `apiVersion`, and the install
command in the README, the installer, the release notes and every page of the
documentation. Using a domain somebody else may register is the one thing the
Kubernetes convention for those keys exists to prevent, and doing it before
anything is published costs nothing where doing it after costs everybody a
migration.

The rename found a latent bug. `internal/provision/scripts.go` wrote
`--node-label=skifity.io/managed=true` by hand while everything else went
through `version.LabelKey`, so changing the domain in the one file that is
documented as the place to change it would have left every node carrying a key
nothing else looked for. It goes through the same function now.

## Phase 48 — the store, and the page that admits what it cannot reach

The runtime could install a plugin. Nothing could find one.

**A store is two static files.** `index.json` lists what is available;
`index.json.sig` is a detached Ed25519 signature over its exact bytes. Two files
rather than one envelope, because a catalogue somebody can open in a browser and
check by eye is worth more than one that is only machine-readable. No database,
no API, no accounts — a store is a directory on a web server.

**The signature is checked before the index is parsed.** Parsing first would
mean the panel had already acted on bytes nobody vouched for. An index entry
pins its manifest by SHA-256, so the operator vouches for the list and the hash
stops a manifest being swapped after the list was signed. That is an apt release
file, and it is chosen because it needs no key registry: a publisher does not
need a key, because the store is what vouches for them.

Three answers, not two, for the key:

* **No key configured** — the index is read and every entry is marked as
  unverified, on the page, in those words. Refusing outright would mean a store
  cannot be used until somebody pastes a key.
* **A key, and a signature that checks out** — verified.
* **A key, and no signature or a wrong one** — an error, not a catalogue. The
  quiet middle answer is what turns a signature into decoration.

**One bad row must not empty the page.** An entry with no id, no manifest
address or an unparseable hash is dropped, because it is not a thing the panel
could install even if it wanted to; the rest of the catalogue still shows. An
index of another version is refused whole, because that is not a bad row, it is
a file this build cannot read.

`skifity admin plugin-key` prints a pair and writes neither. A command that
saved the private key for you is a command that leaves a signing key in `/root`,
and the one thing an operator has to do with it is put it somewhere they already
trust. `skifity admin plugin-sign` signs a file's exact bytes — reformat the
JSON afterwards and the signature stops matching, which is the point. Signing
and verifying live in the same package, because two implementations of "the
exact bytes" is one implementation and one bug waiting.

**The Plugins page is three tabs**: what is installed, the store, and an
address. The third is not a fallback for when the store is down — a cluster
behind a proxy that never reaches `plugins.skifity.com` has to be able to run
plugins too, and so does an operator who wants their own index and their own
key. Both are settings.

Installing from the store is still the runtime's two requests: the manifest is
fetched, its permissions are shown, and the install is refused if they changed
between the screen and the button. The store adds a hash check in front of that
and nothing else — being in a signed index is not a reason to skip the part that
actually protects anybody.

**Verified by running it:** a key generated by the real binary signs a real file,
`pluginstore.VerifySignature` accepts it, and rejects it after one byte changes.
Nine unit tests cover the rest: another key's signature, a tampered index, a
missing signature where a key is configured, the unsigned path, a manifest that
does not match its hash, a wrong index version, and the bad rows that get
dropped.

**Not run:** nothing is published at `plugins.skifity.com`. The panel can read a
store, verify one and install from one; there is no store. The Store tab will
say it could not reach anything, which is the truth.

## Phase 47 — the plugin runtime

The standard said what a plugin is. This runs one.

Installing is four things in an order that matters: a token narrowed to exactly
the permissions the manifest declared, a secret the plugin will verify events
with, a Secret object holding both plus its settings, then the pod. The token
first, because a pod that starts without one is a plugin whose first request
fails for a reason nobody can see. Removing is the same list backwards, and the
token goes even when the cluster cannot be reached — a credential nobody can
trace to anything is worse than a namespace left behind.

**One namespace per plugin**, not one shared namespace with all of them in it.
Two plugins from two publishers have no more reason to reach each other than two
tenants do. What a plugin can reach is the panel, DNS and the internet; what it
cannot is every other namespace, the node network and the cloud metadata
address. It holds no Kubernetes token, runs non-root on a read-only root
filesystem, and its namespace enforces the strict profile — a requirement a
plugin author can meet, because unlike an off-the-shelf application image they
control the Dockerfile.

**Installing is two requests, deliberately.** The first reads a manifest and
answers what it would do; the second installs it, and is refused when the
permissions no longer match what was shown. One request would mean the
permissions were displayed by the same call that granted them, which is a
confirmation nobody reads because it is already too late. Owner-only: an admin
who manages servers is not the same person as the one who decides what code runs
in the cluster.

**Every event carries an HMAC** over its exact bytes, keyed per plugin. The
endpoint is only reachable from the panel's namespace, which is the first line
and not the only one — anything that ever runs beside the panel could otherwise
post "deploy.before, allow it" and be believed.

The four rules about blocking each have a test, because each is a way for the
hook to be decoration:

* **Silence is not consent.** An empty body is a refusal; a plugin that answered
  200 and nothing else has said nothing.
* **Not answering is not a refusal.** A plugin that stops every deploy the
  moment it is upgraded is a plugin nobody installs twice.
* **Every blocking plugin has to agree.** One plugin's yes does not overrule
  another's no.
* **It is capped at ten seconds**, whatever the manifest asked for, because the
  thing on the other end is a person watching a page.

Delivery is not guaranteed and does not pretend to be: an event is posted once,
with a short timeout, and a plugin that must not miss anything reads the state
back through the API. A retry loop that looked like a guarantee and was not one
would be worse.

Still missing: the store, and the interface. A plugin is installed over the API
by pasting a manifest or giving its address.

## Phase 46 — the plugin standard, and the permission model it needed first

The ask was an ecosystem: other people writing features for Skifity, including
commercial ones, published to a store and installed from the panel. What that
needs before it needs a store is a **standard**, because the standard is the one
thing everybody else builds against and the one thing that cannot be changed
casually afterwards.

**The permission model had to come first, and it was not one.** API token scopes
were `read` and `write`, decided by the HTTP method — so a plugin that copies
backups and a plugin that provisions servers would carry the same token, and
installing the first would grant the second's powers. "This plugin may only read
your apps" would have been a sentence on a screen that nothing enforced. Scopes
now name a resource as well as a direction, `apps:read`, `backups:write`, over
twelve resources, checked in the one middleware rather than per handler — a scope
enforced per handler stops being enforced the day somebody adds a route and does
not think about it. A path no scope covers is refused to a scoped token rather
than falling into whichever scope was nearest, and the older unscoped forms keep
meaning exactly what they meant.

**The standard is `internal/plugins`, not prose.** A manifest that parses and
validates there is a valid plugin, and the example in `docs/plugins.md` is a
test: if it stops being valid, the standard changed and it was not on purpose.
See ADR-0018 for why a container rather than a library, with `plugin.Open`'s own
documentation quoted for why that door is closed.

Three rules in it are the ones somebody will try to relax:

* **The image is a digest, never a tag.** A tag can be moved by whoever controls
  the registry, and this image is about to be handed an API token.
* **`read` and `write` alone are refused for a plugin.** They mean every
  resource, which nobody can meaningfully agree to on a screen.
* **Only an event that happens before something may block**, capped at ten
  seconds, because the thing on the other end is a person watching a page.

And one trap avoided by writing the test: the subscription field is `event:` and
not `on:`. `on` is a boolean in YAML 1.1, so `on: backup.completed` parses in
some readers as the key `true` — the scar GitHub Actions carries in every
workflow file ever written. A standard being defined today should not step on
it, and the only reason this was caught is that the example manifest is
exercised rather than admired.

Nothing installs a plugin yet, and `docs/plugins.md` says so in the same words:
the runtime, the event delivery and the store are not written. What exists is
what a plugin *is*, so that one can be written against it.

## Phase 45 — an allowlist, and everything it took to make one real

Asked whether there was an allowlist by address, by network or by country.
There was none of any kind: `netguard` is outbound SSRF protection and
unrelated. So this is one, in the shape people already know from Cloudflare —
an ordered list of rules over the request, combined with and and or.

The expression is a tree rather than a string. Cloudflare writes theirs as a
small language and then has to parse it; the panel does not need one, because
the interface builds the tree directly and "match all of these" and "match any
of these" are groups on a form, not syntax anybody has to learn.

**Unknown is a third answer.** A rule about a country is worthless if nothing
knows the country, and both ways of pretending otherwise are wrong: as false a
Block rule silently stops blocking, as true an Allow rule silently blocks
everybody. So it is neither, and it propagates the way it does in SQL. A rule
that comes out unknown is skipped by name rather than deciding.

**The geo data needs no account.** DB-IP publish country and network databases
monthly under CC BY 4.0 with no sign-up, where GeoLite2 wants a registered
account before a single byte — a registration in the middle of turning on a
firewall rule. The decode tags were checked against the real files rather than
the documentation: 1.1.1.1 is AU and AS13335, 8.8.8.8 is US and AS15169.
Attribution is on the page and in `docs/firewall.md`.

**The guard is its own process, not the panel.** Traefik can ask an external
service whether to let a request through, and the panel is the obvious place and
the wrong one: its Deployment uses Recreate, because its database is a file on
one node's disk, so every panel upgrade would take every protected site down. It
is the same binary in another mode, with no database, no Kubernetes API access
and no credentials — its whole state is a ConfigMap the kubelet drops into its
filesystem.

**Three knobs were removed for being lies**, each caught by writing the test:

* a per-rule-set *fail open*, whose value lived inside the file the guard had
  failed to read, and which Traefik decides before this process is reached;
* `authRequestHeaders` on the middleware, a list of headers to forward, which
  would have made a rule on any header not in the list match nothing — the rule
  saved, the request allowed, the header never sent to the process judging it;
* "is a geo database available", inferred from whether a URL setting was empty
  — and empty means the default, so it was always available and the check meant
  nothing. It is an explicit switch now, on by default.

**And the file the whole feature is worth exactly as much as** is the one that
decides who is asking. `X-Forwarded-For` is walked from the right and stops at
the first entry that did not come from a proxy we trust, because only the
rightmost entry was added by somebody we know. Trust is a list of ranges rather
than a hop count, since a hop count is wrong the moment somebody adds a load
balancer and the failure is silent. Junk in the chain stops the walk rather than
being skipped. Cloudflare's headers are believed only when the peer handing them
over is one of ours — which also makes the country free behind the tunnel, with
no database consulted at all.

## Phase 44 — two things wanting the same name

Asked whether collisions, SSH and the shell were sound. SSH and the shell were.
Naming was not, in two places, and one of them was a way in.

**Two apps called "web" and only one address.** Every app is given an automatic
address worked out from its slug and its environment — and from nothing else. So
two projects each with an app called "web" in Production both wanted
`web.apps.example.com`. The hostname column is unique, so the second insert was
refused, and the deployer swallowed that as a log warning: the second app had no
address at all, and the page showed nothing explaining why. "web", "api" and
"app" are what people call things, so this was not a corner case but the second
project. Proved with a test before it was fixed.

The address is now chosen by asking who holds the readable name. Free, or this
app's own, and it keeps it; somebody else's, and it gets the same name with a
short suffix derived from the app's id. Derived, not random, because it must be
the same on every deploy. And an app that took the longer name keeps it when the
sibling holding the short one is deleted — otherwise a URL would move under the
user on an unrelated deploy. When there is genuinely no address to be had, the
deployment log says so, because a person looking at an app with no URL has no
reason to read the panel's own log and no way to reach it.

**An app could claim the panel's own hostname.** Nothing checked. Two Ingresses
with the same host in different namespaces is not an error Kubernetes reports:
the ingress controller picks one, and which one survives a restart is not
something anybody decided. A member of any team could point an app at the
panel's address and start receiving the requests a browser sends it, sign-in
cookie included. It is refused now, against both places the panel learns its own
address — the installer's environment variable and the Settings value — in every
shape somebody might type it. A subdomain of it is still somebody's own business
and still works.

**The confinement fix had not reached the run Job.** Phase 43 changed the
Deployment and not `BuildRunJob`, which `BuildCronJob` also renders from. So on
an environment lowered to run an image that starts as root, the app ran and a
migration in that same image was refused: a panel saying "your app runs, and you
cannot run anything in it". It uses the app's own confinement now.

**The port scan could not see a UDP socket.** The preflight checked 80, 443,
6443, 10250, 2379 and 2380 with `ss -lnt`, which lists TCP — and every port the
pod network uses is UDP. A VPS running a WireGuard VPN of its own holds exactly
the port flannel's wireguard-native backend wants, and the failure is a cluster
that comes up with every node Ready and no traffic between pods, which looks
like anything except a port conflict. 8472, 51820 and 51821 are checked now:
fatal for the backend this cluster actually uses, with the fix naming the other
one, and a note rather than a refusal for the backend it does not.

What was read and found sound: the SSH client (modern host key algorithms only,
trust on first use with the fingerprint stored and a later change refused rather
than warned about, a handshake deadline of its own, files written through a
quoted heredoc with a delimiter collision check), `shellsafe` and its use at
every interpolation in the generated scripts, `runsafe`, and the run Job's own
isolation — no service account token, no retries, its own labels so a migration
pod is never counted as an app instance.

## Phase 43 — the catalogue that could not start

The question was whether autoscaling, deploying and database replicas were
sound. Two of the three had defects, and one of them was the largest thing found
in this repository.

**Every app's pod was pinned to uid 1000.** `BuildDeployment` wrote
`runAsUser: 1000`, `runAsNonRoot: true` and `drop: ALL` into every pod spec,
including every one-click template, in namespaces enforcing the `restricted`
Pod Security profile. That is exactly right for an app Skifity builds — Railpack
and Nixpacks both produce a process running as 1000 — and it is a guess for
anybody else's image.

It was measured rather than assumed. Of the catalogue's 295 unique images, 124
had their config blob read straight from their registries before Docker Hub's
rate limit ended the run: **120 of the 124 do not run as uid 1000**, and 87 of
those declare no non-root user at all, which `restricted` refuses outright.
Separately, **51 of the catalogue's 334 services listen on a port below 1024** —
WordPress, Nextcloud, Vaultwarden, GitLab, MediaWiki, phpMyAdmin — and with
every capability dropped, no `CAP_NET_BIND_SERVICE`, and containerd leaving
`net.ipv4.ip_unprivileged_port_start` at the kernel default of 1024 where Docker
sets it to 0, not one of them could bind its port. The product's front page was
a catalogue that mostly could not start, and nothing anywhere said so.

The fix is three parts.

**The uid is pinned only for an image Skifity built.** For anybody else's, the
`USER` the image declares is the right answer, and leaving the field unset is
how you say that.

**A low port gets one safe sysctl.** `net.ipv4.ip_unprivileged_port_start: "0"`
is narrower than handing back `CAP_NET_BIND_SERVICE`: it lets this pod's
processes bind a low port in this pod's own network namespace and grants no
capability to anything. Kubernetes has called it safe since 1.22 — namespaced,
unable to affect another pod or the node — so no kubelet configuration is needed,
and it is on the list both the baseline and the restricted profile allow.

**The confinement level belongs to the environment**, defaulting to the strict
one, changed by an admin under the project. At the lower level a third-party
image may start as root and keeps the runtime's default capability set, because
an image that drops to its own user calls setuid and needs `CAP_SETUID` and
`CAP_SETGID` — dropping ALL from a root container breaks the very images the
level exists to run. Everything else stays refused at both levels: privileged
containers, host namespaces, host paths, capabilities beyond the runtime's set,
and gaining privileges the process did not start with. An app Skifity built is
held to the strict rules at either level. And a lowered namespace still carries
`audit` and `warn` at `restricted`, so lowering the bar does not also turn off
the measurement.

The kubelet's own words for the failure are `container has runAsNonRoot and
image will run as root`, which reads like a fault in the image. It is not, and
the panel now replaces it with the sentence that names the switch.

## Phase 42 — the front door, built

## The repository itself

`CONTRIBUTING.md` and a pull request template, which a repository this size
should have had from the start, and the README's documentation table now lists
all ten pages the panel serves rather than eight.

## Next tasks

1. `make verify-remote HOST=root@…` on a server somebody is willing to rebuild.
   It installs, deploys an application from Git, and works through the phases in
   `docs/checklist.md`. Measure idle memory while it is up. Everything below is
   worth less than this.
2. Tag a release. `docs/releasing.md`; everything it needs is in place, and what
   it waits on is the run above rather than a decision.
3. Phase 6: give somebody `docs/quick-start.md` and nothing else, and fill in
   `docs/walkthrough.md` while they use it.
4. Publish a plugin store at `plugins.skifity.com`: an `index.json`, its
   signature, and the public key in the documentation. The panel reads one
   already; nothing is there to read.

## Open issues

* **Nothing has ever run against a real cluster.** `test/smoke` holds exactly
  two scripts — the panel and the installer — and neither touches Kubernetes.
  This entry used to say the cluster smoke tests "exist and are reviewed, not
  executed", and `panel.sh` used to name four of them in its header. They were
  never written. Everything that talks to a cluster — `internal/cluster`,
  `internal/deploy`, `internal/dbsvc`, `internal/backup`, the parts of
  `internal/kube` beyond manifest generation — is checked against the objects it
  renders and against a fake clientset, never against an API server. That is the
  largest single gap in this repository, it is a consequence of ADR-0010, and
  writing scripts that could not be run here would have widened it rather than
  closed it.
* **The first release is v0.1.0**, cut before the cluster run that
  `docs/releasing.md` asks for, at the maintainer's request; the README says
  so beside the install command. The installer names it (`RELEASED_VERSION`),
  the release workflow refuses a tag that disagrees, and
  `release-dry-run.yml` builds the whole release, images included, whenever
  something it is made of changes. *Superseded: this entry used to say nothing
  had been released.*
* **The default branch is `main`.** The project lives at
  `github.com/Skifity/Skifity` (moved from `TegarTheGreat/Skifity`, which
  redirects). Nothing depends on the name: the installer reads its manifests
  from the release's own tag. *Superseded: this entry used to say the default
  branch was the one the project was built on.*
* **No plugin has ever actually run.** The manifest standard, the permission
  model, the rendered Kubernetes objects, the event delivery and the blocking
  verdicts are unit-tested. Whether a real plugin image starts in the namespace
  this renders, and whether an event reaches it over a real cluster network, has
  not been tried — ADR-0010 again. There is a page for it — installed plugins,
  a store catalogue, inspect before install, settings, uninstall — and no store
  for that page to read: nothing is published at `plugins.skifity.com`. All ten
  events the standard declares are sent as of Phase 68; before that, eight of
  them never were.
* **The firewall has never been through a live Traefik.** The rules engine, the
  address handling, the geo lookup, the guard's decisions and the rendered
  Kubernetes objects are unit-tested, and the country and network lookups were
  checked against the real DB-IP databases. Whether Traefik loads the middleware
  and forwards what the guard reads needs a cluster, which is ADR-0010 again.
* **The confinement levels have never been enforced by a real API server.**
  The rendered pod specs, the namespace labels and the rules that choose between
  them are unit-tested; whether the kubelet accepts the sysctl and whether a
  root image then starts needs a cluster, which is the same gap as everything
  else in ADR-0010.
* **The Cloudflare tunnel has never reached Cloudflare.** The manifests, the
  refusal without a token, the validator and the rollover on a changed token are
  unit-tested; connecting requires a real Cloudflare account, which this sandbox
  does not have. It is the same gap as everything else in ADR-0010, and it is
  named in `docs/adding-servers.md` where the instructions are.
* The interface test needs a Chromium. It uses one already on the machine when
  `CHROMIUM_PATH` is set, and CI installs its own.
* k3s's memory footprint is not measured; the panel's is, in
  `docs/performance.md`.
* There is no shell into a running instance, and this is a decision rather than
  a gap. A one-off command runs in the app's own image with the app's own
  variables, can be watched, leaves a log, and works when the app will not
  start — which is when people reach for a shell. See `docs/roadmap.md`.
* A volume backup is a tar taken while the app runs, not a snapshot, so a file
  being written at that moment can be caught half-written. `docs/backups.md`
  says so and says what to do instead.
* The nixpacks builder is written and unit-tested and has never been run: like
  everything else that needs a cluster, it is checked against the rendered Job
  and not against a build. Railpack is the default and the one the product is
  designed around.
* The frontend is 509 kB of the panel's own code, 145 kB compressed, beside
  vendor chunks a browser keeps across upgrades. Served from the binary on the
  same host, so it is not the problem it would be over a CDN.

## Phase 71 — the plugin system did not have a job

Ten events and nothing else. A plugin could be told a deploy succeeded and, in
one case, refuse one. That was the whole standard.

It was tested the only way a plugin system can be, by trying to name a plugin
worth writing. Preview environments per pull request, uptime monitoring with a
status page, a mail service for every app, error tracking from the logs: every
one of them is a **core feature** of a platform-as-a-service, and not one of
them is a plugin. What the standard could express were small operational
conveniences — a deploy freeze, an automatic rollback, mirroring a backup. A
plugin system whose best ideas all belong in the panel does not have a job.

The cause is structural, and ADR-0021 records it: a plugin could add nothing to
the system and change nothing in it. It could only react, and a system that can
only react produces only small things.

### The other direction

A manifest now has `provides`. The panel owns the feature and the plugin owns
the vendor: Skifity knows what a notification is, when to send one and what goes
in it, and does not know what Slack is. The panel calls `/provide` on the
plugin's container, signed with the same key as an event, and **uses the
answer**.

Three judgements are the opposite of the event half's, because somebody is
waiting rather than nobody:

* A failure is returned rather than logged, so an alert that did not go out is
  a person who is told so.
* A plugin refusing is a different error from a plugin being unreachable, and
  they get different words.
* `200` with an empty body is a failure. Reading silence as success is how a
  channel quietly delivers nothing for a month.

The panel keeps each configured instance's settings, sealed with its own
keyring, and sends them with every call. A provider plugin has no database and
no credential of its own.

### Notification channels, all the way to the form

`internal/notify` was a closed switch of four: Telegram, Discord, a webhook,
email. Adding Slack meant editing the binary and cutting a release.

A kind a plugin provides is now validated, listed, chosen, filled in and
delivered. The list of kinds is read from the panel instead of being written
into the frontend, and the plugin's own form is drawn from its manifest — all
five field kinds, not just the two the built-ins use. Stopping at the API would
have been the same defect this repository has now found eight times: a channel
that can be stored and never picked.

A stored kind is `plugin:<plugin id>/<provider id>`, both halves, because two
plugins may each provide a `slack`. A channel whose plugin has been removed
stops working and says which plugin to look for; it does not go somewhere else.

### The gate, and why there is only one kind

A test reads the vocabulary of kinds and actions and the panel's own source, and
fails the build when one lists something the other never asks for. It was proved
by adding a `storage.bucket` kind wired to nothing: both halves of the gate
caught it.

So the list has one kind. The ones that are obviously missing — a DNS provider,
a backup destination, a git source, an identity provider — are each a real place
where the panel is hardcoded to one vendor or to none, and each is blocked on
the panel asking for it first. That order is the point. A kind that can be
declared and is never called is a plugin that installs, is approved, and waits
for a request nobody makes, with no way for its author to find out.

### Still true

No plugin has ever been run. What is proved is the manifest, the validation, the
signature, the call, the resolution and the seam — by tests, against a fake
plugin server, not against a container in a cluster. See ADR-0010.

## Phase 72 — an audit of the parts nothing had swept

Eleven faults, in the packages no earlier phase had read end to end, and a
note on what was read and found sound.

### A scheduled command could break the app it belongs to

Three faults in one path, and the worst of them was structural. The panel
validated a schedule with its own parser and handed the raw string to
Kubernetes, which parses it with a different one. Cron has two spellings for
Sunday and this parser accepts both, so `0 3 * * 7` — an ordinary way to write
it — was accepted, stored, shown with a next-run time the panel worked out, and
refused by the API server.

That refusal then failed the whole deployment, *after* the app's own objects had
been applied: the panel recorded as failed a deployment the cluster was busy
completing, and one schedule the API server would not take blocked every future
deploy of that app for ever. A refused command is now a line in the deployment
log and the app still goes out.

`NextRun` scanned one year, so `0 0 29 2 *` was reported as a schedule that
never fires.

### A half-finished firewall rule blocked everybody

Deleting the last condition out of an "all of" group sends `{"all":[]}`. The
engine read that as "no conditions", which means match everything — so a block
rule somebody was halfway through writing matched every request, locking them
out of the app or out of this panel, which the same engine protects. Nothing on
the screen said so.

The package comment claimed "an empty Any is false". It was not, and it could
not be: after a round trip through the stored JSON an empty Any and an empty All
are the same value.

### One header chose which firewall rules applied

`internal/edgerules/clientip.go` opens by naming the two ways to get the
client's address wrong, and then the address was the only thing guarded.
`X-Forwarded-Host` selects the rule set, and a hostname with no rules is allowed
through — so one header naming a hostname nobody protects, from any client,
left no rules to fail. `X-Forwarded-Uri` and `X-Forwarded-Method` were the same
shape.

A header can also arrive twice, and `Get` answers with the first — the client's,
with the proxy's line behind it unread.

### Six goroutines could end the whole panel

`internal/runsafe` exists because a panic in any goroutine ends the process, and
its package comment listed the goroutines that had been fixed. The list was
written by hand and six were missing: an app's own log output being scrubbed on
its way to a browser, the fan-out to every open tab, an event posted to somebody
else's plugin container, the two SSH readers, the guard, and the panel's own
listener.

Three of them could not simply recover — a goroutine whose job is to send on a
channel turns a crash into a wait that never ends — so those send the failure
instead. The list is now computed by a test rather than remembered, and it found
the last two by itself.

### Upgrading a password hash wrote eleven columns

Signing in upgrades a hash made with older parameters, and did it with
`UpdateUser`, which writes the whole row from whatever the caller last read —
a copy read before the password had even been checked. Anything changed in
between was put back, including whether the account is disabled. The write's
error was discarded too, so a rehash that never landed looked exactly like one
that did.

### The image sweep half-read a reference it could not read

`RepoAndTag` split on the last colon, and a digest reference has a colon of its
own: `ns/app@sha256:abc` read as the repository `ns/app@sha256`. Latent rather
than live — what it invents speaks for a repository the registry does not have —
but it is a violated contract in the one place that decides which images are
deleted, and the failure it sets up is an image removed while an app is still
running it.

### Building from a private repository was broken outright

The clone script built the credential into a variable and expanded it unquoted,
under a comment saying it held "the two -c words this script built itself". It
held four: the header's value is `Authorization: Basic <token>`, which has two
spaces in it, so git was handed a word of the header where it expects a
subcommand. Every build from a private repository failed, with an error that
says nothing about credentials.

The test covering that line asserted the broken text verbatim, which is how it
survived. Reading a shell script can only confirm its text.

### A dump that produced nothing was uploaded as a backup

The check that a backup was worth keeping happened after gzip, and compressing
an empty file gives about twenty bytes — which every "is this file non-empty"
test accepts. A dump tool that produced nothing while reporting success left a
backup that was uploaded, recorded, listed as available, and found to be
worthless on the day somebody needed it.

### A corrupt backup half-restored and reported success

The restore was `gzip -dc backup.gz | psql`. `set -e` reads only the last
command in a pipeline, so psql succeeded on whatever little reached it. With a
dump taken `--clean --if-exists`, part of a restore is tables dropped and not
put back: data lost, reported as success, by the one operation somebody runs
precisely because they cannot afford to lose any.

The backup side spells this exact rule out in a comment and avoids the
pipeline. The restore side did not follow it.

### Nothing was checking any of the shell

That is the thread through the last three. The panel writes shell programs and
runs them in other people's clusters, and no linter had ever read one — while
the clone script carried `# shellcheck disable=SC2086`, silencing precisely the
warning that names its bug, for a tool that was not installed anywhere in the
repository or in CI.

`make lint-shell` now checks the scripts that are files, and CI installs
shellcheck and may not skip it. `internal/shellgen` checks the twenty-five
scripts that are not files, by rendering every job through its exported builder
and reading the scripts back out of the containers.

Its threshold is `style`, which is everything, and that is not fussiness: SC2086
is reported at `info`, so a threshold of `warning` — which sounds like the
sensible middle, and was the first thing written here — lets through the one
finding the check exists for. That was not reasoned out; it was found by putting
the broken script back and watching the check pass.

### Gitea's signature was checked against the secret itself

`X-Gitea-Signature` is an HMAC-SHA256 of the body in hex. It was checked with
the function that compares a header against the webhook secret — a secret
against a digest, two values that can never be equal — so a push from a Gitea
that sends only that header was always rejected, and the message sent the
operator to check a secret that was correct.

Which rule verified a push was also chosen by a header on the request, and the
sender chooses the headers. No bypass followed, because every path still needs
the secret, but the rule that applies to a connection should not be selectable
by the person being checked. It comes from the connection's own kind now.

### What was read and found sound

An audit that only lists faults reads as if everything else was skipped. These
were read as closely as the rest and are right:

* **The plugin store.** The index is verified before it is parsed, an empty key
  means the catalogue comes back marked unsigned rather than silently trusted,
  and the interface says which it was in two places. The promise is kept end to
  end.
* **Fork detection.** Whether a preview environment is handed the project's
  secrets turns on it, and both parsers fail safe: anything missing counts as a
  fork. The GitLab side had no test for that case, which is the shape somebody
  probing would send, so it has one now.
* **The MCP server.** It goes through the API rather than the store, so
  authorization is where it should be, and it exposes nothing destructive — no
  delete, no server removal. A deliberate line, and the right one.
* **Scale to zero.** The HPA stands down when KEDA is in charge, the interceptor
  Service names the port the interceptor actually listens on, and the target
  port matches the app's own Service.
* **The geo databases.** Bounded download, atomic rename, no archive extraction,
  and the only names it writes are this panel's own.
* **The template catalogue.** 283 templates behind ten gates, including one that
  checks the catalogue is what the README says ships. Phase 20.2's claim holds.
* **The export.** Secret values are left out and the rendered Kubernetes objects
  do not include the environment Secret, so the note at the top of the file is
  true.

### Left alone on purpose

An app that flaps — a readiness probe on the edge — gets a notification each
time it crosses, both ways, once a minute. The state in the database stops a
repeated *same* state being sent twice, which is what it was built for, but
there is no hysteresis. The result is a channel somebody mutes, and a muted
channel is the failure notifications exist to prevent.

It is not changed here because how many minutes of quiet make a recovery real
is a product decision, not a bug fix, and guessing at it would replace a known
gap with an unknown one. Written down rather than silently patched.

### How they were found

Three habits, no cleverness.

Reading each unswept package against its own comments, which is where five of
the ten announced themselves: the code did not do what the paragraph above it
said. The restore pipeline is the clearest — the rule it breaks is written out,
in full, forty lines higher up.

Running things instead of reading them. A shell script's behaviour is not in its
text: the clone and the restore both read correctly and both were wrong. They
are tested now by running them against a stub `git` and a stub `psql` and
reading what those were actually given.

And source-scanning tests, which found two faults nobody was looking for.

Every fix was proved by breaking it again and watching the test name the
failure. That is also how the shellcheck threshold was found to be wrong.

## Phase 73 — somebody who has never deployed anything

The question this phase started from: somebody built an app with an assistant,
and has never deployed anything. What happens when they try? Two answers, and
neither was good.

### The first deploy crashed on a setting nothing had set

Detection said "Next.js" and nothing about what the app would reach for. An app
that uses PostgreSQL started, read an empty `DATABASE_URL` and crashed, and
making a database, linking it and deploying again were three screens in an
order nobody new would guess. Data kept in SQLite or a JSON file worked — for an
hour, until the next deploy replaced the container and the file with it,
silently.

Detection now reads what the app needs as well as how to build it
(`internal/builder/needs.go`): database drivers per ecosystem, a Prisma schema's
provider and the variable it reads, Django's default SQLite, Laravel's
`DB_CONNECTION`, a committed `.sqlite` file, and the names in `.env.example`.
Each finding carries the package and the file it came from, so the form can say
*why*. The form ticks the databases the panel can make, and creating the app
creates and links them **before** the first deploy, so the first start finds
them. A database it cannot make (MongoDB) is named rather than dropped, and data
in a file is a warning in amber. A test checks the panel's list of engines it
can make against `internal/dbsvc`, so the form never offers one it cannot.

The real `.env` is never read from a repository — a test holds that line — and a
pasted one goes through the same rule as every variable. That rule changed too:
saying nothing about whether a value is secret used to mean "not a secret", so
a pasted `STRIPE_SECRET_KEY` was stored in the clear. Silence now means "decide
from the key and the value", with the same `logging.LooksSecret` the log
redaction uses.

### There was no way in without a repository

Every way into the panel started with a repository URL, and the app is a folder.
`skifity up` deploys the folder: it reads it with the same detector, creates
the app the first time with its databases made and linked, offers to set the
`.env`'s values as variables (never sending the file), sends the folder, deploys
and prints the address. `skifity.toml` links the folder to the app, and running
it again only sends and deploys. The MCP server has the same thing as
`deploy_folder`, which tells an assistant to ask before sending somebody's
`.env`.

What is sent follows `.gitignore`, read the way git reads it, per directory,
with negation; `.skifityignore` adds to it. `.git`, `node_modules`, every real
`.env` and the project file are left out whatever an ignore file says. The
archive is deterministic — sorted, fixed times — because its hash is the
deployment's "commit" and part of the build fingerprint (ADR-0007): a file that
was only touched must not rebuild. Run from a subfolder, it sends the whole app
rather than replacing the app with the corner the terminal was in.

On the panel, `internal/upload` refuses everything unsafe to unpack before a
byte is stored: absolute paths, `..`, links, device files, a `.env`, and bounds
on entries and on unpacked size. A refusal says which rule, in the reader's own
language, rather than one English sentence passed through. Uploads live beside
the panel's database, ten per app, removed with the app, and swept hourly for
apps that went with a deleted project in one cascade.

The build cannot fetch the code — the build namespace may not reach the panel,
on purpose — so the panel brings it (ADR-0022): an upload build's first
container waits, and the panel execs into it and streams the archive to `tar`.
Its readiness is read from the init container's own state, because a pod stays
`Pending` while an init container runs, and waiting for `Running` would be two
things waiting for each other.

`apps.source_type` had a `CHECK` naming three sources, and SQLite cannot change
one in place. The migrator learned SQLite's documented table rebuild, with
foreign keys off on one connection around the transaction — without which
dropping the old table cascades into every deployment, variable and domain,
inside a migration that reports success. A test puts rows into the old schema,
migrates, and checks they are all there and that the cascades work again after.
It was proved by running the rebuild with foreign keys on and watching the
deployments disappear.

### Without a terminal either

A CLI still assumes a terminal, and the panel only serves a Linux binary. So the
new-app form has a third source, **A folder on my computer**: the browser packs
the folder itself — a tar written in TypeScript, gzipped with the browser's own
`CompressionStream` — and the panel detects from the archive
(`POST /api/teams/{team}/detect-upload`, nothing stored) and shows the same
detection, databases and settings a repository gets. The folder's `.env` is read
into the settings box, where it can be seen and changed before anything is sent,
and goes as variables. The app page of an uploaded app has **Send a new
version**.

What is sent is decided twice — Go for the CLI, TypeScript for the browser — so
both are held to one fixture, `internal/cli/testdata/ignore-cases.json`: the Go
test and `npm run check:ignore` read it, `make check` and CI run both. The
third case's expected answer was checked against `git add --dry-run` rather
than written from memory. The browser's archive was checked by feeding it to
the panel's own Go reader: long names, non-ASCII names and the executable bit
come through. A browser cannot see an executable bit, so `gradlew`, `mvnw`,
`*.sh` and `bin/` are given one.

The one browser test gained a step: a real folder picked in Chromium, the counts
shown, the `.env` offered and not uploaded, the app created, its variable stored
secret, and its deploy naming the upload's hash. Writing it found the one bug a
reading would not have: the picker cleared its input straight after handing
over the `FileList`, which is live, so every folder arrived empty.

### A CLI for the computer people actually have

The panel handed out the binary it was running, which is Linux. The Mac or
Windows laptop most people deploy from got a file that would not run. The image
now builds the other five platforms beside the panel's own (from the same
commit, gzipped, about 90 MB together), `/api/cli/download?os=&arch=` serves
them — the two values checked against a closed list before either touches a
path — and `/api/meta` says which it has. The new-app page shows the commands
for the visitor's own platform, with a selector. The Dockerfile's build loop was
run as written, with `sh`, since there is no Docker here: the host's platform is
skipped and the macOS build comes out a Mach-O binary.

Not done: `Dockerfile.release`, the image GoReleaser packages, still carries
only its own binary. Wiring GoReleaser's cross-builds into that context has
never been run, and the file says so rather than copying a path nobody checked.
A panel from that image answers `cli.platform_unavailable`, which names what it
has and says the browser path needs no CLI at all.

A database created with an app now owns the variable it is linked as: a pasted
`DATABASE_URL` pointing at somebody's `localhost` is left out, by the API
rather than by each client, and the form says so.

### Found on the way

`test/cluster/verify.sh` read the created app's id with `pick id`, and the
answer is `{"app": {...}}`. `make verify` would have died at its first step,
and the check that a failing app sends a notification silently skipped itself.
It has never been run, which is why nobody knew; it now also deploys a folder,
so the exec delivery is exercised there the next time it is.

A CLI error said "run `skifity link`", a command that does not exist.

### CI had been red since the day the frontend tests changed

The Go job in CI runs `go test ./...` without building the frontend, and two
tests in `web/` served the embedded build — which, in that job, is an empty
directory. They failed there on 28 of the last 40 pushes. `make check` passed
the whole time, because on a machine where the frontend had ever been built
the directory was full: the gate and CI disagreed about what they were testing,
which CLAUDE.md names as a gate with a hole in it. The handler now takes the
file system it serves, and the tests hand it a small built frontend of their
own, so they check the same thing with or without `npm run build`.

It was found because every Dependabot pull request showed a red cross, the
two-line prettier bump included — the one thing all of them had in common was
the branch underneath.

### A home of its own

The repository moved to the Skifity organisation, which the project did not own
when `scripts/check-home.sh` was written to forbid `skifity/skifity` for that
reason. It does now, so the rule is turned round: `Skifity/Skifity` and
`ghcr.io/skifity/skifity` are the home, `PROJECT_REPO` in the installer and the
Makefile say so, and the old owner's name is what the check refuses — a
redirect is a courtesy that ends the day anything is created at the old name,
and an install that went through it would end with it. The move happened before
the first release, so nothing published under the old name has to be chased.

### Not executed

The exec stream into a build pod needs an API server. Both scripts at either
end are run against each other in tests (delivery, a stream cut halfway, a
build nobody delivers to), the pod-state logic is tested from pod statuses, and
the smoke test drives `skifity up` against the real binary up to the build.
The build itself from an upload is phase 1 of `make verify`, and has not run.

## Phase 74 — the pull request never heard back

Preview environments were built for every pull request, and the only way to
find one was to open the panel and look for an environment named after it. The
reviewer — the person who most needs the address and the one least likely to
have an account — had nothing. Vercel, Netlify, Coolify and Dokploy all answer
this the same way, and the competitor research (`docs/research/competitors/`)
named it for every one of them.

Every deploy of a commit from a connected repository now reports back:

* **A commit status** on GitHub, GitLab and Gitea — pending when the deploy
  starts, then success or failure — named `skifity/<environment>/<app>`, and
  `skifity/preview/<app>` for every preview so branch protection can require
  one. Its link is the live app when it worked and the deployment's log when it
  did not.
* **One comment per app on a preview's pull request**, with the address, the
  commit and the log, edited in place on every push and found again by a hidden
  marker. A comment per push is the reason people mute deployment bots.

It is never allowed to change the outcome: the report is sent after the
database already says what happened, with a fifteen-second limit, and a token
that cannot write statuses gets one line in the deployment's log instead of a
failure. The team's token only goes to the host its connection is for — the
same rule as the clone, with a test that fails when the check is removed. A
rollback is not reported, and neither is a plain Git connection with no API.

`internal/gitsrc/report.go` speaks the three hosts' dialects and is tested
against a fake host for each; `internal/deploy/gitreport.go` decides what is
reported and is tested with a recorder in place of the host.

### Not executed

No real GitHub, GitLab or Gitea has been sent a status or a comment. The request
shapes follow each host's documented API and are checked against fakes, which
is not the same as the host accepting them.

## Phase 75 — a password in front of an app

Every product in the competitor research has some way to keep a site out of
sight that is not an address rule: Vercel's deployment protection, Netlify's
password, Coolify's and Dokploy's basic auth, CapRover's HTTP auth. Skifity had
the firewall, which is the wrong tool for a client on a phone or a reviewer
whose address changes.

The app's **Firewall** tab now has **Password**: a username and a password, and
everyone who opens the app is asked for them first. Traefik's own basicAuth, so
nothing has to be installed.

* **Only a bcrypt hash is kept**, in htpasswd's `$2y$` form, in its own table
  (`app_passwords`) and in a Secret in the app's namespace. The API never sends
  the hash or the password back, and the Playwright test checks both the page
  and the response.
* **After the redirect to HTTPS, never before it**, so a browser does not answer
  the prompt over plain HTTP one request before it had to. The order — firewall,
  redirect, password — is a test.
* **`removeHeader`**, so the app never sees the password and an app that logs
  its headers does not write it down.
* **Previews inherit it.** A pull request's preview of a locked staging site is
  locked with the same password; only the hash is copied, so a fork learns
  nothing.
* **Admin only**, like the firewall, and the plain-HTTP addresses it guards are
  named on the tab, because the free sslip.io address is one of them.
* **Work factor 6, not 10.** Traefik checks the hash on every request, so the
  cost is paid per image and script, not per visit.

### Not executed

No Traefik has loaded the middleware or asked a browser for the password. The
rendered objects, the middleware order, the stored hash (checked with bcrypt
against the password and a wrong one), the API, the preview copy and the panel
form are all tested; the prompt itself needs a cluster.

## Phase 76 — every preview pointed at production's database

Found by three of the competitor research passes independently, each while
checking whether Skifity's previews match Railway's, Render's or Vercel's. A
preview copied every variable of the app it previewed, and a database link is a
variable: the pull request's copy got production's `DATABASE_URL`, an address in
production's namespace.

Where the environment's default-deny NetworkPolicy is enforced — k3s enforces it
— that address is refused, so a preview of any app with a database could never
connect, and one with a release command failed its first deploy. Where it is not
enforced, a branch's migration would have run against production. Neither had
been seen, because nothing here has run on a cluster.

A preview now gets **a database of its own** for each one the app is linked to:
same engine and version, empty, one instance, 1 GB, in the preview's namespace,
linked under the same variable, and removed with the preview's environment. The
linked variable is never copied, so a database that cannot be made leaves the
preview without the variable rather than with production's. A preview from a
fork gets none: anybody can open one, and a database per pull request is a way
for a stranger to fill a server.

The test that checks production's address never reaches a preview was confirmed
to fail with the old copy put back.

## Phase 77 — single sign-on found accounts by their address

Found by the Coolify research pass, which was reading CVE-2026-86117 — Coolify's
OAuth sign-in matched existing accounts by email alone — and checked whether
Skifity did the same. It did. Every single sign-on looked the account up by the
address in the ID token, and a token with no `email_verified` claim at all was
accepted as if the provider had vouched for it. Entra ID sends no such claim and
lets a user's address be set, which is the 2023 "nOAuth" class; Grafana's
CVE-2023-3128 is the same bug. Whoever could make an identity at the provider
carrying the owner's address could sign in as the owner — password, two-factor
and all bypassed.

Now:

* **A returning person is matched on the provider's issuer and subject**,
  recorded in `user_identities` the first time. An address change at the
  provider no longer matters, and a different identity claiming a linked
  account's address gets its own account or nothing.
* **An address finds an existing account only when that is safe**: the account
  has no password (it only ever existed through single sign-on, and this keeps
  everybody who signed in before identities were recorded working), or the
  provider explicitly said `email_verified: true`. Silence is not true any more.
* **Everything else is refused** with a message, and the person links the
  provider from **Account → Single sign-on** after signing in with their
  password. Linking is behind the same re-authentication as two-factor and API
  tokens, and the browser that comes back from the provider has to be signed in
  as the account that asked.
* **Unlinking** is refused for an account with no password, which would have no
  way in left.

The test that plays the attack — an identity with the owner's address and no
verification claim — was confirmed to fail with the old rule put back.

### Not executed

No real provider has been signed in through. The claim handling, the account
decision, the linking flow and the endpoints are tested; the round trip to Okta,
Entra or Keycloak is not.

## Phase 78 — a fork's preview was handed the project's shared secrets

Found by the Northflank research pass. A preview of a pull request from a fork
never copied the app's own secret variables — `copyPreviewVariables` has drawn
that line since previews existed — and was then handed every one of the
project's shared variables, secret or not, at every deploy: `runtimeVariables`
merges them into whatever environment is being deployed, and nothing recorded
that this environment came from a fork. A stranger's first commit could have
printed them.

The preview environment now records `from_fork` when it is made (migration
0019), and the deployer leaves out every shared variable marked secret for such
an environment, while still passing the plain ones. Everything that runs an
app's code — the deploy, a scheduled command, a one-off run — reads its
variables through the same function, so there is one place for the rule.

A store test that seeded an old schema through today's repository functions
broke on the new column, and now writes that one row by hand: the test is about
a migration from before the column existed.

## Phase 79 — build variables were in the Job, and never reached Railpack

Found by the Netlify and Qovery research passes, each checking how Skifity hands
a build its secrets. Two defects in the same place:

* **Every build variable's value was written into the build's Job** — on the
  `railpack prepare` and `nixpacks build` command lines and as plain
  `build-arg:` options to buildctl. Anybody who can list Jobs in the build
  namespace read them, and a Dockerfile build wrote them into the image's
  history.
* **Railpack, the default builder, was never given them.** Its documentation
  (railpack.com, "Running Railpack in production", read 2026-09-30) says
  `prepare --env` puts the names into the plan and not the values, and the
  values reach the build as `--secret id=NAME,env=NAME`. buildctl was called
  with no `--secret` at all, so a Next.js app's build-time address never
  reached `npm run build`.

The values now go into a Secret of their own for as long as the build runs,
owned by its Job so the cluster collects it if the panel stops, and deleted when
the build ends. Every build step reads them from it under a
`SKIFITY_BUILD_VAR_` prefix, so a variable named `PATH` or `BUILDKIT_HOST` cannot
change how the tools run, and the scripts refer to `${…}` rather than to the
value. Railpack gets a `--secret` per variable and a `secrets-hash` so a changed
value invalidates the cached steps that used it. A Dockerfile gets both a build
argument and a secret. A front end built by the static builder — which never saw
its variables either — now declares each in its build stage.

Found on the way: one BuildKit serves every team, and Railpack's mount caches
are shared across builds unless given a prefix. Each app's build now passes
`cache-key=<app id>`, so one team's build cannot write a package cache another
team's reads.

A test renders a build for every builder and fails if any value appears
anywhere in the Job.

### Not executed

No build has run with this. The rendered Job, the scripts (checked with `sh
-n`) and the flags are tested; whether the Railpack frontend mounts every
secret the way its documentation says needs a cluster.

## Phase 80 — the build images, pinned, and two that did not exist

The Dokploy research pass noted that every image a build runs defaulted to a
floating tag — `alpine/git:latest`, `moby/buildkit:master`,
`ghcr.io/railwayapp/railpack:latest`, `…/railpack-frontend:latest`,
`ghcr.io/railwayapp/nixpacks:latest`. Pinning them meant reading each one from
its registry, and two of them were not what the code assumed:

* **`ghcr.io/railwayapp/railpack` is not a public image.** The registry refuses
  an anonymous pull token for it, as it does for a package that does not exist.
  Every Railpack build — the default builder — would have stopped at pulling
  its first step. What Railway publishes is `railpack-frontend`, which is
  Alpine with the complete `railpack` binary at `/railpack`, `prepare`
  included. The plan step now runs there, as `/railpack prepare`, which also
  means the program that writes the plan and the frontend that reads it are
  the same version, as they have to be.
* **`ghcr.io/railwayapp/nixpacks` is Nixpacks' base image** — Ubuntu and Nix —
  with no `nixpacks` command in it, so the Nixpacks builder could never have
  run. Nobody publishes the command as an image, its makers put it in
  maintenance mode in 2025 and replaced it with Railpack, and the panel could not
  verify a release to download from here. It is no longer offered: an app set to
  it is refused with `build.nixpacks_unavailable`, saying what to pick instead,
  and a panel whose default was Nixpacks builds with Railpack.

Every build image is now pinned by version and by digest, in one file
(`internal/builder/images.go`), with the digests read from the registries on
2026-09-30: Railpack v0.40.1, BuildKit v0.33.0 for both buildctl and the shared
daemon — which had been v0.18.2 while buildctl followed `master` — and
`alpine/git` v2.54.0. A test fails on any build image without a digest or on a
floating tag.

### Still true

A panel that already installed the BuildKit component keeps the daemon it
installed: components are installed once and never revisited, which is the
upgrade gap the Kubero research named. New installs get v0.33.0.

## Phase 81 — what an app can read about itself

The Dokploy research pass found that every app was given `Skifity_APP` and
`Skifity_ENVIRONMENT` — the product's display name glued to a suffix, in mixed
case, mentioned by no documentation — and nothing else about itself. Every
hosted platform in the research gives an app its own address and commit:
Vercel's `VERCEL_URL`, Render's `RENDER_EXTERNAL_URL` and `IS_PULL_REQUEST`,
Railway's `RAILWAY_PUBLIC_DOMAIN`.

Now `SKIFITY_APP`, `SKIFITY_ENVIRONMENT`, `SKIFITY_URL`, `SKIFITY_COMMIT_SHA`,
and in a preview `SKIFITY_PREVIEW` and `SKIFITY_PULL_REQUEST`. `SKIFITY_URL`
matters most for previews: their address changes with every pull request, and a
sign-in callback or a link in an email has nowhere else to learn it. The commit
is only set for an app built from a repository, since an uploaded folder's
deployment carries the upload's hash in the same field. Documented in
`docs/concepts.md` and `llms.txt`.

## Phase 82 — a server added later could be newer than its cluster

Found by the Dokku research pass. A server joining an existing cluster installed
the pinned version if there was one and followed k3s's stable channel if there
was not — which by the time a second server is added months later can be a newer
Kubernetes than the control plane. A kubelet newer than its API server is outside
Kubernetes' version skew policy, and a second control plane on a different
version from the first is a half-upgraded cluster. The setting's own help told
operators to pin it to avoid exactly this, which is a workaround the panel
could do itself.

A join now installs the version the cluster reports, which k3s writes in the
same form the installer takes (`v1.34.1+k3s1`). The setting only chooses the
first server's version. A cluster that will not say which version it runs stops
the join with `provision.cluster_version_unknown` rather than guessing, and a
panel run outside the cluster it manages keeps the old behaviour. Both paths are
tested against the in-process SSH server, including that the cluster's version
wins over a newer pinned one.

## Phase 83 — the competitors, one at a time

Phase 21 re-researched the market against live sources and compared thirteen
products in one file. This pass researched twenty-two, one at a time and in
depth, each in its own file under `docs/research/competitors/` with the same nine
parts: what it runs on, a feature inventory in fourteen groups, what users love
and complain about, its security record, a table against Skifity in which every
Skifity claim names the file it was checked in, the gaps, what not to copy, and
dated sources. `docs/research/competitors.md` is now the summary: the index, what
the earlier passes got wrong, the category's security record, and the gaps still
open, ranked.

Two things came out of it that the earlier page could not have said. The
research reads each competitor's feature against Skifity's *code*, and that
turned up defects rather than only gaps — nine, fixed as Phases 74 to 82, three of
them security and three that the first real build would have hit. And several
claims on the old page were wrong: other products do have MCP servers, Dokploy
has sixty security advisories, Dokku runs on k3s, and the footprint figure is the
panel's, not the stack's.

Firecrawl ran out of credits partway through and GitHub's API is blocked from
this sandbox; the files say which sources were read another way, and which
figures could not be confirmed.

## Phase 84 — the panel backed everything up except itself

Gap 2 of the research's ranked list, named by five of the twenty-two. The panel
backed up every database and volume it looked after to a bucket, on a schedule,
with retention and a notification when it failed — and its own database, which
holds every team, app, domain and variable, it copied only when somebody ran
`skifity admin backup-db` by hand, to a path on the same disk. That disk is the
first control plane server's, which is the one thing a panel backup is for
losing. And there was no command to put a copy back: the troubleshooting page
said to move a file into place, with nothing checking the file first.

Now the backup manager copies the panel's database to the backup bucket on its
own schedule — every day at 03:17 UTC unless the new **Back up this panel**
setting says otherwise, `off` to turn it off — and keeps fourteen unless **Panel
backups to keep** says otherwise. The copy is `db.Snapshot` (the same consistent
copy `backup-db` takes, safe while the panel runs), gzipped, uploaded by the
panel itself rather than a Job because the file is the panel's, under
`skifity/panel/<time>-<backup id>-panel.db.gz`. Retention runs only after a copy
succeeds. A copy that fails is recorded and sent to the channels of every team
an administrator belongs to, since notification channels belong to teams and
this is not one team's problem. With no bucket configured, nothing is recorded
and nobody is told: there is nowhere to put it yet. The master key is never
uploaded, and every page that mentions the backup says so.

`GET` and `POST /api/panel/backups` (administrators) list the copies and take
one now; the settings page has a card for both, the audit log records
`panel.backed_up`. `skifity admin restore-db <file>` takes a copy as it comes
out of the bucket, or a `backup-db` file, unpacks it beside the database, opens
it the way the panel will (which also migrates an older one), runs SQLite's
`integrity_check`, requires at least one account, and checks that a sealed
setting opens with the master key on this server. Without `--yes` it removes
the candidate and prints what it found and the three commands to run; with it,
the current database and its `-wal` and `-shm` are renamed aside, not deleted,
and the candidate renamed into place on the same filesystem. Anything it refuses
has an errdoc entry, `admin.restore_unreadable` or `admin.restore_not_a_panel`.

Tested: the upload against an in-process S3 that decodes the signed streaming
body and checks it is a gzipped SQLite database; retention keeping two of three;
a refused upload recorded and sent to the administrators' team; no storage
meaning no record; the schedule's default, a set one and `off`. For the command:
a restore that puts the backup's account in place and keeps the old database's;
no `--yes` changing no file; a file with no accounts, a truncated gzip, a file
that is not SQLite and a missing file each refused with nothing moved; and a
backup sealed with another key called out. Writing the plan also caught that it
named `deployment/skifity`; the panel's Deployment is `skifity-panel`.

Not executed: an upload to a real S3 service rather than the fake, and a restore
on a real panel's server.

## Phase 85 — a role that can look and not touch

The first half of gap 3. Eight of the twenty-two products have a read-only role
— Coolify, Fly, Portainer, Dokploy, Easypanel, Kubero, Canine, Netlify — and
Skifity had three roles, the lowest of which can deploy, delete apps and change
variables. The client who wants to watch their site's deployments had to be
given all of that or nothing.

`viewer` sits below `member`. The memberships table is rebuilt to let the CHECK
name it (migration 0020, through the same rebuild path as 0016, with foreign
keys off so the drop does not cascade). Every handler that only reads now asks
for `RoleViewer`; everything that changes something still asks for
`RoleMember` or more, unchanged. Reading a deployment and cancelling one shared
a helper that asked for one role, so it now takes the role.

The test is a third walk of the router, beside the anonymous one and the
cross-team one: as a viewer of the team that owns every id in the path, every
route that is not a GET must answer 403 (57 of them), and every GET must answer
something other than a refusal (37), except six named in `viewerMayNotRead`
with a reason each — the same six a member is refused. A second test says every
name in that list is still a route. The two routes that take their target in
the body are asked separately: installing a template is refused, and making an
API token is not — a viewer's token is checked against the viewer's role on
every request, so it reads and does nothing else, which the test proves by
reading and then failing to rename a project with it.

In the panel, the role is offered in the members form and on invitations, and
a viewer sees one notice at the top of every page saying so, rather than
finding out from the first refusal.

## Phase 86 — members limited to some projects

The second half of gap 3. A team is often one agency and several clients, or
one company and a contractor on one product, and a membership was the whole
team or nothing.

A membership now has a `scoped` flag and a list of projects in
`membership_projects` (migration 0021). The two are separate on purpose: with
only the list, no rows would have to mean the whole team, and deleting the last
project somebody was limited to would quietly give them every other one. With
the flag, they are left with none, and a test deletes the project to prove it.
Rows cascade away with the project and with the membership. Only members and
viewers can be limited; the API refuses it for admins and owners, and refuses
an empty list, and a project of another team. Invitations carry the limit to the
membership they become, and one transaction sets role and limit together, so
nobody being added with a limit has the whole team for a moment.

Enforcement is where every other check is. `authorizeApp`, `authorizeEnvironment`,
`authorizeDatabase` and `authorizeProject` now resolve the project as well as
the team and answer 404 for a project outside the limit — checked before the
role, because a role refusal is a 403 and a 403 says the thing is there, which
the first run of the new test caught on every admin-only route.
`authorizeTeam` refuses a limited member outright, so a team-wide route added
later refuses them too; the six that serve them call `authorizeTeamMember`
instead, and `limitedMemberMayUse` in the test names each with its reason. The
project list is filtered, the members list names only the projects the caller
can see in anybody's limits, and operations are visible when their target is in
reach. `authorizeAppID`, a second copy of `authorizeApp` for ids from a body,
still called `authorizeTeam` and would have refused a limited member their own
app; it is now `authorizeApp`.

The test walks the router as a member limited to one project, three ways: with
another project's ids (69 routes, all 404), with the team's own ids (25 refused,
six allowed and named), and with their own project's ids, where nothing may be
refused for being outside the limit — each route with a freshly made app and
database, because a member may delete an app and the walk does.

In the panel, the invitation form and a new **Change access** dialog offer the
limit for members and viewers, the members list says what each person can
reach, and a limited member's sidebar, dashboard and command palette leave out
servers and the cluster rather than showing refusals. Their app page does not
subscribe to the team's event stream, which carries every project's events, and
keeps up by polling. The invitation form also now does what its help text said:
somebody who already has an account is added directly instead of being refused
a link.

## Phase 87 — the MCP server, served by the panel

Gap 4, named by seven products. `skifity mcp` runs on the person's computer
and talks stdio, which needs the binary wherever the assistant runs; a hosted
assistant, a phone or a teammate's editor does not have it. And the fifteen
tools said nothing about what they do, so a careful client — where the
protocol's defaults are "not read-only" and "destructive" — asked before
`list_apps` exactly as before `rollback_app`.

The panel now serves the same server at `/api/mcp`, over streamable HTTP. It is
stateless: a session would be a second credential, bound to whoever opened it
and checked by nobody after that, so every request carries its token and is
authorized on its own. The tools are not reimplemented. The MCP server is still
a client of the API; for the endpoint, its client's transport is the panel's
own router, in process, so each tool call is the same API requests the CLI
would make, with the caller's token, through the same authentication and
authorization. A viewer's assistant reads and cannot change, a token limited to
one project reaches that project, and a token scoped to `read` reads — each
with a test that drives the endpoint with the SDK's own client. Token scopes
let `POST /api/mcp` through for that reason: it does nothing itself, and every
request behind it is checked against the scope.

The in-process requests get a fresh context that carries only the caller's
cancellation. The first version cloned the MCP request's context, and every
tool call came back 404: it carried chi's route for `/api/mcp`. It also carried
that request's signed-in user, which would have outlived a token that failed
on the inner request — a context is not a thing to hand from one request to
another.

`deploy_folder` is not offered over HTTP. It reads files on the computer the
server runs on, which for the endpoint is the panel's — its database, its
master key. A test holds that. A browser session is refused with
`mcp.token_required`: the endpoint wants a token, and a cookie is what another
site can make a browser send.

Every tool now carries annotations: a title, read-only for the `list_`, `get_`
and `check_` tools, and for the rest whether they can destroy something (a
deploy, a rollback, a command, an overwritten variable or instance count) and
whether repeating them changes anything more. None reach beyond the panel. The
test checks the annotations against the names, so a tool added later is held
to what it is called.

## Phase 88 — the channels people already read

Gap 14. Skifity sent to Telegram, Discord, a webhook or email; Coolify and
Dokploy both send to Slack, Mattermost, ntfy and Pushover as well, and a webhook
is not an answer for somebody who wants the message on their phone.

The four are built in now. Slack and Mattermost share one sender, because
Mattermost takes Slack's payload: a coloured attachment with the fields and a
link back to the panel. ntfy is published as JSON to the server's root rather
than as a body with headers, because a title in a header can only be ASCII and
an app called `café-api` is not; failures go at high priority and successes low.
Pushover is a form post with both keys, and nothing goes at its emergency
priority, which repeats until somebody acknowledges it. All four go through the
same netguard client as a webhook. The form validates what can be validated —
Slack's host, a Mattermost webhook's `/hooks/` path, an ntfy topic, Pushover's
30-character keys — and each sender has a test against a server that records
what it was sent.

The form's field labels for the built-in kinds were English written into the
component, which the rule against hardcoded strings should have caught and
could not, since they were data. They are translation keys now, with help text
for the fields that need it, in all five languages.

## Phase 89 — a .env in one rollout, and two queries that never ran

Gap 17, from Dokku's `config:set` taking several pairs at once. The panel had a
"paste a .env" box and the CLI took several `KEY=value` pairs, and both sent
one request per variable. Each request rolled the app out, so thirty lines were
thirty restarts, the first twenty-nine with half a configuration — and a line
that failed halfway left an app with a configuration nobody wrote. For a
project's shared variables it was worse: every line rolled out every app.

`POST /api/apps/{app}/variables/batch` and the same under a project take
`set` and `unset` lists, check the whole batch before writing any of it — a
key that is not one, a key given twice, a key both set and removed, a
build-time shared variable — store it in one transaction, roll out once, and
write one audit event naming the keys. Secretness is decided the same way as
for one variable. The panel's paste box and the CLI's `env set A=1 B=2` and
`env unset` use it, and `skifity env import .env` is new. The smoke test
imports a file.

Writing the test for shared variables found that they had never rolled out
anything. `ListAppsForProject` qualified `appColumns` with `prefixColumns`,
which split the list at every comma, and `appColumns` holds a `COALESCE` with a
comma in it: the SQL did not parse, and the handler logged nothing and moved on.
The other query built the same way was `ListDeployedApps`, which is what the
watcher reads every minute to notice an app that has stopped answering or a
certificate that will not issue. It had failed on every call since it was
written, so `app.unhealthy` and `certificate.failed` — two of the seven events a
notification channel can ask for — were never sent. It also scanned into a
hand-copied list of fields that had missed two columns added later.

`prefixColumns` now splits only at commas outside parentheses and qualifies
the column inside an expression; both queries scan through one shared list of
destinations; and a test runs every column list in the store, plain and
qualified, against the real schema. The watcher's own tests used a fake store,
which is how a query that never worked went unnoticed; the new store test is
the one that runs the SQL.

## Phase 90 — a security policy, and a way back from an upgrade

Gap 20, from Epinio's research: there was no `SECURITY.md`, no statement of
which versions get fixes, and no upgrade policy — before a first release, which
is when somebody installing it wants to know. `SECURITY.md` now says how to
report (GitHub's private vulnerability reporting), what happens and how fast,
that only the latest release is supported before 1.0 and what changes after,
and the trust boundaries the code defends, each pointing at the test or the
decision that holds it: a panel administrator is root on every server by
design, a team is isolated from every other, members, viewers and project
limits are walks of the router, apps and builds are untrusted.

Writing the upgrade policy down found that there was no way back from an
upgrade. The panel upgrades itself by changing its Deployment's image and
handed back `kubectl rollout undo` as the undo — but the new version migrates
the database when it starts, and nothing stopped the old version from then
opening a database a later version had migrated: reading tables whose rules it
did not know, writing rows without columns it had never heard of. And
`restore-db` checked a backup by opening it the way the panel does, which
migrates it, so restoring the copy taken before an upgrade with the upgraded
binary upgraded the copy again on the way in.

Three changes. The store refuses to open a database with a migration newer
than any it has, with `store.schema_newer` saying which version to run or which
copy to restore. An upgrade copies the database beside itself first —
`panel.db.before-upgrade-<time>-to-<version>`, the last three kept, never
touching a copy `restore-db` set aside — and to the bucket too when there is
one, and refuses to start if the local copy cannot be taken; its answer now
gives the four commands that actually go back, with the copy's path in them.
And `restore-db` reads a backup with `store.Inspect`, which opens it read-only
and never migrates it, and says when a backup is from a newer version. Each has
a test: a database from the future is refused and still inspectable; a
restored backup keeps the schema it had; four upgrades leave three copies and
the restore copy alone.

The upgrade is documented in `docs/configuration.md`, with the way back.

## Phase 91 — a team that requires more than a password

Half of gap 15, from Portainer, Epinio and Dokploy. Two-factor was something
each person chose; nothing let a team say that everybody in it had to have it.

A session now records how it was signed into — `password`, `totp` or `sso`
(migration 0022; sessions from before say nothing, which counts as a password
alone) — and a team has `require_strong_auth`. The check sits in
`membershipIn`, the one place every team, project, app and database
authorization passes through, and reads the flag in the same query as the
membership, so it costs nothing and cannot be forgotten by a handler. A session
passes when it came through the identity provider or its person has two-factor
on; a token, which cannot say how it was minted, passes when its owner has
two-factor on or a linked identity. A refusal is `auth.strong_auth_required`,
403 — not 404: the person is in the team, and being told so with the fix is the
point. `/me` carries `strong_auth`, so the panel shows one notice with a link to
Account rather than the same refusal on every card.

Turning it on is `PATCH /api/teams/{team}` with `require_strong_auth`, by an
admin or owner, from Settings → Members. Turning it on from a sign-in that
would not pass it answers `team.strong_auth_self` and changes nothing, because
the next request would have locked out whoever did it. Both directions are
audited.

Tested: a password-only session refused on the team's list and on an app in
it, `/me` still answering and saying why; the same session let in once
two-factor is on; an SSO session let in without it; a token let in once its
owner links an identity; and turning it on refused, then accepted, and audited.

The other half of gap 15 is Phase 92.

## Phase 92 — teams that follow the provider's groups

The rest of gap 15. An organisation that keeps people in groups at Okta, Entra,
Authentik or Keycloak had to add and remove the same person in two places, and
the one forgotten is the person who left.

Two settings: **Groups claim**, the ID token claim to read (`groups` when
empty), and **Groups to teams**, lines of `group = team:role` by team slug,
validated when saved. The claim is read from the verified ID token, as a list
or a lone string. At every single sign-on, before the session is issued, each
team the mappings name is brought in line: the highest role the person's
groups give, and out of the team when none do. A team not named is untouched.
A team's last owner is never removed or demoted by it. A member limited to some
projects keeps the limit when a group only moves them between member and
viewer, since the limit was somebody's decision and a group changing the role
does not undo it. A sign-in whose teams cannot be synced is refused rather than
let through on the previous groups, and each change is audited as
`team.member_synced` with the role it moved from and to.

Tested: the parser with comments, spacing, case and five malformed lines; the
claim read from a list, a string, nothing and a map; and the sync through three
sign-ins — admin from two groups, member from one, out from none — with a team
no mapping names and a mapped team that does not exist both left alone, three
audited changes, the project limit kept, and the last owner kept through both
an empty group list and a demotion.

Not executed: a sign-in against a real provider sending groups.

## Phase 93 — what a preview gets for each variable

Half of gap 8. Vercel, Netlify, Railway, Render and Coolify all let a value be
different in previews. Skifity copied every variable of the app into a pull
request's preview — the database link excepted since Phase 76 — so a branch got
the live payment key and the production mail settings, and a preview could
charge real cards or write to real customers.

A variable now says what previews get: the same value (the default), a value of
their own, or nothing (migration 0023). The preview's own value is sealed like
the variable, under a context of its own so the two ciphertexts cannot be
swapped, and it is listed with the other sealed columns so key rotation
rewraps it; the test that walks the schema for sealed columns holds that. A
secret's preview value is never shown back, as the secret is not.
`PUT /api/apps/{app}/variables/{key}/preview` sets it without rolling anything
out, since the app does not change. `copyPreviewVariables` reads it when a
preview is made; the fork rule still comes first, so a fork gets no secret
variable whatever its preview value.

The Variables tab marks a variable whose previews differ and sets it from a
dialog; an empty box for a secret's preview value is "not typed yet", so saving
it is disabled rather than blanking the value.

Tested through the preview webhook path: the preview gets the test key, not
the live one; does not get the variable marked none; gets the same value for
one left alone; the app's own value is untouched; an unknown variable is 404
and an unknown mode 400; and the list hides a secret's preview value while
showing the modes.

Still open from gap 8: previews of several apps together, with a seed step.

## Phase 94 — locked deploys, and a rollback that says what it does

Two thirds of gap 19, from Kamal and DigitalOcean.

**Locks.** `kamal lock` exists because a push during an incident, a manual
migration or a launch freeze should not ship. An app's deploys can now be
locked with a reason (migration 0024, a table of its own rather than columns on
`apps`, so nothing that writes an app can clear it by accident). The check is in
the deployer's `Deploy` and `Rollback`, which every deploy passes through — the
panel, the CLI, an assistant, a template, a webhook — and refuses with
`deploy.locked`, naming who locked it and why. A push to a locked app is listed
as skipped, with the reason, rather than failed. Variable changes and scaling
still roll out: they are not new code. The app's page has Lock and Unlock and a
notice while it holds; the CLI has `skifity lock "why"` and `skifity unlock`.

**Rollback plans.** A rollback was one click, and `docs/concepts.md` said it
put back the old variables and domains. It never did: `restoreRuntimeSpec` puts
back the image and the instance count, autoscaling, resources, port, health
check and start command, and leaves variables, domains and disks as they are.
The document now says what the code does. `RollbackChanges`, next to the spec
it reads (moved into `store` so the API can use it), lists each setting a
rollback would change, from what to what, and
`GET /api/apps/{app}/rollback/{deployment}/plan` returns it with the version
running now and the one it goes back to. The deployments tab opens that plan
in a dialog before anything happens, and names what is left alone.

Tested: every trigger of a deploy and a rollback refused while locked and
allowed once unlocked, in the deployer; the lock's endpoints, its reason
required, its appearance on the app and its audit events; a plan listing four
changed settings, and one that recorded nothing listing none; and the plan
endpoint naming the commit and what it leaves.

Still open from gap 19: a maintenance page served in front of an app.

## Phase 95 — what an app used, and saying when it is too much

Gap 1, the one twelve of the twenty-two products have: a history of what an app
used, and a warning before it falls over. The panel showed usage now and
nothing about an hour ago, and the first sign of an app running out of memory
was the notification that it had stopped answering.

The watcher already read every app's status once a minute — and since Phase 89
actually does. It now keeps what it read (migration 0025, `app_samples`, three
days): total CPU and memory, instances ready and wanted, restarts, and the
busiest instance's share of its own CPU and memory limits, which is what gets
an instance throttled or killed and what a total hides. Each app's status is
read once per pass now, where it was read only for running apps before.

Three thresholds per app (`app_alerts`; no row is the defaults): memory at 90%
of the limit for three minutes, restarts three times in ten minutes, and CPU,
off by default. `evaluate` is a pure function of the thresholds and the recent
samples, so the rules are tested without a cluster or a clock: a spike is not
news, three minutes are; two minutes of data are not three; zero is off; and
restarts are the sum of the increases, so a replaced instance counting from
zero again neither hides restarts nor makes negative ones. Crossing a threshold
is sent once, as the new `app.alert` event, and so is its end; which are firing
is stored, so a pass that finds the same thing says nothing. Thresholds are not
checked during a deploy.

`GET /api/apps/{app}/metrics?range=` returns up to 120 points for an hour, six,
a day or three, averaging usage in each bucket and keeping the peak — a minute
at the limit is the minute that matters. The Overview tab draws memory and CPU
with the threshold as a dashed line, in an SVG component of its own rather
than a charting library, and sets the thresholds.

Tested: the sample recorded from two instances, with the busiest one's shares;
a crossed threshold said once over two passes and its end said once after; the
rules above; the buckets for an hour and six hours with the peak kept; and the
thresholds' defaults, validation and audit.

Not executed: numbers from a real metrics-server. Still open from gap 1: the
same history for servers, and disk.

## Phase 96 — a push deploys only the apps it touched

Half of gap 18. In a monorepo every push to the branch rebuilt every app built
from the repository, including the ones whose code the push never touched —
minutes of build and a rollout for nothing, several times a day. Railway,
Render and Coolify let an app name the paths it watches, and Vercel skips a
project whose directory did not change.

An app now has watch paths (migration 0026, `apps.watch_paths`, one pattern per
line; empty is every push, which is what every existing app keeps). Patterns
start at the repository's root rather than the app's root directory, since
what an app is built from is often outside it; a plain path covers what is
under it, `*` stays in one directory, `**` crosses any number, `!` leaves
something out and a later line wins. A bare name does not match at any depth,
the one `.gitignore` rule left out on purpose. `internal/gitsrc/paths.go` is
the matcher; `..` and malformed patterns are refused when saved, with two new
errors.

The webhook parsers now read which files each push changed from GitHub's,
GitLab's and Gitea's commit lists, and say when that list cannot be believed:
a new branch, a GitHub force push, twenty or more commits (where hosts stop
listing them, or say there were more), and a commit that names no files —
`git commit --allow-empty` is how people ask for a redeploy. All of those
deploy. A skipped app is named in the webhook's answer, which the host keeps in
its delivery log. GitLab and Gitea do not mark a force push, so one that only
takes changes away from an app's paths is not seen; the docs say to redeploy.

The settings tab has the field while deploy on push is on, and offers the root
directory when there is one and no paths yet.

Tested: the matcher against the rules above; every malformed pattern refused;
each host's payload read into files, and each uncertain case read as unknown;
a push to a shared package deploying the two apps that watch it and naming the
one it skipped; an unknown push deploying all three; and saving, normalising
and refusing paths through the API.

Not executed: a real webhook from each host. The payload shapes are the
documented ones.

## Phase 97 — saying when a framework version is known to be dangerous

The other half of gap 18. When CVE-2025-55182 — remote code execution in React
Server Components, and so in every affected Next.js app — was published in
December 2025, Vercel and Netlify stopped deploying the affected versions
within a day. A panel on somebody's own server said nothing, then or at any
deploy since.

`internal/builder/advisories.go` holds a short list, checked against the
advisories themselves: Next.js CVE-2025-29927 and CVE-2025-55182, and React's
`react-server-dom-*` packages for the second. The version comes from the
lockfile — npm's, pnpm's (versions 5, 6 and 9), Yarn 1's and Berry's, Bun's
text one — read with patterns rather than parsed, because a lockfile read
through a Git host's API can be cut short and what arrived is still true; from
`package.json` only when it pins one exact version, since a range says nothing
about what was locked. Pre-releases are not checked. Lockfiles joined the
files detection reads, and the read limits went from 256 or 512 kB to 2 MB so
an ordinary lockfile fits.

It is said in the new-app form, by `skifity up`, and in the build log of every
deploy: before the build starts the panel reads `package.json` and the
lockfiles at the commit being built, with the app's own Git connection and a
ten-second limit (`TreeRequest.Read` fetches only those files), and writes a
`Warning:` line per advisory. Never a refusal, and a repository that cannot be
read adds nothing.

Tested: every edge of every affected range, and a canary skipped; each
lockfile format read, including neighbours like `next-auth` and `@next/env`
not mistaken for `next`; a range alone not taken for a version, and a
lockfile's fixed version overruling a pinned one; the deploy reading the right
commit with the right token and logging both advisories; an unreadable
repository, a fixed version and an upload adding nothing; and `skifity up`
saying it.

Not executed: a build log in a real cluster, and each Git host's API serving a
lockfile. The reads are the ones the new-app form already makes.

## Phase 98 — maintenance, answered by the guard

The last of gap 19. DigitalOcean, Kamal, Easypanel and Netlify can put an app into
maintenance; here the only ways to keep visitors out while somebody fixed data
by hand were to scale the app to nothing — a Traefik error page, and a cold
start afterwards — or to write a firewall rule that refused everybody with a
bare 403.

The firewall's guard already stands in front of a protected app as Traefik's
forward-auth, and whatever it answers other than a 2xx is what the visitor
gets, headers and body. So maintenance is one more answer there
(`internal/guard/maintenance.go`): a 503 with `Retry-After: 300` and a page
holding the team's message and nothing else — no English chrome, because the
visitor's language is not known and the team wrote the message in theirs. It is
decided after the rules, so a visitor the firewall refuses is refused rather
than told when the site is back. Listed addresses and ranges reach the app, so
the work can be checked first; an entry that does not parse allows nobody.

The app is not touched: no scaling, no redeploy, so ending it is immediate.
Migration 0027 (`app_maintenance`) holds the message, the allow list and who
started it. The guard's config gains `maintenance` per hostname, an app in it
gets the middleware whether or not it has rules, and its namespace gets the
middleware object. The guard is installed the first time an app is put into
maintenance, the way scale to zero installs KEDA; if it cannot be, nothing is
recorded, because maintenance nobody sees is not maintenance. An app with no
domain is refused.

`GET/PUT/DELETE /api/apps/{app}/maintenance` (viewer to read, member to change,
like a deploy lock), audited; the app's own answer carries it for a notice at
the top of its page; the Settings tab has the form and offers the caller's own
public address; `skifity maintenance on|off [--allow] [--allow-me]`.

Tested: the page, its status, header and escaping; the firewall still first;
an allowed address and range through, and an allow list belonging to its own
app; allow-list parsing; the guard config and the middleware for an app in
maintenance, alongside rules and after its end; and the API's validation, the
guard installed once, the message changed without losing who started it, the
audit, and nothing recorded when the guard cannot be installed.

Not executed: Traefik passing the guard's 503 page through to a browser. Its
forward-auth documentation says a non-2xx answer is returned as it is.

## Phase 99 — what a server used, and how full its disk is

The rest of gap 1. Phase 95 kept an app's history; a server's stayed "now",
and its disk was not shown at all — while a full disk is how a self-hosted
server usually dies, images and logs piling up until the kubelet evicts
everything on it.

The disk is read from the kubelet's own summary, through the API server's node
proxy (the panel is cluster-admin already), every node at once with five
seconds each: `kube.NodeDisks`. Used is capacity minus available, which is what
the kubelet measures its eviction threshold against, so the panel's percentage
is the one Kubernetes acts on. The summary is too heavy to ask for on every
page view, so only the watcher reads it, and the server page shows the
watcher's last reading, at most a minute old.

The watcher now keeps a minute of each ready server (migration 0028,
`server_samples`, three days: CPU, memory and disk against capacity, and
instances) and watches three thresholds (`server_alerts`: disk 85%, memory
90%, CPU off). `evaluateServer` is a pure function like the app one; a minute
whose disk could not be read is not a minute above the threshold. Crossing is
said once as the new `server.alert` event, with the disk in gigabytes, and so
is its end.

`GET /api/servers/{server}/usage` buckets to at most 120 points keeping the
highest, and leaves a bucket with no disk reading out of the disk line rather
than drawing an empty disk; `GET/PUT /api/servers/{server}/alerts` (admin to
change, audited). The server page has disk beside CPU and memory, and three
charts with the thresholds.

Tested: reading a kubelet summary, and refusing five malformed ones; the rules
above; a ready server sampled with its disk and a lost one not; a full disk
said once over two passes and its end said once; the buckets, the unread disk
left out, the latest disk; and the thresholds' defaults, validation and audit.

Not executed: a real kubelet's summary through the proxy. The fields read are
`node.fs.capacityBytes` and `node.fs.availableBytes` from its documented
`stats/summary`.

## Phase 100 — what a repository written for Heroku already says

Gap 9. Heroku's two files say most of what the new-app form asks, and nothing
read them past Railpack's use of the `web` line: a migration in the Procfile's
`release` line ran on every Heroku deploy and never here, and an `app.json`
that says "heroku-postgresql" in so many words still left the form guessing
from the drivers.

`internal/builder/heroku.go`: the Procfile's `web` line is the start command
(over a framework guess, never over a Dockerfile's `CMD`), `release` is the
release command whatever the confidence of the rest, and the other lines are
returned as `processes` and named, since an app runs one. `app.json`'s add-ons
become database needs under the variable Heroku sets — or the one its `as`
names — replacing a guess for the same engine rather than doubling it; its
`env` becomes `env_template`, a `.env` with the values filled in, 64 hex
characters generated wherever it says `"generator": "secret"`, descriptions as
comments, and the required ones without a value listed as missing rather than
set to ""; its `postdeploy` script is named with the `skifity run` that runs it.
`app.json` joined the files detection reads.

The form fills the release command and the settings box from these; `skifity
up` sends the release command and sets the template's values on the app it
creates, the folder's own `.env` winning where it is sent, and says the
Procfile's lines.

Tested: a Procfile read line by line; start, release and a named worker from a
Rails repository, and a Dockerfile's `CMD` left alone; app.json's add-ons, one
database for a driver and an add-on, the template's generated secret, values,
quoting, comments, optional and required settings, a name no variable can have
left out, and two detections giving two secrets; a broken app.json said and
ignored.

Not done: running a worker line from the same build as the web one. That is
gap 10.

## Phase 101 — a Compose file as a stack, and apps that are internal

Gap 6. The form read a Compose file and offered its services as a choice:
pick one, fill the form, come back for the next. For a web app, a worker, a
database and a cache that was four trips and a guess at which port each
listened on — and the promise the form made, that the services "reach each
other by name", was not true: an app's Service listened on 80 alone, so
`db:5432`, the address every Compose file writes, connected to nothing.

Three things, in order:

* **The Service listens on the app's own port too** (`kube.servicePorts`),
  beside 80 for the Ingress and KEDA. Pods in an environment may already reach
  each other on any port; the missing piece was the name.
* **Apps can be internal** (migration 0029, `apps.internal`): no automatic
  address, no Ingress — the deployer skips the domain and the spec drops the
  domains, which removes an Ingress the app had. The status answers with an
  internal address, `name:port`, and Settings has the switch.
* **`POST /api/environments/{env}/stack`** takes the services detection read
  and creates each as an app. Everything is planned and checked first — names
  against each other and the environment, a build with no repository, a service
  with nothing to run — and a failure creating one removes the ones before it.
  A build uses its context and Dockerfile, a `command` is the start command
  (a list is quoted word by word), the port is the published one else the
  exposed one else the one the image is known for, a service Compose does not
  publish is internal, named volumes become disks, bind mounts are named, and
  `${NAME:-default}` takes its default. The apps deploy with the ones they
  depend on first. Notes come back as codes the interface translates.

`ConvertCompose` reads `build.dockerfile`, `command` and `expose` now, and
picking a single service fills the start command and the Dockerfile too.

Tested: a five-service file — build context and Dockerfile, a list command, a
database and a cache on their images' ports and internal, an exposed port still
internal, a named volume as a disk and a bind mount named, a default taken and
a bare reference named, a name that had to change, every app deployed, the
audit; a stack refused whole for a taken name, a build without a repository,
nothing to run, two names that collide, and none at all; the deploy order,
cycles included.

Not executed: the stack on a cluster, and the Service's second port in one.

## Phase 102 — an app remembers its template, and is offered its updates

Gap 13. The catalogue ships inside the panel, pinned to versions, so an upgrade
of the panel is what brings a template's newer version — and an installed app
knew nothing about where it came from. The new version sat in the catalogue
and the app ran the old one until somebody noticed and typed a tag by hand.
Cloudron offers the update, backs up first, and applies it only then.

Installing now records the template, the service and the image it set
(migration 0030, `app_templates`). `GET /api/apps/{app}/template` compares that
with the app's image and the catalogue's: an update is available when the
catalogue's image differs from the app's, and "changed by hand" when the app's
differs from what the template last set, which an update does not replace
unless asked. `POST /api/apps/{app}/template/update` backs up every disk and
every linked database first, marks the update `backing_up`, and waits in the
background: all of them succeeded, and it moves the image and deploys; one
failed, and it stops with the reason recorded and nothing changed. An app with
nothing to back up updates at once; one with data and no backup storage is
refused unless the update is asked for without a backup. A locked app is
refused before anything is backed up. The Settings tab has a Template card.

On the way, two bugs in what templates install:

* A service a template marks `public: false` got a public address anyway: the
  installer never read the field. 31 private services with a port — MongoDB,
  Elasticsearch, ClickHouse, internal APIs — are now internal (Phase 101).
* 13 templates address a sibling as `name:port` — `http://elasticsearch:9200`,
  `mongodb://mongodb:27017`, `redis://redis:6379` — which could not connect
  while a Service listened on 80 alone. Phase 101's second port is what makes
  them work.

Tested: the view and an update with nothing to back up; up to date refused; an
image changed by hand kept unless forced; a disk backed up first, the image
unchanged while it runs, the update applied after it succeeds and stopped with
the reason after it fails; and an install recording its template and keeping
the private service internal.

Not executed: a real backup job ahead of a real deploy.

## Phase 103 — promoting a version from one environment to the next

Gap 7. Deploying the same commit to production after staging built it a
second time, and a second build is where "it worked in staging" stops being
true: a dependency resolved differently, a base image moved. Heroku's
pipelines, Render and Northflank promote the artifact itself.

`POST /api/apps/{app}/promote` takes a deployment of the same app — same name,
same kind of source — in another environment of the project, and deploys its
image here without building: `DeployRequest` carries the image, the commit and
the build's fingerprint, and the deployer creates the deployment around them
(trigger `promote`). The target keeps its own variables, domains, disks and
scaling, and its release command runs. A version that did not deploy, one whose
image the registry no longer keeps, one of the app itself, one from another
project or another team's is refused; the registry sweep already keeps any
image a recent deployment references, so a promoted image lives as long as
production's history needs it.

The one refusal worth its own error: an image carries its build-time
variables, so when the target's build settings or build-time variables would
give a different fingerprint, the promotion stops (`promote.built_differently`)
unless asked again with `force` — production running staging's
`NEXT_PUBLIC_API_URL` is exactly the mistake a promotion must not make quietly.

The Deployments tab offers Promote on every version that deployed, the live one
included, with the environments it can go to.

Tested: a promotion running the image with nothing built, a differently-built
image refused and then run when forced; through the API, the request the
deployer is given and the audit; refusals for a failed version, the app's own,
another team's and another project's; and where a version can be promoted to.

Not executed: a promoted image pulled by a second namespace in a real cluster.

## Phase 104 — backups sealed with a passphrase, and checked

Gap 16. A backup is every row of a database, and it sat in a bucket as a gzip
that whoever runs the bucket could read; and nothing had ever shown one could
be restored short of restoring it. Cloudron, Dokploy and Dokku encrypt with a
passphrase; Cloudron checks.

`internal/sealed` is the format, a package of its own because the backup jobs,
the panel verifying a backup and `restore-db` all need it and cannot all import
the backup package: a header of magic, a per-file Argon2id salt, a key-check
value and a nonce prefix, then AES-256-GCM chunks of 64 KiB whose nonces carry
their number and a last-chunk flag and which authenticate the header too. A
changed, dropped, reordered or appended chunk, and a file cut short at or
between chunks, are refused; a wrong passphrase is told apart from damage.

A **Backup passphrase** setting (secret, at least 12 characters) turns it on.
Database and volume jobs gain a step between making the archive and uploading
it — the panel's own image, running the new hidden `backup-seal` command — and
restores one between downloading and loading (`backup-open`); the passphrase
travels in the job's Secret beside the upload URL. A backup meant to be sealed
fails rather than going up in the clear when the panel's image cannot be found.
The panel's own backup is sealed in-process. Backups record whether they are
sealed (migration 0031), and a restore of a sealed one checks the passphrase
against the object's header, read with a ranged request, before anything is
stopped. `restore-db` opens a sealed panel backup with
`SKIFITY_BACKUP_PASSPHRASE`, into a file of its own used only once whole.

**Verify** (`POST …/backups/{id}/verify`, from each list: a database's, a
volume's, the panel's) downloads a backup in the background, opens it when
sealed and reads the gzip to its end, recording when it last worked or why it
did not. The lists show sealed and verified.

On the way: `ExpiredBackups` scanned its own column list, which a new column
would have broken silently — retention would have stopped deleting anything.
Every backup query now shares one list and one scanner, and the panel-backup
retention test caught it.

Tested: round trips at every chunk boundary, two seals of one file differing,
the wrong passphrase, seven kinds of damage; reading a backup through, sealed or
not, and six ways one fails; the jobs with and without a seal step and the
Secret with and without a passphrase; `restore-db` with the passphrase, without
it and with the wrong one, leaving nothing behind; the verify route belonging
to its list.

Not executed: the seal step in a real cluster, where the pod's group is what
lets three containers running as three users share the workspace.

## Phase 105 — `skifity db connect`

Gap 11. A managed database has no address outside the cluster, on purpose, so
the SQL client on somebody's laptop could not reach it: the database page
showed a host that only answers inside. Coolify, Fly, Sealos and Epinio all
offer a way in.

`POST /api/databases/{id}/tunnel` with `Upgrade: skifity-db-tunnel` dials the
database from the panel, answers 101 and then carries bytes both ways until
either side closes. It is an admin's, like the password it is used with, and a
POST, so a read-only token cannot open one; each is in the activity log as
*Database tunnel opened*. A database that does not answer is a
`database.tunnel_unreachable` problem rather than a dead connection.

`skifity db` lists an environment's databases; `skifity db connect [name]`
listens on 127.0.0.1 — the database's own port when it is free, any otherwise,
or `--port` — prints the connection string pointed there, and opens a tunnel
per connection a client makes. The client is HTTP/1.1 on purpose: HTTP/2 has
no upgrades, and a TLS client that offered h2 to an ingress that speaks it
would get no tunnel. Ctrl-C closes the connections still open too. The
database's connection tab shows the command.

Tested: the CLI's request against the panel's handler — a round trip twice
over one tunnel, the audit entry, a member refused, a POST without the upgrade
refused, an unreachable database explained; and the CLI's side against a fake
panel — relaying, a refusal reported while the next connection still works,
Ctrl-C with a client still connected, the local address only, a port asked for
that is taken, the rewritten connection string.

Not executed: a tunnel through the ingress in front of a real panel. Traefik
proxies upgrades of any protocol, but that is its documentation, not a run.

## Phase 106 — processes: a worker beside the app, from the same build

Gap 10. An app ran one process, so a Rails app with Sidekiq, a Django app with
Celery or anything with a clock was two apps, built twice, deployed apart and
able to run different versions of the same code against one schema. The
Procfile's worker line was detected and then answered with "make another app".
Heroku, Fly, Render and Dokku run process types from one build.

A process is a row in `app_processes` (migration 0032): a name, a command and a
number of instances. Each deploy, rollback and settings change applies it as a
Deployment of the app's own image, `<app>--<process>` — two hyphens, which
`Slugify` never writes, so it cannot be another app's Deployment — under
`/bin/sh -c` with the app's variables and `SKIFITY_PROCESS`, and with no port,
Service, probe, volume, password or autoscaler. Its selector is derived from its
own name, so the app's Service never sends a worker a request and neither
Deployment claims the other's pods; its labels find it again, and a deploy
removes the ones no longer wanted, one by one by name. A deploy waits for every
process, and a worker that cannot start fails the deploy with its reason.
Deleting the app removes them; the Advanced view and the team export show them;
a preview gets them at one instance each.

`GET/PUT/DELETE /api/apps/{id}/processes[/{name}]`, a viewer's to read and a
member's to change, audited. A new app can be created with them: the new-app
form offers the Procfile's lines ticked, and `skifity up` sends them. Heroku's
names allow capitals and underscores, so `Celery_Beat` becomes `celery-beat`,
and one that cannot become a name is said rather than dropped; a test holds the
builder's copy of the rule to the panel's. The Scaling tab lists them with how
many are running, `skifity processes set worker -- <command>` and
`--instances` change them, the logs tab and `skifity logs --process` read one,
and the MCP server has `set_process` and a `process` option on `get_app_logs`.

"Service types in the new-app form" is this and Phase 101 together: a public
app, an internal one reached by name, and a worker, which is a process of the
app that feeds it rather than an app of its own.

Tested: the process Deployment — the app's image and variables, its own command
and count, no port, probe or volume, and selectors that match neither way;
names that cannot collide, long ones included; pruning only the unwanted, and
all of them with the app; the API's rules, the limit of ten, a viewer refused,
the audit; status and logs asked for by the process's own name; an app created
with its processes, and refused whole when one is wrong; a preview's copy; the
Procfile's names; the CLI's `--` and count-only changes.

Not executed: a worker in a real cluster.

## Phase 107 — previews of the whole environment, seeded

The rest of gap 8. A preview was a copy of the apps its repository builds and
nothing else: a front end's preview called an API that was not there, a Compose
stack's cache was missing, and a preview's database was empty with nothing to
show. Vercel, Netlify, Railway, Render and Coolify each have some of this;
Railway copies the whole environment, Render runs an initial deploy hook, and
Heroku a review app's `postdeploy`.

An environment's **Previews copy the whole environment** (`preview_stack`,
migration 0033, an admin's, audited) makes a new preview a copy of every app in
it. The previewed app and any other app its pull request builds are built from
the pull request; every other app is deployed at the image its last successful
deployment ran, with that deployment's fingerprint and commit and nothing
rebuilt, and is left with deploy on push and previews off so it stays there.
The copying is one routine for both, `previewCopy`: the app with one instance
and no autoscaling, its variables as each says, its password, its processes at
one instance, and databases of its own — which are now shared when two apps
link the same one, found by name and engine in the preview, where each used to
get an empty database the other never saw. An app never deployed is left out.
It happens once, when the preview environment is made; later pushes build only
their own apps.

An app's **Seed for previews** (`preview_seed`) runs once in each new preview
after its first successful deploy, as a Job of its own kind in the preview's
image and variables, after the release command. `seeded_at` is claimed with a
conditional update before it starts, so two deploys finishing together seed
once, and a failure is not retried: a half-run seed run again is duplicated
rows. It does not fail the deploy; the log has its output and the `skifity run`
command for trying again.

Tested: a preview of an environment with the setting — web built from the pull
request, the API at its running image, the cache pulling its own, an app never
deployed left out, the copies frozen at one instance and still internal, the
seed copied, one database for the two apps sharing one, and a second push
copying nothing again; without the setting, only its own app; the setting an
admin's, audited, not on a preview, and the confinement level still changing on
its own; the seed kept, and claimed once.

Not executed: a seed Job in a real cluster.

## Phase 108 — `skifity.yaml`, with plan and apply

Gap 5. An environment's settings lived in the panel and in the memory of
whoever set them: nothing in a repository said that the web app needs a worker,
a database linked as `DATABASE_URL` and a nightly job, so a new environment was
rebuilt by clicking, and a change to one was never reviewed. Render has
render.yaml, DigitalOcean an app spec, Porter porter.yaml, Railway
railway.json, Portainer stacks.

`internal/blueprint` reads `skifity.yaml` — apps (source, build and start
settings, port, health, watch paths, internal, instances or autoscaling,
resources, plain variables and the names of secrets, processes, domains,
schedules, database links, deploy on push, previews, a preview seed) and
databases (engine, version, storage, instances) — strictly, so a misspelt field
is an error, and reports every problem at once. `Plan` compares it with what the
environment has and returns ordered steps, each with the API call that makes
it: databases before the apps linked to them, an app before what hangs off it,
new apps' first deploys last so none starts without its database. It has no
network and no store; `skifity plan` and `skifity apply` fetch the state through
the API and send the calls through the API, so every change is authorized,
validated and audited as the same click would be — a viewer can plan and not
apply — and apply stops at the first refusal, leaving the next plan to say
what is left.

It never deletes: what the environment has and the file does not is reported
and left alone, and so is a database whose engine or version differs.
Variables in the file are stored as plain values, since the file is in a
repository; `secrets:` only names the ones that must be set, and the plan says
which are not. A field left out is left as it is. An app with neither a
repository nor an image can only describe one that exists, a folder sent with
`skifity up`. A link with no variable uses the panel's default for the engine,
and a test holds the two defaults together.

Tested: the file read strictly, in both forms of a process, and a wrong one
reported whole; an empty environment built in order with references between
new things; a matching one with nothing to do and an extra app noted; only the
differing fields changed; what a plan refuses; references filled from what was
made; and `skifity plan` and `apply` run by the CLI against the real API —
everything made, applied twice being applied once, a changed file changing only
that, and a viewer's apply refused with nothing made.

## Phase 109 — upgrading the components, and k3s

Gap 12, the last. The installer put k3s on every server and nothing moved it;
Kubernetes supports a minor version for about fourteen months. Components were
installed once, never re-applied, and their `version` column was never written,
so a panel release with a newer cert-manager changed nothing on an existing
install. Kubero and Epinio leave both to the operator.

Components now record the version they were installed at — from the manifest
address they were applied from (the last version-looking part of it, so
CloudNativePG's `release-1.29/…/cnpg-1.29.0.yaml` is 1.29.0), or from the image
they run — and the list says what this panel would install now. `POST
/api/components/{name}/upgrade` applies that version's manifest over the
installed one, which is how each project documents its upgrade; a failure
leaves it installed at its old version with the reason, and a success is
audited with both versions.

k3s: `kube.PlanK3sUpgrade` orders the nodes control plane first, then workers,
and returns every reason not to start as a code with values — a target that is
not a release, a skipped minor version, a node already newer, a different major,
a node not ready — plus warnings for a single control plane or a single server;
the API adds the absence of a panel backup from the last day. `GET
/api/k3s/upgrade` shows the nodes, the latest release of each minor from k3s's
channel server, and the plan for a version; `POST` checks it again and hands the
work to Rancher's system-upgrade-controller (v0.20.2, installed as the
*Kubernetes upgrades* component on first use) as the two Plans the k3s
documentation gives, with an explicit version, one node at a time, cordoned and
not drained. The Settings page's Components tab has the card; its reasons are
translated from their codes.

Tested: versions read from every default manifest, from a manifest setting, and
from images; the channel feed parsed, sorted and the stable one marked; the
planner's order, a node at the target left alone, each blocker and warning; the
Plans as documented, never draining, the agents waiting for the servers; the
routes refusing without a backup and with a skipped minor, starting once both
hold, audited, and refused to a team owner who is not a panel administrator; a
component offering its newer version, upgrading, and an external one refused.

Not executed: an upgrade of a real cluster, which is the one thing here that
cannot be tried without one.

## Phase 110 — an independent review of phases 96 to 109

Fourteen phases in a row is a lot of new code nobody else had read. Five
reviewers each took a group of them, read the code against what its
documentation claims, and ranked what they found; every finding was checked
before anything was changed, and each fix came with a test that fails without
it. What they found, and what changed:

* **Previews.** Two repositories' pull requests with the same number were one
  preview, so a fork of one repository could be handed the other's secrets. A
  preview is now keyed by the pull request and a tag of its repository; a copy is
  made whole or not at all, never deploys on a push of its own, and an
  environment preview that failed part-way fills in what is missing next time.
* **Git connections.** An app could be created from another team's connection by
  its id. The connection must now belong to the app's team.
* **Compose.** A port published on `127.0.0.1` was exposed to the internet; it is
  now reachable inside the environment only.
* **Watch paths.** GitLab and Gitea do not list a force-push's files, and a push
  after a missed one lists only its own; both deploy instead of being skipped.
* **Alerts.** A server or an app whose usage could not be read counted as idle
  and cleared its alert, and one hovering at the threshold alerted every minute.
  Unknown usage is now skipped, and an alert clears five points below where it
  fired.
* **The firewall.** `CF-Connecting-IP` was believed from anybody who sent it. It
  is now believed only from a trusted proxy on a Cloudflare Tunnel path.
* **Maintenance.** Starting it answered success when the app's Ingress had not
  changed, so visitors saw the app while the team was told they saw the notice.
* **Processes.** A worker that did not come up failed a deploy whose web part was
  already live, and the next variable change then rolled the web back without
  saying so. A new version now reaches the processes once the web serves it; a
  process that is not ready is said in the deployment's log and does not fail it;
  a settings change no longer waits for every process in turn. A new command
  kept nothing of the old instance count, `SKIFITY_APP` was the worker's
  Deployment's name, a restart and a database restore left the workers on their
  old connections, and a Procfile with `worker_a` and `worker-a` made an app that
  could not be created. Each is fixed.
* **The database tunnel.** An open tunnel was never checked again: revoking the
  token or removing its owner left it open. It is now checked every minute and
  lasts at most twelve hours. A client that half-closed lost the database's
  answer, because the first end of stream closed both ways, and a failed accept
  in the CLI waited for every open connection before it returned.
* **`skifity.yaml`.** Changing a schedule from the file was refused every time
  (its name was not sent) and would have switched a stopped one back on; a
  schedule written `1-5` never matched the `1,2,3,4,5` the panel keeps; a
  repository in the file was sent in a change the API refuses, taking the app's
  other settings with it; an apply that stopped after making an app never
  deployed it; a new value for a build-time variable moved it out of the build;
  a new image was reported and not run; a domain in capitals was added again on
  every apply; `{ a; b; }` was taken for a reference; and a name such as `my_app`
  planned an app the panel then called taken. Each is fixed, a private
  repository can name the Git connection it is read through, and a plan made
  right after an apply is empty — tested through the real API with the real CLI.
* **Promotion and template updates.** Both set an image app's image before the
  deploy was asked for, and a deploy refused — a lock, a cluster that was down —
  left it set, for `skifity run` and the next unrelated deploy to ship. It is now
  put back. A template update was recorded as done before its deploy was
  accepted, and one waiting for its backups when the panel restarted stayed
  "backing up" for good and refused every later update; the first is recorded
  once the deploy is under way, and the second is marked failed at startup. The
  rollback window counted only successful deployments while the registry keeps
  the images of the last ten of any outcome, so behind a run of failed rollouts
  it offered versions whose images were already gone; the two now count alike.
* **Backups.** Any member could verify sealed backups in parallel, each one 64
  MiB of Argon2id, until the panel ran out of memory; verifications now run one
  at a time and a repeat joins the one already asked for. A backup's Job Secret,
  holding the passphrase that seals every team's backups, was left in the
  team's namespace by a restart mid-backup; it now belongs to its Job and goes
  with it. Two backups started in one second were one object, which retention
  then deleted from under the newer; the key carries the backup's id. Clearing a
  setting — the backup passphrase, say — was not audited; it is. A sealed backup
  on a panel outside its cluster failed with the firewall's error; it has its
  own. That a sealed file is not bound to its place in the bucket is said in the
  documentation rather than changed, since binding it would change the format.
* **Upgrades.** A component's final state was written on the request's context,
  so upgrading the Cloudflare tunnel — which drops that request — left it
  "upgrading" for good, and nothing reset that or "installing" after a restart.
  Both are written on a context of their own and settled at startup. Versions
  were compared as text, so a cleared setting offered a downgrade; they are
  compared as numbers, a downgrade is refused, and a manifest component moves one
  minor version at a time. A k3s version typed without `+k3s1` was read as older
  than itself and blocked as a downgrade, and upgrade plans somebody applied by
  hand went unnoticed beside the panel's; the first is read as `+k3s1`, the
  second is a reason not to start.
* **Words in the interface.** Detection's notes and the components' names were
  English in every language; they are now codes the interface translates.

Not executed: the process and tunnel changes on a real cluster. The tunnel's
half-close and re-check are tested end to end through the real handler and the
real CLI; the deploy ordering is not, since the deployer only runs against a
cluster.

## Phase 111 — a second review, of what nobody had re-read

The first review read the fourteen newest phases. This one sent six reviewers
at what it had not: the fixes it made, the API's authorization end to end, the
deploy pipeline and builder, sign-in and encryption, the integrations, and the
interface and the CLI. They found more than eighty things, a handful of them
serious; each was checked in the code before anything changed. The fixes in Go
have a test that fails without them, apart from two that need what this
sandbox does not have: the order a deploy applies things in and the ingress
setting need a cluster, and are said below where they are. The interface's
fixes are checked by the type checker, the linter and the Playwright run, which
covers first run, the shell and the languages, not each form.

The serious ones were all the same mistake: something panel-wide guarded by a
team role, when any signed-in user can create a team and be its owner.

* **Servers.** Every server that joins is given the cluster's join token, and a
  control plane's is the whole cluster. A team admin could add one — so anybody
  could join a machine of their own and read every team's secrets. Adding,
  retrying, promoting and removing a server now need a panel administrator.
* **Plugins.** Owning a team was the bar for installing an image beside the
  panel, rewriting another plugin's settings, or removing a deploy policy. It is
  a panel administrator, signed in rather than by token. A plugin that asked for
  no permissions was given a token with no scopes — which is full access — and
  is now given one that allows nothing. A manifest from the store must still
  have the digest its signed index gave.
* **The SMTP password.** An email channel naming its own server was given the
  panel's SMTP user and password for whatever it left out. A channel's own server
  now gets none of the panel's settings, and is dialled through netguard with a
  deadline.
* **The master key.** The recovery key is the master key, and a token scoped
  "read" could download it; so could anyone at an unlocked admin's laptop. It now
  refuses every token and asks for the password again. A database's password and
  a team's export refuse any token with a scope.
* **Guessing passwords.** Changing a password checked the current one without
  counting failures, a leading space was a fresh set of guesses at a locked
  account, and each IPv6 address counted alone. All three count now, an IPv6
  address by its /64.
* **Signing in.** A page on a sibling subdomain could post a sign-in with no
  content type and fix a visitor's session to the attacker's; a body without a
  type is refused unless a token says who is calling. The return address after
  single sign-on let `/\evil.example` through; an account made by single sign-on
  could be claimed by a second, unverified identity with its address; turning on
  two-factor left older sessions — which then counted as having passed it — and
  setting it up again switched it off. Each is closed.
* **Between teams.** Viewers received every audit entry live, while the audit
  log is an admin's; the stream now only says that something was recorded. An
  admin could demote an owner and then remove them. A token made for one team
  listed the others. An environment could be named into a plugin's or the
  system's namespace. An image app could run another team's built image. Each is
  refused.
* **Builds.** A static site's build command went into a heredoc in the build
  container, which holds the registry's credentials; a line saying the marker
  ran anything after it there. The Dockerfile is written from base64. Two builds
  started in the same quarter of a second shared a Job name — the id's start is
  its time — and with it a Secret of build variables.
* **Logs.** Only string values were scrubbed, so an error quoting a Telegram
  bot's address logged its token; errors are scrubbed, and so are credentials in
  addresses, webhook paths and signed query strings. A release command's output
  is scrubbed like a build's.

The deploy pipeline had the worst of the correctness bugs:

* **Deploy now deployed old code.** The button sends no commit, so the build's
  fingerprint said "no commit" every time, and the second press reused the first
  press's image — whatever had been deployed in between. A Git deploy with no
  commit now always builds.
* **A tag named one image, until it named another.** Images were tagged by commit
  alone, so the same commit built with another build variable was pushed over the
  first, and a server that had the tag never pulled again. Tags now carry a piece
  of the fingerprint. A static site's build command and output folder are part of
  the fingerprint, which they were not; a cancelled or replaced build's Job is
  stopped rather than left to push later.
* **Stuck and rolled-back deploys.** A deploy that ran into its 45-minute limit
  wrote its failure on the context that had just run out, so it stayed
  "building" for good. A deploy is recorded as succeeded the moment the web is
  serving it, before the processes and the seed, so a cancel in between no longer
  leaves the new version live under a row that says otherwise. A settings change
  during a rollout re-applied the previous version's image; it applies the one
  rolling out. An older request could supersede a newer one, and a rollback
  superseded nothing; both are ordered now.
* **The registry.** The sweep keeps each app's running image however many failed
  rollouts follow it, an image is reused only while the registry still keeps it,
  and a build records its image before it lets the sweep in.
* **Variables.** The app's variables were a Secret written with `stringData`,
  which server-side apply cannot take a key away from, so a deleted variable stayed
  in every pod started afterwards. It is written as `data`.
* **Release commands** ran before the namespace, the variables Secret and the
  registry's pull secret existed, so a first deploy's release waited for nothing
  and later ones migrated with the previous variables; those are put in place
  first, and every run pod carries the pull secret. A slow clone no longer counts
  as a build that never started.
* **Names.** An app's wake Service is `<app>-wake`, which is also the Service of
  an app called that; such a name is refused beside the other.

Not fixed, and said here: Kubernetes resets a Deployment to one instance for a
moment when autoscaling is switched on, an app asleep at zero passes the
rollout check without starting, the build workspace has no size limit, and the
registry inside the cluster takes a push from any build — a Dockerfile's `RUN`
could overwrite another team's image. The last needs the registry to have
credentials per team, which is its own piece of work.

Some of Phase 110's own fixes had made new problems:

* **Maintenance in front of a crashing app.** The notice is put in front by
  re-applying the app, which then waits for it to be ready. An app that is
  crash-looping — the usual reason for a notice — failed that wait after the
  Ingress had already changed, and the panel said maintenance had not started
  while visitors saw it. A failed wait for readiness no longer undoes the
  record; an app never deployed is refused, since there is nothing to put the
  notice in front of.
* **Monorepo apps rebuilt every other push.** A push that touched nothing of an
  app was skipped only if the app ran the commit before it; the next push's
  "before" was the skipped one, which it never ran, so it rebuilt. The skipped
  commit now stands in for the running one until another deployment runs.
* **Preview copies deployed production's pushes.** The copies whole-environment
  previews made kept their source's "deploy on push"; they are switched off, and
  the ones already made are corrected by a migration.
* **An unmeasured minute was a missing minute.** Leaving out a minute that
  metrics-server had nothing for also left out its restarts and its disk, so a
  crash-looping app or a full disk — the thing that evicts metrics-server — went
  unwarned. The minute is kept with CPU and memory marked unknown: the graphs
  show a gap, the memory and CPU warnings neither start nor end on it, and the
  restarts and disk warnings go on.
* **Previews the previous release made.** Their ref did not name the
  repository, so the next event for the same pull request made a second preview
  beside the first, and closing it removed only the new one. An old preview
  that copies an app of the event's repository is now taken over and renamed.
* **Gitea previews froze.** Gitea says a pull request was pushed to as
  `synchronized`, not GitHub's `synchronize`; it is read as the same thing.
* **A direct request posing as the tunnel.** Cloudflare's headers are believed
  only from the connector, which was told apart by the last hop being inside
  the pod network — but on k3s every visitor arrives from inside it, handed on
  by ServiceLB, so the firewall's address rules saw one address for everybody
  as well. The installer now runs Traefik on every server with
  `externalTrafficPolicy: Local`, which keeps each visitor's address, and
  leaves an operator's own Traefik configuration alone with a note of what to
  set. A request with no hop recorded at all is no longer taken as the tunnel.
  **Not run on a cluster**: the file is written and checked by the installer's
  smoke test, and nothing here has seen ServiceLB honour it.
* **`skifity.yaml` lost a change whose deploy was refused.** The apply stored
  the new image, the deploy was refused, and the next plan compared the file
  with what was stored, found them equal and planned nothing. A new value for a
  variable the build reads planned no build at all. The plan now compares with
  what runs — the last deployment that succeeded, or one under way — so a
  stored image that is not running, or a build value changed since the running
  build, is still owed a deploy.
* **Deploy order.** Scheduled commands moved to a new image before the app was
  serving it; they move with the processes, after. Once the app serves a
  version, a process or schedule that cannot be moved is said in the log
  instead of failing a deployment that is live.
* **Smaller ones.** The MCP tool that changes a process sent an instance count
  whether asked or not, so a new command stopped the process. The tunnel's
  minute-by-minute check closed a working tunnel on one database hiccup — three
  in a row now — and kept an idle browser session alive; it no longer extends
  it. Two backups sharing one object before each had its own key lost it when
  the older expired; retention now leaves an object another backup uses.

And the integrations — Git hosts, databases linked into apps:

* **The Git token was the webhook secret.** The panel registered the account's
  token as the secret on every repository it hooked, and GitLab sends the
  secret verbatim with every delivery. A connection now gets a secret of its
  own, shown when it is made and under Settings, Git, Webhook; one made before
  keeps signing with its token until the account is connected again, and the
  page says so without showing the token. An unknown connection id is answered
  exactly as a bad signature is, not with a 404 that says which ids exist.
* **Older pushes deployed over newer ones.** Hosts do not promise to deliver in
  order, and a signed delivery is good for ever. A push is now skipped when its
  commit is what already runs or is on its way, or one a later push moved the
  app past; a forced push, somebody going back on purpose, still deploys.
* **Previews from forks had no limit.** Anybody can open a pull request, and
  each made a namespace and ran its author's code. A project runs three at
  most; pull requests from the repository itself are not counted.
* **Previews left behind.** Closing a pull request removed its preview only if
  the app deployed on push, and a deleted branch looked for its preview under
  an empty name. Both remove it now.
* **Database links.** Linking a database again under a new name left the old
  variable, with the full connection string, belonging to no link — kept by
  every preview. Two databases under one name overwrote each other, and
  unlinking either took the other's connection away. The old variable goes, and
  a name another database holds is refused.
* **What a member can do** is written down: deploying is running code with the
  app's secrets, so a member can read any of them, a linked database's password
  included. The admin line keeps a password off screens and out of tokens, not
  from somebody who can deploy.

Checked and found sound: a fork's build never sees the Git token. Only the clone
container has it, as a header scoped to the repository's own host on the one
command that fetches, never on disk; the build that runs the fork's code does
not have it.

Authorization and keys, the last of it:

* **The cluster's servers, per team.** One cluster serves every team, and its
  summary listed every node — addresses, labels, load — to a viewer of any of
  them. A team now sees the servers it added and the capacity it shares; the
  whole list is a panel administrator's.
* **Tests that aim at real objects.** The route walks filled every child id with
  one that does not exist, so "does this deployment belong to this app" had
  never been asked of another team's real deployment. A new walk asks every
  route with a child id — deployments, domains, schedules, volumes, backups, Git
  connections, channels, invitations, members, operations, tokens — through the
  caller's own parent with the other team's real child, and checks nothing of
  theirs changed. It found nothing: the checks hold. The live stream's topics,
  which no test had touched, are refused for another team's too, mixed with the
  caller's own or not.
* **Key rotation put back old values.** It reads every sealed value, rewraps
  it and writes it back; a variable or password changed in between went back to
  what it was. The write is now made only over the value that was read. After a
  rotation that could not rewrap everything, the page says to keep the previous
  recovery key too, since the new one does not open what was left.
* **Adding a server dialled anything.** The SSH connection went straight to the
  socket, past netguard, so the form could probe the panel's own machine and the
  metadata service. It goes through netguard now.

The CLI, which a review ran against a fake panel:

* **`skifity run` exited 0 on a failed migration.** The run's output had no exit
  status, so a CI step running `npm run migrate` passed whatever happened. The
  panel now waits for the command's container to stop and returns its status,
  and `skifity run` exits with it; `--json` waits too, and includes it.
* **`--json` was not JSON** for `deploy`, `rollback`, `up` and `init`: they
  printed "started", the build log and "succeeded" around it, and `init` wrote
  the webhook lines straight to standard output. Each prints one document now;
  `logs --follow --json` prints one object a line.
* **A deploy that failed at once was waited for for ever.** The event stream is
  opened after the deploy starts and replays nothing, so a failure before it
  connected was never heard. The deployment is also asked for every few seconds.
* **Guessing.** With no `skifity.toml`, a command took the team's first project,
  and `login` stored the first team; a rollback run in the wrong folder could
  roll back another project's app. Several projects or environments are now a
  question, and `login` asks which team, or takes `--team`. `panel =` in
  `skifity.toml`, read and never used, now refuses to run against another panel.
* **The stored token went wherever `SKIFITY_URL` pointed.** Set on its own, to a
  typo or another panel, it sent that panel the stored token. It is only used
  for the panel it was made on.
* **Smaller.** `scale --max 8` without `--auto` sent nothing and exited 0; `--help`
  exited 1 with "flag: help requested"; `status --explain CODE`, which every
  error recommends, printed a pointer to the panel and now prints the error
  itself, kept locally, for pasting. A new value for a build variable from `env
  set` without `--build`, from bulk edit or from an assistant took it out of the
  build; saying nothing now keeps what it was. The token form offers read-only
  access and an expiry, which the API took and the form never asked for, and the
  CLI's documentation says what the CLI does.

And the interface:

* **The Console said a command had finished while it ran.** It read the output
  without following it, so a migration showed "printed nothing" and went on. It
  follows to the end now, and says when the command failed and with what status.
* **A rollback was undone by the next Save.** The rollback puts the port, health
  path and commands back, and the settings and resource forms held their values
  from before it; saving anything wrote them back. The forms start again from the
  app when it changes underneath them, and a rollback refreshes what it changed.
* **Adding a variable made a secret plain,** and a build variable a runtime one:
  an unticked box was sent as "no". Unticked now says nothing, and the panel
  keeps what the variable was; bulk edit no longer sends "not for the build".
* **Things that looked like other things.** The recovery-key warning showed to
  every member, who could never clear it; it is an administrator's. A queued
  build said nothing needed building. The Logs tab showed the last 200 lines
  again on every reconnect and pause, and a stream that failed looked like an app
  with nothing running. A failed load of the scaling settings was a skeleton for
  ever. A failed list of servers showed first-run setup to a team with servers.
* **Detection kept the first repository's findings** — its databases, processes
  and seed — after the address was changed to another, and Create used them.
* **English in every language.** A 502 while the panel restarts, a browser that
  is offline, and a failed deployment's headline were not translated.
* **Members limited to projects** were refused the team's live stream, so a
  database stayed "creating" until a reload. Those pages ask again instead.
* **Plugin settings** were all text boxes, and the panel took any value for any
  kind; a switch, a list and a number are offered now, and a value that does not
  fit is refused. Turning a plugin on or off no longer discards unsaved edits.

## Phase 112 — Coolify, Dokploy and Kubero, read from their source

The earlier research read documentation, changelogs and issue trackers. This
phase cloned the three products Skifity is most often compared with — Coolify
(Laravel on Docker), Dokploy (Next.js on Swarm) and Kubero (a Kubernetes
operator) — read their code, then read Skifity's against it.
`docs/research/source-audit.md` is the page about what was found; this section
is what was done about it, in commit order.

**Defects the comparison found in Skifity, fixed first.** Database passwords on
the backup pod's command line (`0d1e5da`); a secret build variable passed as a
build argument, which Docker writes into the image's history (`7123905`); a
backup due while the panel was down skipped without a word (`e61837d`); ten
templates keeping their data where a restart loses it (`4f0e5b3`); 32 images
named by a major line rather than a release (`c9bc382`); two templates public
behind an HTTP ingress that could never reach them (`62d2adb`); four channel
kinds the database refused to store (`faeecc1`); a backup taken by hand pushing
the scheduled one out of retention (`e215a65`); k3s's Traefik refusing the
ExternalName backend every scale-to-zero app is reached through, so each would
have answered 404 (`784fa9f`); two templates pointing at images nobody can pull
(`66f305f`); and one-off commands pinning the group where the app leaves it to
the image (`1c2310f`).

**What they had and Skifity did not.** Files mounted at their paths (`374b40c`,
`514aa3b`); templates with a command, files and a database handed over in
pieces, and 92 more of them (`88a310d`, `f9b4bb2`, `b555b38`); a team's own
registry credentials (`eb520c3`); an OpenAPI description with a test that no
route is left out (`a0f45bd`); public TCP and UDP ports (`62d2adb`); deploy on
a tag, `[skip ci]`, repository and branch pickers (`3c39d94`); run a scheduled
command now (`138d296`); a build queue (`94e6e9b`); a password reset by email
(`adddd03`); cloning an environment (`c09ffc9`); an MCP server that does more
than deploy (`e07ac76`); editing and routing notification channels, Teams and
Gotify (`faeecc1`); configurable health checks and a DNS check before a
certificate (`8ed96a2`); a server hardening report (`d2480c7`); requests, errors
and response times per app (`6fa2876`); hostname redirects (`6308052`); API
tokens limited to networks (`977b982`); a preview started by hand (`a114e05`);
nine database engines (`f4f9d09`); image vulnerability scanning (`ca539aa`);
drift detection and the events feed (`080fa91`); a team's own certificates
(`b16f93f`); Bitbucket (`505b79d`); a team's own template catalogues
(`75ff954`); variables read from secret managers (`25e2a18`), limited to
paths and projects (`43ff267`); passkeys (`6c3eee5`); GPUs (`5dd56cf`);
stopping, resizing and re-passwording a database and importing a dump
(`6920a57`); DNS records kept at the provider (`680449c`); log drains
(`3731416`); servers ordered at Hetzner Cloud (`2f8ef92`).

**Built because the audit showed the need, though none of the three has it.**
Run as user (`6f50d5d`, `1c2310f`): Skifity is the only one of the four that
enforces a non-root user, and 109 catalogue services name theirs rather than
numbering it. Scheduled backups counted apart from those taken by hand
(`e215a65`). `check:destructive` in CI (`bed041f`): the check existed, failed,
and nothing ran it — the gap `make check` warns about, again.

**Found on the way.** The panel's own namespace enforced a Pod Security
profile that refuses the host paths the panel mounts, so the first install
would have had no panel (`6a181d9`, ADR-0024). The Security tab showed about
1,600 Codacy findings, every one from a tool reading a file in a language it
does not speak; the real ones in the maintainer scripts and shell are fixed
and the scanner reads what it can (`5dd150e`, `e75987c`). The API tests had
grown to the edge of the ten minutes the race detector's run allows a package;
each now starts from a copy of one migrated database (`cb1d6e4`).

**What CodeQL found.** Run locally with the same `security-extended` suite,
it reported 83 findings: 47 of `go/log-injection` and 36 others. The 36:

* 25 findings had one cause. The error for an unknown master key carried
  the key's id, read from the sealed value, into every log line that
  printed it.
* Three addresses the panel fetches (a template catalogue, a Git host's API,
  a plugin) were dialled through netguard, but their shape was never checked
  first. Now `netguard.Address` checks it.
* The CLI download's file name used the platform as the request spelled it.
  It now uses the allow-list's own spelling.
* The snapshot taken before an upgrade was named with the requested
  version, and that name goes into `VACUUM INTO`. The name is now rebuilt
  from the version's numbers.
* A build argument's name was quoted into the build's shell without being
  reduced to the characters a variable name can hold.
* The Slack and Discord redaction patterns are unanchored on purpose. They
  now say so, so they no longer look like host checks.
* The guard read its forwarded headers in a form CodeQL could not tell
  apart from reading `Authorization`.

Each one is fixed, so the remaining 47 are `go/log-injection` and nothing
else. That query does not see that both log handlers escape control
characters, so it is left out in `.github/codeql/codeql-config.yml`, and
`TestAValueCannotForgeALogLine` checks the escaping for both formats.

Two more problems turned up while fixing these. The guard was started with
its log level and format swapped, so it ignored both settings. And a 66 MB
build of the binary had been committed by mistake; it is untracked and
ignored now, but it stays in the history, which is not rewritten.

Not executed, as everywhere in this file: nothing here has run against a
cluster, a real Bitbucket, a real secret manager, a real DNS or log provider,
Hetzner, a GPU, or a browser with a real authenticator. Each part below says
what its tests stand in for.

### Nine database engines

Three engines became nine: MySQL (now Oracle's own image; see ADR-0023),
MariaDB, MongoDB, Valkey, Dragonfly, ClickHouse and Memcached beside
PostgreSQL and Redis. `docs/databases.md` is the page about them.

* **One catalogue** (`internal/dbsvc/engine`) says what each engine is, which
  versions it is offered at and the exact image behind each, its port, its
  default variable and whether it is backed up. `GET /api/database-engines`
  serves it to the form, which now offers a version and says under the engine
  what it is for.
* **Migration 0047** widens the `engine` CHECK and renames every `mysql` row to
  `mariadb`, which is what it always ran.
* **Backups** for MySQL (`mysqldump`, with `--no-tablespaces` since the app's
  user lacks PROCESS) and MongoDB (`mongodump --archive`, with the password in a
  `--config` file, the one place the tools read it besides the command line).
  Dragonfly, ClickHouse and Memcached are not backed up, and the panel says why
  on the database's Backups tab, in the form, in the API and in
  `docs/backups.md`.
* **The Redis restore could never have worked.** It piped the RDB snapshot into
  `redis-cli --pipe`, which sends its input as commands. It now replicates: a
  server of the database's own image starts in the job on the snapshot, and the
  database is its replica until the key counts match, then a primary again —
  after refusing if the database is older than that server. The same for Valkey.
* **Passwords off command lines, again.** Redis's `--requirepass "$REDIS_PASSWORD"`
  kept the password out of the manifest and put it in `redis-server`'s own
  arguments; Redis and Valkey now read it from a file their shell writes.
  ClickHouse's image passes it to `clickhouse-client --password` when
  `CLICKHOUSE_DB` is set, so the database is created by a startup probe instead.
* **The PostgreSQL client matches the server's major.** pg_dump 17's output sets
  `transaction_timeout`, which a PostgreSQL 16 refuses on the way back in.
* **Detection** offers MongoDB now, and finds MariaDB, Valkey, ClickHouse and
  Memcached by their clients; the old Node `mysql` package is offered MariaDB,
  since it cannot sign in to MySQL 8. 41 templates that ran MariaDB say so.

What has been checked and what has not: every image tag was read from its
registry (Docker Hub, and ghcr.io behind docker.dragonflydb.io) with both
architectures; every image's user, entrypoint behaviour and client variables
were read from its Dockerfile or source. The manifests are tested for pinned
images, the restricted profile, and a planted password in any container's
command, arguments, probes or literal values; the Redis and Valkey server
commands, and every backup and restore script, are run against stub clients
that record their arguments. None of it has run against a cluster.

### What was changed outside the panel, and what Kubernetes said

The README invites people to use kubectl, and nothing noticed when they did:
an edited Deployment ran as edited until the next deploy put it back, and the
app's page described something that was no longer there. Kubero reconciles;
this does not, on purpose, but it now says so and puts things back on request.

**Drift.** `deploy.render` is the one rendering of an app's objects — the apply
and the check both call it, so the check compares the cluster with exactly what
the last apply wrote. Every object it renders carries a fingerprint of itself
(`skifity.com/applied-hash`, on the object's metadata, never the pod template,
so writing it restarts nothing), and the last apply's list of objects is kept in
`app_drift.applied` (migration 0049). `kube.CompareObject` walks only the fields
the panel sets — the ones server-side apply files under the `skifity` manager —
so nothing the API server defaults or a controller writes is compared, and
reads `managedFields` for the rest: a field another manager now holds is drift,
attributed to it (`kubectl-edit`, `kubectl-patch`, `helm`); a field the panel
still holds is the panel's own change not applied yet (a build variable waiting
for a build, a save the cluster missed) and never drift; a field nobody holds is
drift only when the object's fingerprint says it is the one the panel applied;
and an object that is not there is deleted only if the panel had written it.
Lists are compared as Kubernetes merges them (containers, env, ports and mounts
by key, everything else whole, with what the server adds inside an element
ignored), numbers by value, resource quantities as quantities. A Secret's values
are never sent, only that one differs; values that repeat one of the app's
secret variables are taken out of the rest. A volume somebody grew is left
alone, since it cannot shrink.

Nothing is compared while a deployment is unfinished or a sync is running
(`Deployer.Busy`; the API answers `applying`), so a rollout's new image and a
half-applied change are never drift, and an autoscaler's or scale-to-zero's
replica count is never rendered, and so never compared. The watcher compares
every deployed app every five minutes rather than every minute — a check is a
render and seven or eight GETs, thirteen requests a second at a hundred apps if
it ran every pass — records what it found, and sends `app.drifted` once per
drift, by where it is rather than by the values, again only after the app has
matched once in between. `GET /api/apps/{id}/drift` (viewer) reads the cluster
now; `POST /drift/repair` (member, audited) is `Sync`: no build, no deployment
recorded. `PUT /drift {auto_repair}` has the watcher put it back by itself, off
by default, in the background, never during a rollout, and never twice for the
same drift. The app page says "Changed outside Skifity" and sends people to
Advanced, which lists each difference with the button and the switch.
`skifity drift [--repair]` does the same.

**Events.** `GET /api/apps/{id}/events` and `/api/databases/{id}/events` (viewer)
list the namespace's events for the app's objects: its Deployments and their
ReplicaSets and pods — by the names Kubernetes gives them, so an evicted or
OOM-killed pod that is already gone is still found, and web's are never
web-api's — its Service, Ingress, autoscaler, volumes and one-off runs. Repeats
are folded into a count, newest first, and the common warnings are explained:
FailedScheduling, BackOff, Unhealthy (pointing at the health check),
FailedMount, FailedAttachVolume, FailedCreatePodSandBox, OOMKilling, Evicted,
ErrImagePull and FailedPull. `explain.go`'s sentences now carry codes, looked up
as `events.explain.<code>` in all five languages, with a test that holds the
table and the locales together. The Advanced tab shows them with a Warning
filter and refreshes every ten seconds while open; a database has an Advanced
tab for its own. `skifity events [--warnings] [--db]` and the MCP tool
`get_events` read the same, scrubbed.

Tested, against Kubernetes' fake clients: an untouched object with the server's
defaults is in sync; a field another manager changed is reported with who and
when; the panel's own pending change, its own update, an autoscaler's and a
sleeping app's replica count are not; a removed field only on the object the
panel applied; a deleted object, and one the panel never wrote; a Secret hidden
and a secret variable redacted; repair applies exactly the rendered objects and
records no deployment; nothing compared during a rollout or a sync; one
notification per drift and again after it cleared; auto-repair once, audited,
never during a rollout; the events' ownership, folding, order, explanations and
redaction; viewer reads, viewer cannot repair, another team's are 404; the CLI
and the MCP tool against a fake panel.

Not executed: any of it against a real cluster — in particular which managers
real controllers (KEDA, cert-manager, Traefik) show up as on the fields the
panel sets, and the event reasons a real kubelet writes. Scheduled commands and
databases are not compared for drift.

### Bitbucket Cloud as a Git source

A connection can be to Bitbucket Cloud, with everything the other kinds do:
cloning a private repository, the repository and branch pickers, the webhook
registered by itself, pushes, tags and pull requests deploying, and a status and
one comment reported back. Coolify and Dokploy have Bitbucket; Kubero's parser
read GitHub's field names out of Bitbucket's payloads and marked every delivery
verified. Here a delivery is checked against `X-Hub-Signature` with the
connection's own secret before it is read, and refused unsigned.

* **Tokens.** An API token — with the Atlassian account's email, sent as Basic,
  or without, sent as Bearer — or a repository, project or workspace access
  token, which names its workspace. App passwords are not taken: Atlassian
  switched them off on 28 July 2026. The token is checked with Bitbucket before
  the connection is saved, and sealed with the email beside it.
* **Clone.** `x-token-auth` with the token, the name Bitbucket documents for
  both kinds. The name reaches the build from the clone Secret, beside the
  token, so the Job is the same for every host.
* **Listing** is workspace by workspace: Atlassian removed the cross-workspace
  `GET /2.0/repositories?role=member` in 2026. Pages follow Bitbucket's `next`
  link and only while it stays on the host that was asked.
* **Webhooks.** A push is every ref it moved, so `git push --follow-tags` deploys
  the tag. It lists no files, so watch paths never skip a Bitbucket push. A pull
  request names its head by twelve characters; the whole commit is asked of
  Bitbucket before the preview is built, and when that fails the preview builds
  its branch. A pull request from a fork gets no preview, and says why: Bitbucket
  Cloud keeps no ref for it in the repository it targets. A fork is decided by
  the repositories' ids, then their names, and anything missing is a fork.
* **Reporting.** Build statuses under a key made from the check's name, since
  Bitbucket refuses keys over forty characters; a preview's status names its
  branch so it shows on the pull request. The comment's marker is a Markdown
  reference, because Bitbucket prints an HTML comment as text.
* **Migration 0054** rebuilds `git_sources` for the new kind, with foreign keys
  off, so no app loses its connection.

Tested against a fake Bitbucket speaking API 2.0's shapes, and payloads built
from Atlassian's documented events. Nothing has talked to the real bitbucket.org:
not the token check, the lists, a clone, a webhook delivery, a status or a
comment. The BBQL search (`name ~ "…"`), a branch name with a slash in a
directory listing, and whether a status's `url` may be left out are read from
the documentation and not seen working.

### A team's own certificates

A team admin uploads a chain and its key; every HTTPS domain of the team whose
hostname it covers is served with it instead of Let's Encrypt. The upload is
checked in `internal/tlscert` (the key matches the leaf, the chain is put
leaf-first, expired, nameless, weak and encrypted keys are refused with why),
the key is sealed to the team and certificate, and no answer contains it.
Matching is by the certificate's names — a wildcard covers one label, and of
several the one expiring last wins. Those hostnames go in a second Ingress,
`<app>-own-tls`, with no cert-manager annotation and the same middlewares, and a
`kubernetes.io/tls` Secret per certificate that each sync prunes. Because
Traefik picks certificates by name for the whole cluster, a certificate naming
another team's hostname is refused. `certificate.expiring` is sent once at 21,
7 and 1 days. Not executed: two Ingresses for one app in a real Traefik beside a
real cert-manager.

### A team's own template catalogues

The checks the built-in catalogue's tests ran became functions in
`internal/templates/validate.go`, and a team's catalogue — an index, or a
`.tar.gz` or `.zip` of templates — is held to the same ones; a template that
fails is listed with its reasons and cannot be installed. Downloads go through
netguard, stay on https, drop the team's header when a redirect changes host,
and are capped in size, entries and templates. Each catalogue refreshes once a
day and keeps its last good copy. Not executed: a real catalogue downloaded from
a real host.

### Variables read from secret managers

A variable can point at a secret in Vault or OpenBao, Infisical, Doppler or AWS
Secrets Manager instead of holding a value. Connections sign in before they are
saved and their credentials are sealed; a reference is read once when it is
set. Values are read where the app's Secret is written, a build-time one is
part of the build fingerprint, and one that cannot be read stops the deploy or
sync before the cluster is touched. Refresh compares sealed digests and rolls
out or rebuilds only what changed. The drift check reads referenced values from
the app's Secret rather than asking the managers every five minutes. AWS SigV4
is written with the standard library and checked against AWS's published
vector. Not executed: any real manager.

### Passkeys

WebAuthn through go-webauthn: discoverable credentials, user verification
required, the relying party taken from the panel's configured address and never
the request's Host, so passkeys are offered only on https or localhost.
Challenges are single-use rows, a sign-in challenge is bound to the browser by
an HttpOnly cookie, a counter that does not go up is refused and audited, and
failures share the password lockout. The tests use a software authenticator
written from the specification rather than the library. Not executed: a real
browser and a real authenticator; conditional UI and Safari's user-gesture rule
in particular.

### GPUs

The Servers page reads each node's GPUs by vendor and how many are given out,
and says what is missing for a card Kubernetes cannot use yet. "Enable GPUs"
installs NVIDIA's device plugin, pinned by digest, in kube-system. An app asks
for cards for itself or its processes; builds, commands and previews never get
one, scale to zero is refused, and `NVIDIA_VISIBLE_DEVICES` is refused as a
variable because set by hand it hands an app every card. Not executed: a real
GPU, the plugin under the default seccomp profile, AMD and Intel beyond their
resource names, the install commands in `docs/gpus.md`.

### A database's life

Stop and start (CloudNativePG's hibernation for PostgreSQL), resize with the
engine's minimum, the quota and the storage class's expansion checked first,
a password change ordered so a failure leaves the old one working and the old
one kept until every linked app has rolled out, and a dump import of up to
5 GB with its format read from its first bytes and a backup taken first. Not
executed: any of it against a real engine, CloudNativePG or CSI driver; the
engines' two-password statements and mongosh's scripting in particular.

### DNS records at the provider

Cloudflare, Hetzner (its Cloud API), DigitalOcean and Route 53. A domain in a
connected zone gets its record, kept pointing at the cluster, and the panel
only ever touches a record in its own books with the same id and value and its
own note; somebody else's is left alone or refused by name. Route 53 and AWS
Secrets Manager now sign with one SigV4 package (`80be61f`). Not executed: any
real provider; Cloudflare's exact-name filter and Hetzner's rrset API in
particular. Wildcards through DNS-01 are left out.

### Log drains

Eight kinds of drain, sealed credentials dropped when the address changes, a
test line sent the way the collector sends. One Vector DaemonSet in its own
namespace routes by the namespace label only the panel writes, reads
`/var/log/pods` and nothing else of the host, and drops a dead drain's lines
rather than holding up another team's. `vector validate` passed on the rendered
pipeline outside the suite. Not executed: the DaemonSet on k3s, reading the
pods' log files as group 0, any real provider.

### Servers at Hetzner Cloud

A team's token, checked with a write probe that creates nothing; a key per
server so no password is set; a firewall per server with the cluster's ports
open to members only; the host key generated and pinned before the machine is
ordered, then replaced by the machine's own (ADR-0025); and the machine deleted
on removal only if the panel made it. Not executed: Hetzner itself, cloud-init
honouring the host key on its images, the key rotation on a real distribution.

### The first release

v0.1.0, cut at the maintainer's request before the cluster run
`docs/releasing.md` asks for; the README says so beside the install command.
The release pipeline had never run, and a local GoReleaser dry run (`c3d5131`)
found three things the tag would have got wrong:

* `Dockerfile.release` copied `skifity`, while GoReleaser's `dockers_v2` puts
  each platform's binary at `$TARGETPLATFORM/skifity`. The image build would
  have failed.
* The image was tagged `{{ .Version }}`, which drops the `v`, while the
  installer pulls `:v0.1.0` and the panel upgrades to the tag as typed. It is
  now published as `v0.1.0`, `0.1.0` and `latest`, and the binary reports the
  tag as `make build` does.
* The Windows binaries were named `.exe.exe`.

The image build was not exercised here, because this machine has no Docker
daemon; the tag's own workflow is its first run.

Writing the README's uninstall section found a fourth: every install ended by
naming `skifity-uninstall`, and nothing ever put it on the server. The
installer now installs it from the same release, from the clone or the tag,
parses it first, and names it only if it is there. The installer smoke test
covers both paths.

The README is rewritten around the install: the command, what the server
needs, the installer's options, upgrading and uninstalling, then what is
different, a selection of the scorecard against Coolify, Dokploy and Kubero,
and all thirty-eight screenshots.

**v0.1.0 was published on 2026-10-06**, tagged by the maintainer from the
Releases page on `f4c7afa` — pushing a tag from the session is refused, by the
GitHub proxy, which is not something to route around. The release workflow ran
for the first time and passed, provenance included. Checked from outside
afterwards: ten assets (six binaries, `install.sh`, `uninstall.sh`,
`checksums.txt`, `skifity.intoto.jsonl`); every checksum matches; the
linux-amd64 binary reports `Skifity v0.1.0 (commit f4c7afa, …)`; and
`releases/latest/download/install.sh` answers and is byte for byte the
installer at the tag. The release notes carry GoReleaser's changelog.

**Not yet right:** the image `ghcr.io/skifity/skifity` cannot be pulled
without signing in. Anonymously it answers `UNAUTHORIZED: authentication
required`, where a name that does not exist answers `DENIED` and an image that
is public answers with its digest, so it exists and is private — the default for
a package a workflow creates. Until its visibility is set to Public in the
package's settings, every install ends at the pull, and the panel never starts.
Nothing has pulled this image, started it or installed from it yet, so the
tags (`v0.1.0`, `0.1.0`, `latest`) and the two architectures are what the
workflow says it pushed and not something seen.

### What was not yet dependable

A pass over what was written but would not hold up, after the release was
prepared. Each item says what it fixes and what still has not run.

* **An install onto an existing k3s** (`SKIFITY_SKIP_K3S`) returned before
  writing the registry mirror, so no image the panel built could ever be
  pulled, and refused any server without systemd though only the k3s it
  installs needs it. Both fixed; the smoke test covers the mirror, the
  ingress configuration and the restart message (`ae35ec9`).
* **The release image carried no CLI for other platforms**, so on a
  released install the panel refused a Mac or a Windows laptop its CLI.
  `scripts/release-cli.sh` puts them in the image as `make image` does. The two
  SLSA workflows were GitHub's unedited templates — provenance for files called
  `artifact1`, a Go 1.17 build of a config that did not exist — and are replaced
  by a provenance job in `release.yml`. `release-dry-run.yml` builds the whole
  release, images included, on every change to what it is made of, and looks
  inside both images; its first run passed (`2fb53d8`). This is the first time
  the release image has been built at all.
* **A deployment cut off by a panel restart** used to be failed with "deploy
  again". It is picked up again, once, from where it stood: a build starts
  again, a built image goes to the rollout. Twice is `deploy.interrupted`, now
  in the error catalogue in five languages (`d1e8042`).
* **A volume backup ran as uid 1000** whatever the app ran as. It runs as the
  app's own uid with the app's group; where the environment allows root, as root
  with only the four capabilities that read and write anybody's files, so a
  restore keeps every owner (`7854d55`). Not executed against a real volume.
* **Images that floated**: `alpine:3` and `registry:3` are pinned by version and
  digest, and the transfer image that holds a presigned URL gets a digest
  (`9c45afd`).
* **One cloud provider.** DigitalOcean beside Hetzner Cloud, through the same
  interface, with a fake of its API that the whole order, pin, rotate and join
  flow runs against (`cba585e`). Neither has ordered a real machine from here.
* **Two tests that failed one time in ten.** The Redis restore test's stubs
  wrote their log in five writes each, so a background server's record and a
  client's interleaved, and the stub client answered a ping before the server
  had started. CI's race step failed once on `7854d55` and passed on the same
  code after; this is the likeliest reason (`93a4653`). The new resume test now
  waits for the run it started before reusing its row (`f9522cd`).

Still open, and why:

* **Nothing has run against a real cluster.** Asked for, with the maintainer's
  say-so in so many words ("you run it, I have no access"), and refused again:
  the session's auto-mode classifier denied installing k3s on this machine
  ("Security Weaken"), as it had once before, and nothing was installed. What a
  read-only look at the machine established, so the next attempt does not start
  from nothing:
  * It is a Firecracker VM, Ubuntu 24.04, kernel 6.18, 4 CPUs, 16 GB of memory
    and about 14 GB of free disk. Registries, `get.k3s.io` and the k3s release
    assets are reachable through the session's proxy.
  * The kernel has overlayfs, netfilter with NAT and conntrack, bridge, veth,
    vxlan, user namespaces and seccomp. It has no WireGuard, so the installer
    would choose vxlan, which is the fallback nobody has exercised either.
  * Its cgroups are v1 only. The `stable` k3s is v1.36.5 today, and the kubelet
    refuses v1 by default from Kubernetes 1.35, so a run here needs
    `failCgroupV1: false` for the kubelet or an older channel. A real VPS has v2.
  * There is no systemd — process 1 is not an init system — so the installer's
    systemd check refuses, by design, unless k3s is already running and the
    installer is given `--skip-k3s`.
  * There is no Docker, but the panel image does not need it: distroless plus
    one layer holding the binary, assembled with `crane`, came to 27 MB with
    the same user, working directory, entrypoint and command as
    `Dockerfile.release`.
  * `CAP_SYS_RESOURCE` is missing from the effective capabilities, which may
    matter to pods that ask for a negative OOM score.
  It stays open until a Bash permission rule lets the session do it, or until
  `make verify-remote HOST=root@<a VPS>` is run on a server that can be
  rebuilt.
* **Wildcard certificates over DNS-01.** A team can upload its own wildcard
  certificate, and the panel's wildcard domain gives every app a name under it;
  issuing one automatically needs a DNS credential at the panel's level, where
  DNS providers are the teams'. A decision before code.
* **The interactive terminal** stays a decision (`docs/roadmap.md`), and **the
  panel's `cluster-admin`** an architecture one (ADR-0014).

### The installer, run from start to finish

Every step of `installer/install.sh` had been tested on its own, and none of
them together. The smoke test now sources the installer, moves every path into
a directory of its own, puts stand-ins for kubectl, curl, systemctl, k3s's
installer, ufw and iptables on the PATH, and runs `main` from the options to the
last line: a fresh install behind NAT, a second run after setup, an install
with a domain, a second run of that one, a panel its address does not reach,
and an untested system with nobody to ask. Running it all together found what
running the steps apart never could:

* **No real install could have finished.** `copy_cluster_token` called `run`,
  which the installer never defined — it lives in the uninstaller. Under
  `set -e` every install stopped at "Giving the panel this cluster's join
  token" with `run: not found` and no explanation. The test fails with exit 127
  when it is put back.
* **`/etc/os-release` overwrote the release being installed.** It was sourced
  into the installer, and it sets `VERSION`. It is read in a subshell now.
* **A manifest that failed to download was applied empty.** The fetch was the
  left side of a pipeline, which `sh` does not stop for. It is downloaded to a
  file first, with retries.
* **A second run moved a panel with a domain back to plain HTTP** on an sslip.io
  name, whenever it was run without the first run's options — which is how
  anybody upgrades. It reads the address the panel answers on, and its
  certificate, from the cluster, and keeps them.
* **A second run handed the panel an empty pod network.** The choice was only
  made when k3s was being installed. It is read from the installed k3s's unit
  and configuration now.
* **A question read its answer from the script.** Under `curl | sh`, stdin is
  the rest of the installer; questions go to `/dev/tty`. With no terminal, the
  answer used to be yes; it is no now, and `--yes` says yes.
* **k3s's installer was downloaded to a fixed name in `/tmp`** and run from
  there as root, which another local user could have replaced first. It goes
  to a private temporary directory, and its stdin is closed.

And what it did not do at all:

* **Options.** `--domain`, `--email`, `--public-ip`, `--version`, `--image`,
  `--yes` and the rest, after `sh -s --`, each checked before anything changes;
  `--help` without root. Every environment variable still works.
* **A domain is offered** on a fresh install with a terminal.
* **NAT.** A private node address is looked up from outside once
  (`api.ipify.org`, then `icanhazip.com`), and both are named.
* **The host firewall.** An active ufw or firewalld, or iptables that rejects by
  default (Oracle Cloud's images), is opened for 80, 443 and the pod and service
  networks, the way the panel opens the servers it adds. A Go test keeps the
  two networks the same as `internal/kube`'s.
* **One install at a time**, with a lock that a killed install does not leave
  stuck, and an interrupted one says that running it again finishes it.
* **The whole script is one function called on the last line**, so a download
  cut off halfway runs nothing.
* **A check that the panel answers at its own address**, through the ingress,
  before the link is printed; and after setup, a second run prints where to
  sign in instead of a token that stopped working.
* **Warnings** for an unsynchronised clock and for under 2 GB with no swap; the
  log is readable by root alone; the certificate is waited for and named when it
  is late.

None of this has run against a real k3s either. The stand-ins answer the way
kubectl and curl were read to answer, which is the same distance from a real
run as every other test here.

**The install command names no version.** Every release has `install.sh` and
`uninstall.sh` attached and listed in `checksums.txt`, so
`releases/latest/download/install.sh` is always the newest installer, and each
installs its own release; the dry-run workflow checks both files are there.
`--version latest` asks GitHub for the newest tag by following the
`/releases/latest` redirect, not the API, whose anonymous limit a shared address
can already have spent. Until the first tag is pushed that link is a 404: the
tag has not been pushed from here, because pushing tags is refused.

**The uninstaller got the same treatment**, and the same kind of finding. Its
removals had only ever been tested as a dry run. They run for real now, against
stand-ins for kubectl and k3s's uninstall script, and what that fixed:

* **"Panel removed" was printed whatever happened.** Every `kubectl delete` ended
  in `|| true`, so a namespace that would not go, or a k3s that was not running,
  still ended in success. A failed step is named now, the others still run, the
  exit status is not zero, and the summary says the removal was not complete.
  A stopped k3s is reported, with how to start it, instead of skipped.
* **Without a terminal the answer was empty, which happened to be no.** It is
  now said out loud, and `--yes` is how a script goes ahead. Questions go to
  `/dev/tty`, as in the installer.
* **`--all` deleted every app and volume on a yes.** It asks for a typed `remove
  k3s` and says how many environments are on the cluster; `--purge` keeps its
  `delete my data`.
* **It could run under an install.** It refuses while the installer's lock is
  held by a live process.
* **`rm -rf` took whatever it was pointed at.** `/`, a top-level directory, an
  empty or relative path, or one with `..` in it are refused.
* **Nothing said what was left.** The summary lists what is still on the
  server and the command that removes each part, and `--all --purge` removes
  the uninstaller itself when nothing else is left and nothing failed.
* **Its output went nowhere.** What kubectl and k3s said is in
  `/var/log/skifity-uninstall.log`, readable by root alone.

A Go test keeps the names and the label it deletes by the same as the code that
creates them. None of it has run against a real k3s.

**Two claims in the README were wrong, and one gap was behind them.** It said
the panel is upgraded "from Settings", and that running the installer again
"does the same". Neither was true: the Settings page shows the current version
and nothing to upgrade with — the only panel upgrade is `POST /api/upgrade` —
and the installer changed the image and nothing else. The panel's own upgrade
copies the database first, because a newer version migrates it and an older one
refuses it, so an upgrade made by the installer had no way back. The installer
now takes the same copy, through the CLI of the version that is running,
before it replaces the image; stops if it cannot, unless `--no-snapshot`; keeps
three; and prints the four commands that go back. The README, the release notes
(which also suggested `kubectl set image`, which skips the copy), the releasing
guide and the configuration page say what is true. There was still no
upgrade button, and the next paragraph is what was built when the maintainer
said to.

**An upgrade control, which makes the README's claim true.** Settings → Upgrade
has **Check for updates**, and when there is a newer release, **Upgrade to** it,
behind a confirmation that says the database is copied first and the commands
that go back are shown afterwards (they are, from the panel's own answer, as a
list of commands rather than a sentence). `skifity upgrade` asks,
`skifity upgrade --latest` moves to the newest when it is newer, and
`--to v0.2.0` names one. What it asks, and when, is the decision the README had
to be honest about: the panel **never checks on its own** — no timer, no
start-up check, and a test fails if the Settings page or a plain `GET` sends
anything — and asks only when a panel administrator presses the button or calls
`POST /api/upgrade/check`. A POST, so a prefetch or a crawler cannot trigger
it. It follows GitHub's redirect from `/releases/latest`, not the API, whose
anonymous limit a shared address has often spent, through the same guarded
client as everything else the panel dials, and sends nothing but the request.
The repository it asks about is `SKIFITY_UPDATE_REPOSITORY`, which the installer
fills in from the repository it came from (`__REPOSITORY__` in `deploy/panel.yaml`),
so moving the project is still one line; with it empty the check says there is
nowhere to ask. Two new errors, in five languages, and the README, FAQ,
configuration and CLI pages now say "never on its own" instead of "never".

What was run: the redirect parser, the version comparison (including
`git describe` builds and pre-releases) and the handler against stand-ins; the
real binary through Playwright, which sees exactly one request, after the
button; and `lookupLatestRelease` against github.com, for the shape of a
repository with no release, which is what this repository returns today. What
was not: a repository that has a release — this machine's proxy refuses to
fetch other repositories — so that shape is GitHub's documented redirect and
the stand-in's, not an observation; and the upgrade itself, which needs a
cluster.

**A data race in CI, not reproduced.** The race step failed once on `ed91c10`, in
`TestAVariableFromASecretManagerNeverShowsItsValue`, and passed on the three
commits before it and on the same code run here ninety times alone and three
times as the whole package. The CI summary named the test and nothing else:
the race detector's report, which is the diagnosis, is printed above the
`--- FAIL` line the annotations were built from. So the script now turns each
report into an annotation of its own, forty lines of it, and the next failure
will say who wrote and who read.

What is known is a hazard in the test, not a proven cause: it makes a plain
`bytes.Buffer` slog's default logger so that nothing can log around the
panel's, and reads it back at the end, so any goroutine in the process
writing a log line at that moment — including one left running by an earlier
test — races with the read. The buffer is locked now, in that test and the
passkey one that does the same without the global. If the race was somewhere
else, the annotation will show it.

The README no longer measures Skifity against other products: the comparison
table and the paragraphs that named them are gone, and what Skifity does is
said on its own terms. The FAQ, the log drains page and the templates page
lost their comparisons too; the templates page keeps its credit to the
catalogue its conversions came from, which the licence asks for.

## Idle resource usage

`docs/performance.md`. The panel is measured: 34 MiB resident idle, 38 MiB after
400 requests, 39 MiB of binary. k3s's own footprint is not measured here, for
the same reason as everything else that needs a cluster.
