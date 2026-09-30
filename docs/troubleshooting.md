# Troubleshooting

Every error in Skifity says what happened, what it means and how to fix it, and
has a button that copies the whole thing for an AI assistant. Start there. This
page is for the things that happen outside the panel, or when the panel itself
is the problem.

## The panel does not load

**Nothing at all.** Check it is running:

```sh
kubectl -n skifity-system get pods
```

If the pod is not `Running`, ask why:

```sh
kubectl -n skifity-system describe pod -l app.kubernetes.io/component=panel
kubectl -n skifity-system logs -l app.kubernetes.io/component=panel
```

**The page times out.** The server's firewall may be blocking ports 80 and 443.
Some providers have a firewall in their control panel that the server cannot
see.

**A certificate warning.** With no domain of your own, the panel is served over
plain HTTP at an `sslip.io` address, on purpose: Let's Encrypt rate limits are
per domain, and every Skifity install in the world shares sslip.io's. Add a
domain in Settings and HTTPS is turned on for it automatically.

## I have lost the setup token

It is on the server:

```sh
sudo cat /etc/skifity/setup-token
```

If the file is gone but no account was ever created, restart the panel and it
writes a new one and logs it:

```sh
kubectl -n skifity-system rollout restart deployment/skifity-panel
kubectl -n skifity-system logs -l app.kubernetes.io/component=panel | grep setup_token
```

## I have forgotten my password

**Forgot your password?** on the sign-in page sends a link to your address,
when the panel has a mail server (Settings → Email) and a Panel URL to link to.
The link works once, for half an hour; only the newest one works; and using it
signs out everything that was signed in with the old password. It does not
skip two-factor: an account that has it still needs its code afterwards.

The page answers the same whether or not the address has an account, so it
cannot be used to find out who does.

Without a mail server, or when the mail does not arrive, reset it from the
server instead:

```sh
sudo skifity admin reset-password you@example.com
```

## A deployment failed

The deployment's own page has the build log and the reason. The common ones:

**Out of memory during the build.** Builds are the most memory-hungry thing
Skifity does. Add a server, or raise the build memory limit in Settings.

**No Dockerfile and nothing detected.** Skifity could not tell what the project
is. Add a Dockerfile, or set the builder explicitly in the app's settings.

**The app starts and immediately stops.** Almost always the port. Your app must
listen on the port in the `PORT` variable, which Skifity sets. Listening on a
hardcoded 3000 when Skifity asked for 8080 produces exactly this.

