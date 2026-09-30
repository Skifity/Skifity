# The firewall

Rules on who may reach an app: by address, by country, by network, by path or by
header, combined with **and** and **or**. The shape is the one people already
know from Cloudflare — an ordered list of rules, and the first one that matches
decides.

Find it on an app's **Firewall** tab.

## Turning it on

Two things, once:

1. **Settings → Components → Firewall → Install.** This runs the process that
   actually checks requests. Nothing is checked until it exists, and the app's
   Firewall tab says so rather than looking like a firewall that is on.
2. On the app, switch the firewall on and add a rule.

The firewall needs Traefik, which k3s installs by default. It costs about 128 MB
across two replicas, and nothing at all until you install it.

## Writing a rule

A rule has a name, an action — **Allow** or **Block** — and a condition. The
condition is a group: *match all of these*, or *match any of these*. A group can
hold another group, which is how and-or-and is written without anybody learning
a syntax.

The everyday shape is an allowlist:

> **Rule:** "The office, or anybody in Indonesia on the public site" → **Allow**
>
> Match **any** of these:
> * Visitor's address **is one of** `203.0.113.0/24`
> * Match **all** of these:
>   * Visitor's country **is one of** `ID`
>   * Path **starts with** `/admin` — as a condition you then delete, or invert
>
> **Anything else:** Block

The first rule that matches decides, so order matters: a narrow **Allow** above
a broad **Block** is how you carve an exception out.

### What a rule can look at

| | |
|---|---|
| **Visitor's address** | One or more addresses or ranges, `203.0.113.0/24`. IPv4 and IPv6. |
| **Visitor's country** | Two-letter codes, `ID`, `SG`. Needs the country lookup, below. |
| **Visitor's network** | AS numbers, `AS13335` or `13335`. Needs the network lookup. |
| **Hostname**, **Path**, **Method** | The request's own. The path never includes the query string, so a rule about `/admin` cannot be satisfied by `?next=/admin`. |
| **Browser or bot** | The `User-Agent` header. |
| **A header** | Any header, by name. |

Comparisons are *is one of*, *is not one of*, *contains*, *starts with*, *ends
with* and *matches the pattern*. Text comparisons ignore case, because a rule
about `/admin` that missed `/Admin` would be a rule that does not work.

An address, a country and a network only offer *is one of* and *is not one of*.
"Contains" on an address would save and never match anything, so it is not
offered and would be refused if it were.

## Country and network

These need data, and the panel gets it from [DB-IP](https://db-ip.com), whose
free monthly databases need no account and no licence key. It is on by default
and downloads about 20 MB the first time the firewall starts.

Turn it off under **Settings → Domains and HTTPS → Look up a visitor's country
and network** — on a cluster with no way out to the internet, or if you would
rather nothing was downloaded. A rule about a country then cannot be saved,
which is better than one that is quietly skipped.

Point the two database settings somewhere else to use your own copy, or a
MaxMind subscription. The format is the same.

> Attribution is part of DB-IP's licence. The panel shows it beside the rules
> that use it, and this page is the other half: **IP Geolocation by DB-IP**.

**Behind a Cloudflare tunnel, the country comes from Cloudflare instead**, for
free and without any database — they know the answer already and send it with
the request. The database is then only consulted for what the headers did not
answer.

## What happens to a rule that cannot be answered

A rule about a country is worthless if nothing knows the country. Pretending
otherwise is wrong in both directions: treating it as "did not match" means a
Block rule silently stops blocking, and treating it as "matched" means an Allow
rule silently blocks everybody.

So a rule that cannot be evaluated **does not decide**. It is skipped, by name,
in the firewall's log, and the next rule is tried. The panel refuses to save
such a rule in the first place; this is the backstop for a database that was
configured and then went away.

## Knowing who the visitor is

The firewall reads the visitor's address from `X-Forwarded-For`, and reads it
**from the right**: only the last entry was added by something we know, and
every entry to its left could have been written by the visitor. Anybody can send
that header; nobody can choose which address the firewall judges them by.

If you have a load balancer of your own in front of the cluster, add its address
range to **Settings → Domains and HTTPS → Trusted proxies**. Add only ranges you
control: a trusted range is one whose word is taken for who a visitor is.

## What it costs, and what it cannot do

* **If the firewall's own pods are all down, protected sites return an error.**
  Traefik has no way to ignore a checker that does not answer, so this cannot be
  softened per site. Two replicas run, spread across servers, and a replica with
  no rules loaded reports itself as not ready so a rollout never puts one in
  front of traffic it cannot judge.
* **It is not a WAF.** There is no request body inspection, no rate limiting and
  no bot scoring. It answers "may this address, from this place, reach this
  path", and nothing else.
* A blocked visitor gets a plain 403 and is told nothing about which rule
  stopped them.

> **Never run against a real cluster.** The rules engine, the address handling,
> the geo lookup, the guard's decisions and the rendered Kubernetes objects are
> all unit-tested, and the country and network lookups were checked against the
> real DB-IP databases. Nothing here has been through a live Traefik. See the
> Status section of the README.
