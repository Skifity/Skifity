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
   * The values the server interpolated into the title and the three sentences
   * above, in order, so a translated sentence can carry the same ones.
   */
  args?: { title?: string[]; cause?: string[]; impact?: string[]; fix?: string[] }
  docs_path?: string
  severity: "error" | "warning" | "info"
  retryable: boolean
  context?: Record<string, string>
  at: string
}

/** ApiError carries a Problem and the HTTP status it arrived with. */
export class ApiError extends Error {
  readonly problem: Problem
  /** The HTTP status, or 0 for a request that never reached the panel. */
  readonly status: number
  /** How long the panel asked to be left alone, from Retry-After, in ms. */
  readonly retryAfterMs?: number

  constructor(problem: Problem, status: number, retryAfterMs?: number) {
    super(problem.title)
    this.name = "ApiError"
    this.problem = problem
    this.status = status
    this.retryAfterMs = retryAfterMs
  }

  /** Renders the error as a Markdown block to paste into an AI assistant. */
  toMarkdown(productVersion?: string): string {
    return problemToMarkdown(this.problem, productVersion)
  }
}

/**
 * A Problem as a Markdown block to paste into an AI assistant: the same block
 * the server's errdoc renders, so what is copied from the panel and what the
 * CLI prints read alike.
 *
 * It stays in English whatever language the panel is showing: it is what the
 * server wrote, and what an assistant or an issue tracker reads.
 */
export function problemToMarkdown(problem: Problem, productVersion = "unknown"): string {
  const parts = [
    `## Skifity error: ${problem.title}`,
    "",
    `- **Error code**: \`${problem.code}\``,
    `- **When**: ${problem.at}`,
    `- **Panel version**: ${productVersion}`,
  ]
  if (problem.cause) parts.push("", "**What happened**", "", problem.cause)
  if (problem.impact) parts.push("", "**What it means**", "", problem.impact)
  if (problem.fix) parts.push("", "**Suggested fix**", "", problem.fix)
  if (problem.context && Object.keys(problem.context).length > 0) {
    parts.push("", "**Context**", "", "```")
    for (const [key, value] of Object.entries(problem.context)) {
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

/**
 * The code that means the session is gone.
 *
 * Not every 401 is that: a wrong current password, a wrong password in the
 * re-authentication dialog and a wrong setup token answer 401 too, and taking
 * them for a lost session signed the person out over a typo.
 */
const SIGNED_OUT_CODE = "auth.required"

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

  let response: Response
  try {
    response = await fetch(path, {
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
  } catch (caught) {
    // Somebody stopped waiting. Not a failure, and the caller is listening
    // for exactly this.
    if (caught instanceof DOMException && caught.name === "AbortError") throw caught
    // The request never reached the panel: offline, refused, a name that
    // does not resolve. That is the panel being out of reach, and it is said
    // as such rather than as "something went wrong" in the browser's words.
    throw new ApiError(unreachableProblem(path, caught), 0)
  }

  if (!response.ok) {
    let problem: Problem
    try {
      const payload = (await response.json()) as { error?: Problem }
      problem = payload.error ?? fallbackProblem(response)
    } catch {
      problem = fallbackProblem(response)
    }
    if (response.status === 401 && problem.code === SIGNED_OUT_CODE && !options.allowAnonymous) {
      onUnauthenticated?.()
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
    throw new ApiError(problem, response.status, retryAfter(response, problem))
  }

  if (response.status === 204) return undefined as T
  const text = await response.text()
  if (!text) return undefined as T
  return JSON.parse(text) as T
}

/**
 * How long a 429 asked to wait, in milliseconds. The header is the standard
 * answer and the Problem's context carries the same number. A duration such as
 * "15m0s" — the sign-in lockout's — is not a count of seconds, and is left to
 * the ordinary backoff.
 */
function retryAfter(response: Response, problem: Problem): number | undefined {
  if (response.status !== 429) return undefined
  for (const raw of [response.headers.get("Retry-After"), problem.context?.retry_after]) {
    const seconds = raw?.trim()
    if (seconds && /^\d+$/.test(seconds)) return Number(seconds) * 1000
  }
  return undefined
}

/** A request the browser could not deliver at all. */
function unreachableProblem(path: string, caught: unknown): Problem {
  const reason = caught instanceof Error && caught.message ? caught.message : "no reason given"
  return {
    code: "network.unreachable",
    title: "The panel could not be reached",
    cause: `The browser could not send the request to ${path}: ${reason}.`,
    impact: "The action did not reach the panel, and the page may be showing stale information.",
    fix: "Check your connection. If the panel is restarting, this clears up on its own: try again in a moment.",
    // So the sentence can be said in the reader's language, like a server's.
    args: { cause: [path, reason] },
    severity: "error",
    retryable: true,
    at: new Date().toISOString(),
  }
}

function fallbackProblem(response: Response): Problem {
  return {
    code: "network",
    title: "The panel could not be reached",
    cause: `The request to ${response.url} answered ${response.status}.`,
    impact: "The page may be showing stale information.",
    fix: "Check your connection and reload. If the panel is restarting, this clears up on its own.",
    // So the sentence can be said in the reader's language, like a
    // server's: a 502 while the panel restarts was English in all five.
    args: { cause: [response.url, String(response.status)] },
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
