import type { TFunction } from "i18next"

/**
 * A sentence the server wrote in English, shown in the reader's language.
 *
 * The server has one language: the API, the CLI and an assistant all read its
 * English, and it stays the fallback. The panel has five and looks the same
 * sentence up by a key the server sends alongside it — an error's code, a
 * scaling finding's code, a preflight problem's code, a step's message key.
 *
 * The values are positional, because the Go call sites are: the locale writes
 * {{0}} and {{1}} where the format string wrote %s and %d, in the same order.
 * Nothing here invents a translation — a key with no entry falls back to the
 * English the server sent, which is what every one of these did before the
 * catalogue existed.
 */
export function translated(
  t: TFunction,
  key: string,
  english: string | undefined,
  args?: string[],
): string {
  if (!english) return ""
  return t(key, { defaultValue: english, ...positional(args) })
}

/** Turns ["a", "b"] into { 0: "a", 1: "b" }, which is what i18next interpolates. */
export function positional(args?: string[]): Record<number, string> {
  return Object.fromEntries((args ?? []).map((value, index) => [index, value]))
}
