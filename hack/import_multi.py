"""Convert Coolify's multi-service templates into Skifity catalogue files.

Skifity already installs several apps from one template — `installTemplate`
creates an app per service and links the databases afterwards — so this is a
converter problem rather than a product one. Apps in an environment share a
namespace and reach each other by name, which is what makes the references
between these services work here at all.

What it refuses matters more than what it converts:

  * A service needing the Docker socket cannot run. Containers here are
    unprivileged with every capability dropped, and a template that wants the
    host's Docker is not a template we can honour — the whole stack goes.
  * More than four long-running services and the odds of getting startup order,
    shared state and init steps right without ever running it stop being
    acceptable. A broken Supabase is worse than no Supabase.
  * A one-shot service — creating a bucket, running a migration, generating a
    token — is not an app. It would be created, exit, and crash-loop forever.
    It is dropped and named in the notes, because the step is usually still
    needed and silently skipping it is how a template half-works.
"""
import base64, json, re, sys, yaml

DB_IMAGE = re.compile(r'\b(postgres|postgis|pgvector|mysql|mariadb|redis|valkey|keydb|mongo)\b', re.I)
ONESHOT = re.compile(r'(^|[-_])(init|migrat\w*|setup|bootstrap|seed|token-generator|schema-\w+|createbuckets?|install)([-_]|$)', re.I)
SOCKET = re.compile(r'/var/run/docker\.sock')
COOLIFY_VAR = re.compile(r'\$\{?SERVICE_|\$\{?COOLIFY')
DATASTORE_WORD = re.compile(
    r'(^|_)(DB|DATABASE|POSTGRES|POSTGRESQL|PG|MYSQL|MARIADB|REDIS|VALKEY|KEYDB|MONGO|MONGODB|'
    r'CACHE|QUEUE|BROKER|AMQP|RABBITMQ|ELASTIC|ELASTICSEARCH|MEILI|CLICKHOUSE)($|_)', re.I)
ADDRESS_WORD = re.compile(r'(^|_)(HOST|HOSTNAME|PORT|SERVER|ADDR|ADDRESS|URL|URI|DSN|CONNECTION|CONNECTIONSTRING)$', re.I)

ENGINE = [("postgres", ("postgres", "postgis", "pgvector")),
          ("mysql", ("mysql", "mariadb")),
          ("redis", ("redis", "valkey", "keydb"))]

MAX_SERVICES = 4


def slug(text):
    return re.sub(r'[^a-z0-9-]+', '-', text.lower()).strip("-")[:30]


def engine_of(image):
    for engine, words in ENGINE:
        if any(w in image.lower() for w in words):
            return engine
    return None


# A worker does not listen: sidekiq, a celery beat, a queue consumer. Skifity
# represents one as an app with no port, which gets no Service, no probes and no
# ingress — so the important thing is not to invent a port for it. Giving a
# Sidekiq the web app's port produces a readiness check against something that
# never answers, and an app that is "starting" forever.
WORKER_NAME = re.compile(
    r'(^|[-_])(workers?|sidekiq|celery|beat|scheduler|cron|queue|consumer|daemon|'
    r'runners?|supervisor|jobs?)([-_]|$)', re.I)


MARKER = re.compile(r'SERVICE_FQDN_([A-Z0-9_]+?)(?:_(\d+))?\b')


def markers_in(body):
    """Every SERVICE_FQDN marker written into one service's environment."""
    found = []
    for match in MARKER.finditer(str(body.get("environment") or "")):
        found.append((match.group(1), int(match.group(2)) if match.group(2) else 0))
    return found


def marker_owners(services, names):
    """How many services carry each marker.

    A marker names the service that gets a domain, and the port it answers on:
    SERVICE_FQDN_CWA_8083 is the whole declaration. The first version of this
    insisted the marker's name equal the service's, which is true often enough
    to look right and is not the rule — calibre-web-automated carries CWA, and
    the stack was dropped for having no port when the port was written down.

    What the name check was really defending against is a shared environment
    block: a web app and its Sidekiq declared with one YAML anchor both carry
    the web app's marker, and taking it at face value puts a worker on a port
    it never listens on. That case is visible in the data — the identical
    marker appears under more than one service — so count them and fall back to
    the name only there.
    """
    count = {}
    for name in names:
        for marker in set(markers_in(services[name])):
            count[marker] = count.get(marker, 0) + 1
    return count


