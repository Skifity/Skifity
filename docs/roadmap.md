# Roadmap

Where the work goes next, largest first. `docs/progress.md` is what has been
done; this is what is left and why it is in this order.

The ordering is by how much a person loses without it, not by how hard it is. A
panel that fills its own disk in month two is a worse product than one that
cannot guess a framework, so garbage collection came before detection.

**Phases 11 to 16 below are built.** What they were and what came out of them is
in `docs/progress.md`; they are kept here with their reasoning, because the
reasoning is what the next phase is chosen against.

---

## Phase 11 — A cluster that survives its second month — *done*

Everything in the product worked on day one. These were the things that quietly
degraded afterwards, which is the failure people remember, because it happens
when they have already trusted it with something.

### 11.1 Garbage-collect the in-cluster registry — *done*

Every build pushed an image and nothing ever removed one. Layers are shared and
the images are small, so this was slow rather than sudden, which is exactly why
it goes unnoticed until a node has no disk left and every pod on it stops.

### 11.2 Stop the panel's own database growing forever — *done*

Backups had a retention policy and build logs were pruned. Audit entries,
finished deployments and operations were not, and the database is a file on one
node.

---

## Phase 12 — The panel says what is wrong before it is asked — *done*

The panel was good at reporting that something failed and vague about why.
These were the two questions people actually ask.

### 12.1 Say why an instance cannot start — *done*

### 12.2 Show an environment's quota headroom — *done*

---

## Phase 13 — The panel can be monitored like anything else — *done*

`GET /api/metrics`. Documented in `docs/configuration.md`.

---

## Phase 14 — The deploy story is finished — *done*

### 14.1 Detect the framework before the first build — *done*

### 14.2 Back up a volume, not only a database — *done*

---

## Phase 15 — Frontend weight and keyboard — *done*

Split by what changes rather than by what it does, so an upgrade re-downloads
the panel's own code and nothing else. Plus the labels only a screen reader
hears, which were hardcoded English in a panel shipping five languages.

---

## Phase 16 — The documentation for all of it — *done*

---

## Phase 17 — A second pass over tenant isolation — *done*

Every route proved to reach the store through an authorization helper, every
second id in a URL checked against the first, and the three other ways into the
same API — the CLI, the MCP server and the Git webhook — traced to the same
checks. Three things came out of it, in `docs/progress.md`.

The largest was not an authorization gap at all but an address one: two settings
hold somewhere the panel's own process then makes a request to, and nothing
stopped that being the cloud metadata service. `internal/netguard` refuses it at
connection time.

The lasting part is that the api package now has tests. "Authorization lives in
one place" was true and unenforced, which is a different thing from being safe.

---

## Phase 18 — The code that runs as root on somebody's server — *done*

The provisioning path, which is where a defect does the most damage. It found
that `%q` had been used as shell quoting in every script the panel generates,
and that the guard against removing the last control plane counted rows in one
team's table instead of the cluster's own nodes.

Both had survived review for the same reason: a check placed after a "is this
configured" branch only runs on a configured panel, and nothing in this sandbox
has one. The ordering is fixed and the tests run without a cluster.

---

## What is next

In the order it matters.

1. **Run it on real hardware** — *done, on every push.* The `Cluster`
   workflow installs each push to `main` on a fresh GitHub-hosted Ubuntu server
   with the real installer and works through `test/cluster/verify.sh`, with a
   bucket and an ACME server standing in beside it, and upgrades the last
   release to the commit with an app serving; the `Two servers` workflow runs
   the same across two servers, once more across a link that delays and drops
   packets, and switches one off without warning. Their
   runs found a database volume asking for a storage class that is not there, a
   database kept off the Kubernetes API, an app on a cloud machine given no
   address, a new pod unfenced for its first moment, a scale-to-zero add-on
   fetched by a name no release has, a certificate no app could ever be
   issued, a database engine that never started, an upgrade that dropped the
   panel's domain, a server that could not be removed once it had failed, a
   restart that put both of an app's instances on one server, builds that
   could not clone from a Git host on one's own network, a removed server
   taking its ingress away mid-request, single sign-on refused on a fresh
   install and never given Dex's or Okta's groups, and an app made from a
   clone URL that no push ever deployed; all are fixed. `docs/progress.md` has
   each run.
2. **Tag a release** — *done.* v0.1.0 was cut before the cluster run, by the
   maintainer's decision; v0.1.1 is the first tag cut after it, with everything
   it found fixed. `docs/releasing.md` is the procedure.
3. **Measure k3s's own footprint** — *done.* A fresh install takes about 1 GB
   of memory before the first app, k3s 758 MiB of it, measured on every push
   (`docs/performance.md`). That is why the first server's minimum is now 2 GB
   and not the 1 GB the README used to promise.
4. **A second pair of eyes on the security model.** Phase 17 below was the
   first pass and found three things. It was still the same eyes. Tenant
   isolation is the one class of bug where being wrong is not recoverable, and
   it deserves somebody who did not write any of this.

   Half of that is now arranged and half is not. **CodeQL** runs on every push
   and weekly with `security-extended`: a different analyser, with a different
   model of the code, reading taint from source to sink across packages, into
   the Security tab where a finding cannot be quietly forgotten. **Dependabot**
   opens the update before `make audit` has to report it. Neither is a person.
   A review by somebody who did not write this is still owed, and a tool that
   agrees with the author is not evidence that the author was right.

## Not on this roadmap, and why

* **An interactive shell into a running instance.** A one-off command runs in
  the app's own image with the app's own variables, can be watched, and leaves a
  log, and it works when the app will not start — which is when people reach for
  a shell. An interactive session is more surface, more risk, and less useful at
  the moment it matters.
* **A second component library.** The panel is shadcn/ui and stays that way.
* **Multi-cluster.** One panel, one cluster, is the product. Somebody running
  two clusters runs two panels, and that is a fine answer.
* **A hosted version.** Everything here assumes the person running it owns the
  machine. That assumption is load-bearing: it is why a presigned URL is enough,
  why the master key sits on disk, and why there is no billing anywhere.

## The one thing none of this fixes

Every run is on machines GitHub lends for an hour. Two servers there are two
on one runner, with a link between them that only imitates a long one, a
bucket is a stand-in, and the ACME server is a test server on the same
machine. So servers in different places, a bucket that is not a stand-in, a
certificate from Let's Encrypt itself for a domain on the internet, and
restoring into a different cluster are still owed by somebody running it on
servers of their own. So is
the check no script can do, phase 6: a person, the documentation, and nothing
else.
