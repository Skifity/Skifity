/**
 * The panel's API client.
 *
 * Every call goes through here so that three things happen in one place: the
 * CSRF token is attached, a failure is turned into a Problem the UI can explain
 * properly, and a session that has expired sends the user to the sign-in page
 * rather than showing a wall of 401s.
 */

/** Problem is the panel's error shape: what happened, what it means, how to fix it. */
export type Problem = {
  code: string
  title: string
  cause?: string
  impact?: string
  fix?: string
  /**
   * The values the server interpolated into the three sentences above, in
   * order, so a translated sentence can carry the same ones.
   */
  args?: { cause?: string[]; impact?: string[]; fix?: string[] }
  docs_path?: string
  severity: "error" | "warning" | "info"
  retryable: boolean
  context?: Record<string, string>
  at: string
}

/** ApiError carries a Problem and the HTTP status it arrived with. */
export class ApiError extends Error {
  readonly problem: Problem
  readonly status: number

  constructor(problem: Problem, status: number) {
    super(problem.title)
    this.name = "ApiError"
    this.problem = problem
    this.status = status
  }

  /** Renders the error as a Markdown block to paste into an AI assistant. */
  toMarkdown(productVersion = "unknown"): string {
    const parts = [
      `## Skifity error: ${this.problem.title}`,
      "",
      `- **Error code**: \`${this.problem.code}\``,
      `- **When**: ${this.problem.at}`,
      `- **Panel version**: ${productVersion}`,
    ]
    if (this.problem.cause) parts.push("", "**What happened**", "", this.problem.cause)
    if (this.problem.impact) parts.push("", "**What it means**", "", this.problem.impact)
    if (this.problem.fix) parts.push("", "**Suggested fix**", "", this.problem.fix)
    if (this.problem.context && Object.keys(this.problem.context).length > 0) {
      parts.push("", "**Context**", "", "```")
      for (const [key, value] of Object.entries(this.problem.context)) {
        parts.push(`${key}: ${value}`)
      }
      parts.push("```")
    }
    parts.push(
      "",
      "**What I need**",
      "",
      "Explain what is wrong and give me the exact steps to fix it.",
    )
    return parts.join("\n")
  }
}

/**
 * Reads the CSRF cookie the panel sets alongside the session.
 *
 * Two names, because the panel sets one of them: on HTTPS it uses the __Host-
 * prefix, which a browser refuses to store if a Domain is named, so no page on
 * a sibling subdomain can write it. Over plain http the prefix is not allowed
 * at all and the bare name is what there is. The prefixed one wins when both
 * are present.
 */
function csrfToken(): string {
  for (const name of ["__Host-skifity_csrf", "skifity_csrf"]) {
    const match = document.cookie.match(
      new RegExp(`(?:^|;\\s*)${name.replace("-", "\\-")}=([^;]*)`),
    )
    if (match) return decodeURIComponent(match[1])
  }
  return ""
}

let onUnauthenticated: (() => void) | null = null

/** Registers what to do when the session turns out to be gone. */
export function setUnauthenticatedHandler(handler: () => void) {
  onUnauthenticated = handler
}

let onReauthRequired: (() => Promise<boolean>) | null = null

/**
 * Registers what to do when the panel asks the person to prove who they are
 * again. Resolving true means they did, and the request is sent once more.
 */
export function setReauthHandler(handler: () => Promise<boolean>) {
  onReauthRequired = handler
}

/** The code the panel answers with when an action needs the password again. */
const REAUTH_CODE = "auth.reauth_required"

