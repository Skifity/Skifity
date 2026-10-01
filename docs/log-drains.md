# Log drains

A log drain ships everything your apps print to a service outside the cluster:
Grafana Loki, Elasticsearch or OpenSearch, Datadog, Axiom, Better Stack, New
Relic, a syslog server, or any HTTPS endpoint that takes JSON lines. The panel
shows an app's recent output and follows it live; a drain is where it is kept,
searched and alerted on for as long as you like.

Find them under **Settings → Log drains**, or with `skifity drains`.

## How it works

The first drain starts a small collector — [Vector](https://vector.dev) — on
every server. It reads the output of every app's instances from the server's
disk, where Kubernetes already keeps it, and sends each line to the drains of
the team the app belongs to. The panel writes its configuration from the
team's drains, keeps it up to date when a drain changes, and takes the whole
collector away again when the last drain is removed. Nothing is installed on
your apps, and nothing about an app changes when a drain is added.

A drain belongs to the team. It sends every project's logs, or only the
projects you pick, which is how one client's project goes to that client's own
Loki while the rest go to the company's Datadog.

## The kinds

| Kind | What it asks for | Where to find it |
|---|---|---|
| **HTTPS** | An address, and optionally one header — usually `Authorization` — with its value | Your receiver's documentation. Lines are sent as JSON, one per line (`application/x-ndjson`). Plain `http` is allowed only for a receiver with no credential. |
| **Grafana Loki** | The Loki address, a username and a password or token, an optional tenant id | Grafana Cloud → your stack → Loki → *Send logs*: the URL, the user number and an access policy token with `logs:write`. For your own Loki, its address and, behind a gateway, a token alone. |
| **Elasticsearch / OpenSearch** | The cluster's address, an index, the API version, and a username and password or an API key | The index may hold the date: `skifity-%Y.%m.%d`, the default, is one a day. Choose version 7 for OpenSearch. |
| **Datadog** | The site and an API key | Organization settings → API keys. The site is the one in your Datadog address, such as `datadoghq.eu`. |
| **Axiom** | A dataset and an API token, an optional region and organization id | Settings → API tokens, with ingest rights on the dataset. The region is the edge domain your organization is on, such as `eu-central-1.aws.edge.axiom.co`. |
| **Better Stack** | The ingesting host and the source token | Telemetry → Sources → your HTTP source: *Ingesting host* and *Source token*. |
| **New Relic** | The region, the account id and a license key | API keys → an *Ingest - License* key; the account id is in the URL of any page. |
| **Syslog over TLS** | A host and a port, 6514 by default | Papertrail's *Log destinations*, or your own rsyslog or syslog-ng with TLS. Lines are RFC 5424. The certificate is checked against the host's name. |

A token, a password or a key is encrypted as it arrives, under the team, the
drain and the address it sends to. It is never shown again — not in the panel,
not by the API, not to an assistant — and it is kept when you change a drain
without retyping it, **unless the address changes**: then it has to be given
again, so nobody can send a stored credential somewhere new by editing the
address under it.

## The test

A drain is not saved until a test has worked. The panel sends one line to it,
clearly marked as a test — *"Skifity log drain test: if you can read this, the
drain … reaches this service. It was sent by the panel, not by an app."* — to
the same address, with the same credentials and in the same shape the
collector will use. A change to the address or the credentials is tested the
same way before it is kept, and **Send a test line** sends one whenever you
like.

What a test proves is the address, the credentials, and that this network can
reach the service. **It does not prove the collector**: that is a different
process on every server. Whether it is running is what the drain's status and
the collector's line on the same page say.

The test refuses what the collector could never reach, and what nobody should
be able to point the panel at: the panel's own machine, the cloud's metadata
service, and anything inside the cluster — the collector is not allowed to
connect to an app, so a drain pointed at one would pass a test and never
receive a line. A service on your own network, at a private address, is fine.

## What is sent

Each line is the app's output as it printed it, with its time, whether it went
to stdout or stderr, and what it is about:

```json
{
  "timestamp": "2026-09-30T12:00:00.123Z",
  "message": "GET /checkout 200 12ms",
  "stream": "stdout",
  "skifity": {
    "kind": "app",
    "team": "Acme", "project": "Shop", "environment": "Production", "app": "Web",
    "team_id": "team_…", "project_id": "prj_…", "app_id": "app_…",
    "process": "worker", "instance": "web-6d8f…-x2x7q", "server": "node-1"
  }
}
```

The names are the ones in the panel; a name changed in the panel reaches the
lines within five minutes. Kubernetes' own labels and annotations are not
sent: they are most of a line's size, and a service that bills by the gigabyte
bills for them.

Each service gets the fields it files by: Loki's labels are `team`, `project`,
`environment`, `app` and `kind`; Elasticsearch and OpenSearch get
`@timestamp`; Datadog gets `service` (the app) and `hostname` (the server);
syslog's application name is the app and its process id the instance.

## Which logs

* **Every app, worker, scheduled command, one-off command and database** in the
  team's environments, limited to the drain's projects when it has some.
* **Builds** only when the drain says so. A build's output can be long, and is
  already kept with its deployment.
* **Not** the panel's own work — backups and restores — and nothing outside
  the team's environments: not the panel, not the cluster, not another team.

Which team a line belongs to is decided from Kubernetes, never from the line:
an environment's namespace carries its team, which only the panel writes, so
nothing an app prints can send its lines to somebody else's drain.

A drain is sent lines from when it is added; a new collector starts at the end
of what is already on each server rather than sending everything Kubernetes
still keeps. If a service is slow or down, each drain holds up to a thousand
lines for it and then drops the newest, so one drain that is down never stops
another team's.

## The collector

The collector is Vector 0.58.0, pinned by version and by digest, in a
namespace of its own, `skifity-logs`, as one DaemonSet. What it is allowed is
the least a log collector can be:

* **It reads `/var/log/pods` on each server, read-only, and nothing else of
  the server.** That is where Kubernetes keeps every container's output. This
  host path is why its namespace allows it, at the privileged Pod Security
  level; the namespace warns and audits at restricted, so anything else put
  there is noticed. The panel's own namespace is not lowered for it.
* **It runs as an unprivileged user in the root group**, because the log files
  are readable by root's group and nobody else. No capability, no privilege
  escalation, a read-only filesystem, the runtime's seccomp profile, and a
  memory limit of 512 MB.
* **It may list and watch pods, namespaces and nodes, and nothing else** — to
  know which files are on its server and whose each one is. It cannot read a
  Secret, write anything, or read a log through the Kubernetes API.
* **It may connect to DNS, the Kubernetes API, and addresses outside the
  cluster's pod and service networks** — not to an app or a Service, its own
  team's or anybody else's, nor to the cloud's metadata service — and nothing
  may connect to it.
* **Its configuration is a Secret**, because it holds the drains' credentials.
  None of them is in the DaemonSet, which anything that can list DaemonSets
  can read.

It costs about 100 MB of memory per server while there is a drain, and nothing
when there is none.

## Limits

A team can have ten drains. A drain's name is unique in its team.

## From the command line

```sh
skifity drains                                # the team's drains, and the collector
skifity drains add Axiom --kind axiom --set dataset=apps   # asks for the token
skifity drains add Loki --kind loki --set url=https://logs-prod-012.grafana.net \
  --set username=123456 --secret-file password=./token --project shop --builds
skifity drains test Axiom
skifity drains remove Axiom
```

A credential is never a flag's value, so it does not end up in your shell's
history: it is read from a file (`-` is stdin), piped in, or asked for. An
assistant can list drains (`list_log_drains`) and cannot add one, because
adding one takes a credential.

## Compared with others

Coolify ships logs from a Fluent Bit container on each server, one kind of
drain per server, with its credentials in a file on the server and each app
opted in one by one. Here a drain belongs to a team, can be limited to its
projects, is tested before it is saved, keeps its credentials encrypted and
in a Secret, and every app is shipped without being touched. Dokploy and Kubero
show live logs and do not ship them.
