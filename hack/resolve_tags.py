"""Resolve a container image to a tag that names a version.

A floating tag is not a version, so every template in the catalogue has to name
one. Upstreams disagree about how: some publish a major series (`1`, `5-alpine`)
that takes patches, some publish only exact releases. This asks the registry and
prefers, in order: an existing major or major.minor series tag, then the newest
exact semver release.

Run: python3 hack/resolve_tags.py < images.json > resolved.json
"""
import json, re, sys, time, urllib.error, urllib.parse, urllib.request

TIMEOUT = 25
SKIP = re.compile(r'(nightly|beta|alpha|rc|dev|edge|canary|test|snapshot|unstable|insiders|preview)', re.I)
SEMVER = re.compile(r'^v?(\d+)\.(\d+)(?:\.(\d+))?(-[a-z0-9.]+)?$', re.I)


# Several vanity registries — docker.flipt.io, registry.rocket.chat,
# cr.weaviate.io — are pull-through caches in front of Docker Hub, so they
# share Hub's anonymous rate limit. A 429 is the registry saying "ask again
# later", never "that image is not here", and reading it as the latter dropped
# templates whose images were fine. Wait it out; only then give up.
RETRY = (408, 429, 500, 502, 503, 504)


class Undetermined(Exception):
    """The registry never answered the question, so the answer is not "no"."""


def open_url(request):
    for attempt in range(4):
        try:
            return urllib.request.urlopen(request, timeout=TIMEOUT)
        except urllib.error.HTTPError as err:
            if err.code not in RETRY or attempt == 3:
                raise
            time.sleep(2 ** attempt * 3)
    raise RuntimeError("unreachable")


def get(url, headers=None):
    request = urllib.request.Request(url, headers=headers or {})
    with open_url(request) as response:
        return json.load(response)


def token_for(challenge, repo):
    """Turn a registry's WWW-Authenticate challenge into a bearer token.

    Every v2 registry answers an anonymous request with one of these, and the
    realm it names is the only place the token comes from. Hardcoding the realm
    per host got ghcr.io and codeberg.org right and left every other registry —
    flipt, rocket.chat, weaviate, gcr.io, outline — reading as "unauthorized",
    which is what a registry says when nobody asked it for a token.
    """
    realm = re.search(r'realm="([^"]+)"', challenge)
    if not realm:
        return None
    url = realm.group(1) + f"?scope=repository:{repo}:pull"
    service = re.search(r'service="([^"]+)"', challenge)
    if service:
        url += "&service=" + urllib.parse.quote(service.group(1), safe="")
    try:
        answer = get(url)
    except Exception:
        return None
    return answer.get("token") or answer.get("access_token")


def get_paged(url, repo):
    """A v2 tags listing, authenticating on demand and following Link.

    `tags/list` is paginated and the page size is the registry's to choose:
    ghcr.io hands back a few hundred tags and a Link header, and reading only
    the first page picked a `-dev` tag for Immich because that is what fits on
    it. The bound is a backstop against a runaway, not a page budget — stopping
    at 4000 tags picked immich-machine-learning v1.106.4 while v1.132.3 was
    sitting on a page this never asked for.
    """
    headers, out = {}, []
    while url and len(out) < 20000:
        try:
            request = urllib.request.Request(url, headers=headers)
            response = open_url(request)
        except urllib.error.HTTPError as err:
            if err.code != 401 or "Authorization" in headers:
                raise
            token = token_for(err.headers.get("WWW-Authenticate", ""), repo)
            if not token:
                raise
            headers["Authorization"] = "Bearer " + token
            continue
        with response:
            out += json.load(response).get("tags") or []
            link = response.headers.get("Link", "")
        nxt = re.search(r'<([^>]+)>\s*;\s*rel="?next"?', link)
        if not nxt:
            break
        base = urllib.parse.urlsplit(url)
        url = urllib.parse.urljoin(f"{base.scheme}://{base.netloc}", nxt.group(1))
    return out


