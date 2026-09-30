"""Write converted templates into internal/templates/catalogue as YAML.

Everything here is a quality gate. A template that reaches the catalogue has an
image tag that was resolved to a real version and then fetched from the registry
to prove it exists, a port, a description somebody can read, and a link out to
the upstream project. Anything missing one of those is dropped rather than
shipped half-filled, because a template nobody can run is worse than a template
that is not there.

Eight templates were written by hand before any of this existed and are still
the reference for what a good one looks like. They are never overwritten: an
importer that quietly replaced them would be losing work, not doing it.
"""
import json, os, re, sys, yaml

CATALOGUE = "internal/templates/catalogue"
ORDER = ["id", "name", "description", "category", "website", "beta",
         "services", "databases", "inputs", "notes"]

HAND_WRITTEN = {"wordpress", "n8n", "ghost", "uptime-kuma",
                "plausible", "vaultwarden", "umami", "minio"}

# Categories Coolify uses that mean nothing to somebody browsing a panel, mapped
# onto the ones already in this catalogue.
# The source's categories, folded onto a set somebody browsing a panel would
# recognise. Left as a table rather than a rule because the folding is a
# judgement — "backend" and "api" are developer tools here, "RSS" is media —
# and a rule would hide that.
CATEGORY = {
    "": "other", "(none)": "other",
    "cms": "cms", "documentation": "cms",
    "analytics": "analytics",
    "monitoring": "monitoring",
    "automation": "automation", "ci": "automation",
    "productivity": "productivity", "helpdesk": "productivity", "games": "productivity",
    "storage": "storage", "database": "storage", "databases": "storage", "search": "storage",
    "ai": "ai", "mcp": "ai",
    "messaging": "communication", "email": "communication", "Mail": "communication",
    "developer": "developer", "development": "developer", "devtools": "developer",
    "devops": "developer", "backend": "developer", "api": "developer", "git": "developer",
    "media": "media", "RSS": "media",
    "security": "security", "auth": "security", "vpn": "security",
    "proxy": "networking", "Networking": "networking",
    "finance": "finance",
    "ecommerce": "ecommerce", "crm": "business", "erp": "business",
}

FLOATING_TAG = re.compile(r'(^|[-_.])(latest|main|master|stable|edge|nightly|release|dev)$', re.I)


def usable(template):
    if not template.get("description") or len(template["description"]) < 15:
        return "no description"
    website = template.get("website") or ""
    if not website.startswith("https://"):
        return "no https website"
    services = template.get("services") or []
    if not services:
        return "no services"
    names = set()
    for service in services:
        # Every service, not just the first: a stack ships as one unit, and a
        # second app on a floating tag is the same failure as a first one.
        if service["name"] in names:
            return "two services with one name"
        names.add(service["name"])
        if not 0 <= service["port"] <= 65535:
            return "port out of range"
        if FLOATING_TAG.search(service["image"].rpartition(":")[2]):
            return "floating tag"
        if ":" not in service["image"].rsplit("/", 1)[-1]:
            return "no tag at all"
    if not any(s["port"] for s in services):
        return "nothing in it listens"
    if not any(s.get("public") for s in services):
        return "nothing in it is reachable"
    for database in template.get("databases") or []:
        missing = [t for t in database.get("link_to") or [] if t not in names]
        if missing or not database.get("link_to"):
            return "a database linked to nothing that exists"
    return None


def main():
    converted = json.load(open("/tmp/tpl/converted.json"))
    for key, template in json.load(open("/tmp/tpl/converted-multi.json")).items():
        converted[key] = template  # a multi-service stack wins: it is the whole app

    before = {f for f in os.listdir(CATALOGUE) if f.endswith(".yaml")}
    written, dropped, kept = set(), {}, set()
    for key, template in sorted(converted.items()):
        if template["id"] in HAND_WRITTEN:
            kept.add(template["id"])
            continue
        reason = usable(template)
        if reason:
            dropped[key] = reason
            continue
        template["category"] = CATEGORY.get(template.get("category", ""), "other")
        # A description is a sentence on a card, not a paragraph.
        template["description"] = template["description"].strip().rstrip(".") + "."
        body = {k: template[k] for k in ORDER if template.get(k)}
        with open(f"{CATALOGUE}/{template['id']}.yaml", "w") as fh:
            yaml.safe_dump(body, fh, sort_keys=False, default_flow_style=False,
                           allow_unicode=True, width=100)
        written.add(template["id"] + ".yaml")

    # A template that stopped converting has to leave, or the catalogue keeps
    # shipping a stack this run decided it could not vouch for.
    stale = before - written - {name + ".yaml" for name in HAND_WRITTEN}
    for name in sorted(stale):
        os.remove(f"{CATALOGUE}/{name}")

    print(f"wrote {len(written)}, kept {len(kept)} hand-written, "
          f"removed {len(stale)} stale, dropped {len(dropped)}", file=sys.stderr)
    from collections import Counter
    for reason, count in Counter(dropped.values()).most_common():
        print(f"  {count:4}  {reason}", file=sys.stderr)


if __name__ == "__main__":
    main()