def fqdn_marker(name, body, shared=None):
    """The marker that belongs to this service: (public, port)."""
    wanted = re.sub(r'[^A-Z0-9]', "_", name.upper())
    found = markers_in(body)
    for label, port in found:
        if label == wanted:
            return True, port
    for label, port in found:
        if shared is not None and shared.get((label, port), 1) > 1:
            continue  # one environment block, several services: not a declaration
        return True, port
    return False, 0


# Ports that are documented facts about an image rather than guesses about a
# template. Each of these is the port the project itself publishes, and they
# appear again and again as sidecars in these stacks. Anything not here and not
# written down in the compose file is left alone: that is the line between
# knowing and inferring, and crossing it is what gave HeyForm Redis's port.
KNOWN_PORT = {
    "clickhouse/clickhouse-server": 8123,
    "getmeili/meilisearch": 7700,
    "minio/minio": 9000,
    "ghcr.io/coollabsio/minio": 9000,
    "mongo": 27017,
    "nginx": 80,
    "darthsim/imgproxy": 8080,
    "chrislusf/seaweedfs": 8333,
    "docker.elastic.co/elasticsearch/elasticsearch": 9200,
    "elasticsearch": 9200,
    "opensearchproject/opensearch": 9200,
    "qdrant/qdrant": 6333,
    "typesense/typesense": 8108,
    "memcached": 11211,
    "guacamole/guacd": 4822,
    "ollama/ollama": 11434,
    "ghcr.io/browserless/chrome": 3000,
    "elastic/elasticsearch": 9200,
    "kuzzleio/elasticsearch": 9200,
    "percona/percona-server-mongodb": 27017,
    # ZooKeeper serves clients on 2181. Its healthcheck talks to the admin
    # server on 8080, which answers `ruok` and nothing a peer wants — reading
    # the healthcheck gave SigNoz a ZooKeeper that ClickHouse could not reach.
    "signoz/zookeeper": 2181,
    "zookeeper": 2181,
    "bitnami/zookeeper": 2181,
}


def self_check_port(body):
    """The port this service's own healthcheck talks to.

    `curl -fs http://localhost:8083` is the compose file stating, in the
    service's own words, where it listens. It is not an inference about the
    image; it is a line in the file that only makes sense if that port is open.
    """
    text = str(body.get("healthcheck") or "")
    for match in re.finditer(r'(?:localhost|127\.0\.0\.1|0\.0\.0\.0)[:/](\d{2,5})\b', text):
        port = int(match.group(1))
        if 1 <= port <= 65535:
            return port
    return 0


def peer_port(name, body, services):
    """The port another service in the same stack says it reaches this one on.

    Compose files wire themselves up in the open: changedetection carries
    `PLAYWRIGHT_DRIVER_URL=ws://browser-sockpuppet-chrome:3000`, and Kibana
    carries `ELASTICSEARCH_HOSTS=http://elasticsearch:9200`. Each names a
    service and the port it answers on. Matching the host against the service's
    own name — or the hostname it sets — is what keeps this from being the
    environment-scanning that once handed n8n Postgres's port: a bare
    `DB_PORT=5432` names nothing, and is ignored.
    """
    aliases = {name}
    if body.get("hostname"):
        aliases.add(str(body["hostname"]))
    if body.get("container_name"):
        aliases.add(str(body["container_name"]))
    pattern = re.compile(r'(?:^|[/@\s"\'(])(' + "|".join(re.escape(a) for a in sorted(aliases))
                         + r'):(\d{2,5})(?![\d.])')
    for other, peer in services.items():
        for match in pattern.finditer(str(peer.get("environment") or "")):
            port = int(match.group(2))
            if 1 <= port <= 65535:
                return port
    return 0


