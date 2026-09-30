# Skifity architecture

## One sentence

A single Go binary runs inside a k3s cluster, talks to the Kubernetes API for everything that runs,
talks to SSH for everything that turns a bare VPS into a cluster member, and serves an embedded
React panel that never says the word "pod".

## Is Skifity the cluster, or a panel on top of one?

A panel on top of one, and the installer that puts the one there. The
distinction matters more than it sounds, so it is worth being exact.

**The cluster is k3s**, installed from `get.k3s.io`. The API server, etcd,
containerd, flannel, CoreDNS and the Traefik ingress are all k3s, and Skifity
wrote none of them. Additional servers are k3s nodes joined over SSH.

**Skifity is three things in one binary**: the installer that creates that
cluster and joins servers to it, the panel and API that render Kubernetes
objects and apply them, and its own record of what it has been asked for.

**It is not a Kubernetes distribution.** It ships k3s.

**It is not an operator.** It defines no CustomResourceDefinitions and runs no
reconcile loop. It renders Deployments, Services, Ingresses and the rest, and
applies them with server-side apply. `internal/watch` polls once a minute to
notice what has changed for the worse and say so — it does not reconcile.

**It is not in the request path.** Traffic to your apps goes ingress → Service →
pod. The panel is not on that path and never has been, which is why the apps
keep answering with the panel scaled to zero.

Three consequences follow, and they are the whole trade:

* **Nothing is locked in.** Your apps are ordinary Kubernetes objects in an
  ordinary cluster. `kubectl` works. `skifity export` writes the manifests out.
  Remove the panel and everything keeps running.
* **Kubernetes does the hard parts.** Rescheduling, rolling updates, node
  failure and autoscaling are k3s and its controllers, not panel code. A panel
  that is restarting is not a panel that has stopped your apps.
* **The panel is a single point of *change*, not of traffic.** Its state is one
  SQLite file on one node's disk, mounted from the host, which is why the
  Deployment is pinned to that node. Lose it and your apps keep serving while
  you cannot deploy until it is restored. `docs/configuration.md` says what to
  back up.

It also holds `cluster-admin`, and `deploy/panel.yaml` says why rather than
shipping a narrower role that quietly breaks: it creates a namespace per
environment and installs components that define their own CRDs and ClusterRoles,
and the right to create a ClusterRole is equivalent to cluster-admin anyway. The
isolation that matters is between tenants, and that is enforced on the
namespaces the panel creates.

## Layers

```mermaid
flowchart TB
    subgraph browser["Browser / CLI / AI assistant"]
        UI["Panel (React 19 + shadcn/ui)"]
        CLI["skifity CLI"]
        MCP["MCP client"]
    end

    subgraph panel["skifity binary (one Pod in the cluster)"]
        API["HTTP API (chi)"]
        AUTH["Auth: sessions, TOTP, API tokens, RBAC"]
        CRYPTO["Envelope encryption"]
        STORE["SQLite (WAL) on the node's disk"]
        EVENTS["SSE hub"]
        ORCH["Orchestrators: provision / build / deploy / database / backup"]
        KUBE["Kubernetes client (client-go)"]
        SSH["SSH client"]
    end

    subgraph cluster["k3s cluster"]
        APIsrv["kube-apiserver"]
        TRAEFIK["Traefik ingress"]
        CM["cert-manager"]
        REG["in-cluster registry"]
        BK["BuildKit (rootless)"]
        APPS["User apps"]
        DBS["CloudNativePG / Redis / MariaDB"]
    end

    VPS["Bare VPS being added"]

    UI --> API
    CLI --> API
    MCP --> API
    API --> AUTH --> ORCH
    ORCH --> STORE
    ORCH --> CRYPTO
    ORCH --> EVENTS --> UI
    ORCH --> KUBE --> APIsrv
    ORCH --> SSH --> VPS
    APIsrv --> TRAEFIK & CM & REG & BK & APPS & DBS
```

## Request path of a deploy

