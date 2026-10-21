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
 *
 * "Asks first" means in the same handler. This used to look at the 700
 * characters before each `.mutate(` for anything called confirm-something, and
 * the "Turn it off" button for two-factor passed because the "Get new codes"
 * button beside it asked: one click switched the second factor off and
 * deleted the recovery codes. So the source is parsed, and the question is
 * asked of the handler the call is actually in — the onClick, the onSubmit, or
 * the named function around it — not of whatever happens to be nearby.
 */

import { readFileSync, readdirSync } from "node:fs"
import { join, dirname, relative } from "node:path"
import { fileURLToPath } from "node:url"
import ts from "typescript"

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
  // DELETE is the verb, and nothing is lost: lifting a deploy lock and ending
  // maintenance give the app back its normal state, and either is put back
  // with one click. Asking first would only slow down the way out of an
  // incident.
  [
    "components/app/deploy-lock.tsx:unlock",
    "lifts a lock; nothing is lost and it is put back in one click",
  ],
  [
    "components/app/maintenance.tsx:end",
    "ends maintenance; nothing is lost and it is started again in one click",
  ],
])

/** The hooks that ask, and the functions they return are called. */
const ASKING_HOOKS = new Set(["useConfirm", "useDeleteConfirm"])

/** Hooks whose callback is the handler itself, rather than a step inside one. */
const HANDLER_HOOKS = new Set(["useCallback", "useMemo", "useEffect"])

function* walk(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const full = join(directory, entry.name)
    if (entry.isDirectory()) yield* walk(full)
    else yield full
  }
}

/** Calls fn for every node under root, root included. */
function visit(root, fn) {
  const step = (node) => {
    fn(node)
    ts.forEachChild(node, step)
  }
  step(root)
}

const isFunction = (node) =>
  ts.isArrowFunction(node) ||
  ts.isFunctionExpression(node) ||
  ts.isFunctionDeclaration(node) ||
  ts.isMethodDeclaration(node)

/** The name a call is made through: `useMutation(...)` → "useMutation". */
const calleeName = (call) => (ts.isIdentifier(call.expression) ? call.expression.text : null)

/**
 * Whether a function is a handler of its own, or one step inside one.
 *
 * `.then((yes) => …)`, `.map(…)` and the like are steps: their callback runs
 * as part of whatever called them, so the search carries on outwards. A
 * function anywhere else — an onClick, a property, a named const — is the
 * handler, and the search stops there.
 */
function isHandler(fn) {
  const parent = fn.parent
  if (parent && ts.isCallExpression(parent) && parent.arguments.includes(fn)) {
    return HANDLER_HOOKS.has(calleeName(parent) ?? "")
  }
  return true
}

const problems = []

for (const file of walk(srcDir)) {
  if (!file.endsWith(".tsx")) continue
  const rel = relative(srcDir, file).split("\\").join("/")
  if (rel.startsWith("components/ui/")) continue
  const source = readFileSync(file, "utf8")
  const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)

  // The functions that ask: whatever useConfirm and useDeleteConfirm returned.
  const askers = new Set()
  // Every mutation whose body calls api.delete, with the function it lives
  // in, so a name reused by the next component is not confused with it.
  const deletes = []
  visit(sf, (node) => {
    if (!ts.isVariableDeclaration(node) || !ts.isIdentifier(node.name)) return
    const init = node.initializer
    if (!init || !ts.isCallExpression(init)) return
    const hook = calleeName(init)
    if (ASKING_HOOKS.has(hook)) askers.add(node.name.text)
    if (hook !== "useMutation") return
    let deletes_ = false
    visit(init, (inner) => {
      if (
        ts.isCallExpression(inner) &&
        ts.isPropertyAccessExpression(inner.expression) &&
        ts.isIdentifier(inner.expression.expression) &&
        inner.expression.expression.text === "api" &&
        inner.expression.name.text === "delete"
      ) {
        deletes_ = true
      }
    })
    if (!deletes_) return
    let scope = node.parent
    while (scope && !isFunction(scope) && !ts.isSourceFile(scope)) scope = scope.parent
    deletes.push({ name: node.name.text, scope })
  })

  /** Whether a call is to one of the asking functions, or chains off one. */
  const asks = (call) => {
    let callee = call.expression
    // confirm(…).then(…): the call that matters is the one .then hangs off.
    while (ts.isPropertyAccessExpression(callee) && ts.isCallExpression(callee.expression)) {
      callee = callee.expression.expression
    }
    return ts.isIdentifier(callee) && askers.has(callee.text)
  }

  for (const { name, scope } of deletes) {
    if (ALLOWED.has(`${rel}:${name}`)) continue
    visit(scope, (node) => {
      if (
        !ts.isCallExpression(node) ||
        !ts.isPropertyAccessExpression(node.expression) ||
        !ts.isIdentifier(node.expression.expression) ||
        node.expression.expression.text !== name ||
        !["mutate", "mutateAsync"].includes(node.expression.name.text)
      ) {
        return
      }

      // Outwards from the call to its handler: a confirm that this call is
      // inside the answer of — confirm(…).then((yes) => …) — asks.
      let handler = null
      let asked = false
      for (let up = node.parent; up && up !== scope; up = up.parent) {
        if (ts.isCallExpression(up) && asks(up)) {
          asked = true
          break
        }
        if (isFunction(up) && isHandler(up)) {
          handler = up
          break
        }
      }
      // Or one the same handler awaited before it: `if (await confirm(…))`.
      if (!asked && handler) {
        visit(handler, (inner) => {
          if (ts.isCallExpression(inner) && inner.getEnd() <= node.getStart() && asks(inner)) {
            asked = true
          }
        })
      }
      if (asked) return

      const line = sf.getLineAndCharacterOfPosition(node.getStart()).line + 1
      problems.push(
        `${rel}:${line} fires ${name}, which DELETEs, from a handler that does not ask first — ` +
          `use useConfirm or useDeleteConfirm in that same handler, or add it to ALLOWED in this script with a reason`,
      )
    })
  }
}

if (problems.length > 0) {
  console.error(`\n${problems.length} destructive action(s) that do not ask:\n`)
  for (const problem of problems) console.error(`  - ${problem}`)
  console.error("")
  process.exit(1)
}

console.log("destructive: every delete asks first, in the handler that fires it.")
