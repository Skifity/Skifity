import type { TFunction } from "i18next"

/**
 * A recorded failure's headline in the reader's language, looked up by its
 * code.
 *
 * A deployment or a scan keeps the code and the server's English, not the
 * values its title was built with, so a title that needs them has nothing to
 * fill them with: looked up as it was, "That run does not exist" read "That
 * {{0}} does not exist". Such a title is shown in the English that was stored.
 */
export function recordedTitle(t: TFunction, code: string, english: string): string {
  const said = t(`errors.catalogue.${code.replaceAll(".", "_")}.title`, { defaultValue: english })
  return said.includes("{{") ? english : said
}