def declared_port(body, image_is_unique=True):
    """The port the compose file says this service listens on, or 0.

    Only what is written down. Reading a port out of the environment looked
    clever and was not: DB_PORT=5432 and REDIS_PORT=6379 are in there too, and
    guessing from them gave n8n Postgres's port and HeyForm Redis's.
    """
    for entry in body.get("expose") or []:
        digits = re.sub(r'\D', "", str(entry))
        if digits:
            return int(digits)
    for entry in body.get("ports") or []:
        text = str(entry)
        if text.endswith("/udp"):
            # Skifity publishes an app over an HTTP ingress. A UDP port is a
            # game server's, and giving Palworld an ingress on 8211 produces a
            # domain that will never answer.
            continue
        parts = text.split("/")[0].split(":")
        if parts and parts[-1].isdigit():
            return int(parts[-1])
    # The table is about an image, so it can only answer for a service that is
    # the only one running that image. seaweedfs runs a master and an admin from
    # one image on different ports, and answering 8333 for both was wrong twice.
    if not image_is_unique:
        return 0
    return 0


def known_port(body, image_is_unique=True):
    """What the image itself publishes, for images whose port is a fact.

    The table is about an image, so it can only answer for a service that is
    the only one running that image. seaweedfs runs a master and an admin from
    one image on different ports, and answering 8333 for both was wrong twice.
    """
    if not image_is_unique:
        return 0
    return KNOWN_PORT.get((body.get("image") or "").split(":")[0], 0)


def env_of(body, databases):
    """Variables the app needs, minus the ones that point at a database.

    A reference to another *application* service is kept: those exist here, under
    the same name, reachable in the same namespace. A reference to a database is
    not: Skifity provisions those and injects a connection string instead.
    """
    out, raw = {}, body.get("environment")
    items = raw if isinstance(raw, list) else [f"{k}={v}" for k, v in (raw or {}).items()]
    for item in items or []:
        if not isinstance(item, str) or "=" not in item:
            continue
        key, value = item.split("=", 1)
        key, value = key.strip().strip("'\""), value.strip().strip("'\"")
        if not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', key):
            continue
        if COOLIFY_VAR.search(value) or "${" in value or "$" in value or value == "":
            continue
        if DATASTORE_WORD.search(key) and ADDRESS_WORD.search(key):
            continue
        if value in databases:
            continue
        out[key] = value
    return out


def volumes_of(body):
    out = []
    for entry in body.get("volumes") or []:
        if not isinstance(entry, str) or ":" not in entry:
            continue
        source, target = entry.split(":")[:2]
        if source.startswith(("/", ".")) or not target.startswith("/"):
            continue
        name = slug(source)
        if name and target:
            out.append({"name": name, "mount_path": target, "size_gb": 5})
    return out[:2]