```mermaid
sequenceDiagram
    participant U as User / webhook
    participant A as Panel API
    participant D as Deploy orchestrator
    participant B as BuildKit
    participant R as Registry
    participant K as Kubernetes

    U->>A: Deploy app (commit sha)
    A->>D: create Deployment record (queued)
    D->>D: compute build fingerprint
    alt fingerprint unchanged
        D->>K: patch Deployment (rollout only)
    else new fingerprint
        D->>B: build job (Railpack or Dockerfile)
        B-->>D: log stream (SSE to the UI)
        B->>R: push image
        D->>K: apply Deployment/Service/Ingress/HPA
    end
    K-->>D: rollout status
    D-->>U: succeeded, or a plain-language failure with a suggested fix
```

## Background work

Anything that takes longer than a request runs in a goroutine of its own: a
deployment, a server being added, a database being provisioned, a backup, a
restore, the health watcher, the minute scheduler, a notification.

Each of them starts with a recover (`internal/runsafe`). A panic is a bug, and a
bug in one deployment must not end the panel — on a self-hosted install the
panel is usually the only way to reach the cluster, so the deployment that
crashed it would also remove the way to find out why. The panic is logged with
its stack, and the one thing it was doing is marked failed, so what a user sees
is a deployment that failed rather than one that is still going and never will
be.

The loops recover per iteration rather than around the loop, because a panel
that is alive with no scheduler is worse than one that restarted: nothing says
so, and the backups simply stop happening.

## Adding a server

```mermaid
stateDiagram-v2
    [*] --> Connecting: SSH with password or key
    Connecting --> Fingerprint: record host key
    Fingerprint --> Preflight: OS, RAM, disk, ports, conflicts
    Preflight --> KeyInstall: install panel-owned key, forget the password
    KeyInstall --> Firewall: allow cluster ports from member IPs only
    Firewall --> Connectivity: verify both directions
    Connectivity --> Agent: install k3s agent and join
    Agent --> Ready: wait for node Ready, apply labels
    Ready --> [*]
    Connecting --> Failed
    Preflight --> Failed
    Firewall --> Failed
    Agent --> Failed
    Failed --> Connecting: Retry (every step is idempotent)
    Failed --> [*]: Cancel and clean up
```

## Data model

```mermaid
erDiagram
    TEAM ||--o{ MEMBERSHIP : has
    USER ||--o{ MEMBERSHIP : has
    TEAM ||--o{ PROJECT : owns
    PROJECT ||--o{ ENVIRONMENT : has
    ENVIRONMENT ||--o{ APP : contains
    ENVIRONMENT ||--o{ DATABASE : contains
    APP ||--o{ DEPLOYMENT : has
    APP ||--o{ DOMAIN : has
    APP ||--o{ VARIABLE : has
    APP ||--o{ VOLUME : has
    DATABASE ||--o{ BACKUP : has
    TEAM ||--o{ SERVER : owns
    TEAM ||--o{ AUDIT_EVENT : records
    USER ||--o{ API_TOKEN : owns
```

## Naming: what the user sees vs what Kubernetes sees

| Panel word | Kubernetes object |
|---|---|
| App | Deployment + Service + Ingress |
| Instance | Pod |
| Server | Node |
| Domain | Ingress rule + Certificate |
| Variable / Secret | ConfigMap / Secret |
| Database | CloudNativePG Cluster, or a StatefulSet |
| Environment | Namespace (`<team>-<project>-<env>`) |
| Volume | PersistentVolumeClaim |
| Scaling rule | HorizontalPodAutoscaler |

Kubernetes names appear in the "Advanced" tab of a resource and nowhere else.

## Isolation

Each environment is a namespace carrying a `ResourceQuota`, a `LimitRange` (so a forgotten limit cannot
starve a node), and a default-deny `NetworkPolicy` that permits ingress from Traefik and egress to DNS,
the internet and the environment's own namespace. The panel's ServiceAccount is the only identity with
cluster-wide rights; user-facing tokens are scoped through the panel's own RBAC, never by handing out
kubeconfigs.

## What is installed when

| When | What |
|---|---|
| `install.sh` | k3s server, the panel, cert-manager, and the panel's own directories on the host: its database and its master key |
| First build | in-cluster registry + rootless BuildKit |
| First PostgreSQL | CloudNativePG operator |
| First Redis / MySQL | the matching chart |
| Scale-to-zero enabled | KEDA + http-add-on |
| Cross-node volumes enabled | Longhorn (with a RAM warning) |
| Full monitoring enabled | kube-prometheus-stack (optional; a built-in lightweight view exists without it) |

This is what keeps a fresh install small enough for a 2 GB VPS.
