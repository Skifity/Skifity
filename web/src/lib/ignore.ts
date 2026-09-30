/**
 * What the panel's folder picker leaves out of a folder before sending it.
 *
 * The same rules as `skifity up` (internal/cli/ignore.go), and held to the same
 * cases: internal/cli/testdata/ignore-cases.json is read by the Go test and by
 * web/scripts/check-ignore.mjs, which runs this file. Two implementations of
 * one rule stay one rule only that way.
 *
 * A folder somebody picks has node_modules in it, a .next cache, the .env with
 * their real keys. .gitignore already draws that line, so it is read the way
 * git reads it, in every folder, and .skifityignore adds to it.
 *
 * No imports, so Node can load this file directly for the check.
 */

/** Never sent, whatever an ignore file says. */
const alwaysLeftOut = new Set([
  ".git",
  ".hg",
  ".svn",
  "node_modules",
  ".DS_Store",
  "Thumbs.db",
  "skifity.toml",
])

/** Left out as if a .gitignore said so, so a "!" line can bring one back. */
const leftOutUnlessKept = [
  "__pycache__/",
  "*.pyc",
  ".venv/",
  "venv/",
  ".tox/",
  ".pytest_cache/",
  ".mypy_cache/",
  ".next/",
  ".nuxt/",
  ".svelte-kit/",
  ".turbo/",
  ".cache/",
  ".parcel-cache/",
  ".vercel/",
  ".netlify/",
  ".idea/",
  ".vscode/",
  "coverage/",
  "*.log",
]

/** Read in every folder, in this order. */
export const ignoreFileNames = [".gitignore", ".skifityignore"]

/** A .env with real values, as opposed to the template of one. */
export function isSecretsFile(name: string): boolean {
  if (name !== ".env" && !name.startsWith(".env.")) return false
  return ![".env.example", ".env.sample", ".env.template", ".env.dist"].includes(name)
}

type Rule = { base: string; pattern: RegExp; negate: boolean; dirOnly: boolean }

function parseRule(base: string, raw: string): Rule | null {
  let line = raw.replace(/[ \t\r]+$/, "")
  if (line === "" || line.startsWith("#")) return null
  let negate = false
  if (line.startsWith("!")) {
    negate = true
    line = line.slice(1)
  } else if (line.startsWith("\\!") || line.startsWith("\\#")) {
    line = line.slice(1)
  }
  let dirOnly = false
  if (line.endsWith("/")) {
    dirOnly = true
    line = line.replace(/\/+$/, "")
  }
  if (line === "") return null
  // A slash anywhere but the end ties the pattern to its own folder.
  const anchored = line.includes("/")
  line = line.replace(/^\//, "")
  const body = globToRegExp(line)
  try {
    const pattern = new RegExp(anchored ? `^${body}$` : `^(?:.*/)?${body}$`)
    return { base, pattern, negate, dirOnly }
  } catch {
    // A line git would not understand either; git skips it too.
    return null
  }
}

function escape(c: string): string {
  return c.replace(/[.*+?^${}()|[\]\\/-]/g, "\\$&")
}

function globToRegExp(glob: string): string {
  let out = ""
  for (let i = 0; i < glob.length; i++) {
    const rest = glob.slice(i)
    const c = glob[i]
    if (rest.startsWith("**/")) {
      out += "(?:.*/)?"
      i += 2
    } else if (rest.startsWith("/**") && i + 3 === glob.length) {
      out += "(?:/.*)?"
      i += 2
    } else if (rest.startsWith("**")) {
      out += ".*"
      i += 1
    } else if (c === "*") {
      out += "[^/]*"
    } else if (c === "?") {
      out += "[^/]"
    } else if (c === "[") {
      const end = glob.indexOf("]", i + 1)
      if (end < 0) {
        out += "\\["
        continue
      }
      let cls = glob.slice(i + 1, end)
      if (cls.startsWith("!")) cls = "^" + cls.slice(1)
      out += "[" + cls.replace(/\\/g, "\\\\") + "]"
      i = end
    } else if (c === "\\" && i + 1 < glob.length) {
      i++
      out += escape(glob[i])
    } else {
      out += escape(c)
    }
  }
  return out
}

function basename(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1)
}

/**
 * Decides, path by path, what is sent.
 *
 * `ignoreFiles` maps a folder ("" for the top) to the text of its ignore files,
 * in the order of ignoreFileNames. A path is left out when it, or any folder it
 * is in, is — the same answer as the Go walk, which never enters a folder it
 * has left out, and so never reads that folder's own ignore files either.
 */
export function makeFolderFilter(ignoreFiles: Map<string, string[]>): (path: string) => boolean {
  const rules: Rule[] = []
  for (const line of leftOutUnlessKept) {
    const rule = parseRule("", line)
    if (rule) rules.push(rule)
  }
  const loaded = new Set<string>()
  const load = (dir: string) => {
    if (loaded.has(dir)) return
    loaded.add(dir)
    for (const text of ignoreFiles.get(dir) ?? []) {
      for (const line of text.split("\n")) {
        const rule = parseRule(dir, line)
        if (rule) rules.push(rule)
      }
    }
  }

  const ignored = (rel: string, isDir: boolean): boolean => {
    const name = basename(rel)
    if (alwaysLeftOut.has(name) || (!isDir && isSecretsFile(name))) return true
    let result = false
    for (const rule of rules) {
      if (rule.dirOnly && !isDir) continue
      let sub = rel
      if (rule.base !== "") {
        if (!rel.startsWith(rule.base + "/")) continue
        sub = rel.slice(rule.base.length + 1)
      }
      if (rule.pattern.test(sub)) result = !rule.negate
    }
    return result
  }

  const dirVerdicts = new Map<string, boolean>()
  load("")
  return (path: string) => {
    const parts = path.split("/")
    let dir = ""
    for (let i = 0; i < parts.length - 1; i++) {
      dir = dir === "" ? parts[i] : `${dir}/${parts[i]}`
      let out = dirVerdicts.get(dir)
      if (out === undefined) {
        out = ignored(dir, true)
        dirVerdicts.set(dir, out)
      }
      if (out) return false
      load(dir)
    }
    return !ignored(path, false)
  }
}
