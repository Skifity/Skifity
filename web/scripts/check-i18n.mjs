#!/usr/bin/env node
/**
 * Fails the build when a translation is missing, extra, empty, or has the wrong
 * plural forms for its language.
 *
 * This is the one thing Lingui offers over i18next: a missing translation
 * becomes a build failure rather than an English string appearing in the middle
 * of a Russian page. It is about a hundred lines to reproduce, which is a much
 * better trade than a macro-based toolchain.
 */

import { readFileSync, readdirSync } from "node:fs"
import { join, dirname } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const localesDir = join(here, "..", "src", "locales")
const srcDir = join(here, "..", "src")

/** The language every other language is checked against. */
const SOURCE = "en"

/**
 * The plural categories each language must provide, from Intl.PluralRules.
 * Getting these wrong is not cosmetic: Russian needs one/few/many, and without
 * "many" the panel says "5 сервера" instead of "5 серверов".
 */
const PLURAL_CATEGORIES = {
  en: ["one", "other"],
  id: ["other"],
  hi: ["one", "other"],
  ru: ["one", "few", "many", "other"],
  "zh-CN": ["other"],
}

const PLURAL_SUFFIXES = ["_zero", "_one", "_two", "_few", "_many", "_other"]

function loadLocale(code) {
  const path = join(localesDir, `${code}.json`)
  try {
    return JSON.parse(readFileSync(path, "utf8"))
  } catch (error) {
    console.error(`\nCannot read ${path}: ${error.message}\n`)
    process.exit(1)
  }
}

/** Flattens a nested object into dotted paths. */
function flatten(object, prefix = "", out = new Map()) {
  for (const [key, value] of Object.entries(object)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (value && typeof value === "object" && !Array.isArray(value)) {
      flatten(value, path, out)
    } else {
      out.set(path, value)
    }
  }
  return out
}

/** Splits a key into its base and plural suffix, if it has one. */
function splitPlural(key) {
  for (const suffix of PLURAL_SUFFIXES) {
    if (key.endsWith(suffix)) {
      return { base: key.slice(0, -suffix.length), category: suffix.slice(1) }
    }
  }
  return { base: key, category: null }
}

/** Returns the interpolation placeholders a string uses, such as {{count}}. */
function placeholders(value) {
  if (typeof value !== "string") return new Set()
  return new Set([...value.matchAll(/\{\{\s*([\w.]+)\s*\}\}/g)].map((match) => match[1]))
}

const available = readdirSync(localesDir)
  .filter((name) => name.endsWith(".json"))
  .map((name) => name.replace(/\.json$/, ""))

const source = flatten(loadLocale(SOURCE))
const problems = []

// Every language named in PLURAL_CATEGORIES must actually exist.
for (const code of Object.keys(PLURAL_CATEGORIES)) {
  if (!available.includes(code)) {
    problems.push(`${code}.json is missing, but the panel ships in that language`)
  }
}
for (const code of available) {
  if (!PLURAL_CATEGORIES[code]) {
    problems.push(`${code}.json exists but is not in the shipped language list`)
  }
}

// Group the source keys by their base, so plural sets are checked as a whole.
const sourceGroups = new Map()
for (const [key, value] of source) {
  const { base, category } = splitPlural(key)
  if (!sourceGroups.has(base)) sourceGroups.set(base, { plural: false, value, categories: [] })
  const group = sourceGroups.get(base)
  if (category) {
    group.plural = true
    group.categories.push(category)
    group.value = value
  }
}