def hub_tags(repo):
    if "/" not in repo:
        repo = "library/" + repo
    out, url = [], f"https://hub.docker.com/v2/repositories/{repo}/tags/?page_size=100&ordering=last_updated"
    try:
        for _ in range(3):
            data = get(url)
            out += [t["name"] for t in data.get("results", [])]
            url = data.get("next")
            if not url:
                break
    except urllib.error.HTTPError as err:
        if err.code != 404 or out:
            raise
        # The hub's own API does not know every repository it serves — it
        # answers 404 for tiredofit/freescout, which `docker pull` fetches
        # happily. The registry behind it does know, so ask that instead.
        return get_paged(f"https://registry-1.docker.io/v2/{repo}/tags/list?n=500", repo)
    return out


def registry_tags(host, repo):
    """Any v2 registry: authenticate from its own challenge, follow its pages."""
    # lscr.io is a mirror of linuxserver's images; ask the registry that has them.
    if host == "lscr.io":
        host = "ghcr.io"
    return get_paged(f"https://{host}/v2/{repo}/tags/list?n=500", repo)


def strip_variable_tag(ref):
    """Drop a tag that is an unexpanded shell variable.

    `ghcr.io/immich-app/immich-server:${IMMICH_VERSION:-release}` has a colon
    inside the tag, so splitting on the last one produced the repository name
    `ghcr.io/immich-app/immich-server:${IMMICH_VERSION` — which every registry
    answered with 403, and which read in the log as the registry refusing us
    rather than as this function being wrong.
    """
    if "${" in ref or "$" in ref:
        head = ref.split("$", 1)[0]
        return head.rstrip(":")
    return ref


def tags_for(image):
    ref = strip_variable_tag(image.split("@")[0])
    # docker.io is Docker Hub written out; its v2 API needs auth, the hub API does not.
    for prefix in ("index.docker.io/", "docker.io/", "registry-1.docker.io/"):
        if ref.startswith(prefix):
            ref = ref[len(prefix):]
    # lscr.io mirrors linuxserver's images, and the same images are on Docker
    # Hub, whose API returns them newest first. The v2 tags/list is unordered
    # and truncates, which picked a four-year-old release.
    if ref.startswith("lscr.io/linuxserver/"):
        ref = "linuxserver/" + ref.split("/")[-1]
    ref = ref.rsplit(":", 1)[0] if ":" in ref.rsplit("/", 1)[-1] else ref
    parts = ref.split("/")
    if "." in parts[0] or ":" in parts[0]:
        # A v2 tags listing is in no particular order, so "the newest" cannot be
        # read off it — only computed from tags that carry a version.
        return registry_tags(parts[0], "/".join(parts[1:])), ref.replace("lscr.io/", "ghcr.io/"), False
    return hub_tags(ref), ref, True


# What a tag has to carry before it counts as naming a release of the
# application, when the tag is not semver. Each of these was a wrong answer
# before it was a rule: organizr's newest tag is `linux-arm-v7`, which is an
# architecture; classicpress's is `php8.3-apache`, which is the runtime the
# image was built on; label-studio's is a build of the main branch; prefect's
# is a commit. None of them is a version of the thing being installed.
PLATFORM = re.compile(r'(^|[-_.])(linux|windows|amd64|arm64|armv?\d|arm|386|ppc64le|s390x|riscv64)($|[-_.])', re.I)
BRANCH_BUILD = re.compile(r'(^|[-_.])(main|master|sha|merge|pr|branch|snapshot|build|fb|feat|feature|wip)($|[-_.])', re.I)
# A toolchain in the tag means a build variant — prefect's 3.8-python3.14-conda
# is Prefect on conda, not the image the template asked for.
TOOLCHAIN = re.compile(r'(^|[-_.])(conda|python|php|node|ruby|jdk|jre|dotnet|cuda|rocm|gpu|cpu)', re.I)
RUNTIME_VERSION = re.compile(r'(^|[-_.])(php|python|node|ruby|openjdk|jdk|jre|go|perl|dotnet|erlang)\d', re.I)
DATE_TAG = re.compile(r'(^|[-_.])(20\d\d)[-_.]?(\d\d)[-_.]?(\d\d)?')
NUMBERED = re.compile(r'(^|[-_.])v?\d+\.\d+')


