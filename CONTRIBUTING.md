# Contributing

Thank you for looking. This is a small codebase with a few rules that are
load-bearing; the rest is ordinary Go and TypeScript.

## Before you open a pull request

Run `make check`. It is what CI runs — every linter, the tests, the
vulnerability scan — and if it does not contain something CI fails on, that is a
bug in the Makefile and worth reporting on its own.

```
make check       Linters, tests, govulncheck, npm audit
make smoke       The panel and the installer, end to end, against a real binary
make e2e         The Playwright interface test, against the real binary
```

Nothing in this repository runs an installer, k3s or anything else
system-level on your machine. If a change needs a cluster, it needs a VM or a
container — see [ADR-0010](docs/decisions.md).

## The rules that are not obvious

**English only** in code, comments, commit messages, documentation and CLI
output. The interface is translated; the source is not.

**Every user-visible string is a key** in `web/src/locales/*.json`, in all five
languages (`en`, `id`, `hi`, `ru`, `zh-CN`). `npm --prefix web run check:i18n`
fails the build if one is missing. There are no hardcoded strings in the UI.

**The UI is shadcn/ui only**, from `web/src/components/ui`. No second component
library.

**A failure returns an `errdoc.Problem`**, not a bare error: a cause, an impact
and a fix, with an entry in `internal/errdoc/catalogue.go`. A `WithDocs` link
must resolve to a page the panel serves, at an anchor that exists; a test in
`internal/docsite` enforces both.

**Nothing is logged that a secret could be inside.** `internal/logging` redacts
by key and by value pattern; a value that genuinely has to be logged is wrapped
in `logging.Public`, and that should stay rare.

**Authorization lives in `internal/api`**, in `authorizeTeam`, `authorizeApp`
and friends. A handler that reaches into the store without one of those is a
tenant-isolation bug.

**Never commit a secret.** `.env.example` documents every setting; tests and
fixtures use obviously fake credentials.

## Commits

[Conventional Commits](https://www.conventionalcommits.org), small and often.
The first line says what changed; the body says why, and what it was like
before if that is the interesting part.

## Adding a template

A template is a YAML file in `internal/templates/catalogue`, and adding one
touches no Go. That directory's README has the format and the two rules that
are not obvious — name a version, and do not wire the database by hand — and
`make check` tells you whether it is right.

## Where things are

`docs/architecture.md` is how it fits together, `docs/decisions.md` is why it is
the way it is, and `docs/progress.md` is where the work stands, including what
has never been executed. That last one is kept honest on purpose: if you find
something it claims that is not true, that is the most valuable bug report you
can file.
