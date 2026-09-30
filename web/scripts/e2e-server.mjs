#!/usr/bin/env node
/**
 * Starts the panel for the user interface test, with a throwaway database.
 *
 * Playwright's webServer needs one command, and this needs a temporary
 * directory that is cleaned up afterwards, so it is a script rather than a
 * shell one-liner. The setup token is written where the test can read it.
 */
import { spawn } from "node:child_process"
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"

const port = process.env.SKIFITY_E2E_PORT ?? "18099"
const root = resolve(import.meta.dirname, "..", "..")
const binary = process.env.SKIFITY_BINARY ?? join(root, "bin", "skifity")

if (!existsSync(binary)) {
  console.error(`\n${binary} does not exist. Run: make build\n`)
  process.exit(1)
}

const workdir = mkdtempSync(join(tmpdir(), "skifity-e2e-"))
// The test reads this to complete first-run setup.
process.stdout.write(`e2e workdir: ${workdir}\n`)

const panel = spawn(binary, ["server"], {
  stdio: "inherit",
  env: {
    ...process.env,
    SKIFITY_LISTEN: `127.0.0.1:${port}`,
    SKIFITY_DATABASE_PATH: join(workdir, "panel.db"),
    SKIFITY_MASTER_KEY_PATH: join(workdir, "master.key"),
    SKIFITY_SETUP_TOKEN_PATH: join(workdir, "setup-token"),
    SKIFITY_PUBLIC_URL: `http://127.0.0.1:${port}`,
    SKIFITY_LOG_FORMAT: "text",
    // Not dev mode: the test must exercise the frontend embedded in the
    // binary, which is what actually ships. Chrome treats http://127.0.0.1 as
    // a trustworthy origin, so Secure cookies work there without relaxing them.
    SKIFITY_E2E_WORKDIR: workdir,
  },
})

// The test finds the setup token through this file, whose path is fixed so the
// config and the spec agree without passing anything between them.
writeFileSync(join(root, "web", ".e2e-workdir"), workdir)

const cleanup = () => {
  panel.kill("SIGTERM")
  try {
    rmSync(workdir, { recursive: true, force: true })
  } catch {
    // A leftover temporary directory is not worth failing over.
  }
}
process.on("SIGTERM", () => {
  cleanup()
  process.exit(0)
})
process.on("SIGINT", () => {
  cleanup()
  process.exit(0)
})
panel.on("exit", (code) => {
  cleanup()
  process.exit(code ?? 0)
})