def names_a_version(tag):
    if PLATFORM.search(tag) or BRANCH_BUILD.search(tag) or TOOLCHAIN.search(tag):
        return False
    # A release tag is short. label-studio's newest is
    # 20260915.171829-chore-dependabot-security-36cd703, which is a build of
    # somebody's branch and reads as a version only because it starts with a
    # date. Four parts is `version-2026-07-14c`; five is a sentence.
    if len(re.split(r'[-_]', tag)) > 4:
        return False
    body = RUNTIME_VERSION.sub(r'\1', tag)  # php8.3-apache leaves apache, which has no version
    return bool(DATE_TAG.search(body) or NUMBERED.search(body))


def best(tags, variant, ordered=False):
    """Pick the tag that names a version, preferring a series over an exact one."""
    usable = [t for t in tags if not SKIP.search(t) and t not in ("latest", "main", "master", "stable")]
    if variant:
        same = [t for t in usable if t.endswith("-" + variant)]
        if same:
            usable = same

    releases = []
    for tag in usable:
        base = tag[: -len("-" + variant)] if variant and tag.endswith("-" + variant) else tag
        match = SEMVER.match(base)
        if match and not match.group(4):
            releases.append((tuple(int(g or 0) for g in match.groups()[:3]), tag, base))
    if not releases:
        # Plenty of projects never publish semver: GitLab ships 18.4.1-ce.0,
        # SearXNG and Excalidraw ship dates, it-tools ships a date and a commit.
        # Each of those names a build exactly — they are pins, not floating tags
        # — and the first version of this threw all of them away for not looking
        # like semver. Where the listing comes back newest-first, which is what
        # the Hub API promises and a v2 tags/list does not, the first pinned tag
        # in it is the newest release.
        if ordered:
            for tag in usable:
                if is_pinned(tag) and names_a_version(tag):
                    return tag
        return None
    releases.sort()
    version, newest, base = releases[-1]

    # A series tag that exists and tracks this release is better than pinning a
    # patch: it takes fixes without taking a new major.
    for series in (f"{version[0]}.{version[1]}", str(version[0])):
        for candidate in ({series, "v" + series} if base.startswith("v") else {series}):
            full = candidate + ("-" + variant if variant else "")
            if full in tags:
                return full
    return newest


# A tag is floating when it means "whatever is newest" — that is the only thing
# this refuses. Everything else already names a version, whether or not it looks
# like semver: a commit SHA is the strongest pin there is, and MinIO's
# RELEASE.2025-10-15T17-29-55Z and Jitsi's stable-10888 are exact builds. The
# first version of this insisted on semver and threw away dozens of images that
# were already pinned harder than semver pins anything.
FLOATING = re.compile(
    r'^(latest|main|master|stable|edge|release|dev|develop|nightly|canary|rolling|'
    r'current|prod|production|next|beta|alpha|rc|testing|unstable|insiders?|preview)$', re.I)


def is_pinned(tag):
    if not tag or FLOATING.match(tag):
        return False
    # A suffix that names a moving target moves, however it is spelled:
    # postgresql-latest, main-stable, 16-master. `stable-10888` is not one of
    # these — it ends in a build number, which is the thing that pins it.
    if re.search(r'(^|[-_.])(latest|nightly|edge|canary|rolling|stable|main|master|release|dev|prod)$',
                 tag, re.I):
        return False
    # An unexpanded shell variable is not a tag at all.
    return "$" not in tag and "{" not in tag


def resolve(image):
    ref = strip_variable_tag(image.split("@")[0])
    image = ref if ref != image.split("@")[0] else image
    for prefix in ("index.docker.io/", "docker.io/", "registry-1.docker.io/"):
        if ref.startswith(prefix):
            ref = ref[len(prefix):]
            image = ref
    if ref.startswith("lscr.io/linuxserver/"):
        ref = image = "linuxserver/" + ref.split("/")[-1]
    # A reference with no tag has no tag. `busybox` rpartitions to ("", "",
    # "busybox"), and taking that as the tag made this answer "already pinned"
    # for the most floating reference there is.
    name, _, tag = ref.rpartition(":")
    if not name or "/" in tag:
        name, tag = ref, ""
    variant = ""
    for suffix in ("alpine", "slim", "apache", "fpm", "debian"):
        if tag.endswith(suffix) and tag != suffix:
            variant = suffix
    # A tag can also be prefixed by an edition — cockpit publishes `core-…` and
    # `pro-…` from one repository, and dropping the prefix resolved the free
    # edition to the paid one.
    edition = ""
    head = tag.split("-", 1)[0]
    if head.isalpha() and head not in ("v", "version", "latest", "stable", "main", "master"):
        edition = head
    if is_pinned(tag):
        # A tag that names a version still has to be a tag the registry has.
        # Coolify's Mealie template says 3.17.0 and Mealie publishes v3.17.0,
        # so taking "already pinned" at its word dropped a template whose
        # newest release was one lookup away.
        try:
            if exists(image):
                return image, "already pinned"
        except Undetermined:
            return image, "already pinned"

    tags, repo, ordered = tags_for(image)
    if edition:
        same = [t for t in tags if t.split("-", 1)[0] == edition]
        if same:
            tags = same
    picked = best(tags, variant or (tag if tag in ("alpine", "slim", "apache") else ""), ordered)
    if not picked:
        return None, f"no versioned tag among {len(tags)} tags"
    return f"{repo}:{picked}", "resolved"


