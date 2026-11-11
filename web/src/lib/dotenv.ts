/**
 * Reads the text of a .env file into names and values.
 *
 * One reader for every place a person pastes one — the variables editor and
 * the new-app form — so a file that works in one works in the other.
 *
 * It accepts what .env files actually contain: comments, blank lines, an
 * `export ` in front for files that are also sourced by a shell, and values in
 * single or double quotes. A line that is not NAME=value is skipped rather than
 * failing the paste, because a real .env file often has a stray line in it.
 *
 * It deliberately says nothing about which values are secrets. That is decided
 * by the panel, with the same rule its log redaction uses, so that a pasted
 * API key is never stored as an ordinary value because this file guessed wrong.
 */
export function parseDotEnv(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of text.split(/\r?\n/)) {
    let trimmed = line.trim()
    if (!trimmed || trimmed.startsWith("#")) continue
    if (trimmed.startsWith("export ")) trimmed = trimmed.slice("export ".length).trim()
    const index = trimmed.indexOf("=")
    if (index < 1) continue
    const key = trimmed.slice(0, index).trim()
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) continue
    const raw = trimmed.slice(index + 1).trim()
    out[key] = raw.replace(/^(["'])(.*)\1$/, "$2")
  }
  return out
}

/**
 * One KEY=value line that parseDotEnv reads back as exactly that value, or
 * null for a value no line can hold: one over several lines.
 *
 * Quoted only where the reader would otherwise change it — spaces at either
 * end, which it trims, and a value that is itself wrapped in quotes, which it
 * strips — so the lines read the way somebody would have written them.
 */
export function dotEnvLine(key: string, value: string): string | null {
  // The two separators too: the reader's quote stripping does not reach
  // across them.
  if (/[\r\n\u2028\u2029]/.test(value)) return null
  const altered = value !== value.trim() || /^(["']).*\1$/.test(value)
  return `${key}=${altered ? `"${value}"` : value}`
}
