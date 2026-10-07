# Security

Skifity runs other people's code on servers it installs, holds their secrets,
and is granted `cluster-admin` to do it. A hole in it is a hole in everything it
runs. This page says how to report one, what is supported, and where the lines
of trust are.

## Reporting a vulnerability

Report it privately, through **GitHub's private vulnerability reporting**: the
**Security** tab of this repository, then **Report a vulnerability**. Please do
not open a public issue, a pull request or a discussion for it.

A useful report says:

* the version (`skifity version`, or Settings → Upgrade in the panel);
* what an attacker needs first — no account, a viewer, a member, an admin of
  one team, network access to a server — because that decides how bad it is;
* the steps, and what they got that they should not have.

What happens next:

* an acknowledgement within **seven days**;
* for anything that gets past authentication, reaches another team or a
  project outside someone's limits, reads a secret, or runs a command, a fix or
  a plan within **thirty days**, and a release with the fix as soon as it is
  ready rather than on a schedule;
* an advisory once a fixed release exists, crediting you unless you would
  rather not be.

## Supported versions

Skifity has not had a 1.0. Until it does:

* only the **latest release** receives fixes, security fixes included;
* a fix ships as a new release, not as a patch to an older one;
* upgrading is one request — see [Upgrading](docs/configuration.md#upgrading) —
  and takes a copy of the panel's database first, so going back is possible.

After 1.0 the latest minor version receives every fix, and the one before it
receives security fixes for three months after its successor is released.

## Trust boundaries

These are the lines the code defends, and the ones it does not.

* **A panel administrator is root on every server.** The panel is granted
  `cluster-admin` (ADR-0014 in [docs/decisions.md](docs/decisions.md)), and an
  administrator controls the panel. Nothing a panel administrator can do to
  their own servers is a vulnerability.
* **A team owner or admin controls that team**, and nothing else: another
  team's resources answer exactly as if they did not exist. A test walks every
  route in the router to hold that.
* **A member** deploys and changes apps. **A viewer** reads and changes
  nothing. **A member or viewer limited to projects** reaches those projects
  and nothing that belongs to the whole team. Each of those is also a walk of
  the whole router, in `internal/api`.
* **An API token** can do what its owner can, at the moment it is used, and
  no more; a scoped token less. The MCP endpoint is the API with a token, not a
  second way in.
* **Secrets** are sealed with a master key kept outside the database, bound to
  where they are stored, never logged, and never returned once set.
* **An app is untrusted.** It runs in its environment's namespace with a
  default-deny network policy and, unless an admin lowered it for that
  environment, the `restricted` pod security profile. An app reaching the
  panel, another environment, or a secret that is not its own is a
  vulnerability.
* **A build is untrusted**, and runs in a namespace of its own. It is at the
  `privileged` profile, because BuildKit needs it (ADR-0016); a build escaping
  to the node is a known limit of that, not a surprise — but reaching the
  panel's secrets from one is a vulnerability.

## Known limits

What follows is what Skifity does not do, or does only in part, today. It is
here so that nobody finds it out the hard way. Nothing below, or above, has been
run against a real cluster by the people who wrote it: `docs/progress.md` says
what has been executed and what has only been written and tested against fakes.

**Builds are not isolated from each other.**

* One BuildKit serves every team. It listens inside the build namespace without a
  password, and a build step runs beside it with its process sandbox off, because
  rootless BuildKit needs that in Kubernetes. A hostile build step can read what
  another team's build holds in that daemon, and push over images in the registry.
* The registry takes no password and tags are mutable. What keeps the world and the
  apps away from it is a firewall rule that drops its port from outside the
  machine and a network policy that keeps pods off the machine's own address; a
  build in the build namespace is not stopped by either, because it needs the
  registry to work.
* The fix is a daemon and a short-lived push token per build. It is the boundary
  that matters, and it cannot be tested without a cluster, so it has not been
  done. Until it is, treat a team that can build as able to affect every
  team's images, and do not host teams that do not trust each other.

**Floods are not handled.** A volumetric attack, or one address asking faster
than a server can answer, is out of scope above, and the panel does little about
it beyond a per-address limit on its own API, a cap on how much memory Traefik may
take, and per-app allow and block rules. There is no per-app rate limit. Put
Cloudflare, or your provider's DDoS protection, in front of anything that matters.

**The panel is a single point of failure.** It is one pod with its database and
master key on one server's disk. Three control plane servers keep the cluster up
and do not change that. If its server is lost, apps keep serving and the panel is
down until its database and key are restored elsewhere. Nothing watches the
panel from outside: use an uptime check on its address.

**Backups are opt-in, and data is on one server.** Nothing is backed up until a
backup storage is set. k3s's default storage keeps a volume on the server that
made it: a server lost is its volumes lost, and an app with one waits for it. A
managed Postgres can have several instances; the other engines have one. There is
no point-in-time recovery. etcd is snapshotted every six hours onto the same disk,
which is not a backup of the server.

**The host firewall is yours.** The installer opens ports on a firewall that is
already running and turns none on. On a server with none, the Kubernetes API and
the kubelet are reachable from the internet behind their own authentication unless
your provider's firewall closes them. The installer does close one port itself,
the registry's.

**The first install is plain HTTP.** With no domain the panel answers on an
sslip.io address over HTTP, and the setup token, the first password and the
session cookie cross the network in clear. Give the installer a domain, or do the
first sign-in from a network you trust.

**Node protection is a request, not a guarantee.** Every server keeps memory back
for k3s and evicts a pod that grows past what it asked for, and every container
has a default limit on what it may write to disk. Nothing stops a tenant from
requesting limits larger than the machine, and a full registry volume fails every
team's pushes. Disk and memory alerts notify; they do not act.

**A panel administrator is root everywhere** (above), and the panel can be made to
request any address a private network answers, including the Kubernetes API, by
the URL of a webhook or a log drain that an administrator of a team sets.

## In scope

The panel, the CLI, the MCP server, the installer, the Kubernetes objects the
panel renders, and the plugin store's signature checks.

## Out of scope

* vulnerabilities in k3s, Traefik, cert-manager, BuildKit, Railpack or the
  images you deploy — report those to their projects, and tell us if Skifity
  needs to pin around one;
* anything that needs a panel administrator to attack their own panel;
* denial of service by sheer volume against a server you are allowed to reach;
* findings from automated scanners with no demonstrated impact.

## What is already checked

Every commit runs `govulncheck` and `npm audit` (`make audit`), the linters,
the tests — including the router walks above — and a smoke test against
the real binary. `docs/progress.md` records what has and has not been run
against a real cluster, plainly.