for (const code of available) {
  if (code === SOURCE) {
    // The source still has to be free of empty strings.
    for (const [key, value] of source) {
      if (typeof value !== "string" || value.trim() === "") {
        problems.push(`${SOURCE}: ${key} is empty`)
      }
    }
    continue
  }

  const target = flatten(loadLocale(code))
  const categories = PLURAL_CATEGORIES[code] ?? ["other"]

  for (const [base, group] of sourceGroups) {
    if (group.plural) {
      // A plural key must have exactly the categories this language uses.
      for (const category of categories) {
        const key = `${base}_${category}`
        const value = target.get(key)
        if (value === undefined) {
          problems.push(`${code}: ${key} is missing (${base} is a plural, and ${code} needs ${categories.join(", ")})`)
        } else if (typeof value !== "string" || value.trim() === "") {
          problems.push(`${code}: ${key} is empty`)
        }
      }
      for (const key of target.keys()) {
        const split = splitPlural(key)
        if (split.base === base && split.category && !categories.includes(split.category)) {
          problems.push(`${code}: ${key} uses the plural form "${split.category}", which ${code} does not have`)
        }
      }
      continue
    }

    const value = target.get(base)
    if (value === undefined) {
      problems.push(`${code}: ${base} is missing`)
      continue
    }
    if (typeof value !== "string" || value.trim() === "") {
      problems.push(`${code}: ${base} is empty`)
      continue
    }

    // A translation that drops or invents a placeholder renders as a literal
    // "{{count}}" or silently loses a value.
    const wanted = placeholders(group.value)
    const got = placeholders(value)
    for (const name of wanted) {
      if (!got.has(name)) {
        problems.push(`${code}: ${base} is missing the placeholder {{${name}}}`)
      }
    }
    for (const name of got) {
      if (!wanted.has(name)) {
        problems.push(`${code}: ${base} has an extra placeholder {{${name}}} that ${SOURCE} does not use`)
      }
    }
  }

  // Keys that exist only in a translation are dead weight, and usually a sign
  // that a key was renamed in English and not everywhere else.
  for (const key of target.keys()) {
    const { base } = splitPlural(key)
    if (!sourceGroups.has(base)) {
      problems.push(`${code}: ${key} is not in ${SOURCE}.json`)
    }
  }
}

// A string only a screen reader hears is still a user-visible string.
//
// These are the ones that stay English in a panel shipping five languages,
// because nobody sighted ever sees them: the close button's label, the
// spinner's, the sidebar toggle's. They came in with vendored components and
// went unnoticed until somebody went looking. This is what stops them coming
// back in with the next one.
const SCREEN_READER = [
  // <span className="sr-only">Close</span>
  /className="sr-only"[^>]*>\s*[A-Za-z]/g,
  // aria-label="Loading"
  /aria-label="[A-Za-z]/g,
]

/**
 * Text inside an sr-only block, rather than directly in the tag that carries
 * the class.
 *
 * The pattern above looks at what follows the class on the same tag, so a
 * header marked sr-only with a title and a description inside it slipped
 * through: the mobile sidebar read out "Sidebar. Displays the mobile sidebar."
 * in every language, and nothing said so.
 */
