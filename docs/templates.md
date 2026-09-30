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

**282 applications**, grouped by what they are for: productivity, developer
tools, media, storage, publishing, communication, security, AI, analytics,
monitoring, automation, finance and networking. WordPress, Ghost, n8n,
Vaultwarden, Uptime Kuma, Plausible, Umami, MinIO, BookStack, Gitea, GitLab,
Chatwoot, Langfuse, Metabase, Redmine and about two hundred and sixty others.

Eight of them were written by hand. The rest were converted from the Coolify
catalogue, which is the largest in this category and has been exercised by tens
of thousands of installations — so the ports, the variables and the volumes come
from somewhere that works rather than from guesswork.

Thirty-eight install more than one app: a web app and its worker, or a service
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
and an upstream release arrives on a restart nobody asked for. Where the project
publishes a series tag that takes patches without breaking changes, that is what
a template uses; where it does not, an exact version is.

So a template does not update itself. Moving one forward is a change to Skifity,
and you move your own installation forward by changing the image on the app, the
same way you would for anything else you run.

## Adding one

A template is a file in `internal/templates/catalogue`, and adding one does not
require touching any Go. The README in that directory has the format and the two
rules that are not obvious: name a version, and do not wire the database by
hand. `make check` tells you whether it is right.

That is how this catalogue can keep growing without a release.

## Changing what a template made

Everything a template sets is editable afterwards: variables on the app's
Variables tab, size and scaling on its settings, the database on its own page.
Reinstalling the template does not reconcile anything — it installs a second
copy under a new name.

If you outgrow a template, nothing has to be undone. It left you an app and a
database, and those are the things Skifity actually runs.
