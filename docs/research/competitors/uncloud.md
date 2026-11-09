# Uncloud (with Unregistry)

> **Status.** Written on 2026-11-08 against Skifity's code at `23ec07f`, Uncloud's `main` at
> `ffd9309` (v0.22.0, released 2026-11-06, and the nightly built from the same day) and
> Unregistry's `main` at `f379139` (v0.4.3). Nothing named here has been changed in Skifity yet;
> the gaps below are proposals.

Uncloud (`psviderski/uncloud`, uncloud.run) is "a lightweight clustering and container
orchestration tool" that turns a handful of Docker hosts into one cluster without Kubernetes, Swarm
or a control plane. The homepage calls it "Multi-node Docker Compose for production". It is the
most-discussed answer to the question Skifity answers the other way: its README promises to
"deploy and scale containerised apps across servers without Swarm or Kubernetes overhead", and its
author's stated reason is "years of managing Kubernetes in small envs and at a unicorn" (HN,
2025-12-04). It is for developers who already know Docker Compose and want several servers, cloud
and home machines mixed, with HTTPS and service discovery, and nothing else to run. **Unregistry**
(`psviderski/unregistry`) is its companion: a container registry whose storage is the Docker host's
own containerd image store, plus `docker pussh`, a Docker CLI plugin that pushes an image to a
remote host over SSH and transfers only the layers the host lacks.

**Architecture.** There is no server-side control plane. The `uc` CLI runs on a laptop or in CI and
speaks gRPC to one machine's `uncloudd` daemon, tunnelled over SSH (`ssh ... uncloudd dial-stdio`,
the system `ssh` client by default since v0.18.0); that daemon forwards requests to the other
machines with `grpc-proxy` over the cluster's WireGuard network. Every machine is equal and keeps a
full copy of the cluster state in **Corrosion**, Fly.io's CRDT-replicated SQLite with gossip,
which since v0.20.0 runs as a Docker container from Uncloud's own fork. Each machine gets a `/24`
out of `10.210.0.0/16` for itself and its containers, the Docker bridge network `uncloud` is routed
over a WireGuard mesh, and containers on different machines reach each other directly by IP. Each
daemon also runs an internal DNS server for `<service>.internal` names, an embedded Unregistry on
the machine's address (port 51500) and, since v0.20.0, a Prometheus endpoint (port 51090). Ingress
is **Caddy**, run as a global service on every machine, its Caddyfile generated from the cluster
state; since v0.22.0 the image is `ghcr.io/unlabs-dev/caddy-uncloud`, which keeps certificates in
the cluster store. An optional hosted service, **Uncloud DNS** (`dns.uncloud.run`, backed by AWS
Route 53), hands each cluster a name such as `*.xuw3xd.uncld.dev`. Uncloud's design document puts
the trade plainly: it "favors Availability and Partition tolerance", and containers "won't be moved
to other machines automatically" (`misc/design.md`). The code is about 86,000 lines of Go, 34,000
of them in tests, generated protobuf code included.

**Licence:** Apache-2.0 (Uncloud, Unregistry, `uncloud-dns` and `caddy-uncloud`). **Pricing:** free.
There is no pricing page (`uncloud.run/pricing` answers 404); the project takes GitHub Sponsors and
lists Sentry as a sponsor. The author's plan (newsletter, 2026-01-25) is "a self-hosted control
center and web UI" and a paid "hosted managed option (bring your own machines)" for "Day 2
operations: observability, monitoring, alerts, backups and recovery"; neither exists yet.
**Maturity:** repository created 2024-07-20; first tag v0.0.1 (2024-09-23), first release v0.1
(2025-01-24), then a minor release roughly every month to **v0.22.0 (2026-11-06)**, still pre-1.0:
"expect occasional breaking changes between releases" (README). v0.20.0 (2026-06-26) required the
CLI and every machine's daemon to be upgraded together. A nightly channel exists since v0.19.0.
Unregistry is at v0.4.3 (2026-06-12), with its last commit on 2026-07-14. **Adoption** (GitHub
search API, 2026-11-08): Uncloud 5,604 stars, 184 forks, 72 open issues, 17 open and 112 merged pull
requests; Unregistry 4,893 stars, 97 forks, 17 open issues. Two Hacker News front pages: "Show HN:
Unregistry" (2025-06-18, 726 points, 164 comments, reached #1) and "Uncloud – Tool for deploying
containerised apps across servers without k8s" (2025-12-04, 403 points, 181 comments). The author
counted "300+ community members on Discord", 14 contributors and 13 sponsors in January 2026, and
in April 2026 said "Myself and a handful of others from our Discord run it in production" (HN,
2026-04-23); the largest named user is Radboud University's science faculty IT, which announced
trials of it for faculty container hosting on 2026-04-15. **People:** Pasha (Pavel) Sviderski,
full time "from my savings" and with no plans for venture funding (HN, 2025-12-04), wrote 1,130
of the 1,287 commits on `main` under two spellings of his name; Anton Ovchinnikov (52), Miek
Gieben (39) and Justin Bradford (21) follow.

## Feature inventory

### What shipped, release by release

Dates are GitHub's publication dates (through the ungh.cc mirror of the releases API); release notes
from v0.16.0 on are also in the repository under `website/docs/10-release-notes/`.

