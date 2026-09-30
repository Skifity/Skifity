/**
 * Packing a folder somebody picked, in the browser, the way `skifity up` packs
 * one on a laptop: the same files left out (see ignore.ts), a gzipped tar, in
 * a fixed order with fixed times so the same folder is the same upload.
 *
 * Nothing leaves the browser here. The .env is read only to offer its values as
 * variables, which the person sees in the form before anything is sent; the
 * file itself is never part of the archive.
 */
import { ignoreFileNames, isSecretsFile, makeFolderFilter } from "@/lib/ignore"

/** The panel's own limits (internal/upload.DefaultLimits), checked here first
 * so a folder with node_modules in it fails in a second rather than after a
 * hundred megabytes have gone up the line. */
export const folderLimits = {
  compressed: 100 * 1024 * 1024,
  unpacked: 1024 * 1024 * 1024,
  entries: 50_000,
}

export type PackedFolder = {
  /** The folder's own name, which is the natural name for the app. */
  name: string
  archive: Blob
  files: string[]
  /** How many files were left out, for "12 files, 3,408 left out". */
  leftOut: number
  unpacked: number
  /** The text of the folder's .env, when it has one with values. */
  dotenv: string
  /** The largest top-level parts, to explain a folder that is too big. */
  largest: { name: string; size: number }[]
}

export class FolderTooLarge extends Error {
  readonly kind: "entries" | "unpacked" | "compressed"
  readonly largest: { name: string; size: number }[]

  constructor(kind: FolderTooLarge["kind"], largest: FolderTooLarge["largest"]) {
    super(kind)
    this.name = "FolderTooLarge"
    this.kind = kind
    this.largest = largest
  }
}

export class FolderEmpty extends Error {
  constructor() {
    super("empty")
    this.name = "FolderEmpty"
  }
}

/** A picked file and the path it has inside the folder. */
type Picked = { file: File; path: string }

/**
 * Packs the files an `<input webkitdirectory>` returned.
 *
 * Their paths start with the folder's own name, which is not part of the app:
 * "my-app/src/index.js" is "src/index.js" in the archive.
 */
export async function packFolder(list: File[]): Promise<PackedFolder> {
  const all: Picked[] = []
  let name = ""
  for (const file of list) {
    const full = file.webkitRelativePath || file.name
    const slash = full.indexOf("/")
    if (slash < 0) continue
    name ||= full.slice(0, slash)
    all.push({ file, path: full.slice(slash + 1) })
  }

  const ignoreFiles = new Map<string, string[]>()
  let dotenv = ""
  for (const { file, path } of all) {
    const slash = path.lastIndexOf("/")
    const dir = slash < 0 ? "" : path.slice(0, slash)
    const base = path.slice(slash + 1)
    const index = ignoreFileNames.indexOf(base)
    if (index >= 0) {
      const texts = ignoreFiles.get(dir) ?? ignoreFileNames.map(() => "")
      texts[index] = await file.text()
      ignoreFiles.set(dir, texts)
    }
    if (
      dir === "" &&
      isSecretsFile(base) &&
      !dotenv &&
      [".env", ".env.local", ".env.production"].includes(base)
    ) {
      dotenv = await file.text()
    }
  }

  const keep = makeFolderFilter(ignoreFiles)
  const kept = all.filter(({ path }) => keep(path)).sort((a, b) => compare(a.path, b.path))

  const topLevel = new Map<string, number>()
  let unpacked = 0
  for (const { file, path } of kept) {
    unpacked += file.size
    const top = path.split("/")[0]
    topLevel.set(top, (topLevel.get(top) ?? 0) + file.size)
  }
  const largest = [...topLevel.entries()]
    .map(([name, size]) => ({ name, size }))
    .sort((a, b) => b.size - a.size)
    .slice(0, 3)

  if (kept.length === 0) throw new FolderEmpty()
  if (kept.length > folderLimits.entries) throw new FolderTooLarge("entries", largest)
  if (unpacked > folderLimits.unpacked) throw new FolderTooLarge("unpacked", largest)

  const tar = await writeTar(kept)
  const archive = await gzip(tar)
  if (archive.size > folderLimits.compressed) throw new FolderTooLarge("compressed", largest)

  return {
    name,
    archive,
    files: kept.map(({ path }) => path),
    leftOut: all.length - kept.length,
    unpacked,
    dotenv,
    largest,
  }
}