**Health checks never pass.** The health check path returns something other
than 200, or the app takes longer to start than the check allows. Both are in
the app's settings: the path, and **Time to start**, which is two minutes unless
you raise it. A JVM or an image that migrates its database before it listens
often needs five to ten. See [Health checks](concepts.md#health-checks).

## Bitbucket refuses the token or a webhook

**Connecting answers that Bitbucket did not accept the token (401).** The token
is wrong, expired — an API token lasts a year at most — or sent the wrong way.
An API token goes with the email of the Atlassian account that made it, or with
no email at all; an access token goes with no email. An app password does not
work any more: Atlassian switched them off on 28 July 2026. Make a new token
and connect again.

**Connecting answers 403.** The token works and lacks a scope. Without a
workspace named, an API token needs `read:workspace:bitbucket` to list your
workspaces; with one named, it needs `read:repository:bitbucket`. An access
token cannot list workspaces at all, so name its workspace. The full list is in
[Connecting Bitbucket](concepts.md#connecting-bitbucket).

**The repository list or the branches do not load.** The same scopes, and the
panel has to reach `api.bitbucket.org` over HTTPS.

**Deploy on push never happens.** Look at the webhook in Bitbucket, under
**Repository settings → Webhooks → View requests**:

* No request at all: the webhook is not there or does not have the triggers.
  The panel adds it only when the token has the webhook scopes; otherwise add it
  by hand, with the address and secret from **Settings → Git → Webhook**.
* **401 "This webhook could not be verified"**: the delivery was not signed with
  this connection's secret. The webhook has no secret, or another one — Bitbucket
  never shows a secret again, so edit the webhook, paste the secret from the
  panel into **Secret**, and save. An unsigned delivery is always refused.
* **202** and nothing deployed: the answer names each app and why it was
  skipped — another branch, deploy on push off, a `[skip ci]`, or a pull
  request from a fork, which Bitbucket gives no preview.

**A status or a comment does not appear on the pull request.** The deployment's
log says once why. The token needs `read:repository:bitbucket` for statuses, and
the pull request scopes (an access token: **Pull requests: Write**) for the
comment.

## An app says it is waiting for a server

The panel reads the scheduler's own answer, so the sentence on the page is the
reason rather than a guess. The common ones:

| What it says | What to do |
| --- | --- |
| No server has enough free CPU or memory | Lower what the app reserves under **Scaling**, or add a server. Reserved is a guarantee, so an app reserving more than it uses keeps other apps off the machine. |
| This environment has reached its limit | Raise the environment's limits, or give this app less. The bars on the project page show which limit. |
| This app's volume has not been created yet | On one server this is usually the storage class still starting. Across servers it means no server can provide the volume. |
| The only servers with room are not accepting apps | A control-plane server does not run apps unless you allow it, and a server being drained accepts nothing. |
| Every server already runs an instance of this app | Instances are spread across servers, so a three-instance app wants three servers. Add one, or run fewer. |

An app with no instances at all and a message about a limit never got as far as
creating one: the environment's quota refused it, and there is nothing to look
at on the instances tab because nothing was made.

## An app is crashing

Open the app: the instance list shows the restart count and the last reason.
The Logs tab has the output from before it died.

If the app worked before, **Roll back**. That restores the previous image *and*
the settings it ran with, so a bad variable is undone too.

## An app that scales to zero answers 404

An app that scales to zero is reached through a Service that points at KEDA's
interceptor, and Traefik ignores such a Service unless it is told to accept
them. Installs made before Skifity told it answer 404 for every app that scales
to zero, while its other apps are fine.

Run the installer again on the panel's server; it rewrites Traefik's
configuration and changes nothing else. If Traefik is configured by a
HelmChartConfig of your own, set
`providers.kubernetesIngress.allowExternalNameServices: true` in it instead.

## What Kubernetes said

When an app will not start and its logs are empty, the reason is usually in
what Kubernetes itself wrote down: the scheduler that could not place an
instance, a health check that failed, a volume that would not attach, an
instance the kernel stopped for using too much memory. The app's **Advanced**
tab lists these events, newest first, with repeats counted, and refreshes
itself while it is open. **Warnings only** leaves out the routine ones.

The common warnings come with what they mean and what to do:

| Kubernetes says | What it means |
|---|---|
| `FailedScheduling` | No server has room, or none is allowed to run the app. Which one is named. |
| `BackOff` | The app keeps stopping soon after it starts. Its logs from before the restart say why: **Logs**, then the previous instance. |
| `Unhealthy` | A health check failed. Check its path, port and start time under **Settings**. |
| `FailedMount`, `FailedAttachVolume` | A volume is still held by another server, or configuration the app reads is missing. |
| `FailedCreatePodSandBox` | The server could not set up the instance's network. A problem on the server, not in the app. |
| `OOMKilling` | The app used more than its memory limit. Raise it under **Scaling**. |
| `Evicted` | The server ran short of memory or disk and moved the instance. |
| `ErrImagePull`, `FailedPull` | The image is missing, private, or the registry cannot be reached. |

Kubernetes keeps events for about an hour, so an old crash is not here; the
instance list and the Logs tab keep more. The same list is `skifity events`
(with `--warnings`, and `--db` for a database), and `get_events` for an
assistant. A message that quotes one of the app's secret variables has the
value taken out.

## A domain does not work

The panel shows the DNS record to create, and **Check DNS** on the domain asks
DNS whether it has taken effect: it says whether the name points here, points
somewhere else (and at what), points here only in part — often an old AAAA
record left beside a new A record — or has no record yet. Adding a domain does
the same check once. You can ask from your own computer too:

```sh
dig +short app.example.com
```

That has to return your server's IP address. If it returns nothing, DNS has not
propagated yet, which can take up to an hour. The certificate is only issued
once the name points here, so a domain whose DNS is wrong stays waiting.

If DNS is right but the certificate is not issued, cert-manager is still
working. Watch it:

```sh
kubectl get certificate -A
kubectl describe certificate -n <environment-namespace> <name>
```

Let's Encrypt rate-limits per domain. If you have been experimenting, you may be
paused for a week; the certificate's events say so.

A domain served with a certificate of the team's own says so on the Domains
tab, and cert-manager has nothing to do with it. If browsers refuse it, look at
the certificate itself: `openssl s_client -connect app.example.com:443
-servername app.example.com </dev/null | openssl x509 -noout -subject -dates`
shows which one is being sent and until when.

## Your own certificate is refused

Uploading a certificate under **Settings**, **Certificates** (or with
`skifity certs add`) checks it before anything is kept, and says which of these
it is. None of them stores anything.

**That is not a certificate the panel can read.** The certificate box needs PEM:
text starting `-----BEGIN CERTIFICATE-----`. A `.cer` or `.crt` file that looks
like binary is DER; convert it:

```sh
openssl x509 -inform der -in cert.cer -out cert.pem
```

A `.pfx` or `.p12` bundle holds both halves; take them out with
`openssl pkcs12 -in bundle.pfx -clcerts -nokeys -out cert.pem` and
`openssl pkcs12 -in bundle.pfx -nocerts -nodes -out key.pem`, and add the
intermediates your certificate authority gave you to `cert.pem`.

**That is not a private key the panel can read.** The key box needs an
unencrypted PEM key — `-----BEGIN PRIVATE KEY-----`, `RSA PRIVATE KEY` or
`EC PRIVATE KEY`. One protected by a passphrase (`ENCRYPTED PRIVATE KEY`, or a
`Proc-Type: 4,ENCRYPTED` line) has to be decrypted first:

```sh
openssl pkey -in encrypted.key -out plain.key
```

**The private key does not belong to this certificate.** The key has to be the
one the certificate request was made with. These print the same thing when the
two belong together:

```sh
openssl x509 -noout -pubkey -in cert.pem | openssl sha256
openssl pkey -pubout -in key.pem | openssl sha256
```

**These certificates are not one chain.** Paste your certificate and the
intermediates issued for it, in any order: the panel puts them leaf first. What
it refuses is a certificate that signed nothing in the chain — another site's
certificate, or an old intermediate from a previous renewal. The refusal names
it.

**This certificate has expired** or **is not valid yet.** Browsers refuse both.
Renew it; if a new certificate says it is not valid yet, check the clock on the
server the panel runs on.

**This certificate's key is not strong enough.** The panel serves RSA of 2048
bits or more, ECDSA on P-256 or P-384, and Ed25519. Make a new key and have the
certificate issued again:

```sh
openssl req -new -newkey rsa:3072 -nodes -keyout key.pem -out request.csr
```

**This certificate names no hostname.** It has no DNS names in its Subject
Alternative Name, and browsers stopped reading the Common Name years ago. Have
it issued again with `DNS:shop.example.com` (or `DNS:*.example.com`) as a
Subject Alternative Name. `openssl x509 -noout -ext subjectAltName -in cert.pem`
shows what it has.

**This certificate names a hostname that is not this team's.** The ingress
controller serves every certificate in the cluster by name, whichever app it
came from, so a certificate naming another team's hostname, a hostname in
another team's certificate, or the panel's own address would be sent to those
visitors too. Use a certificate that names only your team's hostnames. The same
rule works the other way: a domain named exactly in another team's certificate
cannot be added (**Another team's certificate names this hostname**). A
wildcard is refused only when another team has the same wildcard, because an
exact name always wins over one.

## The cluster is unreachable

The panel keeps working and says so on every page. Your apps keep running: they
do not need the control plane to serve traffic.

On the control plane server:

```sh
sudo systemctl status k3s
sudo journalctl -u k3s -n 100 --no-pager
```

The usual cause is the server running out of disk. Check with `df -h`.

## A server says "not responding"

The node stopped reporting. The server may be off, out of disk, or unreachable.
Instances that were on it are rescheduled elsewhere automatically if there is
capacity.

If the server is gone for good, remove it in the panel: its work is already
elsewhere.

## Restoring after losing the panel's server

The panel's database and master key live on the first control plane server, in
`/var/lib/skifity` and `/etc/skifity`. To move to a new server:

1. On the old server, take a consistent copy of the database:

   ```
   skifity admin backup-db /root/panel-backup.db
   ```

   Do not copy `panel.db` on its own. The database runs in WAL mode, so a
   committed change can still be sitting in `panel.db-wal`; a copy of the one
   file comes back missing it and nothing tells you. `backup-db` takes the copy
   through SQLite itself, and can be run while the panel is up.

2. Install Skifity on the new server.
3. Stop the panel: `kubectl -n skifity-system scale deploy/skifity-panel --replicas=0`
4. Put `/root/panel-backup.db` in place as `/var/lib/skifity/panel.db`, and copy
   `/etc/skifity/master.key` across.
5. Start it again: `kubectl -n skifity-system scale deploy/skifity-panel --replicas=1`

If the old server is gone rather than being moved, the copy is the newest one
in the backup bucket under `skifity/panel/`. Install Skifity on the new server,
put the master key in place, and follow [Putting it back](backups.md#putting-it-back):
`skifity admin restore-db` checks the copy before it replaces anything.

Without the master key the database is unreadable, which is the point of the
recovery key you were asked to download. [Configuration](configuration.md) has
the full list of what to back up.

## Full monitoring

Skifity shows CPU and memory for every server and every instance without
installing anything: it reads them from metrics-server, which k3s ships.

Prometheus and Grafana are a different thing — dashboards, alerting rules,
history — and they are installed with Helm rather than by the panel. Several
dozen objects with their own release cadence make for a worse installer than
the one their authors wrote:

```
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm install monitoring prometheus-community/kube-prometheus-stack \
  --namespace monitoring --create-namespace
```

It costs roughly 900 MB of memory. The panel lists it under **Settings →
Components** so the cost is visible when you are deciding, and says there that
it does not install it.

## Everything is fine but I want to look underneath

Every app's **Advanced** tab shows the exact Kubernetes objects the panel
applies, ready to copy. `kubectl` is on the control plane server and works
normally; Skifity uses server-side apply with its own field manager, so it will
not fight you over a field you change by hand — but it will change it back on
the next deployment, and it notices in the meantime: see the next section.

## Changed outside Skifity

Every five minutes the panel compares each deployed app's objects in the
cluster — its Deployment, Service, Ingress, network policy, autoscaler,
volumes, the Secrets holding its variables and files, and its workers — with
what it would apply now. When something differs, the app's page says **Changed
outside Skifity**, a notification goes out once (`app.drifted`), and the
**Advanced** tab lists each difference: the object, the field, what the panel
applies, what the cluster holds, and who changed it.

**Put it back** applies the app's objects again, the same apply a change to a
variable makes: nothing is built and no deployment is recorded. Members can do
it; viewers can see the list. `skifity drift` prints the list and
`skifity drift --repair` puts it back. An app can also be set to be put back
automatically; that is off unless somebody turns it on, because a change made
on purpose during an incident is not something to undo behind anybody's back.
An automatic repair is said once, like the drift, and never attempted twice
for the same difference: if something keeps changing the field again, the
panel and it would otherwise take turns for ever.

How it tells a change from noise:

* **Only the fields the panel sets are compared.** Whatever the API server
  fills in, a controller writes, or another tool adds — a default, a status,
  an annotation of its own — is not the panel's and is not reported.
* **Who changed it comes from Kubernetes.** Server-side apply records which
  field manager set each field. A field that now belongs to `kubectl-edit`,
  `kubectl-patch` or `helm` was changed by that tool, and the list says so.
* **The panel's own changes are never drift.** A field the panel still owns,
  and that differs, is a change of its own it has not applied yet — a build
  variable waiting for the next build, a save that could not reach the
  cluster — and is not listed. Nothing is compared while an app is deploying,
  rolling back or being applied, and an autoscaler's instance count, or zero
  instances for an app that sleeps, belongs to the autoscaler.
* **Something missing needs proof.** Each object the panel applies carries a
  fingerprint of itself (`skifity.com/applied-hash`). A field that is missing
  is only reported when the object is still the one the panel applied, since
  otherwise it may simply be a field the panel is about to add. In the same
  way, an object that is not there at all is only called deleted if the panel
  had written it; the panel keeps the list of what its last apply wrote.
* **A volume somebody made bigger is left alone.** A volume can grow and never
  shrink, so there is nothing to put back.

A Secret's values are never shown, only that one differs, and a value that
repeats one of the app's secret variables is taken out. Objects applied by a
panel older than this check carry no fingerprint, so until their next deploy
only changes with a named owner and deleted objects are reported. Scheduled
commands and a database's objects are not compared yet.
