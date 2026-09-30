# Writing a Skifity plugin

A plugin is a **container image and a manifest**. Nothing else.

Skifity runs your image in a namespace of its own, gives it an API token
carrying exactly the permissions your manifest asked for and an administrator
granted, and posts the events you subscribed to. Write it in any language.

A plugin does two kinds of thing, and most useful ones do only the second:

* **It hears what happened.** You subscribe to [events](#events) and react.
* **It provides a vendor.** The panel owns a feature and asks your container to
  be the thing behind it — see [Providing something](#providing-something).
  This is the half that makes a plugin worth writing.

## Why not a library

Skifity is one static Go binary with CGO turned off, and Go cannot load code
into one. The `plugin` package needs CGO, works on three operating systems, and
its own documentation warns that "runtime crashes are likely to occur unless all
parts of the program (the application and all its plugins) are compiled using
exactly the same version of the toolchain, the same build tags, and the same
values of certain flags and environment variables". Recompiling the panel to add
a plugin, which is how Caddy and Traefik's static plugins work, means every user
needs a Go toolchain.

A container has none of those problems, and three properties a library cannot
offer: your crash is not the panel's crash, your language is your choice, and
what you may do is a list an administrator read before saying yes.

## The manifest

```yaml
apiVersion: plugin.skifity.com/v1
id: com.example.backup-to-b2
name: Backup to Backblaze B2
description: Copies every database backup to a Backblaze bucket as it is taken.
version: 1.2.0
homepage: https://example.com/skifity-b2
license: MIT
author:
  name: Example Ltd
  url: https://example.com

image: ghcr.io/example/skifity-b2@sha256:0000000000000000000000000000000000000000000000000000000000000000

requires:
  skifity: ">=1.0.0 <2.0.0"

permissions:
  - databases:read
  - backups:read
  - backups:write

events:
  - event: backup.completed
  - event: backup.failed

settings:
  - key: bucket
    label: Bucket
    required: true
  - key: application_key
    label: Application key
    kind: password
    secret: true

runtime:
  port: 8080
  health: /healthz
  memoryMB: 64
```

That exact document is a test in `internal/plugins`. If it ever stops being
valid, the standard changed and it was not on purpose.

### The rules that will surprise you

* **`id` is reverse-DNS and never changes.** You own a domain, so nobody has to
  run a registry of names for yours to be unique.
* **`image` is a digest, never a tag.** A tag can be moved by whoever controls
  the registry, and this image is about to be handed an API token. "The plugin
  you approved" has to mean the bytes you approved.
* **The field is `event:`, not `on:`.** `on` is a boolean in YAML 1.1, so
  `on: backup.completed` parses in some readers as the key `true`. GitHub
  Actions carries that scar in every workflow file ever written.
* **An unknown field is an error, not a warning.** A plugin written against a
  later panel refuses to install rather than installing and quietly losing half
  of what it declared.
* **`events:` and `provides:` are both optional, and a plugin needs at least
  one of them to do anything.** A plugin that provides something and subscribes
  to nothing never has to implement `/events` at all.

## Permissions

A permission is a resource and an action: `apps:read`, `backups:write`. The
resources are `apps`, `deployments`, `databases`, `backups`, `projects`,
`servers`, `templates`, `operations`, `events`, `teams`, `settings` and
`account`.

**`read` and `write` on their own are refused for a plugin.** They mean every
resource, which is not something anybody can meaningfully agree to on a screen.
Name what you need.

There is no permission that grants the master key, because there is no route
that exposes it, and no permission that reaches another team, because that check
is separate and runs anyway. Ask for the least you can: an administrator reads
this list, and a plugin asking for `servers:write` to copy a file is a plugin
they will not install.

## Events

Every event carries `event`, `team_id` and a `data` object. What is in `data`:

| Event | `data` | |
|---|---|---|
| `app.created`, `app.deleted` | `app_id`, `app`, `environment_id` | |
| `deploy.before` | `app_id`, `app`, `environment`, `deployment_id`, `image`, `commit` | **Can be blocking.** You may refuse the deploy. |
| `deploy.succeeded` | the same six | |
| `deploy.failed` | `app_id`, `app`, `deployment_id`, `commit`, `reason`, `error` | |
| `backup.completed` | `backup_id`, `target_type`, `target_id`, `target`, `kind` | `target_type` is `database` or `volume`. |
| `backup.failed` | the same five, plus `error` | |
| `database.created` | `database_id`, `database`, `engine`, `environment_id` | |
| `server.added` | `server_id`, `server`, `host`, `location`, `size` | |
| `server.removed` | `server_id`, `server`, `host` | |

Subscribing to an event the panel does not send is refused at install time, not
discovered six months later when you notice your plugin has never run.

That sentence was half true for longer than it should have been. The manifest
checked a subscription against the list above and the list was right, but only
two of these events were ever actually sent: a plugin could subscribe to
`backup.failed`, be accepted, and never hear anything. A test now reads the
standard and the panel's own source and fails when an event in one is missing
from the other, which is the only way a promise like this stays true.

**You hear about the teams whoever installed you is in, and no others.** A
plugin is installed panel-wide and given a token belonging to the person who
installed it, so it can already read what they can read; the events draw the
same line. On a panel with one team that is everything. On a panel with several,
an owner of one team cannot install a plugin and have it watch — or, with a
blocking hook, stop — another team's deploys.

**Blocking is only for an event that happens before something.** Refusing a
deploy is a decision; refusing to acknowledge that a backup already finished is
not, so `blocking: true` on anything else is refused. A blocking hook holds up
somebody watching a deployment page, so the panel caps it at ten seconds
whatever you asked for. If you need longer, answer straight away and do the work
afterwards.

## Providing something

Events are the half where the panel talks to you. This is the half where you
answer.

A plugin that only subscribes to events can react to what happened and nothing
more, and every genuinely useful idea for one turned out to belong in the panel
itself. So a plugin can also be the thing behind a feature the panel already
has: **the panel owns the feature, your plugin owns the vendor.** Skifity knows
what a notification is, when to send one and what goes in it. It does not know
what Slack is. That is the seam.

```yaml
provides:
  - kind: notify.channel
    id: chat
    name: Chat
    description: A message in a channel.
    settings:
      - key: webhook_url
        label: Webhook URL
        kind: password
        secret: true
        required: true
```

`name` is what somebody picks from a list, so it names **the vendor and not
your plugin**: "Slack", not "Acme notification pack". `id` is unique inside
your manifest; you may provide several of the same kind.

`settings` here is not the same as your plugin's own settings above. Yours are
configured once, when the plugin is installed. These are the form for **one
configured instance** — a plugin providing Slack has one set of its own
settings and any number of channels configured against it, each with its own
webhook URL. The panel collects them, seals the secret ones with its own
keyring, and sends them to you with every call. **You keep nothing.** No
database, no credential of your own for the vendor, and removing your plugin
removes nothing the panel still needs.

### What you can provide

| Kind | What the panel asks | Actions |
|---|---|---|
| `notify.channel` | Somewhere a notification can be sent. | `validate`, `send` |

One kind. That is not the roadmap being coy — a kind exists in this table only
once the panel really asks for it, and a test fails the build if one is listed
that no line of code anywhere calls. A kind you can declare and that nobody
ever calls is a plugin that installs, is approved, and sits waiting for a
request that never comes, with no way for you to find out except by watching
nothing happen. Adding the second kind is cheap; adding it before a caller
exists is how the list fills up with promises.

### Answering a call

`POST /provide` on your container, signed exactly like an event:

```json
{
  "plugin": "com.example.chat",
  "kind": "notify.channel",
  "provider": "chat",
  "action": "send",
  "config": {"webhook_url": "https://example.test/hook"},
  "data": {"title": "web is unhealthy", "body": "...", "level": "error", "url": "https://..."}
}
```

`X-Skifity-Provider` carries `kind/id` and `X-Skifity-Action` the action, so
you can route without parsing the body. Check `X-Skifity-Signature` the same way
you check it on an event.

Answer with:

```json
{"ok": true, "data": {}}
```

or, when you cannot:

```json
{"ok": false, "error": "that webhook URL was rejected"}
```

**Your `error` is shown to a person**, so write it for them and not for a log.
It is cut at 500 characters. `{"ok": false}` with no reason gets them "it
refused and did not say why", which is a support ticket with your name on it.

**An empty body is a failure, not a yes.** A `200` with `{}` means `ok` is
false, deliberately: reading silence as success is how a channel quietly
delivers nothing for a month.

You have fifteen seconds. Unlike an event, **a failure here is not swallowed**
— somebody is waiting for it, so it reaches them. A plugin that is restarting
is a notification that did not go out and a person who is told so, not a
silence.

`validate` is asked while somebody is still looking at the form, before
anything is stored. Check what you can cheaply — the shape of a URL, a token
that is obviously not one. It is not the place to send a test message.

### What the panel does not promise

A channel row in the panel's database outlives your plugin. It stores
`plugin:<your id>/<provider id>`, both halves, because two plugins may each
provide a `slack` and a channel configured against one must never start being
delivered by the other. If your plugin is removed, or upgraded so that it no
longer provides that id, the channel stops working and says which plugin to
look for. It does not silently go somewhere else.

## Settings

Declare them; the panel collects them. That way the form exists before your
plugin is running, the labels can be translated, and a secret is sealed with the
panel's own keyring rather than your container becoming somewhere credentials
live.

Kinds are `text`, `password`, `number`, `bool` and `choice`. `secret: true`
means it is sealed at rest and never shown again — the same promise the panel
makes about every other secret it holds.

## Commercial plugins

`license: commercial` is a first-class value, and the store shows it before
anything is installed.

**Skifity does not process payments and will not.** A commercial plugin sells
through its own site and validates its own licence key — declare a setting for
it, check it against your own server, and refuse to work without it. That keeps
Skifity out of being a payment processor, and it keeps your customer
relationship yours.

## What your container is given

Everything arrives as environment variables, from one Secret:

| | |
|---|---|
| `SKIFITY_PLUGIN_ID` | your own id |
| `SKIFITY_API` | the panel's address inside the cluster |
| `SKIFITY_TOKEN` | your API token, carrying exactly the permissions you asked for |
| `SKIFITY_SIGNING_SECRET` | the key events are signed with |
| `SKIFITY_SETTING_<KEY>` | one per declared setting, upper-cased |
| `PORT` | the port to listen on |

**Your image has to run as a non-root user.** Your namespace enforces the strict
Pod Security profile, and an image that starts as root is refused. Unlike an
off-the-shelf application image, you control the Dockerfile, so this is a
requirement rather than a problem — and an image that will not start is the most
common reason a plugin looks broken.

You get one replica, a read-only root filesystem, no Kubernetes API token, and a
namespace of your own with a network policy that lets you reach **the panel, DNS
and the internet** — and nothing else. Not another plugin, not an app, not a
database, not the node network, and not the cloud metadata address.

## Receiving events

The panel POSTs to `/events` on your port:

```
POST /events
X-Skifity-Event: deploy.succeeded
X-Skifity-Signature: sha256=<hmac>
Content-Type: application/json

{"event":"deploy.succeeded","at":"2026-09-18T09:00:00Z","data":{...}}
```

**Verify the signature.** It is an HMAC-SHA256 over the exact request body,
keyed with `SKIFITY_SIGNING_SECRET`. Your endpoint is only reachable from the
panel's namespace, which is the first line — but a plugin that does not check
the signature is a plugin that trusts its network. Compare in constant time.

Answer `200`. For a **blocking** event, answer with a verdict:

```json
{"allow": false, "reason": "there is a change freeze until Monday"}
```

Three things about that answer:

* **Silence is not consent.** An empty body is a refusal, because a plugin that
  answered `200` and nothing else has said nothing.
* **Not answering is not a refusal.** If you are down or slow, the deploy goes
  ahead. A plugin that stops every deploy the moment it is upgraded is a plugin
  nobody installs twice.
* **Every blocking plugin has to agree.** One plugin's yes does not overrule
  another's no.

Give a reason. It is shown to the person whose deploy you just stopped, and
without one they are told a plugin refused and nothing else.

### Delivery is not guaranteed

A notification is posted once, with a short timeout. A plugin that was
restarting misses it, and there is no retry queue — saying so plainly is better
than a loop that looks like a guarantee and is not one. If you must not miss
anything, read the state back through the API. That is what your token is for.

## Publishing

Publish the manifest anywhere it can be fetched over HTTPS. An operator installs
it by URL, or finds it in a store.

An operator who does not want a store at all can point the panel at their own
index, or paste a manifest. That path is not an afterthought: a cluster with no
way out to the internet has to be able to run plugins too.

### Running a store

A store is two static files at one address. Nothing else — no database, no API,
no account system.

`index.json` lists what is available:

```json
{
  "version": 1,
  "generated_at": "2026-09-18T09:00:00Z",
  "plugins": [
    {
      "id": "acme-deploy-guard",
      "name": "Deploy Guard",
      "description": "Stops a deploy during a change freeze.",
      "version": "1.2.0",
      "license": "commercial",
      "author": "Acme",
      "homepage": "https://acme.example/deploy-guard",
      "category": "policy",
      "manifest_url": "https://acme.example/deploy-guard/plugin.json",
      "manifest_sha256": "3b1f…",
      "paid": true,
      "purchase_url": "https://acme.example/deploy-guard/buy"
    }
  ]
}
```

An entry is a summary and not a manifest. What a plugin may do stays in its
manifest, which the panel fetches and shows before anything is installed — an
index that carried permissions would be a second place for them to be written
and a second place for them to disagree.

`index.json.sig` is a detached Ed25519 signature over the index's exact bytes:

```
skifity admin plugin-key
skifity admin plugin-sign <private key> index.json > index.json.sig
```

`plugin-key` prints a pair and writes neither: the private key is yours to put
somewhere you already trust, and a command that saved it for you would be a
command that left a signing key in `/root`. The public key goes into Settings,
then Plugins, on every panel that should trust the store.

The signature covers the file's exact bytes. Reformatting the JSON after signing
invalidates it, which is the point.

### What the signature means, and what it does not

The store operator vouches for the list; each entry's `manifest_sha256` pins the
manifest, so a manifest cannot be swapped after the list was signed. That needs
no key registry — a publisher does not need a key of their own, because the
store is what vouches for them. It is the same shape as an apt release file.

It is **not** proof a plugin is safe. It says this entry is the one the store
published. What a plugin may do is its permission list, which an administrator
reads before installing, and that is the part that actually protects anybody.

A panel with no public key configured still reads an index, and the page says
plainly that nobody vouched for it. Refusing outright would mean a store is
unusable until somebody pastes a key; accepting quietly would make the signature
decoration.

### Pointing a panel somewhere else

Settings, then Plugins, has two values: the index address and the public key.
The default address is the Skifity store. An internal index on a machine only
your network can reach works exactly the same way, and so does an index with no
signature at all — the panel just says so.

> **Never run against a real cluster.** The manifest standard, the permission
> model, the rendered Kubernetes objects, the event delivery, the blocking
> verdicts, and the store's signature and hash checks are all unit-tested.
> Whether a real plugin image starts in the namespace this renders, and whether
> an event reaches it over a real cluster network, has not been tried — the same
> gap as everything else in ADR-0010.
>
> **There is no store at `plugins.skifity.com` yet.** The panel can read one,
> verify one and install from one; nothing is published at that address. Until
> something is, a plugin is installed by pasting a manifest or giving its
> address, and the Store tab will say it could not reach anything. See the
> Status section of the README.
