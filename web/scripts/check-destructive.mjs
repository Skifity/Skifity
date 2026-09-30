#!/usr/bin/env node
/**
 * Fails the build when something destructive happens on one click.
 *
 * The panel asks before it deletes an app, a project, a database, a server, a
 * disk, a domain and a variable — and used to fire six others straight from a
 * bin icon the same size and colour as all the rest: revoking an API token,
 * removing a team member, unlinking a database from an app, deleting a
 * notification channel, revoking a session and cancelling an invitation. The
 * inconsistency is the bug: somebody who has learned that this panel asks is
 * exactly the person who clicks without reading.
 *
 * Every mutation whose request is a DELETE has to be fired from something that
 * asks first — useConfirm or useDeleteConfirm — or be named here with a reason.
 */

import { readFileSync, readdirSync } from "node:fs"
import { join, dirname, relative } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, "..", "src")

/**
 * Deletes that do not ask, on purpose.
 *
 * Keep this short, and say why. "It is only a list row" is not a reason; the
 * question is whether the person can put it back.
 */
const ALLOWED = new Map([
  // Nothing is lost: the schedule is three fields the user typed and can type
  // again, and the dialog for it is already open when the bin is pressed.
  ["components/app/console-tab.tsx:remove", "asks through its own confirm dialog"],
])

function* walk(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const full = join(directory, entry.name)
    if (entry.isDirectory()) yield* walk(full)
    else yield full
  }
}

const problems = []

for (const file of walk(srcDir)) {
  if (!file.endsWith(".tsx")) continue
  const rel = relative(srcDir, file).split("\\").join("/")
  if (rel.startsWith("components/ui/")) continue
  const source = readFileSync(file, "utf8")

  // Every mutation whose body calls api.delete.
  const mutations = [...source.matchAll(/const (\w+) = useMutation\(\{([\s\S]{0,400}?)\n\s{2}\}\)/g)]
  for (const [, name, body] of mutations) {
    if (!/api\.delete\(/.test(body)) continue

    // Where is it fired from? Look at what precedes each .mutate( call.
    for (const call of source.matchAll(new RegExp(`${name}\\.mutate\\(`, "g"))) {
      const before = source.slice(Math.max(0, call.index - 700), call.index)
      const asks = /confirm\w*\(|askThen\w*\(|\bconfirmDelete\(/i.test(before)
      if (asks) continue
      if (ALLOWED.has(`${rel}:${name}`)) continue
      const line = source.slice(0, call.index).split("\n").length
      problems.push(
        `${rel}:${line} fires ${name}, which DELETEs, without asking first — ` +
          `use useConfirm or useDeleteConfirm, or add it to ALLOWED in this script with a reason`,
      )
    }
  }
}

if (problems.length > 0) {
  console.error(`\n${problems.length} destructive action(s) that do not ask:\n`)
  for (const problem of problems) console.error(`  - ${problem}`)
  console.error("")
  process.exit(1)
}

console.log("destructive: every delete asks first.")