def convert(key, template, compose, pinned):
    services = compose.get("services") or {}
    databases = {n: b for n, b in services.items()
                 if isinstance(b, dict) and DB_IMAGE.search(str(b.get("image", "")))
                 and engine_of(str(b.get("image", "")))}
    apps = [n for n in services if n not in databases]
    if len(apps) <= 1:
        return None, "not multi-service"

    if any(SOCKET.search(str(services[n].get("volumes") or "")) or services[n].get("privileged")
           for n in apps):
        return None, "needs the Docker socket or a privileged container"

    oneshot = [n for n in apps
               if ONESHOT.search(n) or str(services[n].get("restart", "")).lower() in ("no", '"no"')]
    real = [n for n in apps if n not in oneshot]
    if not 1 <= len(real) <= MAX_SERVICES:
        return None, f"{len(real)} long-running services"

    # How many services share each image, so the table above knows when it can
    # answer and when the answer would be a coincidence.
    image_count = {}
    for name in real:
        base = (services[name].get("image") or "").split(":")[0]
        image_count[base] = image_count.get(base, 0) + 1

    shared_markers = marker_owners(services, real)
    marked_public = [n for n in real
                     if fqdn_marker(n, services[n], shared_markers)[0]
                     and not WORKER_NAME.search(n)]

    out_services, names = [], set()
    for name in real:
        body = services[name]
        image = pinned.get(f"{key}::{name}") or pinned.get(body.get("image", ""))
        if not image:
            return None, f"no verified image for {name}"
        public, marked_port = fqdn_marker(name, body, shared_markers)
        unique = image_count.get((body.get("image") or "").split(":")[0], 0) == 1
        # In order of how directly the source says it: the domain marker, an
        # explicit ports/expose entry, the port a peer dials, the image's own
        # published port, and last the port its healthcheck talks to.
        port = (marked_port or declared_port(body, unique)
                or peer_port(name, body, services)
                or known_port(body, unique) or self_check_port(body))
        if WORKER_NAME.search(name) and not (marked_port or declared_port(body, unique)):
            # A worker's readiness probe would be pointed at a port nothing is
            # listening on. A healthcheck that shells out, or a peer reference
            # that names the web app, is not this service declaring a port.
            port, public = 0, False
        if port == 0 and public and len(marked_public) == 1:
            # The source says this service is the one with a domain, and says
            # separately which port the template publishes. Both come from the
            # file; neither is inferred. Only when it is the only public one,
            # or the number would be right for at most one of them.
            port = int(template.get("port") or 0)
        if not public and port == 0 and WORKER_NAME.search(name):
            pass  # a worker: no port, and none invented
        elif port == 0:
            # Nothing said what this listens on, and guessing is what produced
            # a search engine with no port and a web app on a database's.
            return None, f"nothing says what {name} listens on"
        if port and not 1 <= port <= 65535:
            return None, f"port {port} for {name}"
        service_slug = slug(name) or slug(key)
        if service_slug in names:
            return None, "two services with the same slug"
        names.add(service_slug)

        entry = {
            "name": service_slug,
            "image": image,
            "port": int(port),
            # Public when the source marked it with a domain of its own. Each
            # app here can have a domain, so several public services is fine.
            "public": public,
            "mem_request_mb": 128, "mem_limit_mb": 1024,
            "cpu_request_m": 50, "cpu_limit_m": 1000,
        }
        variables = env_of(body, set(databases))
        if variables:
            entry["variables"] = variables
        volumes = volumes_of(body)
        if volumes:
            entry["volumes"] = volumes
        out_services.append(entry)

    listening = [s for s in out_services if s["port"] > 0]
    if not listening:
        return None, "nothing in it listens on a port"
    if not any(s["public"] for s in out_services):
        # Nothing was marked, so the one carrying the template's own port is it.
        main = max(listening, key=lambda s: s["port"] == template.get("port"))
        main["public"] = True

    out = {
        "id": slug(key),
        "name": template.get("name") or key.replace("-", " ").title(),
        "description": (template.get("slogan") or "").strip(),
        "category": template.get("category") or "other",
        "website": (template.get("documentation") or "").split("?")[0],
        "services": out_services,
    }

    infrastructure = set()
    for name in real:
        base = (services[name].get("image") or "").split(":")[0]
        if base in KNOWN_PORT or DB_IMAGE.search(base):
            infrastructure.add(slug(name))
    linkable = {s["name"] for s in out_services} - infrastructure

    if databases and linkable:
        first = list(databases)[0]
        out["databases"] = [{
            "name": (slug(key) + "-db")[:40],
            "engine": engine_of(str(databases[first].get("image", ""))),
            "storage_gb": 5,
            # Every app in the stack, not just the first: a web app and its
            # worker share one database, and linking only one of them leaves
            # the other without the variable it cannot run without.
            #
            # Not the datastores and sidecars, though. ClickHouse, MinIO,
            # Meilisearch and a headless Chrome are not applications that read a
            # DATABASE_URL, and handing one to ClickHouse says this stack's
            # analytics store depends on its Postgres, which is not true.
            "link_to": [s["name"] for s in out_services if s["name"] in linkable],
            "var_name": "DATABASE_URL",
        }]

    notes = []
    # A compose volume is shared between the services that mount it. A Skifity
    # volume belongs to one app: each gets its own claim, read-write-once. Two
    # apps mounting the same path therefore get two different directories, and
    # for something like Chatwoot — where the web app writes an upload and the
    # worker reads it — that is a real difference, not a detail.
    mounted = {}
    for service in out_services:
        for volume in service.get("volumes") or []:
            mounted.setdefault(volume["mount_path"], []).append(service["name"])
    shared = {path: names for path, names in mounted.items() if len(names) > 1}
    if shared:
        notes.append(
            "In the original this stack shares "
            + ", ".join(sorted(shared))
            + " between "
            + " and ".join(sorted({name for names in shared.values() for name in names}))
            + ". Skifity gives each app storage of its own, so those are separate "
            "directories here. If the apps need to see the same files, point them "
            "at object storage — MinIO is in this catalogue — rather than a path.")
    if oneshot:
        notes.append(
            "This stack has a set-up step that runs once and exits — "
            + ", ".join(sorted(oneshot))
            + ". Skifity runs apps, not one-shot containers, so that step is not "
            "installed: do it yourself from the app's Commands tab, or follow the "
            "project's own instructions, before expecting everything to work.")
    if len(out_services) > 1:
        notes.append(
            "This template installs "
            + str(len(out_services))
            + " apps. They reach each other by name inside the environment, so "
            "keep them together.")
    if notes:
        out["notes"] = " ".join(notes)
    return out, "ok"