| Release | Date | What changed that matters for a comparison |
|---|---|---|
| v0.5.0 | 2025-03-31 | First Compose support: `uc deploy` reads a Compose file and plans the deployment |
| v0.6.0 | 2025-04-25 | Volumes across machines, contexts for several clusters, CPU and memory limits; config moved from TOML to YAML (breaking) |
| v0.7.0 | 2025-05-01 | Internal DNS for `<service>.internal`, Compose profiles; v0.7.1 adds an `UNCLOUD-INPUT` iptables chain |
| v0.8.0 | 2025-06-12 | `uc build` and build-then-push inside `uc deploy` ("still experimenting"); Caddy passive health checks and three retries |
| v0.9.0 | 2025-07-09 | Docker 28.2 broke cross-machine traffic; the fix covers new machines only, existing ones need a migration script that "may cause temporary service disruption" |
| v0.10.0 | 2025-08-04 | `uc machine rm`, `rename`, `update`; `x-machines` placement |
| v0.12.0 | 2025-08-28 | `x-caddy`: a Caddyfile per service with `{{upstreams}}` templates, and a global one; v0.12.2 tries each saved connection in turn, adds `--yes` and `--no-install` |
| v0.13.0 | 2025-10-10 | **Unregistry built in**: `uc image push` to one or every machine, missing layers only; `uc images`; Compose configs; containerd image store by default on new machines |
| v0.14.0 | 2025-11-12 | **`uc deploy` from source**: builds locally, tags with the Git version, pushes to the machines, deploys; `uc exec`; GPUs; `nearest.<service>.internal` |
| v0.15.0 | 2025-12-12 | `uc logs` across replicas, `uc ps`, `uc start`/`stop`, the `ssh+cli://` scheme, HTTP/3, the shorter `uncld.dev` domain |
| v0.16.0 | 2026-01-13 | `uc wg show`; `cap_add`, `cap_drop`, `sysctls` |
| v0.17.0 | 2026-03-01 | **Health monitoring and rollback during rolling deploys**, Compose `healthcheck`, `update_config.order`, `devices`, `ulimits`; v0.17.1 adds `--wg-endpoint` and `stop_grace_period` |
| v0.18.0 | 2026-04-10 | **Pre-deploy hooks** (`x-pre_deploy`), a redesigned deployment plan, system `ssh` as default, `x-context` to pin a file to a cluster |
| v0.19.0 | 2026-04-24 | A failed deploy prints the failed container's last log lines; `uc machine logs`, `uc machine rtt`; nightly builds; the install script sent over SSH instead of `curl \| bash`; `StrictHostKeyChecking=accept-new` |
| v0.20.0 | 2026-06-26 | **Breaking.** Secrets (`secret://` resolved at deploy time), `uc proxy` port forwarding, Prometheus metrics, OS/kernel/arch/Docker/daemon versions in `uc machine ls`, Corrosion as a container with API authentication |
| v0.21.0 | 2026-10-02 | **Shared certificate storage for Caddy** (opt-in), `uc caddy cert ls`, systemd socket activation, runtime templates for bind mounts, `uc dns set` for a domain of one's own |
| v0.22.0 | 2026-11-06 | Cluster storage for Caddy by default (certificates reissued unless migrated first), service and container labels, `uc service update` changing CPU and memory without a restart, `uc image rm`, DNS names for machines, a fix for reconcile storms that could "freeze small machines" |

### Deploy sources and builds

* **A Compose file is the deployment.** `uc deploy` reads it, inspects what runs, computes a plan,
  shows it and asks for confirmation (`-y` or `UNCLOUD_AUTO_CONFIRM=true` for CI). Running it again
  after editing the file deploys the difference. A support matrix lists each Compose key as
  supported, limited or not; `networks`, `deploy.placement`, `restart_policy`, `rollback_config`,
  `security_opt` and external configs are not supported, `depends_on` only orders deployment.
* **Builds happen where `uc` runs**, with the local Docker: a service with `build:` is built, tagged
  `<project>/<service>:<git date>.<short sha>` or with a Go template (`gitsha`, `gitdate`,
  `.Git.IsDirty`), and pushed. Multi-platform builds need the containerd image store locally. Build
  secrets are not supported. There is no Git integration, no webhook and no build on the servers.
* **Images reach the machines through Unregistry**, transferring only missing layers, so a laptop or
  a CI runner needs no registry account (`uc image push IMAGE -m all`). Pre-built images are pulled
  from registries by `pull_policy`; credentials for a private registry are taken from the local
  `~/.docker/config.json` and passed to the machines (v0.11.1).
* `uc run` starts one service from an image without a Compose file (`-p app.example.com:8000/https`).
* **Pre-deploy hooks** (`x-pre_deploy`, v0.18.0): a one-off container with the service's image,
  environment, volumes and placement runs before the rollout, with a five-minute default timeout; a
  failure stops the deploy and prints the hook's logs.
