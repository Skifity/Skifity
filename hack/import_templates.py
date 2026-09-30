"""Convert Coolify's service templates into Skifity catalogue files.

Skifity runs one service per app and provisions databases itself, so a Coolify
template converts when it has exactly one application service; its database
services become managed databases, which is better than running them as apps
because they then get backups.

Nothing here runs at build time. It is a one-off that produced the files in
internal/templates/catalogue, kept so the next batch is a re-run rather than an
afternoon of copying.
"""
import base64, json, re, sys, yaml

sys.path.insert(0, "hack")
from import_multi import declared_port, fqdn_marker, known_port, self_check_port

DB_IMAGE = re.compile(r'\b(postgres|postgis|pgvector|mysql|mariadb|redis|valkey|keydb|mongo)\b', re.I)
ENGINE = [("postgres", ("postgres", "postgis", "pgvector")),
          ("mysql", ("mysql", "mariadb")),
          ("redis", ("redis", "valkey", "keydb"))]

# Coolify writes these into a compose file and substitutes them itself. Skifity
# has its own equivalents, so a value mentioning one is dropped rather than
# carried over as a literal.
COOLIFY_VAR = re.compile(r'\$\{?SERVICE_|\$\{?COOLIFY')


def engine_of(image):
    for engine, words in ENGINE:
        if any(w in image.lower() for w in words):
            return engine
    return None


# A compose file wires services together by service name: DB_HOST=mariadb,
# REDIS_HOST=redis. Skifity has no sibling container to point at — the database
# is a managed one, reached through the variable the link injects — so carrying
# these over produces an app that starts, cannot resolve its database, and
# crash-loops with a hostname nobody recognises in the log.
# The prefix may be the application's own — GLPI_DB_PORT, MB_DB_PORT,
# KC_DB_URL_PORT — so this is two conditions rather than one shape: the key
# mentions a datastore somewhere, and ends in something that is an address.
DATASTORE_WORD = re.compile(
    r'(^|_)(DB|DATABASE|POSTGRES|POSTGRESQL|PG|MYSQL|MARIADB|REDIS|VALKEY|KEYDB|MONGO|MONGODB|'
    r'CACHE|QUEUE|BROKER|AMQP|RABBITMQ|ELASTIC|ELASTICSEARCH|MEILI|CLICKHOUSE)($|_)', re.I)
ADDRESS_WORD = re.compile(
    r'(^|_)(HOST|HOSTNAME|PORT|SERVER|ADDR|ADDRESS|URL|URI|DSN|CONNECTION|CONNECTIONSTRING)$', re.I)


def is_wiring(key):
    return bool(DATASTORE_WORD.search(key) and ADDRESS_WORD.search(key))


def env_of(body, siblings=()):
    out, raw = {}, body.get("environment")
    items = raw if isinstance(raw, list) else [f"{k}={v}" for k, v in (raw or {}).items()]
    for item in items or []:
        if not isinstance(item, str) or "=" not in item:
            continue
        key, value = item.split("=", 1)
        key, value = key.strip().strip("'\""), value.strip().strip("'\"")
        if not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', key):
            continue
        # A value Coolify fills in, or one that is empty, is not ours to invent.
        if COOLIFY_VAR.search(value) or "${" in value or value == "":
            continue
        if is_wiring(key):
            continue
        # A value the compose file expected a shell to expand is not a value.
        if "$" in value:
            continue
        # Any value that is the name of another service in the same compose
        # file is a container address, whatever the key is called.
        if value in siblings:
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
            continue  # a bind mount is the host's, not the app's
        name = re.sub(r'[^a-z0-9-]+', '-', source.lower()).strip("-")
        if name and target:
            out.append({"name": name[:30], "mount_path": target, "size_gb": 5})
    return out[:2]


def convert(key, template, resolved, compose):
    services = compose.get("services") or {}
    databases = {n: b for n, b in services.items()
                 if isinstance(b, dict) and DB_IMAGE.search(str(b.get("image", "")))
                 and engine_of(str(b.get("image", "")))}
    apps = [n for n in services if n not in databases]
    if len(apps) != 1:
        return None, "not a single-service template"

    name = apps[0]
    body = services[name]
    pinned = resolved.get(key, {}).get("to")
    if not pinned:
        return None, "no verified image tag"

    # Coolify's own metadata names the port for most templates. Where it does
    # not, the compose file still says so — in a domain marker, an expose entry,
    # or the healthcheck the service runs against itself — and reading that is
    # not the same as guessing.
    port = (int(template.get("port") or 0) or fqdn_marker(name, body)[1]
            or declared_port(body) or known_port(body) or self_check_port(body))
    if not port:
        return None, "no port"
    if not 1 <= port <= 65535:
        # Coolify's metadata says healthchecks listens on 80000. It does not:
        # there is no such port. A number out of range is a typo upstream, not
        # a port to carry into a manifest.
        return None, f"port {port} is not a port"

    slug = re.sub(r'[^a-z0-9-]+', '-', key.lower()).strip("-")
    out = {
        "id": slug,
        "name": template.get("name") or key.replace("-", " ").title(),
        "description": (template.get("slogan") or "").strip(),
        "category": template.get("category") or "other",
        "website": template.get("documentation", "").split("?")[0] or "",
        "services": [{
            "name": re.sub(r'[^a-z0-9-]+', '-', name.lower()).strip("-")[:30] or slug,
            "image": pinned,
            "port": int(port),
            "public": True,
            "mem_request_mb": 128, "mem_limit_mb": 1024,
            "cpu_request_m": 50, "cpu_limit_m": 1000,
        }],
    }
    variables = env_of(body, siblings=set(services))
    if variables:
        out["services"][0]["variables"] = variables
    volumes = volumes_of(body)
    if volumes:
        out["services"][0]["volumes"] = volumes

    if databases:
        first = list(databases)[0]
        engine = engine_of(str(databases[first].get("image", "")))
        out["databases"] = [{
            "name": (slug + "-db")[:40], "engine": engine, "storage_gb": 5,
            # A list: a template may install several apps that share it, and a
            # single name silently linked only the first.
            "link_to": [out["services"][0]["name"]], "var_name": "DATABASE_URL",
        }]
    return out, "ok"


if __name__ == "__main__":
    coolify = json.load(open("/tmp/tpl/coolify.json"))
    resolved = json.load(open("/tmp/tpl/resolved.json"))
    made, skipped = {}, {}
    for key, template in coolify.items():
        try:
            compose = yaml.safe_load(base64.b64decode(template["compose"]).decode())
        except Exception:
            skipped[key] = "unreadable compose"
            continue
        out, why = convert(key, template, resolved, compose)
        (made if out else skipped).__setitem__(key, out or why)
    json.dump(made, open("/tmp/tpl/converted.json", "w"), indent=1)
    print(f"converted {len(made)}, skipped {len(skipped)}", file=sys.stderr)
    from collections import Counter
    print(Counter(skipped.values()).most_common(8), file=sys.stderr)
