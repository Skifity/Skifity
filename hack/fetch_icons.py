#!/usr/bin/env python3
"""Fetch a logo for every template in the catalogue.

The catalogue is the first thing anybody looks at and it was 282 grey squares
with a letter in them. Every panel in this category shows logos; the reason to
have written this is that ours did not.

Where they come from: homarr-labs/dashboard-icons, the collection Homarr,
Homepage and Dashy all draw on. It is CC0-1.0, which is what makes vendoring
them into this repository possible at all — the collection waives its own
rights. The logos themselves are trademarks of the projects they belong to,
used here to identify the software being installed, which is what a catalogue
entry is for.

They are downloaded once and committed, not fetched at render time. Three
reasons, in order of how much they matter:

  * The panel's Content-Security-Policy is `img-src 'self'`. Pointing at a CDN
    means widening it, and a panel that loads images from somebody else's
    server tells that server which self-hosted apps each of its users is
    browsing.
  * An install with no outbound network still has a catalogue with pictures.
  * jsDelivr being down is then not a thing that makes this product look
    broken.

    python3 hack/fetch_icons.py            # fetch what is missing
    python3 hack/fetch_icons.py --report   # say what is missing, change nothing

An icon is named after the template it belongs to, so nothing in the YAML needs
to change and the lookup is a file that either exists or does not.
"""

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CATALOGUE = os.path.join(ROOT, "internal", "templates", "catalogue")
ICONS = os.path.join(ROOT, "internal", "templates", "icons")

TREE = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons@main/tree.json"
FILE = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons@main/{kind}/{name}.{kind}"

# An SVG is a few kilobytes and scales to any size. WebP is the fallback for the
# icons the collection only has as a bitmap: the same picture as the PNG at
# roughly a third of the bytes, and every browser this panel supports reads it.
# A 260 KB PNG drawn at 40 pixels is a waste nobody sees and everybody carries.
KINDS = ("svg", "webp")


def get(url: str, binary: bool = False):
    request = urllib.request.Request(url, headers={"User-Agent": "skifity-icons/1"})
    with urllib.request.urlopen(request, timeout=60) as response:
        data = response.read()
    return data if binary else data.decode("utf-8")


def slug(text: str) -> str:
    return re.sub(r"[^a-z0-9]+", "-", text.lower()).strip("-")


def templates() -> dict[str, str]:
    """Every template id, with the name it shows on its card."""
    out = {}
    for entry in sorted(os.listdir(CATALOGUE)):
        if not entry.endswith(".yaml"):
            continue
        tid = entry[:-5]
        name = tid
        with open(os.path.join(CATALOGUE, entry), encoding="utf-8") as handle:
            for line in handle:
                if line.startswith("name:"):
                    name = line.split(":", 1)[1].strip()
                    break
        out[tid] = name
    return out


def candidates(tid: str, name: str) -> list[str]:
    """Names this template might be filed under, best guess first.

    The collection files things under the project's own name. A template id
    carries what Skifity needed to tell two of them apart — `forgejo-with-mariadb`
    is Forgejo, and the database it was paired with is not part of its identity.
    """
    seeds = [tid, slug(name)]
    seeds += [re.split(r"-with-|-and-|-without-", seed)[0] for seed in seeds]

    out = []
    for seed in seeds:
        if not seed:
            continue
        out += [seed, seed.replace("-", "")]
        # `actualbudget` is filed as `actual-budget`. There is no way to know
        # where the boundary is, so every position is tried.
        for cut in range(3, max(4, len(seed) - 2)):
            out.append(seed[:cut] + "-" + seed[cut:])

    seen, unique = set(), []
    for name in out:
        if name not in seen:
            seen.add(name)
            unique.append(name)
    return unique


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", action="store_true", help="say what is missing and stop")
    args = parser.parse_args()

    os.makedirs(ICONS, exist_ok=True)
    have = {entry.rsplit(".", 1)[0] for entry in os.listdir(ICONS) if "." in entry}

    print("reading the icon collection")
    tree = json.loads(get(TREE))
    # A -dark or -light variant is for a dashboard that knows its own theme.
    # The plain name is the one that works on both.
    available = {}
    for kind in KINDS:
        for entry in tree.get(kind, []):
            if not entry.endswith("." + kind):
                continue
            base = entry[: -len(kind) - 1]
            if base.endswith(("-dark", "-light")):
                continue
            available.setdefault(base, []).append(kind)

    found, missing, fetched = {}, [], 0
    for tid, name in templates().items():
        match = next((c for c in candidates(tid, name) if c in available), None)
        if match is None:
            missing.append(tid)
            continue
        found[tid] = match

    print(f"{len(found)} of {len(found) + len(missing)} templates have a logo in the collection")

    if args.report:
        print("\nno logo for:")
        for tid in missing:
            print(f"  {tid}")
        return 0

    for tid, match in found.items():
        if tid in have:
            continue
        kind = "svg" if "svg" in available[match] else available[match][0]
        url = FILE.format(kind=kind, name=match)
        try:
            data = get(url, binary=True)
        except (urllib.error.URLError, urllib.error.HTTPError) as err:
            print(f"  {tid}: {err}", file=sys.stderr)
            continue
        with open(os.path.join(ICONS, f"{tid}.{kind}"), "wb") as handle:
            handle.write(data)
        fetched += 1
        if fetched % 25 == 0:
            print(f"  {fetched} fetched")

    print(f"\n{fetched} downloaded, {len(have)} already here")
    print(f"icons live in {os.path.relpath(ICONS, ROOT)} and are committed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