type RequestOptions = {
  method?: string
  body?: unknown
  signal?: AbortSignal
  /** Suppresses the redirect to sign-in, used by the calls that probe auth. */
  allowAnonymous?: boolean
  /** Set once a step-up has already been asked for, so it is never a loop. */
  steppedUp?: boolean
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? "GET"
  const headers: Record<string, string> = { Accept: "application/json" }

  const raw = options.body instanceof Blob
  if (options.body !== undefined) {
    headers["Content-Type"] = raw ? "application/gzip" : "application/json"
  }
  if (method !== "GET" && method !== "HEAD") {
    // Double-submit: the cookie is readable by the frontend on purpose so it
    // can be echoed here, which a cross-site request cannot do.
    headers["X-Skifity-CSRF"] = csrfToken()
  }

  const response = await fetch(path, {
    method,
    headers,
    credentials: "same-origin",
    signal: options.signal,
    body:
      options.body === undefined
        ? undefined
        : raw
          ? (options.body as Blob)
          : JSON.stringify(options.body),
  })

  if (response.status === 401 && !options.allowAnonymous) {
    onUnauthenticated?.()
  }

  if (!response.ok) {
    let problem: Problem
    try {
      const payload = (await response.json()) as { error?: Problem }
      problem = payload.error ?? fallbackProblem(response)
    } catch {
      problem = fallbackProblem(response)
    }
    // Turning two-factor off, reading the recovery codes and minting a token
    // each need the password again. The panel says so with a code rather than
    // by failing, so the dialog opens here and the request is repeated — the
    // caller never learns it happened, and nothing has to remember to ask.
    if (response.status === 403 && problem.code === REAUTH_CODE && !options.steppedUp) {
      if (await onReauthRequired?.()) {
        return request<T>(path, { ...options, steppedUp: true })
      }
    }
    throw new ApiError(problem, response.status)
  }

  if (response.status === 204) return undefined as T
  const text = await response.text()
  if (!text) return undefined as T
  return JSON.parse(text) as T
}

function fallbackProblem(response: Response): Problem {
  return {
    code: "network",
    title: "The panel could not be reached",
    cause: `The request to ${response.url} answered ${response.status}.`,
    impact: "The page may be showing stale information.",
    fix: "Check your connection and reload. If the panel is restarting, this clears up on its own.",
    severity: "error",
    retryable: true,
    at: new Date().toISOString(),
  }
}

export const api = {
  get: <T>(path: string, signal?: AbortSignal) => request<T>(path, { signal }),
  post: <T>(path: string, body?: unknown) => request<T>(path, { method: "POST", body }),
  put: <T>(path: string, body?: unknown) => request<T>(path, { method: "PUT", body }),
  patch: <T>(path: string, body?: unknown) => request<T>(path, { method: "PATCH", body }),
  delete: <T>(path: string) => request<T>(path, { method: "DELETE" }),
  /** Sends a packed folder as it is: the one request whose body is not JSON. */
  upload: <T>(path: string, archive: Blob, method: "POST" | "PUT" = "PUT") =>
    request<T>(path, { method, body: archive }),
  /** Used by the sign-in and setup screens, where a 401 is an expected answer. */
  anonymous: <T>(path: string, options: RequestOptions = {}) =>
    request<T>(path, { ...options, allowAnonymous: true }),
}

/** A list response, which every list endpoint returns. */
export type List<T> = { items: T[]; total: number }

/**
 * Subscribes to the panel's event stream.
 *
 * Returns a function that closes the stream. The browser reconnects on its own
 * and resends the last event id, so a dropped connection resumes rather than
 * losing the middle of a build log.
 */
export function subscribe(
  topics: string[],
  handlers: Record<string, (data: unknown) => void>,
  onError?: () => void,
): () => void {
  if (topics.length === 0) return () => {}

  const source = new EventSource(`/api/events?topics=${encodeURIComponent(topics.join(","))}`)

  for (const [event, handler] of Object.entries(handlers)) {
    source.addEventListener(event, (message) => {
      try {
        handler(JSON.parse((message as MessageEvent).data))
      } catch {
        // A frame we cannot parse is not worth tearing the stream down for.
      }
    })
  }
  if (onError) source.addEventListener("error", onError)

  return () => source.close()
}
