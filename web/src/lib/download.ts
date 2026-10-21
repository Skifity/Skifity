/**
 * Hands the browser a file to save, made here rather than fetched: a recovery
 * key, recovery codes, an export.
 */
export function downloadBlob(filename: string, blob: Blob) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement("a")
  link.href = url
  link.download = filename
  link.click()
  // On the next turn rather than at once: a browser may start reading the
  // object only after the click has returned.
  window.setTimeout(() => URL.revokeObjectURL(url), 0)
}

/** downloadBlob for plain text. */
export function downloadText(filename: string, text: string) {
  downloadBlob(filename, new Blob([text], { type: "text/plain;charset=utf-8" }))
}
