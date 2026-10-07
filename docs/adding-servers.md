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
5. **Checking the network.** Both directions, before anything long-running
   starts. Without this step, a firewall in the provider's control panel shows
   up as a five-minute Kubernetes install that times out with no explanation.
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
be removed until they are, because deleting their machines needs it.

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

The first server runs the control plane. Others join as workers unless you
promote them.

With one control plane server, rebooting it means the cluster cannot be changed
for a few minutes. Your apps keep running the whole time; you just cannot deploy.

With three, losing one changes nothing at all. Two is worse than one: a
two-member etcd cluster cannot form a majority when either member is lost, so
the panel refuses to leave you there.

Promoting a server moves its instances elsewhere first, then rejoins it as a
control plane member.

## Hardening

Adding a server opens the ports the cluster needs and closes the rest; it does
not change how the machine lets people in. A cloud image usually arrives with
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

Instances are moved off first, then the server leaves the cluster. Nothing is
lost.

Tick **Also remove Kubernetes from the machine** to clean the server up
completely, leaving it as you found it.

Skifity refuses to remove a control plane server if that would leave the cluster
without a majority, and explains why rather than letting you break it.

A server Skifity created at a cloud provider can have its machine deleted too,
after it has left the cluster; see [Creating a server at a cloud
provider](#creating-a-server-at-a-cloud-provider).