const SR_BLOCK = /className="sr-only"[\s\S]{0,400}?>\s*([A-Z][A-Za-z ,.'’-]{3,})\s*</g

function checkSourceStrings() {
  for (const file of walk(srcDir)) {
    if (!file.endsWith(".tsx")) continue
    const contents = readFileSync(file, "utf8")
    for (const pattern of SCREEN_READER) {
      for (const match of contents.matchAll(pattern)) {
        const line = contents.slice(0, match.index).split("\n").length
        problems.push(
          `${file.slice(srcDir.length + 1)}:${line} has a hardcoded label a screen ` +
            `reader reads out: ${match[0].trim()}… — use t("…") instead`,
        )
      }
    }
    for (const match of contents.matchAll(SR_BLOCK)) {
      const line = contents.slice(0, match.index).split("\n").length
      problems.push(
        `${file.slice(srcDir.length + 1)}:${line} has hardcoded text inside an ` +
          `sr-only block: "${match[1]}" — use t("…") instead`,
      )
    }
  }
}

/**
 * A call site that does not pass what its string asks for.
 *
 * `t("apps.portHelp")` for a string containing "{{product}}" renders the braces
 * to the user, on the page, in every language. It shipped: the app's settings
 * tab read "which {{product}} sets for you" while the same key on another page
 * read correctly, because that one passed the value.
 *
 * The source's placeholders are the truth; the call has to name each of them.
 * The window is generous because the options object is usually on the next
 * line, and the check is deliberately one-sided: passing a value a string does
 * not use is harmless, leaving one out is not.
 */
function checkInterpolations() {
  const needed = new Map()
  for (const [key, { value }] of sourceGroups) {
    const names = [...placeholders(value)].filter((name) => name !== "count")
    if (names.length > 0) needed.set(key, names)
  }

  for (const file of walk(srcDir)) {
    if (!file.endsWith(".tsx") && !file.endsWith(".ts")) continue
    const contents = readFileSync(file, "utf8")
    for (const match of contents.matchAll(/\bt\(\s*"([\w.]+)"/g)) {
      const names = needed.get(match[1])
      if (!names) continue
      const window = contents.slice(match.index + match[0].length, match.index + match[0].length + 300)
      const missing = names.filter((name) => !new RegExp(`\\b${name}\\s*[:,}]`).test(window))
      if (missing.length > 0) {
        const line = contents.slice(0, match.index).split("\n").length
        problems.push(
          `${file.slice(srcDir.length + 1)}:${line} calls t("${match[1]}") without ` +
            `${missing.map((name) => `{{${name}}}`).join(", ")}, which renders the braces to the user`,
        )
      }
    }
  }
}

/**
 * A user-visible string written straight into a prop.
 *
 * `label="Host"` renders "Host" in Hindi, Russian and Chinese, and no other
 * check saw it: it is not inside an sr-only block, it carries no aria-label,
 * and it is not a key that could go missing. Three of these had been sitting
 * on the database page since it was written — Host, Port and User beside a
 * database name that was translated.
 *
 * The vendored components in components/ui are excluded: they are upstream
 * files that are re-copied from shadcn, and their own strings are covered by
 * the screen-reader checks above.
 */
const LITERAL_PROPS = /\s(label|title|description|confirmLabel|placeholder)="([A-Z][^"]{2,})"/g

/**
 * The same thing written as an expression: `placeholder={host || "Frankfurt 1"}`.
 *
 * The example server name escaped the check above by being a fallback rather
 * than a value. Only prose is flagged here — a capitalised word or words and
 * nothing else — because the brace form is also how a code sample, a product
 * name and an interpolation argument are written.
 */
const LITERAL_IN_BRACES = /\s(label|title|description|confirmLabel|placeholder)=\{[^}\n]*"([A-Z][A-Za-z]*(?: [A-Za-z0-9]+)*)"/g

/**
 * A placeholder that is code rather than prose: a variable name, a header, an
 * environment key. SCREAMING_SNAKE_CASE is the shape of all of them, and
 * translating one would be an instruction to type the wrong thing.
 */
const CODE_TOKEN = /^[A-Z][A-Z0-9_]*$/

/** Names that are the same word in every language. */
const PROPER_NOUNS = new Set([
  "Kubelet",
  "Kubernetes",
  "Skifity",
  "Docker",
  "Dockerfile",
  "PostgreSQL",
  "MySQL",
  "MariaDB",
  "Redis",
  "GitHub",
  "GitLab",
  "Traefik",
  "WireGuard",
])

function checkLiteralProps() {
  for (const file of walk(srcDir)) {
    if (!file.endsWith(".tsx")) continue
    const relative = file.slice(srcDir.length + 1)
    if (relative.startsWith("components/ui/")) continue
    const contents = readFileSync(file, "utf8")
    for (const match of [
      ...contents.matchAll(LITERAL_PROPS),
      ...contents.matchAll(LITERAL_IN_BRACES),
    ]) {
      if (PROPER_NOUNS.has(match[2])) continue
      if (match[1] === "placeholder" && CODE_TOKEN.test(match[2])) continue
      const line = contents.slice(0, match.index).split("\n").length
      problems.push(
        `${relative}:${line} has a hardcoded ${match[1]}: "${match[2]}" — use t("…") instead`,
      )
    }
  }
}

function* walk(directory) {
  let entries = []
  try {
    entries = readdirSync(directory, { withFileTypes: true })
  } catch {
    return
  }
  for (const entry of entries) {
    const full = join(directory, entry.name)
    if (entry.isDirectory()) yield* walk(full)
    else yield full
  }
}

checkSourceStrings()
checkLiteralProps()
checkInterpolations()

if (problems.length > 0) {
  console.error(`\n${problems.length} translation problem(s):\n`)
  for (const problem of problems.slice(0, 60)) console.error(`  - ${problem}`)
  if (problems.length > 60) console.error(`  ... and ${problems.length - 60} more`)
  console.error(
    `\nEvery user-visible string must exist in all ${Object.keys(PLURAL_CATEGORIES).length} languages.\n`,
  )
  process.exit(1)
}

const keyCount = sourceGroups.size
console.log(
  `i18n: ${keyCount} keys complete in ${available.length} languages (${available.sort().join(", ")}).`,
)
