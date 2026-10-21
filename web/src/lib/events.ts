import { queryClient } from "@/lib/query"

/**
 * The panel's live updates: one event stream per browser tab, shared by every
 * page and card that wants them.
 *
 * Each subscriber used to open a stream of its own. Over the plain-HTTP
 * address a fresh install has, a browser allows six connections to a host,
 * and an app's page alone held three of them open, so a few panel tabs left
 * every ordinary request queueing behind them. Now the topics every
 * subscriber asks for travel on one stream, and each event says which topic it
 * is on (the panel's ?multiplex=true), so two topics carrying an event of the
 * same name cannot be told apart wrongly.
 *
 * The stream also comes back by itself. A browser retries a connection that
 * dropped, but gives up for good on one that is answered with an error — the
 * 502 a proxy gives while the panel restarts for an upgrade — and every open
 * page then silently stopped being live. Here a stream that gave up is opened
 * again with a backoff, from the last event it saw, and once it is back every
 * query is refetched, because a panel that restarted remembers nothing it
 * would have replayed.
 */

type Handlers = Record<string, (data: unknown) => void>

type Subscriber = {
  topics: Set<string>
  handlers: Handlers
  onError?: () => void
}

type Connection = {
  source: EventSource
  topics: Set<string>
}

/** One event as a multiplexed stream sends it. */
type Frame = { topic?: string; type: string; data: unknown }

/** How long subscribers have to settle before the stream follows them. */
const SETTLE_MS = 50
/** How many event ids are remembered to drop one delivered twice. */
const REMEMBERED_IDS = 256

const subscribers = new Set<Subscriber>()
/** Topics the panel will not send to this session; asking again changes nothing. */
const refused = new Set<string>()
/** Ids recently delivered, so two connections overlapping deliver each event once. */
const recent = new Set<string>()

let current: Connection | null = null
/** A connection with a different set of topics, taking over from current. */
let next: Connection | null = null
let lastEventId = ""
/** The stream failed, and has not been open since. */
let broken = false
let failures = 0
let settleTimer: number | undefined
let retryTimer: number | undefined
let listening = false

/**
 * Subscribes to the panel's event stream.
 *
 * Returns a function that ends the subscription. onError is called if the
 * panel refuses one of these topics to this session.
 */
export function subscribe(topics: string[], handlers: Handlers, onError?: () => void): () => void {
  const wanted = topics.filter(Boolean)
  if (wanted.length === 0) return () => {}

  const subscriber: Subscriber = { topics: new Set(wanted), handlers, onError }
  subscribers.add(subscriber)
  settle()
  return () => {
    subscribers.delete(subscriber)
    settle()
  }
}

/**
 * Closes the stream and forgets where it was, for signing out: nothing that
 * arrives afterwards belongs to the next person to sign in.
 */
export function closeEventStreams() {
  window.clearTimeout(settleTimer)
  window.clearTimeout(retryTimer)
  settleTimer = retryTimer = undefined
  next?.source.close()
  current?.source.close()
  next = current = null
  lastEventId = ""
  broken = false
  failures = 0
  refused.clear()
  recent.clear()
}

/**
 * Follows the subscribers after they have settled: a page that is replaced
 * unsubscribes and the next one subscribes in the same moment, and that is
 * one change of topics rather than two.
 */
function settle() {
  if (settleTimer !== undefined) return
  settleTimer = window.setTimeout(() => {
    settleTimer = undefined
    sync()
  }, SETTLE_MS)
}

function sync() {
  if (!listening) {
    listening = true
    // Coming back online is a better moment to try again than the backoff.
    window.addEventListener("online", () => {
      if (retryTimer === undefined) return
      window.clearTimeout(retryTimer)
      retryTimer = undefined
      sync()
    })
  }
  const wanted = new Set<string>()
  for (const subscriber of subscribers) {
    for (const topic of subscriber.topics) wanted.add(topic)
  }
  // A refusal is kept only while somebody still asks for that topic, so the
  // same page opened again later, perhaps with another role, asks afresh.
  for (const topic of refused) {
    if (!wanted.has(topic)) refused.delete(topic)
  }
  for (const topic of refused) wanted.delete(topic)

  if (wanted.size === 0) {
    // Nobody is listening, so there is nothing to catch up on either: whoever
    // subscribes next has just read what they show, and starts from now.
    window.clearTimeout(retryTimer)
    retryTimer = undefined
    next?.source.close()
    current?.source.close()
    next = current = null
    broken = false
    failures = 0
    return
  }

  // A failed stream is retried on its own timer, not sooner because the
  // person moved to another page.
  if (retryTimer !== undefined) return

  const target = next ?? current
  if (target && sameTopics(target.topics, wanted)) return

  if (current && current.source.readyState === EventSource.OPEN) {
    // Hand over without a gap: the stream that is open keeps delivering until
    // the new one is, and an event both deliver is passed on once. The new one
    // replays nothing, so a card that has just subscribed is not sent what
    // happened before it asked, exactly as with a stream of its own.
    next?.source.close()
    next = connect(wanted, false)
    return
  }

  // Nothing is flowing. A stream that broke picks up from the last event it
  // saw; one that was simply not needed for a while starts from now.
  next?.source.close()
  current?.source.close()
  next = null
  current = connect(wanted, broken)
}

