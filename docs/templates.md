# Templates

A template installs a known application in one step: the app, the database it
needs, the variables that wire the two together, and the volume its files live
on.

What comes out is ordinary. A templated app is an app: it can be scaled, backed
up, rolled back and deleted like anything else you deploy, and nothing in the
panel treats it differently afterwards. The template is how it started, not what
it is.

## Installing one

**Templates** in the sidebar lists what is there, grouped by category. Choose an
environment, answer whatever the template asks for, and install.

Some templates ask for something only you can know — an admin email, a licence
key. Some ask for a secret and offer to generate it; take the offer, because a
generated one is longer and more random than one you would type. The panel seals
it like any other secret and shows it once.

Databases are created before the services that need them start, so the
connection string exists by the time the app looks for it. That is the
difference between a template that works on the first try and one that
crash-loops until somebody redeploys it.

## What is in the catalogue

**374 applications**, grouped by what they are for: productivity, developer
tools, media, storage, publishing, communication, security, AI, analytics,
monitoring, automation, finance and networking. WordPress, Ghost, n8n,
Vaultwarden, Uptime Kuma, Plausible, Umami, MinIO, BookStack, Gitea, GitLab,
Chatwoot, Langfuse, Metabase, Redmine, Outline, Coder, Baserow, Linkwarden and
about three hundred and fifty others.

A hundred of them were written by hand, each from the project's own image,
entrypoint and documentation. The rest were converted from the Coolify
catalogue, which is the largest in this category and has been exercised by tens
of thousands of installations — so the ports, the variables and the volumes come
from somewhere that works rather than from guesswork.

Forty-six install more than one app: a web app and its worker, or a service
and its search index, or a stack of three or four. They land in the same
environment and reach each other by name. A worker among them has no port at
all, which is what Skifity gives an app that does not listen: no Service, no
readiness probe, no domain.

What the conversion added is the part Coolify does not do:

* **Every image names a version**, and that version was fetched from its
  registry to prove it exists. Coolify's own catalogue ships `latest` for more
  than half its entries. Where a project does not publish semver — GitLab's
  `19.1.8-ce.0`, SearXNG's date-and-commit, DokuWiki's `version-2026-07-14c` —
  the exact build it does publish is what a template names; an architecture, a
  runtime variant or a build of somebody's branch is not a version and is
  refused.
* **The database wiring was taken out.** A Compose file points an app at a
  sibling container (`DB_HOST=mariadb`); here the database is a managed one and
  arrives as a connection string, so the old variables would point at nothing.
* **A port comes from the source, never from a guess.** A template's own domain
  marker, an `expose` or `ports` entry, the port a sibling service dials, a short
  table of ports that are documented facts about an image, or the port the
  service's own healthcheck talks to — in that order. Anything else is dropped as
  ambiguous, which is how a search index avoided being given a dashboard's port.
* **Anything that could not be converted cleanly was dropped** rather than
  shipped half-filled — stacks needing the host's Docker socket, stacks of five
  or more services, and anything with no port or no image whose version could be
  verified.

### What that does not mean

None of these has been deployed by us, because nothing in this product has been
deployed by us yet — see the Status section of the README. What is checked is
that each template is structurally sound and that its image exists. Treat a
template as a well-informed starting point, not as a guarantee.

## Versions

Every template names a version of the software it installs. None of them runs
`latest`.

A floating tag is not a version. Two deploys of what looks like the same app run
different software, a rollback restores a tag rather than the thing that worked,
and an upstream release arrives on a restart nobody asked for. A tag that names
only a major version — `1`, `5-alpine` — moves as much as `latest` does within
that line, so a template names the exact release instead, and a test refuses the
first kind as it refuses the second.