/** Byte order, as Go sorts, so the order never depends on the locale. */
function compare(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0
}

const encoder = new TextEncoder()

/**
 * A tar archive, in the ustar format with PAX records for the names ustar
 * cannot hold. Go's archive/tar reads both, and that is the only reader.
 */
async function writeTar(files: Picked[]): Promise<Blob> {
  const parts: BlobPart[] = []
  for (const { file, path } of files) {
    const nameBytes = encoder.encode(path)
    // Anything longer than the 100-byte field, or not plain ASCII, goes in a
    // PAX record: splitting into ustar's prefix and name is where tar writers
    // have historically disagreed with tar readers.
    if (nameBytes.length > 99 || /[^\x20-\x7e]/.test(path)) {
      const record = paxRecord("path", path)
      parts.push(header("PaxHeader", record.length, "x", 0o644), record, padding(record.length))
    }
    parts.push(header(path, file.size, "0", modeFor(path)), file, padding(file.size))
  }
  // Two empty blocks end an archive.
  parts.push(new Uint8Array(1024))
  return new Blob(parts)
}

/**
 * A browser cannot see a file's executable bit, so the scripts a build runs
 * by name are given one: `gradlew`, `mvnw`, anything ending in .sh, and
 * whatever is in a bin/ folder. Everything else is an ordinary file.
 */
function modeFor(path: string): number {
  const base = path.slice(path.lastIndexOf("/") + 1)
  if (base === "gradlew" || base === "mvnw" || base.endsWith(".sh")) return 0o755
  if (path.startsWith("bin/") || path.includes("/bin/")) return 0o755
  return 0o644
}

function paxRecord(key: string, value: string): Uint8Array<ArrayBuffer> {
  // "<length> <key>=<value>\n", where the length counts itself.
  const body = ` ${key}=${value}\n`
  const bodyLength = encoder.encode(body).length
  let length = bodyLength + 1
  while (String(length).length + bodyLength !== length) length++
  return encoder.encode(`${length}${body}`)
}

function header(name: string, size: number, type: string, mode: number): Uint8Array<ArrayBuffer> {
  const block = new Uint8Array(512)
  const put = (value: string, offset: number, width: number) => {
    const bytes = encoder.encode(value)
    block.set(bytes.subarray(0, width), offset)
  }
  const octal = (value: number, width: number) => value.toString(8).padStart(width - 1, "0")

  // A name that did not fit is in the PAX record before this header; what is
  // here is only a readable stand-in for an old tar.
  put(name.length > 99 ? name.slice(0, 99) : name, 0, 100)
  put(octal(mode, 8), 100, 8)
  put(octal(0, 8), 108, 8) // uid
  put(octal(0, 8), 116, 8) // gid
  put(octal(size, 12), 124, 12)
  put(octal(0, 12), 136, 12) // mtime: fixed, so the same folder is the same upload
  put("        ", 148, 8) // checksum, counted as spaces
  put(type, 156, 1)
  put("ustar", 257, 6)
  put("00", 263, 2)

  let sum = 0
  for (const byte of block) sum += byte
  put(octal(sum, 7) + "\0", 148, 8)
  return block
}

function padding(size: number): Uint8Array<ArrayBuffer> {
  const rest = size % 512
  return new Uint8Array(rest === 0 ? 0 : 512 - rest)
}

async function gzip(blob: Blob): Promise<Blob> {
  const stream = blob.stream().pipeThrough(new CompressionStream("gzip"))
  return new Response(stream).blob()
}