def align_versions(key, compose, pinned):
    """Give services that shared one version variable one version again.

    Immich's compose writes ${IMMICH_VERSION:-release} as the tag of both the
    server and the machine-learning image: upstream requires the two to match,
    and the file says so by using the same variable twice. Resolving each on its
    own produced a v1.132.3 server beside a v1.106.4 model runner — two tags
    that exist, one stack that does not work. Take the newest tag any member
    resolved to and use it for all of them, but only after the registry confirms
    every repository has it.
    """
    import resolve_tags

    groups = {}
    for name, body in (compose.get("services") or {}).items():
        ref = str(body.get("image") or "")
        _, _, tag = ref.rpartition(":")
        if "$" not in tag or "/" in tag:
            continue
        groups.setdefault(tag, []).append(name)

    for tag, names in groups.items():
        entries = [(n, pinned.get(f"{key}::{n}")) for n in names]
        have = [(n, v) for n, v in entries if v]
        if len(have) < 2 or len(have) != len(entries):
            continue
        picked = sorted({v.rpartition(":")[2] for _, v in have},
                        key=lambda t: [int(p) if p.isdigit() else 0
                                       for p in re.findall(r'\d+', t)] or [0])[-1]
        aligned = {n: v.rpartition(":")[0] + ":" + picked for n, v in have}
        if all(resolve_tags.exists(image) for image in aligned.values()):
            for name, image in aligned.items():
                pinned[f"{key}::{name}"] = image
        else:
            print(f"  {key}: {tag} does not resolve to one tag all of "
                  f"{', '.join(names)} have", file=sys.stderr)


if __name__ == "__main__":
    sys.path.insert(0, "hack")
    coolify = json.load(open("/tmp/tpl/coolify.json"))
    pinned = {}
    for source in ("/tmp/tpl/multi-resolved.json",):
        for key, value in json.load(open(source)).items():
            if value["to"]:
                pinned[key] = value["to"]
    for image, to in json.load(open("/tmp/tpl/already.json")).items():
        pinned[image] = to

    made, skipped = {}, {}
    for key, template in coolify.items():
        try:
            compose = yaml.safe_load(base64.b64decode(template["compose"]).decode())
        except Exception:
            continue
        align_versions(key, compose, pinned)
        out, why = convert(key, template, compose, pinned)
        if out:
            made[key] = out
        elif why != "not multi-service":
            skipped[key] = why

    json.dump(made, open("/tmp/tpl/converted-multi.json", "w"), indent=1)
    print(f"converted {len(made)}, skipped {len(skipped)}", file=sys.stderr)
    from collections import Counter
    for reason, count in Counter(skipped.values()).most_common(10):
        print(f"  {count:4}  {reason}", file=sys.stderr)
