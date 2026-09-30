# Phase 6: the person

The last row of `docs/checklist.md` is documentation, and it is the only one no
script can settle. Everything else can be asserted. This one is a person who has
not seen this before, trying to get an app running, while somebody writes down
where they stop.

It is not a usability study and it does not need one. It needs an hour, a
throwaway server, and somebody who will not be helped.

## What to hand them

`docs/quick-start.md`, the address of a server they can rebuild, and nothing
else. Not a demo. Not a summary. Not "just ask me if you get stuck".

## The one rule

**Do not help.** The moment you answer a question, that question stops being a
documentation bug and becomes a thing you know and they do not. If they ask,
write the question down and say you cannot answer it yet.

They may look at anything the product or the documentation gives them: the
panel, the error messages, `docs/`. Not at you.

## What to write down

For every stop, four things:

| | |
|---|---|
| **Where** | The page and the step. "Quick start, step 3." |
| **What they expected** | In their words, not yours. |
| **What happened** | What the screen or the terminal actually said. |
| **What they did next** | This is the expensive one. See below. |

The stops that cost the most are the ones where **they worked it out and carried
on**. Nobody reports those, because from the inside it feels like success. They
are a documentation bug that will hit every future reader, and they are only
ever visible to the person watching. Write down every hesitation, every re-read,
every back button.

## Where to watch hardest

Places this has already been wrong, so they are the places to expect it again:

* **Before the install.** Does the one-line command work at all, or does it need
  a version they have to find? If they end up cloning the repository, the quick
  start did not tell them soon enough.
* **The recovery key.** They are told it is the only copy. Do they download it,
  or do they click past it? If they click past it, the screen is not doing its
  job — and that is the one mistake in this product that cannot be undone.
* **The first app's address.** It is plain HTTP, on purpose (ADR-0015). Do they
  notice? Do they think something is broken? Two pages used to promise HTTPS
  there, which is precisely the kind of sentence this exercise exists to catch.
* **A private repository.** The quick start says "paste a Git repository URL".
  If theirs is private, what do they do? Watch whether they find Settings → Git
  on their own.
* **The first failure.** Break something on purpose if nothing breaks by itself:
  a repository with no Dockerfile and no recognisable framework. Does the error
  say what to do, or only what went wrong?

## Afterwards

1. Every stop is a documentation bug. Fix the page, not the person.
2. A stop they worked around silently is a bug with a higher priority than one
   they asked about.
3. If they never got an app running, that is the result. Write it down plainly,
   and do not tag a release until the next person does.
4. Move row 18 in `docs/checklist.md` only when somebody has got to the end
   without being helped, and say who and when.

## Why this is not a script

A script knows where the buttons are. That knowledge is the entire thing being
tested: the question is not whether the steps work, but whether the steps are
findable by somebody who does not already know them. The moment this becomes a
checklist somebody runs, it stops measuring anything.
