# Questions

## Is this production-ready?

Depends on your production. A single Skifity server runs real applications with
HTTPS, automatic certificates, health checks, backups and rollback. Three
control plane servers survive losing one.

What it is not is a managed service: nobody is on call for you. If the server
goes down at 3am, that is your 3am.

## How is this different from Coolify or Dokploy?

They run Docker Compose on one machine and add a second machine as a remote
target. Skifity runs Kubernetes, so several servers are one pool: an app that
needs three instances gets them wherever there is room, and an app whose server
dies is restarted elsewhere without you doing anything.

The trade is that Kubernetes needs more memory. Skifity keeps that down by
installing almost nothing until you use it.

The other difference is deliberate: changing a setting here does not rebuild
your app. That is the single most common complaint about panels of this kind,
and it is fixed at the design level, not worked around.

## How is this different from running Kubernetes myself?

It is the same Kubernetes. Skifity does the parts that are tedious and easy to
get wrong: writing the manifests, wiring up ingress and certificates, building
images, joining servers, isolating tenants, and taking backups.

Everything it does is visible under **Advanced**, and `kubectl` works normally.

## Do I need to know Kubernetes?

No. That is the point. You will meet it if you want to.

## What does it cost?

Nothing, and it never asks for a licence key or phones home. You pay whoever
rents you the servers.

## How much memory does it need?

1 GB works for a small app. 2 GB is comfortable. Kubernetes and the panel take
roughly 700 MB between them on a fresh install; everything else is your apps.

Each optional component says what it costs before you install it.

## How do I make one address survive a server going down?

Every server runs the ingress, so your apps answer on all of them — but DNS
names one, and Kubernetes cannot move a DNS record.

The answer that costs nothing and needs no public IP is **Cloudflare Tunnel**:
`cloudflared` in the cluster with two or three replicas, each making outbound
connections to Cloudflare. Kubernetes spreads them across your servers, so one
going down changes nothing, and there are no inbound ports to open. The trade is
that traffic goes through Cloudflare and your domain lives on their DNS.

Otherwise: round-robin DNS is free and handles a server that is *off* but not
one that is *sick*; managed DNS with health checks fixes that for a few dollars;
a floating IP or a provider load balancer is the most reliable.

**kube-vip and MetalLB will not work** on most VPS — they hold an address by
answering ARP, which does not cross a router, so the nodes must share a network
segment and the provider must route that address to you. `docs/adding-servers.md`
has the detail.

## Which operating systems does it run on?

Servers need systemd, because k3s is installed as a unit. Ubuntu 24.04 and
Debian 12 are the tested pair; AlmaLinux, Rocky, RHEL, CentOS, Fedora, openSUSE,
SLES and Arch are expected to work and are not tested. Alpine will not work —
it uses OpenRC — and neither will a container with no init system. x86-64 and
arm64, nothing else.

The CLI runs on Linux, macOS and Windows, on both architectures, as one static
file. See the quick start for the full table.

## Can I use my existing Kubernetes cluster?

Not yet. Skifity installs and manages k3s itself. Pointing it at a cluster
somebody else built is on the list.

## Does it work behind a NAT or on a home server?

The panel does, as long as you can reach it. Public domains need ports 80 and
443 reachable from the internet for certificates to be issued.

## What happens if the panel is down?

Your apps keep running. They are served by Kubernetes, not by the panel. You
cannot deploy or change anything until the panel is back.

## What happens if I lose the master key?

Every stored secret becomes unreadable, and so does every backup taken with it.
Your apps keep running with the secrets they already have, but the panel cannot
read them again.

This is why the panel asks you to download the recovery key and keeps asking
until you say you have.

## Can I take the data with me?

Yes, and there is a command for it:

    skifity export --out ./leaving

That writes `skifity-export.json` — every project, app, domain, database,
variable and schedule this team has — and `manifests/<namespace>/<app>.yaml` for
each app: the Deployment, Service, Ingress, autoscaler, disruption budget,
volume claims and scheduled commands, as plain Kubernetes objects. `kubectl
apply -f` them on any cluster and the apps run there with nothing of ours. The
panel has the same thing as a download under **Settings → Take your data with
you**.

The value of anything marked secret is not in the export, because a stored
secret is never shown again. You do not need the panel for those: they are in
your own cluster as ordinary Kubernetes Secrets, and the README the export
writes has the one-line `kubectl` command that reads them out.

Underneath, the panel's state is one SQLite file at `/var/lib/skifity/panel.db`
and the key that opens it is at `/etc/skifity/master.key`, if you would rather
take the whole thing.

## Does it support Docker Compose?

It reads one. Paste the repository address when you create an app and press
"Check this repository": if there is a Compose file, Skifity lists the services
it found — what each one runs, the port it listens on, the variables it sets —
and you pick the service this app is. The form fills itself in from it, the
variables come with it, and the app is created like any other.

What it does not do is import the whole file in one action. Skifity runs one
service per app, so several services means creating the app several times, once
per service, in the same environment. There they reach each other by name, which
is what the links in a Compose file become. A service that is really a database
is better created as a managed database in the panel, which gets you backups.

Whatever cannot carry over is named rather than dropped: `privileged`,
`cap_add`, `devices`, host networking and `extends` are listed against the
service they came from, because a conversion that half-works is worse than one
that clearly needs attention.

## Can an AI assistant use it?

That is a design goal. The binary is an MCP server, every error is written to be
acted on rather than just read, and [llms.txt](../llms.txt) describes the whole
product on one page. See [the CLI guide](cli.md).

## What languages does the panel speak?

English, Indonesian, Hindi, Russian and Simplified Chinese. All five are
complete: the build fails if any string is missing from any of them.

## How do I update it?

Settings shows the version you are running. Skifity does not check for updates
and has nothing of ours to check with, so watch the releases page. Updating
changes the panel's own image and Kubernetes rolls it out.

It does reach the internet for things you ask for — a certificate from Let's
Encrypt, a component's manifest from GitHub, your Git provider, your backup
bucket — and the preflight asks a public-IP service for the address of a server
being added, because a server behind NAT cannot tell you its own. None of that
is a call to us, and none of it carries anything about you.
