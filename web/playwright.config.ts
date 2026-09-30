import { defineConfig, devices } from "@playwright/test"

/**
 * The user interface smoke test.
 *
 * It runs against the real binary serving the real embedded frontend, not a dev
 * server: what is tested is what ships. The panel is started with a throwaway
 * database in a temporary directory, so a run leaves nothing behind and can
 * never touch a real install.
 */
const PORT = Number(process.env.SKIFITY_E2E_PORT ?? 18099)
const BASE_URL = `http://127.0.0.1:${PORT}`

export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  // One worker: the panel has one database and first-run setup happens once.
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  timeout: 30_000,
  expect: { timeout: 10_000 },

  use: {
    baseURL: BASE_URL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },

  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        // Use whatever Chromium is already on the machine when one is there.
        // CI images and sandboxes often ship a browser that does not match the
        // build this Playwright version would download, and downloading a
        // second one to run four tests is not worth the minutes.
        launchOptions: process.env.CHROMIUM_PATH
          ? { executablePath: process.env.CHROMIUM_PATH }
          : {},
      },
    },
  ],

  webServer: {
    command: "node scripts/e2e-server.mjs",
    url: `${BASE_URL}/api/health`,
    reuseExistingServer: false,
    stdout: "pipe",
    stderr: "pipe",
    timeout: 60_000,
    env: { SKIFITY_E2E_PORT: String(PORT) },
  },
})
