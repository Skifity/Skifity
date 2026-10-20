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
* **A build is untrusted**, and runs in a namespace of its own, with a BuildKit
  of its own that lives as long as it does (ADR-0026). It is at the `privileged`
  profile, because rootless BuildKit needs it (ADR-0016); a build escaping to the
  node is a known limit of that, not a surprise — but reaching the panel's
  secrets, another build, or another team's images in the registry for writing
  is a vulnerability.
* **A pod's own code starts behind its fence.** Every pod that runs a team's
  code — an app, a command, a build, a plugin — first waits in a container of
  the panel's until its network policy is in place (ADR-0027). Code of a team's
  that runs before its policy is a vulnerability.

## Known limits

What follows is what Skifity does not do, or does only in part, today. It is
here so that nobody finds it out the hard way. The tenant boundaries above are
checked on a real, single-server k3s cluster by `test/cluster/verify.sh`, which
CI runs on every push to `main`; `docs/progress.md` says what each run found.
None of it has run on more than one server, and none of it has been attacked by
anybody who meant it.

**What builds still share.**

* Reading the registry. Every server's container runtime pulls without a
  password, and so may anything in the build namespace: a build can pull
  another team's image, which is that team's code. Secret build variables never
  enter an image, and runtime variables never do either; anything a team puts
  into its image itself is readable by every team that builds here. Writing,
  deleting and listing the repositories need a token the panel signed, and a
  build's token is for its own app's repository, for an hour.
* The server. The build namespace is at the `privileged` profile, and a build's
  BuildKit runs with its seccomp and AppArmor profiles off so that it can make a
  user namespace. A build that escapes that namespace is on the server, beside
  every team's apps there. That is a kernel bug away, not a setting.
* Inside one build, BuildKit's process sandbox is off, because a Kubernetes pod
  masks the `/proc` it would need; the steps of a build can see each other and
  the daemon. The daemon is that build's alone.
* What a server allows. Ubuntu from 23.10 refuses a user namespace to an
  unconfined program unless a profile allows it, which is what rootless BuildKit
  needs. Ubuntu ships one for `rootlesskit`, and builds run under the
  restriction with it (the cluster runs show both); a server without it fails
  its builds at the start, saying so in the build's log.

**Floods are not handled.** A volumetric attack, from many addresses at once, is
out of scope above. What the panel does is per address: a limit on its own API, a
cap on how much memory Traefik may take, and, on an app with the firewall on,
allow and block rules and rate limits per visitor's address — which stop one
address asking too often and do nothing against a thousand asking at once. Put
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

**A pod that waits a minute for its fence starts without it.** The fence holds a
pod's code back until the policy controller has written the pod's rules, and
gives up after a minute rather than keep every pod from starting on a cluster
that enforces no policy at all. It says so in its own log. On a cluster Skifity
installed, the rules arrive within a second or two.

**A panel administrator is root everywhere** (above). A team's administrator sets
addresses the panel then requests — a webhook, a Git host, a secret manager, a
registry, a log drain, a template catalogue — and those may be anywhere a private
network answers, because a Gitea or a receiver on your own network is the
ordinary case. They may not be inside the cluster, where every team's apps run
and which every environment lets the panel into, nor the panel's own machine or
the cloud metadata service. A server's own address is not refused: the
Kubernetes API and the kubelet there answer only to credentials the panel does
not send, and the registry's port drops anything that is not from the machine
itself.

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