def hub_has_tag(repo, tag):
    """Ask Docker Hub's own API whether a tag is there.

    The registry and the API are two different services with two different
    limits: a manifest fetch counts against the anonymous pull limit and the
    API does not. So when a Hub-backed registry answers 429 — and the vanity
    hosts in front of Hub (docker.flipt.io, registry.rocket.chat, cr.weaviate.io)
    all do, because they share its limit — this still answers the question,
    without pulling anything.
    """
    if "/" not in repo:
        repo = "library/" + repo
    try:
        return get(f"https://hub.docker.com/v2/repositories/{repo}/tags/{tag}").get("name") == tag
    except Exception:
        return False


def exists(image):
    """Ask the registry for the manifest. A tag this resolver invented, or one
    that was deleted since, must not reach the catalogue."""
    ref = image.split("@")[0]
    name, _, tag = ref.rpartition(":")
    parts = name.split("/")
    if "." in parts[0]:
        host, repo = parts[0], "/".join(parts[1:])
    else:
        host, repo = "registry-1.docker.io", (name if "/" in name else "library/" + name)
    if host == "lscr.io":
        host = "ghcr.io"

    # A tag can contain a colon-free timestamp; rpartition already split on the
    # last colon, which is right, but an image with no tag at all leaves the
    # name in `tag`. Guard it rather than asking for a manifest called "".
    if not tag or "/" in tag:
        return False
    url = f"https://{host}/v2/{repo}/manifests/{tag}"
    headers = {"Accept": "application/vnd.oci.image.index.v1+json,"
                         "application/vnd.docker.distribution.manifest.list.v2+json,"
                         "application/vnd.docker.distribution.manifest.v2+json"}
    hub = host == "registry-1.docker.io"
    for _ in range(2):
        try:
            request = urllib.request.Request(url, headers=headers, method="HEAD")
            with open_url(request) as response:
                return response.status == 200
        except urllib.error.HTTPError as err:
            if err.code in RETRY:
                # `hub` is set once a challenge has named auth.docker.io, which
                # is how a vanity host admits it is Docker Hub underneath.
                if hub and hub_has_tag(repo, tag):
                    return True
                raise Undetermined(f"{host} answered {err.code}") from err
            if err.code != 401 or "Authorization" in headers:
                return False
            challenge = err.headers.get("WWW-Authenticate", "")
            hub = hub or "auth.docker.io" in challenge
            token = token_for(challenge, repo)
            if not token:
                raise Undetermined(f"{host} would not hand out an anonymous token")
            headers["Authorization"] = "Bearer " + token
        except urllib.error.URLError as err:
            raise Undetermined(f"{host} unreachable: {err.reason}") from err
    return False


if __name__ == "__main__":
    images = json.load(sys.stdin)
    out = {}
    for key, image in images.items():
        try:
            pinned, why = resolve(image)
        except Exception as exc:  # a registry that refuses is a template we drop
            pinned, why = None, f"{type(exc).__name__}: {exc}"
        if pinned:
            try:
                if not exists(pinned):
                    pinned, why = None, "the resolved tag does not exist"
            except Undetermined as exc:
                pinned, why = None, f"could not verify the tag: {exc}"
        out[key] = {"from": image, "to": pinned, "why": why}
        print(f"{key:36} {image:55} -> {pinned or 'SKIP'} ({why})", file=sys.stderr)
    json.dump(out, sys.stdout, indent=1)
