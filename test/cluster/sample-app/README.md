# The sample application

Deployed from Git by `test/cluster/verify.sh`, so that the check covers building
as well as running. It is a single Go file and a two-stage Dockerfile with
nothing to download at build time: if it fails to build, the builder is what
failed, not the network.

| Path | Answers |
|---|---|
| `/` | `version=<VERSION> instance=<hostname>` |
| `/healthz` | `ok`, once it is serving |
| `/data` | the contents of `DATA_FILE`, or 404 |
| `/write?<text>` | writes `<text>` to `DATA_FILE` |

`/data` and `/write` are what make a persistent volume checkable: write, restart
the instance, read it back. `VERSION` is an environment variable rather than
something baked in, which is how the rollback check tells two deployments apart
without a second build — and is itself a check that changing a variable does not
rebuild.