* **No rollback command.** "Rollback" means the per-container undo during a rolling deploy;
  `rollback_config` is unsupported (#151), and going back means deploying an older tag.
* `--recreate` forces new containers, `--skip-health` skips monitoring, `--no-build` deploys
  configuration only.

### Domains, TLS and routing

* Caddy runs on every machine on 80, 443 and 443/UDP. `x-ports: app.example.com:8000/https`
  publishes a service; Let's Encrypt certificates are automatic.
* **Free names:** `uc machine init` reserves `<6 characters>.uncld.dev` from Uncloud DNS unless
  `--no-dns`, and points `*.<name>.uncld.dev` at the machines whose Caddy answered a reachability
  check from outside (a random token at `/.uncloud-verify`). A service published without a hostname
  is `https://<service>.<name>.uncld.dev` from its first deploy. The records are refreshed only when
  the CLI runs `machine init`, `machine add`, `caddy deploy` or `dns reserve`. Since v0.21.0
  `uc dns set apps.example.com` uses a domain of one's own instead; its records are the user's.
* **`x-caddy`** replaces `x-ports` with a full Caddyfile for the service, re-rendered when containers
  start, stop or change health; a global Caddyfile is deployed with `uc caddy deploy --caddyfile`;
  `uc caddy config` shows what Caddy actually loaded. Redirects, basic auth, headers, path routing and
  load-balancing policy are all done this way.
* **Certificates shared across machines** (v0.21.0 opt-in, v0.22.0 default): certificates, keys and
  ACME challenge tokens live in the cluster store with distributed locks, so any machine can answer a
  challenge and use a certificate another obtained (issue #31, opened 2025-02-27). Switching storage
  reissues every certificate unless they are first copied by a six-step procedure, which can hit
  Let's Encrypt rate limits on a large cluster.
* The v0.22.0 Caddy image includes the `caddy-l4` and Cloudflare DNS modules, so a wildcard
  certificate through a DNS challenge is a Caddyfile away.
* Passive health checks, three retries, unhealthy containers taken out of the upstreams; since
  v0.19.0 local replicas are listed first.
* TCP and UDP only in host mode (`5432:5432/tcp@host`), bound per machine; L4 ingress is open (#108).
* No firewall, rate limit or maintenance page beyond what a Caddyfile can do.

### Databases and services

* **No managed databases.** A database is a Compose service with a volume. Issue #78, "Hosting
  PostgreSQL" (2025-06-20), is open with no answer in the docs; the recipes repository has a
  PostgreSQL, a Stolon high-availability PostgreSQL and a WordPress with MariaDB. "Self-managed
  databases as the first-class citizen (taking inspiration from Fly.io's Postgres support)" is a 2026
  topic in the author's newsletter.
* `uc proxy` (v0.20.0) forwards any service port to a local port, "similar to `kubectl
  port-forward`".

### Storage and backups

* Docker named volumes, bind mounts and tmpfs; volume drivers, NFS and CIFS among them; external
  volumes. A volume belongs to one machine and a service is scheduled where its volume is. Global
  services create their volume on every machine (v0.17.0). Runtime templates give each replica its
  own bind path (v0.21.0).
* **No backups** of volumes or of anything else. Radboud's IT supplies "an S3 backup and a
  ready-made container" of its own. "Modern persistent volumes with snapshots, incremental backups,
  and replication" is another 2026 topic.
* The cluster's own state is on every machine, which the README calls "a full backup of your control
  plane".

### Scaling and high availability

* Replicated services (`replicas`, `uc scale`) spread evenly across machines, the first picked at
  random; global services run one container per machine; `x-machines` pins a service.
* **No rescheduling and no autoscaling, by design.** "There is no automatic rescheduling in uncloud by
  design ... If you want your service to tolerate a host going down, you should deploy multiple
  replicas for that service on multiple machines in advance"; autoscaling "is out of scope at least
  for now" (author, HN, 2025-12-04/05). Placement ignores free resources.
* **Rolling deploys** replace one container at a time, starting the new one first, and switch to
  stopping the old one first for a single replica with a volume or a host port. Each new container
  is watched for 5 seconds (`update_config.monitor`), longer if it has a health check; one that
  restarts or stays unhealthy is stopped and kept for inspection, the old one is kept or restarted,
  and the deploy stops. Running `uc deploy` again resumes. `stop_grace_period` is supported;
  draining Caddy connections before a container stops is not (#38).
* Ingress availability is DNS: several A records give round robin, "not failover"; the author
  recommends a load balancer or Cloudflare's proxy.
* No drain of a machine before removing it (#417, 2026-07-22, open).

### Observability (logs, metrics, alerts, uptime)

* `uc logs` merges every replica in timestamp order, follows, filters by machine, container and time
  (`--since 2d` since v0.22.0); `uc ps` and `uc inspect` show containers, health and addresses.
* `uc machine logs` streams the journald logs of `uncloud`, `uncloud-corrosion` and `docker` from
  every machine; `uc machine rtt` shows the median round trip between every pair of machines from
  Corrosion's gossip; `uc wg show` lists WireGuard peers, endpoints and handshakes with an RTT column;
  `uc caddy cert ls` lists certificates and expiry.
* Each daemon serves "the initial set of Prometheus metrics" on its internal address (v0.20.0).
* No dashboards, metrics history, alerts or uptime checks; those are what the planned paid option
  is for. Containers log to Docker's `local` driver, 100 MB each (v0.9.0).

### Security, auth, roles, SSO, audit

* **Access is SSH.** Whoever can SSH to any machine as root or as a member of the `uncloud` group
  controls the whole cluster; the docs say to treat the API socket "like the Docker socket". There
  are no accounts, roles, SSO, API tokens or audit log. Giving a colleague access means giving them
  SSH and a copy of `~/.config/uncloud/config.yaml` (HN, 2025-12-04).
* Between machines, the gRPC API (port 51000) and Corrosion's gossip (51001) are accepted from the
  management IPv6 range on the WireGuard interface, with no authentication of their own: membership
  of the mesh is the credential. Corrosion's local API has required authentication since 2026-05-26
  (#110, in v0.20.0).
* **Secrets** (v0.20.0): `secret://name` in `environment` is resolved on the machine running
  `uc deploy`, by reading a file or running a command (`x-command: op read ...`). The docs say the
  values are "stored unencrypted" in each container's Docker configuration, where "anyone with
  `root` access to that machine or able to run `docker inspect` can read them". Mounting a secret as
  a file is not supported (#442).
* No network isolation between services or Compose files: "there is no network isolation between
  containers from different services/stacks at the moment" (author, HN, 2025-12-05; discussion #94).
* SSH host keys: `StrictHostKeyChecking=accept-new` since v0.19.0.

### Preview environments and branches

* None. Separate contexts or clusters, `x-context` and several Compose files serve staging and
  production.

### Templates and catalogue

* `uncloud-recipes`: seven folders on 2026-11-08 — NATS, PostgreSQL, Stolon PostgreSQL, WordPress
  with MariaDB, services outside the cluster, four media apps (Jellyfin, an *arr stack, FlexGet,
  qBittorrent) and a proof-of-concept web UI that runs `uc` read-only — last changed 2026-09-21.
  Most Compose files from elsewhere work with `x-ports` added.

### CLI, API, IaC, integrations

* The CLI is the product: `machine` (init, add, ls, rm, rename, update, logs, rtt), `run`, `deploy`,
  `build`, `scale`, `service update`, `start`, `stop`, `exec`, `logs`, `ps`, `inspect`, `rm`,
  `image` (push, ls, rm), `volume`, `caddy`, `dns`, `ctx`, `wg`, `proxy`; shell completion, man pages
  (`uc docs --manual`), JSON output on some lists.
* The API is gRPC over a Unix socket, with a Go client in `pkg/client`. There is no HTTP API, no MCP
  server and no Terraform provider ("It should be pretty straightforward to wrap the CLI logic in a
  Terraform or Pulumi provider", author, HN).
* CI: `uc --connect user@host deploy` with no config file, and a third-party GitHub Action
  (`thatskyapplication/uncloud-action`, 9 stars).

### Notifications

* None.

### Multi-server and networking

* `uc machine init user@host` and `uc machine add` SSH in, install Docker with Docker's convenience
  script (`curl -fsSL https://get.docker.com | sh` in `scripts/install.sh`) and `uncloudd` from
  GitHub releases, and need root or passwordless sudo; `--no-install` leaves provisioning to the
  user. Unofficial Debian packages exist.
* **WireGuard mesh with endpoint discovery.** Each machine advertises every address it has; a peer
  that does not complete a handshake is tried at the next address; a 25-second keepalive and
  WireGuard's roaming let a machine behind NAT be learned from its outbound connection;
  `--wg-endpoint`, `--wg-port` and an MTU override cover the rest (v0.17.1, v0.20.0). There is no
  STUN and no relay in the code: two machines that cannot reach any of each other's addresses have
  no path. Homelab users report forwarding ports 80 and 443 on the router and reaching machines over
  Tailscale for SSH (suranyami.com, 2026-01-04).
* Partitions keep working: each side can be managed alone and converges when the network heals.
* Upgrading the daemon is a `curl`, `install` and `systemctl restart` on each machine, or re-running
  `machine init`/`add` against it (v0.20.0); a cluster-wide upgrade command is open (#142).
* Linux machines only; the CLI runs on macOS and Linux, Windows through WSL (#200, #209 open).
  IPv6 for containers (#126) and Podman (#101) are open.
* **Footprint:** "no more than 150 MB of RAM and a few percent of a CPU core in small setups ... You
  can run Uncloud on machines with as little as 512 MB of RAM" (docs overview).

### Team and collaboration

* None beyond SSH. Service names are global across every Compose file deployed to a cluster.
  Radboud's announcement makes a virtual machine "the security boundary": each group gets its own
  cluster.

### Developer experience and onboarding

* `brew install psviderski/tap/uncloud` or a `curl | sh` for the CLI, then `uc machine init
  root@<ip>` and `uc run -p app.example.com:8000/https image/my-app`; with the reserved name, the
  app has an HTTPS address before any DNS is set up.
* Interactive tutorials and a playground on iximiuz Labs; a documentation site with a Compose
  support matrix and generated CLI reference.
* No web interface; the author describes the project as "lower-level, CLI-only (for now)".

## What users love

* **Compose, on several machines, without Swarm.** "Uncloud [1] fixed all those issues for me and is
  (mostly) Compose-compatible" — remote management, growing past one host, TLS boilerplate (HN,
  2026-05-05). "I am stunned at how easy it was to get working ... This is honestly the easiest way to
  self-host anything I've found" (suranyami.com, 2026-01-04, migrating a homelab from Swarmpit).
  A Swarm user in the launch thread called it "the first alternative that looks interesting to me".
* **Familiar parts that can be debugged.** "It's all built on top of simple and familiar technologies
  ... if something goes wrong, or even if the entire project goes under, I will be able to debug
  myself. I cannot say the same for SwarmKit or etcd" (user testimonial in the January 2026
  newsletter).
* **No quorum to keep.** "Losing etcd quorum results in an unusable cluster. Many people want to avoid
  this" (author, HN, 2025-12-04); the homepage quotes a user calling it "the only robust alternative
  to the bloat of K8s".
* **Unregistry solved a real annoyance.** "This should have always been a thing!" (HN, 2025-06-18);
  the top comment of that thread was from Docker's creator ("Docker creator here. I love this",
  quoted in the newsletter). A Kamal user filed it for Kamal the next day (kamal #1588).
* **A maintainer who answers.** "I've had more contact with its devs here than in years of using
  swarm" (HN, 2025-12-05).

## What users complain about

* **`curl | bash` as root.** "Lot's of `curl | bash` run as root ... this is not something I would run
  on any machine" and "It's ridiculous to pipe curl | bash in the actual code" (HN, 2025-12-04). The
  author agreed; v0.19.0 sends the script over SSH, but it still pipes `get.docker.com` into `sh` and
  downloads `uncloudd` without checking a checksum.
* **The SSH user is root in all but name.** #425 (2026-08-20): "I don't like having a user with
  paswordless sudo privileges, or a user with direct access to the docker socket, which is
  essentially equivalent."
* **What a control plane would do.** No rescheduling when a machine dies, no autoscaling, no placement
  by free resources (HN, 2025-12-04); no machine drain (#417); no `uc down` to remove what a
  Compose file deployed (#369, 20 comments).
* **No isolation between stacks**, which Swarm users with per-stack networks miss (HN, 2025-12-05).
* **State and mesh bugs that are hard to see.** A machine added to a cluster with old Corrosion gaps
  "never completes initial store sync", never starts its DNS server, and "a web app on the new machine
  restarted 632 times" while `uc machine ls` showed it `Up` (#434, closed 2026-09-25). Docker 28.2
  silently cut cross-machine traffic on existing machines (v0.9.0). Container changes triggered a
  full reconcile on every machine and "could burn a lot of CPU and even freeze small machines" (#438,
  fixed in v0.22.0). A custom global Caddyfile was lost when the daemon regenerated the config (#412).
* **Not production-ready for everyone**, in the author's words: "The lack of certain capabilities such
  as automated rollbacks can be a deal-breaker for some use cases" (newsletter, 2026-01-25); the
  per-container rollback arrived in v0.17.0 two months later, and nothing more since.
* **Smaller gaps:** IPv6 (#126, 5 votes), Podman (#101, 5), image pulls from other machines (#168,
  5), a daemon upgrade command (#142, 4), L4 ingress (#108, 4); "I actually tried uncloud. Believe it
  or not I was too dumb to get it to work" (HN, 2025-12-21).
* **Kubernetes people do not see the point.** "Since k8s is very effective at running a bunch of
  containers across a few machines, it would appear to be exactly the correct thing to reach for"
  (a reply to the author's opening comment, 2025-12-04) — the argument Skifity makes, from the
  other side.

## Security record

* **Advisories:** none found. OSV has nothing for either Go module and NVD's keyword search returns
  no CVE for "uncloud" or "unregistry" (2026-11-08). GitHub's advisory pages could not be read from
  here. The repository has no `SECURITY.md`.
* **Fixes without advisories:** Corrosion's local API took no authentication until 2026-05-26 (#110,
  in v0.20.0), and its authorisation for CLI query and exec commands was fixed in a Corrosion update
  on 2026-06-15; secrets given to a pre-deploy hook were replicated to every machine's store while the
  same value in `environment` was not (#422, opened 2026-08-13, fixed in v0.22.0 by #424);
  `machine.db` was made readable by root only because it "may container secrets as env vars"
  (2025-03-29).
* **The embedded registry takes writes from inside the network.** Each machine's Unregistry listens on
  the machine's address with no authentication, and the firewall rule that admits it carries its own
  note: "TODO: allow access only from the machine IPs (10.210.N.1) but not the containers running on
  them" (`internal/machine/firewall/iptables_linux.go`). Read from the code, not tried: a container on
  one machine can push an image under any tag into another machine's image store. Skifity had the
  same class of hole and closed it with a write token per build (ADR-0026).
* **The managed DNS name.** The token for a cluster's `uncld.dev` name is kept unencrypted in the
  cluster store ("TODO: encrypt the token in the store"); `uc dns release` deletes it locally and
  does not tell the service ("TODO: implement and call Uncloud DNS endpoint to release/delete the
  domain", `internal/machine/cluster/dns.go`); and the service's own deployment turns purging off
  (`PURGE_INTERVAL_SECONDS: 0` in `uncloud-dns/deploy/compose.yaml`). A released or abandoned name
  therefore keeps pointing at its last addresses, and whoever is given one of those cloud addresses
  next can answer for the name and obtain certificates for it. Inferred from the code; no case found.
* **A shared rate limit.** `uncld.dev` is not on the Public Suffix List (checked 2026-11-08), so every
  Uncloud cluster's certificates count against one Let's Encrypt limit of 50 new certificates per
  registered domain per 7 days, refilling one every 202 minutes. No override for it was found. This
  is the reason ADR-0015 gives for Skifity not requesting certificates for sslip.io names.
* **Exposure by design:** secrets in plain text in Docker's container configuration; any member of the
  `uncloud` group, or any container with the API socket mounted, controls the cluster; the gRPC API
  between machines relies on WireGuard alone; no isolation between services.
* What is small is real: there is no web interface, no HTTP API and no long-running central service
  to attack. What listens publicly is Caddy, WireGuard and SSH.

## Against Skifity

Uncloud is the strongest version of the argument against Skifity's choice of Kubernetes: the same
multi-server, encrypted, Compose-shaped result with a tenth of the memory and no control plane to
lose. Where Uncloud has nothing (databases, backups, accounts, previews), that is mostly a decision
to stay small, and the verdicts below say "Skifity ahead" only in the sense of what a user gets.
Skifity statuses come from `docs/progress.md`: the single-server path runs on a real k3s on every
push to `main`, and a second server, a KVM guest on a bridge, in the `Two servers` workflow.
Uncloud was read, not run.

| Capability | Uncloud | Skifity (evidence) | Verdict |
|---|---|---|---|
| **Deploy sources and builds** | | | |
| Where images are built | On the laptop or CI runner, with local Docker; nothing builds on servers | In the cluster, one BuildKit per build, from Git, an uploaded folder or a Dockerfile (`internal/builder`, ADR-0026); no path for an image built elsewhere except a registry | different: Skifity ahead for people without Docker, **behind for teams that build in CI** |
| Getting an image to the servers | Unregistry: missing layers only, over SSH, no registry account | An image from a registry (`--image` on `skifity init`, `internal/cli/project.go:133-187`); the in-cluster registry takes writes only with a build's token (`internal/registryguard`); `skifity deploy` has no `--image` and the deploy body has none (`internal/api/deploy_handlers.go:19-26`) | **behind** |
| Git, webhooks, statuses | None | GitHub, GitLab, Gitea, Bitbucket with signed webhooks and commit statuses (`internal/gitsrc`) | Skifity ahead |
| Work before the rollout | `x-pre_deploy`, logs printed on failure | `release` command as a Job before the rollout, its output in the deploy log (`internal/deploy/run.go:180-226`) | parity |
| Seeing a change before it happens | Every `deploy`, `scale` and `caddy deploy` prints a plan and asks | `skifity plan`/`apply` for `skifity.yaml` (`docs/cli.md`), rebuild-or-not said before saving (ADR-0007), rollback with a plan | parity |
| Compose | The file is the source of truth: edit, `uc deploy`, the difference is applied | Imported once as a stack (`internal/api/stack_handlers.go`, `web/src/pages/new-app.tsx:378`); nothing in `internal/cli`, `internal/blueprint` or `internal/mcpserver` reads Compose | **behind** for Compose users |
| A version that crashes on start | Watched 5 s, stopped, old one kept, last 10 log lines printed | Waited on for at least 10 minutes unless the image cannot be pulled (`rolloutWait`, `internal/deploy/deployer.go:110`; `WaitForRollout`, `internal/kube/client.go`); the failure says "Check the app logs" without them (`RolloutTimedOut`, `internal/errdoc/catalogue.go:1396`); `errdoc.CrashLoop`, which carries a log tail, is never called | **behind** |
| Rollback | Deploy an older tag by hand | Old image and its settings, refused cleanly when the image is gone (`internal/deploy/deployer.go:841`); automatic for an app with a volume left down (`rollBackIfLeftDown`, `:1090`) | Skifity ahead |
| Rollout shape | `order`, `monitor`, `stop_grace_period` per service | Rolling `maxSurge: 1` or Recreate with a volume, fixed 30 s grace (`internal/kube/manifests.go:130,192-203`) | behind (open since `kamal.md`) |
| **Domains, TLS and routing** | | | |
| An address before owning a domain | `https://<service>.<id>.uncld.dev` from the first deploy, through the project's DNS service | `http://` on sslip.io by design, HTTPS once a wildcard domain is set (ADR-0015, `EnsureAutoDomain` in `internal/cluster/cluster.go:305-378`) | behind on the first minute; ahead on not sharing a rate limit or phoning home |
| Certificates on several servers | Cluster storage in Corrosion since v0.21.0/v0.22.0, reissue unless migrated | cert-manager Secrets read by every server's Traefik; HTTP-01 through the ingress (`internal/cluster/issuer.go:95-100`); a team's own certificates (`internal/tlscert`) | Skifity ahead (from the start) |
| Wildcard certificates | Cloudflare DNS module in the Caddy image, by hand | No DNS-01 solver anywhere in `internal/`; DNS records kept at four providers (`internal/dnsprov`) | behind |
| Redirects, auth, headers | Anything a Caddyfile can say | Redirects, password protection, maintenance, firewall rules as settings (`internal/api/api.go:485-487`, `internal/edgerules`, `internal/guard`) | different: typed and checked against free-form |
| TCP and UDP | Host mode, per machine | `skifity ports open` on every server (`internal/cli/ports.go`, `internal/api/api.go:519-521`) | parity |
| **Databases and services** | | | |
| Managed databases | None | Nine engines, PostgreSQL through CloudNativePG (`internal/dbsvc`, `docs/databases.md`) | Skifity ahead |
| Reaching something private | `uc proxy` for any service port | `skifity db connect` for databases (`internal/cli/dbconnect.go:42-46`); nothing for an app's own port | behind (small) |
| **Storage and backups** | | | |
| Volume and database backups | None | Scheduled, sealed, verified, restorable (`internal/backup`, `docs/backups.md`) | Skifity ahead |
| The manager's own state | On every machine | Panel copies in the bucket, `skifity admin restore-db` (`docs/backups.md`); etcd snapshots on the control plane's disk | different |
| **Scaling and high availability** | | | |
| A server dies | Nothing moves; replicas placed in advance keep serving | Stateless apps are restarted elsewhere (`docs/faq.md:14-20`) | Skifity ahead |
| Autoscaling | Out of scope | Autoscaler and scale to zero (`internal/kube/scaletozero.go`, `internal/deploy/scaling.go`) | Skifity ahead |
| Choosing servers | `x-machines`; one per machine with `mode: global` | Only GPUs set a node affinity (`internal/kube/gpu.go:383-391`) | behind (open since `sailbox.md`) |
| Changing CPU or memory | In place, no restart (v0.22.0) | A rollout | behind (small) |
| Taking a server out | No drain (#417) | Apps moved first, then the ingress stopped, then the node deleted (`internal/provision/move.go`, `docs/progress.md`) | Skifity ahead |
| **Observability** | | | |
| Logs | `uc logs` across replicas | Live logs over SSE, `skifity logs --follow`, eight drain kinds (`internal/logdrain`) | Skifity ahead |
| Metrics and alerts | A Prometheus endpoint; no history, no alerts | 72 hours of usage per app (`internal/watch/usage.go:18`), thresholds and alerts, requests and latency from Traefik (`internal/traffic`) | Skifity ahead |
| The network between servers | `uc wg show` (peers, endpoints, handshakes, RTT), `uc machine rtt` | Nothing reads WireGuard handshakes or round trips (`internal/cluster`, `internal/provision`); a server is "ready" when its kubelet reports | **behind** |
| The manager's own logs | `uc machine logs` from every machine | The provision log on the panel's server (`docs/adding-servers.md:360`); k3s's journal over SSH by hand | behind (small) |
| **Security, auth, roles, SSO, audit** | | | |
| Who can do what | SSH as root or the `uncloud` group is everything | Accounts, Argon2id, TOTP, passkeys, OIDC, viewer role, project-limited members, scoped expiring tokens (`internal/auth`, `internal/api`) | Skifity ahead |
| Audit | None | Team audit log (`internal/api/api.go`) | Skifity ahead |
| Secrets | Resolved on the laptop, plain in Docker's config on servers | Sealed per row, write-only; Vault, AWS, Doppler, Infisical (`internal/crypto`, `internal/secretmgr`) | Skifity ahead |
| Isolation | One flat network | A namespace per environment with default-deny policies (`internal/kube/namespace.go`) | Skifity ahead |
| Writing to the registry | Unauthenticated inside the mesh | A token per build and repository (`internal/registryguard`, ADR-0026) | Skifity ahead |
| SSH host keys | `accept-new` | Learned on first connection, refused when changed (`internal/sshx/client.go:54-93`) | parity |
| **Preview environments** | None | Per pull request, seeded if asked (`internal/api/api.go:529-530`) | Skifity ahead |
| **Templates** | About ten community recipes | 374 templates with updates (`internal/templates/catalogue`) | Skifity ahead |
| **CLI, API, IaC, integrations** | | | |
| Surfaces | A good CLI, gRPC and a Go client; no HTTP API, no MCP | CLI, HTTP API with an OpenAPI description, an MCP server of more than forty tools (`internal/cli`, `internal/api`, `internal/mcpserver`) | Skifity ahead |
| CI | `--connect`, auto-confirm, a third-party Action | `SKIFITY_URL` and `SKIFITY_TOKEN` (`docs/cli.md`); no CI section or Action | parity (both thin) |
| **Notifications** | None | Ten channels, fourteen events (`internal/notify/notify.go`) | Skifity ahead |
| **Multi-server and networking** | | | |
| Encryption between servers | WireGuard mesh | WireGuard through flannel (ADR-0003) | parity |
| A server whose public address is not on its interface (most clouds' 1:1 NAT, a home server) | Every address advertised, rotation, roaming, `--wg-endpoint`; no relay | `--node-external-ip` is passed and `--flannel-external-ip` is not (`serverArgs`, `internal/provision/scripts.go:406-418`); the test guest's address is on its own interface (`test/cluster/second-server.sh:37-41`) | **behind**, and untested |
| Managing after losing a server | Any machine; each side of a partition | The panel is pinned to one server (ADR-0013); the documented recovery is to install on a new server (`docs/troubleshooting.md:582-610`) | **behind** |
| Upgrades | By hand on each machine; v0.20.0 needed all at once | k3s and the panel upgraded from the panel (`internal/api/api.go:621-622`, `internal/api/upgrade.go`) | Skifity ahead |
| Memory | ≤150 MB per machine; 512 MB machines | About 1,000 MiB added by a fresh install, 2 GB for the first server, 900 MB to 1 GB for a worker (`docs/performance.md`, `internal/provision/preflight.go:71,77`) | **behind** |
| **Team and collaboration** | None; a VM per group at Radboud | Teams, invitations, roles, project limits | Skifity ahead |
| **Developer experience** | | | |
| Interface | CLI only; a web UI is planned | A panel in five languages with errors that say cause, impact and fix (`web/src/locales`, `internal/errdoc`) | Skifity ahead |
| Maturity | v0.22.0, pre-1.0, some production use | v0.1.x since 2026-10-06; cluster workflows on every push (`docs/progress.md`) | comparable; Uncloud has more users |

### Claims in this repository that no longer hold

* `docs/faq.md:12-24` answers "Why Kubernetes underneath?" with rescheduling and says "The trade is
  that Kubernetes needs more memory than running containers on one machine." Uncloud shows that the
  trade is against running containers on *several* machines too: WireGuard between servers, service
  discovery, rolling deploys and certificates shared across servers in about 150 MB a machine. What
  Kubernetes still buys Skifity that Uncloud does not have is rescheduling, autoscaling, isolation
  between tenants and a place to hang accounts, backups and databases; the answer should say that
  rather than leave memory as the only cost.

## Gaps worth closing in Skifity

### P1 — A version that crashes on start fails in seconds, with its last log lines

* **What:** while waiting for a rollout, read the new version's instances; one that has restarted
  twice (or is in `CrashLoopBackOff`) ends the wait at once. The deployment fails with
  `app.crash_loop`, carrying the last lines of the crashed container's previous log, and the same
  lines go into the deployment log; the failure page shows them under the cause.
* **Evidence:** Uncloud watches each new container for 5 seconds, stops a crashing one and prints its
  last 10 log lines (v0.17.0, v0.19.0). Skifity's `WaitForRollout` ends early only for an image that
  cannot be pulled (`pullRefused`, `internal/kube/client.go`); a version that exits on start is waited
  on for `rolloutWait` — at least ten minutes (`internal/deploy/deployer.go:110`) — or until
  Kubernetes' progress deadline, and then reported as "The new version did not start" with "Check the
  app logs" (`internal/errdoc/catalogue.go:1396-1405`). `errdoc.CrashLoop` with a `log_tail` exists
  (`catalogue.go:1419`), `rolloutFailures` lists `app.crash_loop` (`deployer.go:1069-1073`), and
  nothing produces it. `docs/troubleshooting.md` names "The app starts and immediately stops" as a
  common failure.
* **Fit:** a crash check beside `pullRefused` in `internal/kube`, returning a typed error with the
  pod and container; the deployer reads the last lines of the previous container, as the logs page
  already does (`Previous` in `internal/api/stream_handlers.go:449`), and raises `errdoc.CrashLoop`.
  `rollBackIfLeftDown` already acts on `app.crash_loop`.
* **Size:** S. **Without a cluster:** yes with a fake clientset; the `Cluster` workflow can deploy an
  image that exits to prove the timing.

### P1 — Deploy an image built elsewhere, sending only the layers the cluster lacks

* **What:** two steps. First `skifity deploy --image ghcr.io/acme/web:<sha>` (and `image` on the
  deploy request and the MCP tool), recorded on the deployment so rollback restores it. Then
  `skifity push web:dev`, or `docker push <panel>/<team>/<app>:<tag>` after `docker login` with a
  Skifity token: the panel answers the registry protocol for pushes, checks the token may deploy that
  app, and forwards to the in-cluster registry with a registry-guard write token for that app's
  repository, so only missing layers cross and no outside registry is needed. The pushed digest can
  then be deployed.
* **Evidence:** this is Unregistry's whole pitch (726 points on HN) and, since v0.14.0, how
  `uc deploy` works; Kamal added a local registry over an SSH tunnel in 2.8.0 for the same reason.
  Skifity builds in the cluster or pulls from a registry; the deploy body has `commit_sha`, `force`
  and `accept_vulnerabilities` and no image (`internal/api/deploy_handlers.go:19-26`); `docs/cli.md`
  has no CI section. `kamal.md` asked for `deploy --image` on 2026-09-30 and it is still open.
* **Fit:** the registry guard already mints per-repository HMAC write tokens (ADR-0026); the panel's
  `/v2/` handler checks the Skifity token with `authorizeApp`, mints one and streams through, never
  buffering a layer on the panel's disk; the image is pulled by the servers exactly as a built one
  is. A GitHub Actions and GitLab CI example in `docs/cli.md`.
* **Size:** S for `--image`, M for the push. **Without a cluster:** the API, the token exchange and
  the CLI against an `httptest` registry, yes; a push through the guard into containerd needs the
  `Cluster` workflow.

### P1 — Servers whose public address is not on their own network interface

* **What:** detect, when a server is added, that the address other servers must use is not the one
  on its interface (preflight already reads both), and in a cluster that spans networks pass
  `--flannel-external-ip` so WireGuard uses the public addresses; check before joining that the new
  server's WireGuard port answers from an existing server; warn on promote when a control plane
  candidate cannot reach the others on private addresses, which k3s says embedded etcd needs.
* **Evidence:** k3s's "Distributed hybrid or multicloud cluster" page says servers need
  `--node-external-ip=<SERVER_EXTERNAL_IP> --flannel-backend=wireguard-native --flannel-external-ip`,
  and that "Embedded etcd is not supported in this type of deployment ... all server nodes must be
  reachable to each other via their private IPs". Skifity passes `--node-external-ip` and the
  backend, never `--flannel-external-ip` (`serverArgs`, `internal/provision/scripts.go:406-418`),
  while its preflight computes `private_ip` and `public_ip` separately (`scripts.go:92-100`). On AWS,
  Google Cloud or Oracle Cloud, where the interface holds a private address, flannel would give other
  providers an endpoint they cannot reach, and nothing reports it (ADR-0003: nodes "join without any
  error and then never exchange a packet"). Read from the code and the k3s documentation, not run:
  the `Two servers` guest has its address on its own interface (`test/cluster/second-server.sh`).
  Uncloud advertises every address, rotates on failed handshakes and learns a NATed machine from its
  outbound packets; homelab users run it across NAT (#155, suranyami.com).
* **Fit:** `internal/provision` (arguments, a UDP reachability step in preflight, an errdoc entry
  naming the port and the provider firewall), the promote check, and a guest behind a NAT rule in
  `test/cluster/second-server.sh`.
* **Size:** M. **Without a cluster:** argument choice and preflight logic, yes; proving it needs a
  NATed guest in the `Two servers` workflow.

### P2 — Bring the panel back on a surviving control plane server

* **What:** a documented, tested path for "the cluster survived, the panel's server did not": on any
  remaining control plane server, `skifity admin take-over` (or the installer with `--skip-k3s`)
  fetches the newest panel copy from the bucket, asks for the recovery key, re-pins the panel's
  Deployment to this server and restores, without installing a new cluster.
* **Evidence:** Uncloud's central argument is that any machine can manage the cluster. Skifity pins
  the panel to one server (ADR-0013), `docs/adding-servers.md:281-287` says the panel "stays down
  until you restore its database and key onto another server", and the recovery in
  `docs/troubleshooting.md:582-610` says to "Install Skifity on the new server" — on a cluster with
  three control planes that is a second cluster beside the one still running every app. The
  installer can already install onto an existing k3s (`--skip-k3s`, `installer/install.sh:41`).
* **Fit:** `internal/cli/admin.go`, the bucket reader in `internal/backup`, `internal/manifests` for
  the pinning; a job in the `Two servers` workflow that stops the panel's server and brings the
  panel up on the other.
* **Size:** S–M. **Without a cluster:** the command's checks, yes; the take-over needs the workflow.

### P2 — See whether servers can reach each other

* **What:** on each server's page, its WireGuard peers with the age of the last handshake and the
  round trip to each, and a server marked "cannot reach X" when a handshake is older than a few
  minutes; the same in `skifity servers`.
* **Evidence:** `uc wg show` and `uc machine rtt` exist because broken meshes are the commonest
  multi-machine failure (#434, #155, the Docker 28.2 break). Skifity reads a server's readiness from
  its kubelet, which talks to the API server over its own connection, so a server whose pod network
  is cut reads as healthy; ADR-0003 describes exactly that failure. Nothing in `internal/cluster` or
  `internal/provision` reads handshakes or round trips.
* **Fit:** the server Check already signs in over SSH with the panel's key
  (`internal/provision/hardening.go`); `wg show flannel-wg dump` gives peers and handshakes; a
  minute-tick reading in `internal/watch` raises `server.alert` when a peer goes quiet.
* **Size:** M. **Without a cluster:** parsing and the alert logic, yes; real readings need two servers.

### P2 — Apply a Compose file again, as a plan

* **What:** `skifity plan -f compose.yaml` and `skifity apply -f compose.yaml` read a Compose file the
  way the stack import does, compare it with the environment and show the difference, so a Compose
  user keeps editing the file they already have; an `x-skifity` block for what Compose cannot say
  (domains, instances, databases to link).
* **Evidence:** Uncloud's workflow is exactly that loop, and Swarm and Compose users are its
  audience. Skifity imports Compose once as a stack (`internal/api/stack_handlers.go`); after that
  the file means nothing to it, and `skifity.yaml` is a second format to learn
  (`internal/blueprint`). Coolify and Dokploy also deploy a Compose file from the repository on
  every push (`coolify.md`, `dokploy.md`).
* **Fit:** a Compose reader in `internal/blueprint` reusing `builder.ComposeService` and the stack
  planner's notes for what does not carry over; the CLI flag; the MCP `plan`/`apply` tools.
* **Size:** M. **Without a cluster:** yes.

### P2 — HTTPS for the first sign-in, without a domain

* **What:** a certificate for the panel's own IP address at install, so the setup screen and the
  first password travel over HTTPS; sslip.io names for apps stay as ADR-0015 decided.
* **Evidence:** Uncloud's `uncld.dev` gives HTTPS in the first minute, at the price of a shared rate
  limit and a hosted service (see "Things to deliberately not copy"). Let's Encrypt now issues
  `shortlived` certificates (160 hours) with IP identifiers (profiles page, updated 2026-10-04), and
  counts them per IPv4 address rather than per registered domain (rate-limits page, updated
  2026-08-05), so one install no longer shares a limit with every other. Skifity serves the first
  sign-in over HTTP and says so (ADR-0015).
* **Fit:** the installer requests the certificate before the panel is reachable, through cert-manager
  if its version can select an ACME profile and an IP identifier (not checked for v1.21.2,
  `internal/cluster/components.go:25`), otherwise through a short ACME client in the panel; renewal
  every few days is the risk to plan for.
* **Size:** M. **Without a cluster:** the certificate handling against Pebble, yes; issuance needs a
  public address.

### P3 — Already proposed, and Uncloud has each

* **Run on chosen servers, and on every server.** `sailbox.md` P2, still open (only GPUs set an
  affinity, `internal/kube/gpu.go:383-391`); Uncloud adds the one-per-server mode (`mode: global`),
  which an agent or a cache wants. **Size:** M.
* **Drain time and rollout order per app.** `kamal.md` P2; the grace is still a fixed 30 s
  (`internal/kube/manifests.go:130`). Uncloud has `stop_grace_period`, `update_config.order` and
  `monitor`. **Size:** S.
* **Mixed architectures.** `kamal.md` P2; `kubernetes.io/arch` still appears nowhere in `internal/`,
  while Uncloud's `uc machine ls` shows each machine's architecture (and #146 shows it suffers too).
  **Size:** S for pinning.
* **A port forward to an app, not only a database.** `uc proxy`; Skifity's `db connect` tunnel
  (`internal/cli/dbconnect.go`) could take any internal app port. **Size:** S.

## Things to deliberately not copy

* **No control plane, and no rescheduling.** Uncloud's simplicity is real and its cost is stated by
  its author: replicas have to be placed in advance, nothing moves when a machine dies, and there is
  nowhere for accounts, backups or alerts to live — which is why the paid plan is a hosted service.
  Skifity's panel is the point; the gap to close is the panel's own recovery, not the panel.
* **A hosted DNS service in the product.** `uncld.dev` gives HTTPS in a minute by sending every
  cluster through one person's Route 53 zone, one shared Let's Encrypt limit and tokens nobody
  revokes. Skifity "never phones home" (`README.md`); keep sslip.io, a team's wildcard domain and
  the DNS providers it already manages.
* **Secrets resolved by running commands from the deployment file.** `x-command` runs whatever a
  Compose file says on the deploying machine, and the result is stored in plain text on the servers.
  Skifity resolves secret managers in the panel and seals the value; a Compose import should never
  execute anything in it.
* **A registry anyone in the network can write to.** ADR-0026 already answers this; do not trade it
  for convenience when adding the push in P1.
* **A free-form proxy configuration per app.** In Uncloud everybody is an administrator, so a
  Caddyfile per service hurts nobody else. In Skifity one team's free-form proxy configuration could
  claim another team's hostname, and Coolify's custom proxy files became a command-injection CVE
  (CVE-2025-66212, `coolify.md`). Typed redirects, guard rules and middlewares are the safer shape.
* **Docker's convenience script and binaries without checksums.** Skifity downloads k3s's installer
  and runs it with pinned arguments; keep it that way and verify what the installer fetches.
* **A flat network shared by every service.** Namespaces with default-deny policies are what let
  Skifity host several teams on one cluster.
* **Upgrades that need every machine at once, by hand.** Skifity's upgrades from the panel, with a
  snapshot first, are the better model.

## Sources

All read on 2026-11-08.

* https://github.com/psviderski/uncloud — cloned (`main` at `ffd9309`, 1,287 commits, tags v0.0.1
  to v0.22.0 and `nightly`); read: `README.md`, `misc/design.md`, `scripts/install.sh`,
  `internal/machine/firewall/iptables_linux.go`, `internal/machine/machine.go`,
  `internal/machine/cluster/dns.go`, `internal/machine/network/peer.go`, `wireguard.go`, `config.go`,
  `internal/machine/constants/constants.go`, `cmd/uc/dns/reserve.go`, `cmd/uc/caddy/deploy.go`, and
  `git log` for security-related commits. Repository API calls were refused from this sandbox.
* The documentation in the same repository (`website/docs/`): overview, connecting to a cluster,
  ingress overview, publishing services, cluster storage for Caddy, secrets, deploy an app, rolling
  deployments, pre-deploy hooks, the Compose support matrix, the list of CLI reference pages, and the
  release notes for v0.16.0 to v0.22.0.
* https://ungh.cc/repos/psviderski/uncloud/releases and `/repos/psviderski/unregistry/releases`
  (release notes and publication dates, v0.4.0 to v0.15.1 and Unregistry v0.1.0 to v0.4.3) and
  https://ungh.cc/repos/psviderski/uncloud
* GitHub search API: repository metadata for `psviderski/uncloud`, `psviderski/unregistry`,
  `psviderski/uncloud-dns`, `psviderski/uncloud-recipes`, `unlabs-dev/caddy-uncloud` and
  `thatskyapplication/uncloud-action`; issue and pull request counts; open issues by reactions and
  comments; bodies of #78, #155, #369, #417, #422, #425 and #434; merged pull requests since
  2026-10-02.
* https://github.com/psviderski/unregistry — cloned (`main` at `f379139`); `README.md`, `docker-pussh`
* https://github.com/psviderski/uncloud-dns — cloned (`main` at `76b6196`); `README.md`,
  `pkg/apiserver/server.go`, `pkg/db/implementation.go`, `deploy/compose.yaml`
* https://github.com/psviderski/uncloud-recipes — cloned (`main` at `f42d8cd`)
* https://uncloud.run/ and https://uncloud.run/pricing (404)
* https://news.ycombinator.com/item?id=46144275 and https://news.ycombinator.com/item?id=44314085,
  every comment, through the hn.algolia.com items API; https://news.ycombinator.com/item?id=43285730
  (story only); comments mentioning Uncloud since 2025-12-01 through the hn.algolia.com search API,
  among them /item?id=48021003, /item?id=47873865, /item?id=48550447 and /item?id=46346060
* https://psviderski.substack.com/p/a-year-of-building-uncloud (2026-01-25)
* https://suranyami.com/migrating-my-homelab-from-swarmpit-to-uncloud (2026-01-04)
* https://miek.nl/2026/march/16/uncloud/ (2026-03-16)
* https://blog.garambrogne.net/uncloud-en.html (January 2026)
* https://cncz.science.ru.nl/en/news/2026-04-15_uncloud/ (created 2026-04-15, updated 2026-10-08)
* https://api.osv.dev/v1/query for `github.com/psviderski/uncloud` and `github.com/psviderski/unregistry`
  (nothing found)
* https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=uncloud and `?keywordSearch=unregistry`
  (no results)
* https://publicsuffix.org/list/public_suffix_list.dat (`uncld.dev` absent)
* https://letsencrypt.org/docs/rate-limits/ (last updated 2026-08-05) and
  https://letsencrypt.org/docs/profiles/ (last updated 2026-10-04)
* https://docs.k3s.io/networking/distributed-multicloud
* Not read: Reddit (reddit.com refused this sandbox and the web search tool had no credits left), the
  Discord server, and GitHub's advisory pages.
* Skifity files named in "Against Skifity" and the gaps, read in this repository at `23ec07f`.
