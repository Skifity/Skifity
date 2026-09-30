# Research: stack and component versions

Verified 2026-09-16. Every version here is a *floor*, not a pin: the panel reads the
actual version to install from configuration so an operator can move forward without
waiting for a Skifity release. See [configuration](../configuration.md).

Reconciled with the code on 2026-09-17. This page is research, so it records what was
found and why it was chosen; where the code ended up somewhere else, the code is what
is written here, because a research page nobody can trust is worse than no page.

## Cluster

| Component | Version found | Notes |
|---|---|---|
| k3s | v1.36.x (Kubernetes 1.36 landed 2026-05-27; 1.35 in 2026-01) | We pin a *channel* (`stable`) by default rather than a version, which is what `get.k3s.io` expects. |
| Flannel backend | `wireguard-native` | Encrypts pod-to-pod traffic between VPSes of different providers. Requires the `wireguard` kernel module. Chosen once for the whole cluster: the installer picks it on the first node and falls back to `vxlan` with a visible warning when the kernel cannot do it, and the panel installs every server added afterwards with the same backend. There is no per-server fallback, because nodes on different backends join without an error and then never exchange a packet. See ADR-0003. |
| Embedded etcd | `--cluster-init` on the first server | Makes the single node HA-ready later without a rebuild. Without it, k3s uses SQLite and cannot be promoted. |
| Traefik | bundled with k3s | Kept. Removing it to install ingress-nginx costs RAM and buys little. |
| metrics-server | bundled with k3s | Needed by HPA and by the server dashboard. |
| local-path-provisioner | bundled with k3s | Default StorageClass for single-node. |

### k3s flags we use

Server (first node):

    --cluster-init
    --flannel-backend=<the cluster's backend>
    --secrets-encryption                 # Secrets are encrypted at rest in etcd
    --write-kubeconfig-mode=0600         # 0644 would make it world-readable on the node
    --tls-san=<public ip>
    --node-external-ip=<public ip>       # required when nodes are on different providers
    --node-label=skifity.com/managed=true

A control plane node joining an existing cluster gets the same flags with `--server
<url>` in place of `--cluster-init`. The backend has to match, which is why it is
read from the cluster's setting rather than written here.

Agent:

    K3S_URL=https://<server ip>:6443 K3S_TOKEN=<token>
    --node-external-ip=<public ip>
    --node-label=skifity.com/managed=true
    --node-label=skifity.com/location=<location>   # when one was given
    --node-label=skifity.com/size=<size>           # when one was given

The node role is not a label Skifity sets: Kubernetes already marks a control plane
node, and a second name for the same thing is a second thing to keep true.

Ports that must be open *between cluster members only*: 6443/tcp (API), 10250/tcp (kubelet),
8472/udp (flannel vxlan) or 51820/udp + 51821/udp (wireguard-native, IPv4/IPv6),
2379-2380/tcp (etcd, control-plane nodes only). 80/443 are public.

## Add-ons (installed on demand, never at install time)

| Component | Version found | Installed when |
|---|---|---|
| cert-manager | v1.21.2 (2026-09-11) | At install (small, and HTTPS is a day-one promise) |
| CloudNativePG | 1.29 | First PostgreSQL database is created |
| KEDA + http-add-on | KEDA 2.20, http-add-on 0.15 (beta) | Scale-to-zero is first enabled. Marked **Beta** in the UI because the add-on is beta upstream. |
| Longhorn | v1.10.0 | Cross-node volumes are first enabled; warns about the RAM cost |
| Redis / MariaDB | Bitnami-style charts rendered by us | First database of that type is created |

## Builds

Railway replaced Nixpacks with **Railpack** (Go, BuildKit-native, March 2026); Nixpacks is in
maintenance mode with critical fixes only. Railpack reports 38% (Node) to 77% (Python) smaller
images and better cache hits because it drives BuildKit layers directly. Coolify has already
adopted it as a build pack.

**Decision:** Railpack as the default builder, Dockerfile when the repo has one, and a
"prebuilt image" path. Nixpacks is kept as a selectable fallback because Railpack is younger.
Builds run in-cluster on rootless BuildKit, and push to an in-cluster registry.

A Compose file in the repository is read rather than built: Skifity runs one service per app, so
the panel lists the file's services and fills the form in from the one that is picked, naming what
does not carry over. See ADR-0008.

## Panel

| Concern | Choice | Reason |
|---|---|---|
| Language | Go 1.26 | Single static binary, good Kubernetes and SSH libraries. `CGO_ENABLED=0` everywhere. |
| Kubernetes client | `k8s.io/client-go` v0.37 | client-go tags track Kubernetes: v0.NN matches v1.NN |
| Database | SQLite through `modernc.org/sqlite` | Pure Go, so `CGO_ENABLED=0` cross-compiles cleanly. The panel is a single writer; WAL mode is enough. Postgres would mean bootstrapping a database before the platform that creates databases exists. |
| HTTP | net/http + `chi` router | Standard library shaped, no framework lock-in |
| Realtime | Server-Sent Events | One-directional (progress, logs) is all we need; survives proxies better than WebSocket and needs no upgrade handling |
| Secrets | AES-256-GCM envelope encryption | FIPS-friendly, in the standard library, hardware accelerated |
| MCP | `github.com/modelcontextprotocol/go-sdk` v1.8.0 | Official SDK, stable API guarantee since v1.0 |

## Frontend

* React 19 + TypeScript + Vite + Tailwind CSS v4 + **shadcn/ui** (new-york style, `data-slot`
  primitives, `sonner` for toasts since the old `toast` component is deprecated).
* i18n: **react-i18next**. Key-based, one JSON file per language, native plural handling through
  `Intl.PluralRules` (which is what Russian needs: one/few/many), ~2.8M weekly downloads, and it
  does not need a Babel/SWC macro step the way Lingui does. Lingui's build-time completeness check
  is the one thing it has over i18next, so we reimplement that as a standalone script
  (`web/scripts/check-i18n.mjs`) wired into `npm run build` and CI.
* Fonts: a system stack, not a self-hosted web font. This was researched the other way round —
  Inter plus Noto Sans Devanagari plus Noto Sans SC, self-hosted — and that is several megabytes
  covering four scripts, every one of them embedded in the binary and downloaded by someone on a
  cheap VPS. The stack in `web/src/index.css` names the system faces that exist for each script, so
  Hindi renders in Devanagari and Chinese in a CJK face, with nothing to download and no request to
  a font CDN, which a self-hosted panel should not be making.

## Sources

- https://docs.k3s.io/release-notes/v1.36.X
- https://docs.k3s.io/blog/2026/05/27/K3s-1.36-release
- https://cert-manager.io/docs/releases/
- https://cloudnative-pg.io/releases/cloudnative-pg-1-29.0-released/
- https://keda.sh/http-add-on/0.15/
- https://blog.railway.com/p/introducing-railpack
- https://github.com/railwayapp/railpack
- https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0
- https://ui.shadcn.com/docs/tailwind-v4
- https://tolgee.io/blog/react-i18n-libraries-comparison