function sameTopics(carried: Set<string>, wanted: Set<string>): boolean {
  let size = 0
  for (const topic of carried) {
    // A topic refused on this connection is not carried by it either.
    if (refused.has(topic)) continue
    if (!wanted.has(topic)) return false
    size++
  }
  return size === wanted.size
}

function connect(topics: Set<string>, resume: boolean): Connection {
  const query = new URLSearchParams({ multiplex: "true", topics: [...topics].sort().join(",") })
  if (resume && lastEventId) query.set("since", lastEventId)
  const connection: Connection = { source: new EventSource(`/api/events?${query}`), topics }
  connection.source.onopen = () => opened(connection)
  connection.source.onerror = () => failed(connection)
  connection.source.onmessage = (message) => received(message)
  return connection
}

function opened(connection: Connection) {
  if (connection === next) {
    current?.source.close()
    current = next
    next = null
  }
  failures = 0
  if (broken) {
    broken = false
    resync()
  }
}

function failed(connection: Connection) {
  if (connection.source.readyState === EventSource.CONNECTING) {
    // The browser is trying again by itself, with the last id it saw. Once it
    // is back, what it missed is read again: a panel that restarted has
    // nothing to replay.
    if (connection === current) broken = true
    return
  }
  // The browser has given up: an answer that was not a stream, such as a
  // proxy's 502 while the panel restarts, or a 429.
  connection.source.close()
  if (connection === next) {
    // The connection that is open keeps working; the change is tried again.
    next = null
  } else if (connection === current) {
    current = null
    broken = true
  } else {
    return
  }
  failures++
  const delay = Math.min(30_000, 1000 * 2 ** (failures - 1)) + Math.random() * 1000
  retryTimer = window.setTimeout(() => {
    retryTimer = undefined
    sync()
  }, delay)
}

function received(message: MessageEvent) {
  let frame: Frame
  try {
    frame = JSON.parse(message.data) as Frame
  } catch {
    // A frame that cannot be read is not worth tearing the stream down for.
    return
  }

  if (frame.type === "refused") {
    refuse(frame.topic)
    return
  }
  // The panel dropped events because this tab fell behind. A page that
  // missed one cannot know what it missed, so the only honest answer is to
  // throw away what it has and ask again.
  if (frame.type === "desync") {
    resync()
    return
  }

  const id = message.lastEventId
  if (id) {
    if (recent.has(id)) return
    recent.add(id)
    if (recent.size > REMEMBERED_IDS) recent.delete(recent.values().next().value as string)
    lastEventId = id
  }

  for (const subscriber of subscribers) {
    if (!frame.topic || !subscriber.topics.has(frame.topic)) continue
    const handler = subscriber.handlers[frame.type]
    if (!handler) continue
    try {
      handler(frame.data)
    } catch (error) {
      // One card's handler failing must not keep the event from the others.
      console.error(`A handler for "${frame.type}" on ${frame.topic} failed.`, error)
    }
  }
}

function refuse(topic: string | undefined) {
  if (!topic || refused.has(topic)) return
  refused.add(topic)
  // Not silent: whoever asked for it is told, and so is the console, since a
  // page asking for a topic its reader may not see is a bug in the page.
  console.warn(`The panel refused live updates on ${topic}.`)
  for (const subscriber of subscribers) {
    if (subscriber.topics.has(topic)) subscriber.onError?.()
  }
}

/** Reads everything again, after events may have been missed. */
function resync() {
  // A panel that restarted counts its events from one again, so ids seen
  // before tell nothing about the ones that follow.
  recent.clear()
  void queryClient.invalidateQueries()
}
