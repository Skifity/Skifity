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
