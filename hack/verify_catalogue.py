"""Ask every registry whether the image in every catalogue file is still there.

A tag that existed when a template was imported can be deleted afterwards —
MinIO dropped its old RELEASE tags from Docker Hub, and the catalogue would have
gone on naming one. Nothing in `make check` can ask a registry (CI has no reason
to depend on Docker Hub being up, and the anonymous rate limit would make it
flaky), so this is a thing somebody runs before a release.

    python3 hack/verify_catalogue.py

It prints one line per image it could not confirm and exits after writing
/tmp/tpl/verify.json. "GONE" means the registry said no; "UNKN" means it never
answered, usually a rate limit, and is worth re-running rather than acting on.
"""
import json, os, sys, yaml
sys.path.insert(0, "hack")
import resolve_tags as R

CAT = "internal/templates/catalogue"
seen, out = {}, {}
for name in sorted(os.listdir(CAT)):
    if not name.endswith(".yaml"):
        continue
    tpl = yaml.safe_load(open(f"{CAT}/{name}"))
    for svc in tpl["services"]:
        seen.setdefault(svc["image"], []).append(f"{tpl['id']}/{svc['name']}")
print(f"{len(seen)} distinct images across {len(os.listdir(CAT)) - 1} templates", flush=True)
for image, users in seen.items():
    try:
        ok, why = R.exists(image), ""
    except R.Undetermined as exc:
        ok, why = None, str(exc)
    except Exception as exc:
        ok, why = None, f"{type(exc).__name__}: {exc}"
    out[image] = {"ok": ok, "why": why, "used_by": users}
    if ok is not True:
        print(f"{'GONE' if ok is False else 'UNKN'} {image:62} {why[:40]} <- {', '.join(users[:3])}", flush=True)
json.dump(out, open("/tmp/tpl/verify.json", "w"), indent=1)
good = sum(1 for v in out.values() if v["ok"] is True)
print(f"== {good} present, {sum(1 for v in out.values() if v['ok'] is False)} gone, "
      f"{sum(1 for v in out.values() if v['ok'] is None)} unverifiable", flush=True)
