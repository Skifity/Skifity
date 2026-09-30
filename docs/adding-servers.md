# Adding servers

You already have one. The machine you installed Skifity on is the cluster's
first node, and it is listed under **Servers** from the moment you finish
setup — Skifity records it rather than asking you to add a machine you are
already looking at. It is marked as one Skifity found rather than one it
installed, which is why it has no Remove or Promote button: there is no key to
it. To take it out of the cluster, run the uninstaller on the machine itself.

Everything below is about the *second* server.

You give Skifity an IP address and a way to sign in. It does the rest.

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
at all of them.

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

Instances are moved off first, then the server leaves the cluster. Nothing is
lost.

Tick **Also remove Kubernetes from the machine** to clean the server up
completely, leaving it as you found it.

Skifity refuses to remove a control plane server if that would leave the cluster
without a majority, and explains why rather than letting you break it.
