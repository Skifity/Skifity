# What it costs

Numbers that were measured, and numbers that were not. Anything not marked
*measured* is an estimate and says so.

## The panel itself

**Measured** on this build, `linux/amd64`, with an empty database:

| | |
|---|---|
| Resident memory, idle | **35 MiB** |
| Resident memory, after 400 requests | **38 MiB** |
| Binary, stripped | 39 MiB |
| Frontend, gzipped over the wire | 314 KiB across nine files, of which 145 KiB is the panel itself |
| Cold start to answering `/api/health` | under a second |

The memory figure does not drift: 400 requests against the interface, the API
and the documentation moved it by 3 MiB, most of which is the documentation
pages being touched in the binary's read-only data for the first time. It does
not come back down because Go's allocator keeps the arena, not because anything
leaked.

The binary is large because the whole user interface, in five languages, and the
whole documentation set with its screenshots, are inside it, along with the
Kubernetes client. That is the trade for having one file to copy, nothing to
install alongside it, and a troubleshooting page that works on a server with no
route to the internet.

## On a real cluster

**Measured** by the `Cluster` workflow, which installs every push to `main` on
a fresh GitHub-hosted Ubuntu 24.04 server (4 cores, 16 GB) and runs
`test/cluster/verify.sh` against it. The figures are taken at the end of the
run, with cert-manager, the registry, CloudNativePG and the firewall installed
(and KEDA, in the last of them) and the apps the run deployed still running, so
they are a busy server rather than an idle one. Four runs:

| | Measured |
|---|---|
| k3s server process (API server, etcd, controllers), resident | 922–988 MiB |
| The panel's pod, working set | 19–24 MiB |
| The whole machine in use, the runner's own services included | 2.2–2.6 GB |
| The installer, from nothing to a panel answering | 161–169 seconds |

The panel reads smaller in its pod than on a laptop: the kubelet reports the
working set, which leaves out file pages the kernel can drop, and the laptop
figure is everything resident.

k3s is the large one. Every component above brings resource definitions and
controllers of its own, and the API server holds all of them. On a 16 GB
machine nothing asks it to give memory back, so this is the most it took rather
than the least it needs. k3s's own documentation gives
[2 GB](https://docs.k3s.io/installation/requirements#hardware) as the minimum
for a server, and measures
[about 1.6 GB](https://docs.k3s.io/reference/resource-profiling) for one
running a workload and a monitoring stack.

**What a 1 GB server can hold has not been measured.** The installer accepts
900 MB and warns below 1.8 GB without swap, and the README asks for 1 GB at
least and recommends 2 GB, or 1 GB with swap. Every optional component below
makes a small server tighter.

## Components, when you install them

Each is installed the first time it is needed, not at install time. The panel
shows the figure before you agree to it.

| Component | Roughly | Installed when |
|---|---|---|
| cert-manager | 120 MiB | You add a domain |
| Image registry | 60 MiB | You first deploy from source |
| CloudNativePG | 150 MiB | You create a PostgreSQL database |
| KEDA (scale to zero) | 180 MiB | You turn it on |
| Longhorn | 700 MiB **per server** | You turn it on |
| Prometheus and Grafana | 900 MiB | You turn it on |

There is no builder in the table, because none runs between builds: each build
starts a BuildKit of its own and it goes with the build. A build is the most
memory-hungry thing that happens, and it is transient: its BuildKit and the work
it does share the build's limit, 3 GiB by default, and need room to unpack and
compile, so on a 1 GB server a large JavaScript project will run out. The error
says so, and says to add a server or raise the build limit, rather than failing
with an exit code. A build starts cold, with no cache on the server; what makes
an app's second build fast is the cache its first one pushed to the registry.

## Reproducing the panel figures

```sh
make build
SKIFITY_DATABASE_PATH=/tmp/panel.db \
SKIFITY_MASTER_KEY_PATH=/tmp/master.key \
SKIFITY_LISTEN=127.0.0.1:8080 ./bin/skifity server &
grep VmRSS /proc/$!/status
```