So a template does not update itself. Moving one forward is a change to Skifity,
and a newer version arrives when the panel is upgraded — or, for a template in a
team's own catalogue, when the catalogue is refreshed; see
[Private catalogues](#private-catalogues).

## Updates

An app installed from a template remembers which one, and which of its
services it is. When an upgrade of the panel brings a newer version of that
template, the app's **Settings** tab says so and offers **Update**:

1. **It backs up first.** Every disk of the app and every database linked to it
   is backed up to your backup storage, and the update waits for all of them.
2. **Only then does it change anything.** The image moves to the template's
   version and the app deploys, the way any deploy does, so a failed start
   rolls back to what was running.
3. **A backup that fails stops it.** The app keeps running what it ran, and the
   tab says which backup failed and why.

An app with nothing to back up updates straight away. With disks or databases
and no backup storage configured, the update asks you to set storage up — or to
update without a backup, if you have one of your own.

If you changed the image yourself since the template last set it, the update
says so rather than replacing your choice; **Update anyway** goes back to the
template's version. Updates only ever move the image: variables, disks and
databases are yours and are left as they are. A template's notes for a release
with breaking changes are worth reading before pressing the button — the
backup is what makes pressing it safe, not what makes it a good idea.

Apps installed before the panel remembered where they came from do not know,
and are updated by changing the image under Settings.

## Adding one

A template is a file in `internal/templates/catalogue`, and adding one does not
require touching any Go. The README in that directory has the format and the two
rules that are not obvious: name a version, and do not wire the database by
hand. `make check` tells you whether it is right.

That is how this catalogue can keep growing without a release. Templates that
belong to one team rather than to everybody go in a catalogue of the team's own;
see [Private catalogues](#private-catalogues).

## Private catalogues

The built-in catalogue is fixed when the panel is built, and your own software —
the internal wiki, the licence server, a pinned fork of something public — has no
place in it. A team can add catalogues of its own: a name and an https address
that answers a file of templates, in the same schema as the built-in ones.

Their templates appear on the **Templates** page beside the built-in ones, with a
badge naming the catalogue, for that team and nobody else. They install exactly
as a built-in template does, and an app installed from one is an ordinary app.

**Templates → Catalogues** lists them, with when each was last downloaded, how
many of its templates can be installed, and why each of the others cannot.
Adding, refreshing and removing one is an owner's or an administrator's; anybody
in the team sees the list. From a terminal:

```sh
skifity templates catalogues                  # the team's catalogues
skifity templates catalogues add Acme https://raw.githubusercontent.com/acme/templates/v1.4.0/catalogue.yaml
skifity templates catalogues add Acme 'https://git.acme.example/api/v4/projects/7/repository/archive.tar.gz?sha=v1.4.0' --header PRIVATE-TOKEN
skifity templates catalogues refresh Acme     # download it again now
skifity templates catalogues remove Acme
skifity templates --search acme               # what the team can install
```

`--header` asks for the header's value rather than taking it as an argument, so
the token is not left in your shell's history; piped in, it is read from stdin.
Every command takes `--json`.

### The file

Either one YAML or JSON document with the templates in a list under
`templates:`, or a `.tar.gz` or `.zip` laid out like the built-in catalogue: one
template per `*.yaml` file, in a `catalogue/` directory or at the top, and logos
beside them as `icons/<id>.svg`, `.png` or `.webp`. The archive GitHub, GitLab or
Gitea makes of a repository wraps everything in one directory named after the
commit, and that directory is looked through, so the address of a tag's archive
works as it is.

Each template is exactly what a file in the built-in catalogue holds; the
README beside those files, `internal/templates/catalogue/README.md`, describes
every field. One more field is read here: `icon`, the address of its logo.

```yaml
templates:
  - id: wiki
    name: Acme Wiki
    description: The internal wiki, with its database.
    category: productivity
    website: https://wiki.acme.example/about
    icon: icons/wiki.svg          # relative to this file, on the same host
    services:
      - name: wiki
        image: registry.acme.example/wiki:3.2.1
        port: 3000
        public: true
        health_path: /healthz
        volumes:
          - name: uploads
            mount_path: /app/uploads
            size_gb: 5
    databases:
      - name: wiki-db
        engine: postgres
        storage_gb: 5
        link_to: [wiki]
        var_name: DATABASE_URL
    inputs:
      - key: SESSION_SECRET
        label: Session secret
        secret: true
        generate: true
```

### What is checked

Every template in a catalogue is held to the checks the built-in ones are held
to in the panel's own tests: an id that is a slug, an https website, at least
one service somebody can open, an image that names a release — never `latest`,
a branch or a bare major version — mount paths that are absolute, databases of
an engine Skifity runs and linked to services the template has, no database
wired by hand, ports and files that can be opened and mounted, and inputs that
are asked for or filled in. A field the panel does not know is refused rather
than ignored, so a misspelt `mount_path` is an error rather than a volume that
is mounted nowhere.

Each template is checked on its own. One that fails is listed with why and
cannot be installed; the rest of the catalogue loads. A file that is not a
catalogue at all — an HTML login page answered with a 200, an archive that does
not open, a document with no templates in it — is refused as a whole.

### The same id in two places

An id only has to be unique within its catalogue. A team's catalogue may have a
`wiki` when another team's has one too, or a `wordpress` when the built-in
catalogue does: each is its own card under its own badge, and installing one
names the catalogue it is in. The catalogue is looked for among the team's own,
so no team can install from another's, whatever id it asks for.

An app remembers the catalogue it came from. When a refresh brings a newer image
for its template, the app's **Settings** tab offers the update exactly as it
does for a built-in one — backing up first — and removing the catalogue leaves
the app running with nothing to offer.

### A private Git host

A catalogue on a private repository needs a token. Give the header the host
reads one from — `Authorization` with `Bearer` and the token, or `PRIVATE-TOKEN`
for GitLab — and its value. The value is sealed with the panel's master key,
bound to the team, the catalogue and its address, and never answered by any API
or shown again. It is sent to the catalogue's own host and nowhere else: a
redirect to another host goes without it.

An address with a credential in it — `https://user:token@…`, or a query
parameter such as `?private_token=` or a signature — is refused, because the
address is shown to everybody in the team. Put the token in the header.

### Downloading, refreshing, and the last good copy

A catalogue is downloaded and read when it is added, and kept only if it reads.
After that it is downloaded again every day, at a minute between two and six in
the morning (UTC) of its own, and whenever somebody presses **Refresh**; a panel
that was not running at that minute catches up when it starts.

A refresh replaces the copy only with one that reads. One that fails — the host
is down, the token has expired, the file has become something else — is recorded
on the catalogue, shown on its entry, and changes nothing else: its templates are
still installed from the copy downloaded before.

The download is the panel's own request, so it goes through the same guard as
every other address you give the panel: the cloud metadata service and the
panel's own machine are refused. It is limited to 5 MB and 30 seconds, and an
archive to 32 MB unpacked, 1 MB a template and a thousand templates. A redirect
must stay on https, stops after five, and may not lead to a private address the
catalogue's own address did not: a Gitea on your network is fine when that is the
address you gave, and a public host answering "go to 10.0.0.5 instead" is not.

### Logos

A logo is never loaded by your browser from somewhere else. The panel's own
policy allows pictures from the panel only, and a page that loads them from
another server tells that server who is looking at what, and when.

So a catalogue's logos come with it. An archive carries them in `icons/`. A
template's `icon:` is fetched by the panel itself, when the catalogue is — not
when a page is opened — through the same guard as the catalogue, with the same
header, and only from the catalogue's own host: an `icon:` on any other host is
ignored, so a catalogue cannot point the panel, or anybody's browser, at a third
party. What comes back is kept only if its bytes are an SVG, a PNG or a WebP,
whatever the host said it was, up to 256 KB, and it is served by the panel with
the same sandbox as the built-in logos. A template without one shows its first
letter.

## Changing what a template made

Everything a template sets is editable afterwards: variables on the app's
Variables tab, size and scaling on its settings, the database on its own page.
Reinstalling the template does not reconcile anything — it installs a second
copy under a new name.

If you outgrow a template, nothing has to be undone. It left you an app and a
database, and those are the things Skifity actually runs.
