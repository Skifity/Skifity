/**
 * Copies text to the clipboard, and says whether it worked.
 *
 * navigator.clipboard exists only in a secure context, and a fresh install
 * answers on plain http at its sslip.io address — so on the very first visit
 * every copy button threw before it could even fail politely. Where the
 * clipboard API is missing or refuses, the text is copied the way browsers
 * have always allowed: selected in a hidden field, then copied.
 */
export async function copyText(text: string): Promise<boolean> {
  if (window.isSecureContext && navigator.clipboard) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // Refused, for instance because the document is not focused. The older
      // way below is still worth a try.
    }
  }
  return copyBySelecting(text)
}

function copyBySelecting(text: string): boolean {
  const active = document.activeElement instanceof HTMLElement ? document.activeElement : null
  // Inside an open dialog if there is one: a dialog keeps focus inside itself,
  // and a field that cannot take focus cannot be selected.
  const host = active?.closest<HTMLElement>("[role=dialog], [role=alertdialog]") ?? document.body
  const field = document.createElement("textarea")
  field.value = text
  field.setAttribute("readonly", "")
  field.setAttribute("aria-hidden", "true")
  field.style.position = "fixed"
  field.style.top = "0"
  field.style.left = "-9999px"
  field.style.opacity = "0"
  host.appendChild(field)
  field.focus({ preventScroll: true })
  field.select()
  let copied: boolean
  try {
    // Deprecated, and still the only copy a page served over http can do.
    copied = document.execCommand("copy")
  } catch {
    copied = false
  }
  field.remove()
  active?.focus({ preventScroll: true })
  return copied
}
