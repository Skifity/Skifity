# Adding servers

You already have one. The machine you installed Skifity on is the cluster's
first node, and it is listed under **Servers** from the moment you finish
setup — Skifity records it rather than asking you to add a machine you are
already looking at. It is marked as one Skifity found rather than one it
installed, which is why it has no Remove or Promote button: there is no key to
it. To take it out of the cluster, run the uninstaller on the machine itself.

Everything below is about the *second* server.

You give Skifity an IP address and a way to sign in. It does the rest.

Only the panel's administrator adds, retries, promotes or removes a server.
Every server that joins is given the cluster's join token, and whoever controls
that machine controls the token — which, for a control-plane server, is the
whole cluster: every team's apps and secrets, and the panel's own. A team's
members see the team's servers and can rename one; which machines are in the
cluster is not a team's decision.

## What it does, step by step

The panel shows these as they happen, and says what each one found.

1. **Connecting over SSH.** The server's host key is recorded the first time,
   and checked on every connection after that. A changed key stops the process
   rather than continuing, because that is what a machine-in-the-middle looks
   like.
2. **Checking the server.** Operating system, architecture, memory, disk,
   whether a firewall is in the way, whether something already has port 6443.
   Anything fatal stops here, before the server has been changed.
3. **Installing a key.** Skifity generates a key pair that belongs to this
   server alone and installs the public half. It then reconnects with that key
   to prove it works before going any further.
4. **Configuring the firewall.** The cluster's ports are opened to the other
   servers' addresses only, never to the world. The rules are checked before
   they are added, so running this again changes nothing.
