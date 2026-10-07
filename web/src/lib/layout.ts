/**
 * The header of a card that has a button beside its title.
 *
 * It was `flex flex-row` at every width, in nine cards. On a phone the buttons
 * (a documentation link and "Upload a certificate", in a language whose words
 * are longer than English's) took most of the row, the title and the paragraph
 * under it were left a column a few words wide, one word to a line, and the
 * button ran off the right-hand edge of the card. It stacks on a narrow screen
 * and sits beside the text from the `sm` breakpoint up.
 *
 * `[&>:first-child]:min-w-0` is what lets the text give way instead of pushing
 * the buttons out when the row is too narrow for both.
 */
export const cardHeaderWithActions =
  "flex flex-col items-start gap-3 space-y-0 sm:flex-row sm:justify-between sm:gap-4 [&>:first-child]:min-w-0"

/**
 * A row of buttons beside a title: it wraps rather than running past its card,
 * and does not shrink once there is room for it beside the text.
 */
export const cardActions = "flex max-w-full flex-wrap gap-2 sm:shrink-0"