5. **Checking the network.** The new server opens a connection to a control
   plane server on port 6443, before anything long-running starts. Without this
   step, a firewall in the provider's control panel shows up as a five-minute
   Kubernetes install that times out with no explanation. It is one port in one
   direction: the pod network's UDP ports, etcd's ports between control plane
   servers and connections towards the new server are not tested, here or
   after the join. See [When a step fails](#when-a-step-fails).
6. **Installing Kubernetes.** k3s joins the existing cluster, with the same pod
   network as every other server. That is normally WireGuard, which encrypts
   traffic between servers; a server whose kernel has no WireGuard module is
   refused before this step rather than joined into a cluster it could not
   reach. Settings -> Cluster -> "How servers talk to each other" shows which
   network this cluster uses.
7. **Joining the cluster.** The panel waits for the node to report ready.

## Creating a server at a cloud provider

You do not need a machine first. **Add server → Create at a cloud provider**
orders one at Hetzner Cloud or DigitalOcean, puts a firewall in front of it and
joins it, in one step, and the same is
`skifity servers create web-2 --provider hetzner --location fsn1 --type cx22`,
or `--provider digitalocean --location fra1 --type s-2vcpu-4gb`. Like adding
any server, it is the panel administrator's.

**Connect an account once.** Paste a token under **Settings → Cloud providers**
(or `skifity cloud providers add --kind hetzner --token-file FILE`, or
`--kind digitalocean`). The token has to be able to write:

* **Hetzner Cloud:** in the Hetzner Console, open the project the servers
  should go in, then **Security → API tokens**, and generate a token with
  **Read & Write** permission.
* **DigitalOcean:** in the control panel, open **API → Tokens** and generate a
  token with **Full Access**, or with custom scopes that include creating
  droplets, SSH keys and firewalls.

The panel asks the provider whether the token works, and whether it may create
anything, before it keeps it — a read-only token is refused there, not at the
first order. The token is sealed under that connection, is rotated with every
other secret when the master key is, and is never shown again; the list shows
its last four characters. A connection that servers were created with cannot
be removed until they are, because deleting their machines needs it; its
token can be replaced, below.

**Pick what to order.** Locations, server types and images come from the
provider as they are now: only types that can be ordered in the location picked
are offered, each with its monthly price there. Hetzner's prices include VAT
and are in euros; DigitalOcean's are in US dollars before tax, which is how
each writes them. At Hetzner both processors work — x86 and Arm (the CAX types)
— because k3s and the panel's images are built for both; droplets are x86.
Ubuntu 24.04 LTS is the default, and Debian 12 the alternative: the two
distributions the preflight check calls supported.

**What happens, step by step.** Three steps come before the usual seven:

1. **Ordering the machine.** The panel generates a key pair for this server
   alone, as it does for every server, and gives the provider the public half, so
   it sets no root password and emails nobody one. It finds or creates a
   firewall at the provider for the server (below). Then it generates the machine's
   SSH **host key** itself, records its fingerprint, and orders the machine
   with that key in its cloud-init user data — so the panel knows who the
   machine is before it exists.
2. **Waiting for it to start.** The panel waits for the provider to report the
   machine running with an address, and lets that address through the
   firewalls of the other machines it created. The key it gave the provider is
   removed from the account; the machine already has it.
3. **Checking its identity.** The panel connects and accepts nothing but the
   host key it generated. A machine that presents another one is refused
   *before* the panel signs in, so nothing is sent to it. Then, over that
   checked connection, the machine generates a host key of its own, the panel
   pins it, and proves a new connection gets it. The first key was in the user
   data, which the provider keeps and anything on the machine can read back from
   the metadata service; it is not the one the server ends up with. See
   ADR-0025 in the design decisions for why this, and not reading the key
   from the console.

From there it is exactly the join every server gets: preflight, the server's
own firewall, the network check, k3s at the cluster's version, ready.

**The firewall.** The provider's firewall sits in front of the machine from its
first boot and lets in, from anywhere, HTTP and HTTPS — what the server is for —
and SSH (passwords are off; the panel signs in with its key). Everything the
cluster needs between its own servers is let in from the cluster's addresses
and nobody else: the Kubernetes API on 6443, the kubelet on 10250, the pod
network's VXLAN on UDP 8472 and WireGuard on UDP 51820 and 51821, and etcd's
2379 and 2380 on a control plane server — the same list the server's own
firewall opens. Outbound traffic is not limited: a node downloads k3s and pulls
images. (A DigitalOcean firewall with no outbound rules lets nothing out, so
the panel writes its outbound rules open; a droplet is put behind its firewall
by a tag of that firewall's own, which it carries from its first boot.) Every machine the panel created has its firewall updated when a server
joins or leaves, so the list follows the cluster.

Tick **SSH from the cluster's servers only** to close port 22 to the internet
as well. The panel's connections leave from the server it runs on, so that is
what the panel needs when it runs inside the cluster, as `install.sh` sets it
up. A panel run elsewhere could not reach a machine created that way, so it is
refused when the panel is not connected to a cluster.

**What it costs.** The machine is billed by the provider from the moment it is
ordered until it is deleted, whether or not it joined. The form says the price
before you order.

**Deleting it.** Removing a server the panel created offers **Also delete the
machine at the provider**. The server is drained and leaves the cluster first,
as any removal does; then the machine, its firewall and anything else the
panel made for it are deleted. You type the server's name to confirm. The panel
only ever deletes the machine it recorded ordering, and only while that machine
still carries the label the panel gave it (a tag, at DigitalOcean): a machine somebody relabelled, and
any server that was added with an address rather than created, is never
deleted — it is left for you to delete in the provider's console. Without the box
ticked, the machine keeps running, and keeps being billed.

### Replacing the token

A token that expired, was revoked or is being rotated is replaced on the
connection: **Replace token** under **Settings → Cloud providers**, or
`skifity cloud providers update NAME --token-file FILE`. It is allowed while
servers created with the connection are in the panel, which removing it is
not.

The provider is asked before anything is saved, as it is for a new
connection: whether the token works, and whether it may write. When servers
were created with the connection, the new token also has to see their
machines. A token for another project passes every other check and sees none
of them, and saving it would leave those machines where only the provider's
console could delete them, still billed; it is refused. If the machines were
deleted in the console, remove their servers in the panel first.

## The password

If you sign in with a password, it is used exactly once: to install the key from
step 3. It is never written to the database, never logged, and is gone from
memory once the step succeeds.

Once the key is installed, everything afterwards uses it.

## What happens after

Skifity starts placing new instances on the server straight away. Existing
instances are not moved — that would restart apps for no reason — so a new
server fills up as you deploy.

## How traffic reaches your apps

Every server runs the ingress, so an app answers on **every** server's address,
wherever its instances happen to be. What decides which address people use is
DNS, and DNS names one.

That is the honest limit of a several-server install. If an app has three
instances across three servers and the server your domain points at goes down,
the app is still running and the name is still dead. Kubernetes moved the work;
it cannot move your DNS record.

Skifity does not need to know which way you solve it. Four work, in the order
worth trying them.

### Cloudflare Tunnel — automatic, free, and needs no public IP at all

`cloudflared` runs in the cluster as an ordinary Deployment with two replicas.
Each one makes **outbound** connections to Cloudflare — four of them, to servers
in at least two data centres — and traffic arrives through those. Kubernetes
already spreads those replicas across your servers, so if one server goes the
other carries the traffic, with nothing to configure and no health check to set
up.

What it buys, beyond failover: **no inbound ports and no public IP**. It works
on a server behind NAT, on a home connection, and on a provider that charges for
IPv4. Your servers' addresses stop being public at all.

**Skifity installs it for you.** Three steps, once:

1. In the Cloudflare dashboard, go to **Zero Trust → Networks → Tunnels** and
   create a tunnel. Choose the **Cloudflared** connector and copy the token it
   shows. It is one long line; do not copy the `cloudflared service install`
   command it sits inside, and do not copy the tunnel's ID from the address bar.
   Skifity refuses both and says which one you pasted.
2. On the same tunnel, add a **public hostname**. Use a wildcard — `*.apps.example.com`
   — so that every app you ever deploy is covered by this one route. Set the
   service to **HTTP** and the address to:

   ```
   traefik.kube-system.svc.cluster.local:80
   ```

3. In the panel, paste the token into **Settings → Domains and HTTPS →
   Cloudflare tunnel token**, then press **Install** next to **Cloudflare
   tunnel** under **Settings → Components**.

That is all of it. Nothing is needed per app: `cloudflared` forwards the Host
header untouched and the ingress routes on exactly that, so an app that has a
domain works the moment its Ingress exists.

Changing the token later is the same box. Saving a new one reaches the cluster
straight away and the connectors roll over to it without a gap; clearing it
stops them and puts the component back to not installed, which is how this
integration is disconnected.

The trade, and it is a real one: every request goes through Cloudflare, and your
domain has to be on their DNS. Replicas are also not load balanced in the
round-robin sense — a request goes to the replica geographically closest to
where it arrived, which is failover rather than spreading. And the free plan
caps an upload at 100 MB, which matters if your app takes large files.

Free for up to 25 replicas per tunnel.

> **Not run against a real tunnel.** The manifests, the refusals and the
> rollover are covered by tests, and no one has yet pointed this at a Cloudflare
> account. See the Status section of the README.

### Round-robin DNS — free, nothing to install

One A record per server, all with the same name. A browser that cannot connect
to the first address tries the next.

Honest about what it is: **a failover for a server that is off, not for a server
that is sick.** A machine that accepts the connection and then answers nothing
is one DNS gives out for as long as the record exists. Good enough for a great
many installs, and it costs nothing.

#### Which servers are taking traffic

The panel watches for the sick half, and says so; it does not act on it.

**Servers** says of each server whether it takes traffic on ports 80 and 443,
read from the cluster while the page is open. A server takes traffic while it
is ready, the load balancer k3s runs on it (ServiceLB) is ready, and a Traefik
on it is ready — Traefik answers only the visitors who reach the server it runs
on, because that is how it keeps their address (see
[Knowing who the visitor is](firewall.md#knowing-who-the-visitor-is)); with a
Traefik you configured without that, a ready one anywhere will do. These are
the rules k3s itself uses to decide which addresses its load balancer offers.
A server that is up and fails one of them is marked **Not taking traffic**,
counts among the servers that need attention, and its own page says which rule
it fails. When Traefik is what is missing, such a server drops a connection
rather than refusing it, so a browser sent there waits before it tries another
address, if it tries at all.

What it does not see:

* **The outside.** This is the cluster's own view, not a request from the
  internet. A provider's firewall closing port 443, a broken route between your
  visitors and the server, or a Traefik that is ready and answers everything
  with an error all look healthy here.
* **Anything but k3s's load balancer.** With ServiceLB turned off — MetalLB, a
  provider's load balancer — or Traefik replaced, the cluster does not say
  which servers take traffic, and the panel says nothing rather than guess.
* **Nobody is told.** It is shown, not sent: a server that is up and not
  serving does not reach the team's notification channels. A server that stops
  answering altogether does, as **not responding**.

**It does not change your DNS.** The records at a round-robin name are yours,
and the panel only ever changes or deletes a record it created (see
[Connecting a DNS provider](concepts.md#connecting-a-dns-provider)); the
records it creates name one address, not one per server. Take the sick
server's address out of the name by hand until it is fixed. Doing that
automatically was considered and left out: the panel would need to own every
address at the name, a resolver keeps the old answer for the record's TTL
after it changes, and a check from inside the cluster would remove an address
for a reason the outside never saw, or keep one the outside cannot reach. A
health-checking DNS service, below, does this properly from outside.

> **Not run against a real cluster.** The rules are tested against a fake one;
> no run has yet stopped Traefik on one server of several and watched
> **Servers** say so.

### DNS with health checks — automatic, a few dollars

Cloudflare Load Balancing, Route 53 health checks, and most managed DNS
providers do the same thing: they watch each address and stop handing out the
ones that stopped answering. This is round-robin DNS with the missing half, and
it is the smallest amount of money that buys real automatic failover.

### A floating IP, or the provider's load balancer

One address that moves between servers, or one that fronts them. The most
reliable, and the one that ties you to a provider.

### What does not work, and people try it first

**kube-vip and MetalLB in layer-2 mode do not work on most VPS.** They hold a
virtual IP by answering ARP, and ARP does not cross a router — so every node has
to be on the same network segment *and* your provider has to route that extra
address to you. On cloud VPS, "an extra address the provider routes to you" is
exactly what a floating IP is, sold as a product. Their BGP modes work and need a
provider that speaks BGP to you, which the cheap ones do not.

On bare metal in one rack, or on Hetzner nodes sharing a private network, they
are the right answer. Between providers, or across regions, they are not.

### Telling Skifity

With a floating IP, a load balancer or a tunnel, put the address people will
reach in **Settings → Domains → Cluster public IP**. That is the address the
panel uses for the free `sslip.io` names it hands out, instead of one server's
own. With round-robin DNS there is nothing to set: the name is yours and points
at all of them. A change here moves the DNS records the panel keeps at a
connected DNS provider too (see
[Connecting a DNS provider](concepts.md#connecting-a-dns-provider)).

## Control plane servers

The first server runs the control plane: etcd, which holds the cluster's
state, and the Kubernetes API. Others join as workers unless you add them as
control plane servers or promote them.

**A fresh install has one control plane server, and it is a single point of
failure for the whole cluster.** Rebooting it means the cluster cannot be
changed for a few minutes; your apps keep running the whole time, you just
cannot deploy. Losing it for good loses the cluster: nothing can be deployed,
moved or restarted on another server, the panel goes with it, and the way back
starts with a new install (see
[Restoring after losing the panel's server](troubleshooting.md#restoring-after-losing-the-panels-server)).
Adding workers adds capacity, not this.

With three, losing one changes nothing at all *to the cluster*. Two is worse
than one: a two-member etcd cluster cannot form a majority when either member is
lost, so the panel refuses to leave you there.

### Going to three

Three control plane servers are what a cluster you depend on should have, and
they ask more of the servers than workers do:

* **An odd number.** Three, or five. etcd needs a majority of its members to
  agree on every change, so three carry on with one lost and five with two.
  Four survive no more than three do, and two, as above, are worse than one.
* **One place.** Every change to the cluster waits until a majority has
  written it, so the round trip between members is added to every change, and
  a link that drops packets has members miss each other and elect a new
  leader. Keep them in one region, a few milliseconds apart, as servers in one
  data centre are. Servers far apart make good workers and poor control planes.
* **A fast disk.** etcd waits for every write to reach the disk before it
  answers. A local SSD keeps up; a virtual disk on shared network storage often
  does not, and shows up as `slow fdatasync` in the k3s log and a cluster that
  stalls (see [The cluster is unreachable](troubleshooting.md#the-cluster-is-unreachable)).
* **Room.** 2 GB of memory, 20 GB of disk and two cores each, against 1 GB,
  8 GB and one core for a worker, because etcd and the Kubernetes API run there
  too. This is checked before anything is touched.

The first server is one; add two more. Tick **Control plane server** when you
add each one, or when you order it at a cloud provider (`--control-plane` with
`skifity servers create`), or add them as workers and press **Make it a control
plane server** on each one's page, which the panel allows once the cluster has
three servers.

Neither way has been run on a real cluster yet: the `Two servers` workflow
adds its second server as a worker, so joining or promoting a control plane
server, and three of them surviving the loss of one, have unit tests but have
not been seen working. Try it on servers you can rebuild before you depend on
it.

Three control planes do not make the panel highly available. The panel is one
pod, with its database and master key on one server's disk, and it runs where
they are. If that server is lost, your apps keep serving and the cluster keeps
its quorum, and the panel stays down until you restore its database and key onto
another server. Nothing tells you meanwhile, because the thing that sends
notifications is the panel: watch it from outside, with an uptime check on its
address.

Promoting a server moves its instances elsewhere first, then rejoins it as a
control plane member.

Before a server becomes a control plane server, added as one or promoted, the
panel measures it for etcd, which keeps the cluster's state and, on every
change, waits for a majority of the control plane servers and for its own disk.
It times 20 TCP connections from the server to port 6443 on each control plane
server already there, and 200 small writes to its disk with `dd` and
`oflag=dsync` under `/var/lib`, about half a megabyte that it removes again. An
average round trip over 50 ms, round trips that vary by more than 10 ms, more
than 1% of connections losing a packet, or a write that takes more than 10 ms
on average to be made durable is shown as a warning: under the network check
when adding, under the preflight when promoting. A warning never stops
anything. The four thresholds are under **Settings → Cluster**, where 0 turns
one off. What could not be measured, on a server without bash or one that
cannot reach a control plane server's port 6443, is said as well rather than
passed over.

It is a few seconds of one link, timed from one side. It does not show that a
link stays good, it does not time the other control plane servers against each
other, and it is no substitute for etcd's own disk guidance, which uses fio and
runs for longer.

## Hardening

Adding a server opens the cluster's ports to the other members, on a firewall
that is already running; it does not turn one on, and it closes nothing. A server
with no firewall has the Kubernetes API (6443) and the kubelet (10250) open to
the internet, protected only by their own authentication, unless the provider's
firewall closes them: allow TCP 22, 80 and 443 there, and the other members'
addresses for the rest. The one port Skifity does close itself is 30500, the
registry's, which takes no password. It also does not change how the machine
lets people in. A cloud image usually arrives with
SSH taking passwords and root allowed to sign in with one, and that stays as it
was.

**Check** on a server's page, for a panel administrator, signs in over SSH with
the key Skifity installed and reads, changing nothing:

| Check | Fine | Worth fixing, or a risk |
|---|---|---|
| SSH passwords | keys only | passwords or keyboard-interactive accepted: anybody can keep guessing |
| Root over SSH | not at all, or with a key | with a password |
| fail2ban | running, or not needed because SSH takes no passwords | not installed, or installed and stopped |
| Security updates | install themselves (`unattended-upgrades`, `dnf-automatic`) | off, or nothing installed to do it |
| Firewall | ufw, firewalld or iptables rules active | none |
| Reboot | none waiting | updates installed that need one |

SSH is read from `sshd -T`, the configuration the daemon is really running
with, rather than from `sshd_config`, which drop-in files override on most
distributions.

One change is offered, because it matters most and because Skifity can make
it safely: **Turn off SSH passwords**. It writes
`/etc/ssh/sshd_config.d/00-skifity-hardening.conf` with
`PasswordAuthentication no`, `KbdInteractiveAuthentication no` and
`PermitRootLogin prohibit-password`, checks it with `sshd -t`, checks that the
daemon's effective setting changed, and only then reloads SSH. It refuses, with
nothing changed, when the daemon does not accept keys or does not read that
directory. Keep your own key in `~/.ssh/authorized_keys` first: from then on,
everybody signs in with a key.

The rest are left to you on purpose. Installing packages and changing how a
machine updates itself are decisions about somebody else's server that a panel
should report, not make.

## When a step fails

Every failure names what happened, what it means and what to do.

**Could not connect.** The address or port is wrong, the server is off, or a
firewall is blocking SSH. Try `ssh root@<address>` yourself: if that fails, so
will Skifity.

**The password was refused.** Password authentication may be turned off. Use a
private key instead, which is the better option anyway.

**The host key changed.** The server was rebuilt, or something is sitting
between you and it. If you rebuilt it, remove the server in the panel and add it
again. If you did not, stop and find out why.

**Preflight found a problem.** The message names it: too little memory, too
little disk, an unsupported distribution, or a port already in use. Fix it and
press Retry.

**The network check failed.** Something is dropping traffic between your
servers. Providers such as Hetzner and DigitalOcean have their own firewall in
the control panel, which the server cannot see or change. That is the usual
cause.

**The server is ready, but apps on it cannot reach the others.** The network
check tries only TCP 6443 from the new server to a control plane server, and
a node reports ready without a single packet of pod traffic having crossed to
another server. Instances on it then time out talking to a database or an app
on another server, and requests through another server's ingress to an
instance on it hang. That is the pod network: UDP 51820 and 51821 with
WireGuard, the default, or UDP 8472 with vxlan, between every pair of servers
and in both directions. A control plane server also needs TCP 2379 and 2380
to and from the other control plane servers; without them etcd cannot take it
in, and the join stops at the five-minute wait for the server to be ready. The
new server's own firewall is opened to the other servers for all of these, and
so is the provider firewall of every server the panel created at a cloud
provider. Nothing else is: a firewall running on a server that was already in
the cluster, or one in your provider's control panel in front of a server you
brought yourself, has to let them in from the new server's address.

**Kubernetes did not install.** The log at
`/var/log/skifity/provision-<id>.log` on the panel's server has the installer's
own output.

Retry picks up where it stopped. Every step is safe to repeat.

## Removing a server

Before anything is touched, the panel asks the cluster what exists only on the
server. k3s's own storage ties a volume to the node it was made on, so an app
with one does not start on another server after a drain — its pod waits for a
node that has been deleted — and the data is gone once the machine is wiped or
deleted. A server that holds some is not removed until you say so: the panel
lists the volumes and offers "Remove anyway", and the API takes
`accept_data_loss=true`. Back the data up first, or delete the apps that use it,
if it matters. A cluster that cannot be asked is not a reason to refuse, since
the likeliest reason to remove a server is that it has died.

Instances are moved off first, then the server leaves the cluster. An app whose
only instances are on the server is started on another one before they are
stopped, as a deploy does, so it keeps answering; the others move one instance
at a time, as their disruption budgets allow. What runs on every server, the
ingress among it, is then stopped the same way: the other servers stop sending
it requests and it finishes the ones it holds, for up to a minute and a half,
before the server leaves. Nothing is lost.

A server that has stopped answering — switched off, or gone — cannot be asked
to stop anything, so it is not waited on: it leaves the cluster straight away,
and Kubernetes starts its instances on the other servers. The removal names
whatever kept its data on that server's own disk, since that cannot start
anywhere else; restore it from a backup.

The vulnerability scanner's cache is not counted as data on a server: it is
made again wherever the next scan runs.

Tick **Also remove Kubernetes from the machine** to clean the server up
completely, leaving it as you found it.

Skifity refuses to remove a control plane server if that would leave the cluster
without a majority, and explains why rather than letting you break it.

A server Skifity created at a cloud provider can have its machine deleted too,
after it has left the cluster; see [Creating a server at a cloud
provider](#creating-a-server-at-a-cloud-provider).
