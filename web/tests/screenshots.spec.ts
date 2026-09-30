import { test, expect, type Page } from "@playwright/test"
import { readFileSync, mkdirSync } from "node:fs"
import { join, resolve } from "node:path"

/**
 * Every screen in the panel, captured from the real binary.
 *
 * A test rather than a script so it runs against the same binary as everything
 * else, and so a screenshot can never show a screen that no longer exists: if a
 * page is renamed or a tab disappears, this fails rather than quietly keeping
 * the old picture.
 *
 *   SKIFITY_SCREENSHOTS=1 npm run test:e2e -- screenshots
 *
 * Skipped by default, because a documentation image changing on every run makes
 * for a noisy diff.
 *
 * The panel is seeded first. An empty install photographs badly and, worse,
 * dishonestly: the empty states are already covered, and what somebody wants to
 * see before they install is what it looks like with something in it. Nothing
 * here needs a cluster — the records exist, the runtime status says so, and a
 * screenshot that shows "no instances" is the truth about a panel with no
 * servers attached.
 */

const EMAIL = "owner@example.test"
const PASSWORD = "a reasonable passphrase"
const OUT = resolve(import.meta.dirname, "..", "..", "docs", "images")

test.skip(!process.env.SKIFITY_SCREENSHOTS, "set SKIFITY_SCREENSHOTS=1 to capture")
test.describe.configure({ mode: "serial" })
test.use({ viewport: { width: 1440, height: 900 } })
// One test that visits about thirty pages, two of them lazy chunks, with a
// settle pause before each picture. The suite's 30 seconds is for a check, not
// for a tour.
test.setTimeout(10 * 60 * 1000)

function setupToken(): string {
  const workdir = readFileSync(resolve(import.meta.dirname, "..", ".e2e-workdir"), "utf8").trim()
  return readFileSync(join(workdir, "setup-token"), "utf8").trim()
}

/** shoot waits for the page to settle before it takes the picture. */
async function shoot(page: Page, name: string) {
  // A tab highlight is a CSS transition, and a screenshot taken the instant the
  // content mounts catches it half way with two tabs looking selected.
  await page.waitForTimeout(500)
  await page.screenshot({ path: join(OUT, `${name}.png`) })
}

test("capture", async ({ page, request }) => {
  mkdirSync(OUT, { recursive: true })

  // --- first run ----------------------------------------------------------

  await page.goto("/")
  await expect(page.getByLabel(/setup token/i)).toBeVisible()
  await shoot(page, "setup")

  await page.getByLabel(/setup token/i).fill(setupToken())
  await page.getByLabel(/^email$/i).fill(EMAIL)
  await page.getByLabel(/your name/i).fill("Owner")
  await page.getByLabel(/^password$/i).fill(PASSWORD)
  await page.getByLabel(/team name/i).fill("Acme")
  await page.getByRole("button", { name: /create account/i }).click()

  // The recovery key, blurred: a screenshot of a real key would be a bad
  // example even though this one is throwaway.
  await expect(page.getByText(/^SKIFITY-RECOVERY-v1-/)).toBeVisible()
  const blur = await page.addStyleTag({ content: "pre, code { filter: blur(6px) !important; }" })
  await shoot(page, "recovery-key")
  // Removed rather than reloaded: the key is shown once, and a reload would
  // lose the screen this test still has to click through.
  await blur.evaluate((element) => element.remove())

  await page.getByRole("checkbox").check()
  await page.getByRole("button", { name: /done|continue|finish/i }).click()
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible()

  // --- seed, through the API ---------------------------------------------
  //
  // Through the API rather than by clicking: this test is about what the pages
  // look like, and driving eight forms to get there would fail for reasons that
  // have nothing to do with a screenshot.

  const cookies = await page.context().cookies()
  const csrf = cookies.find((c) => c.name === "skifity_csrf")?.value ?? ""
  const headers = {
    "Content-Type": "application/json",
    "X-Skifity-CSRF": csrf,
    Cookie: cookies.map((c) => `${c.name}=${c.value}`).join("; "),
  }
  const post = async (path: string, body: unknown) => {
    const response = await request.post(path, { headers, data: body })
    expect(
      response.ok(),
      `POST ${path}: ${response.status()} ${await response.text()}`,
    ).toBeTruthy()
    return response.json()
  }
  const get = async (path: string) => (await request.get(path, { headers })).json()

  // Some of what makes a good screenshot needs a cluster: a database is
  // provisioned by an operator, a domain waits on a certificate. Without one
  // those calls are refused, correctly, and the page shows its empty state.
  // That is a true picture of a panel with no servers attached, so it is
  // photographed rather than faked — and on a machine that does have a cluster
  // this same test fills them in.
  const optional = async (method: "post" | "put", path: string, body: unknown) => {
    const response = await request[method](path, { headers, data: body })
    if (!response.ok()) {
      console.log(`  (skipped ${path}: ${response.status()}, which needs a cluster)`)
    }
    return response.ok()
  }

  const teams = await get("/api/teams")
  const teamID = teams.items[0].id

  const project = await post(`/api/teams/${teamID}/projects`, {
    name: "Storefront",
    description: "The shop, its API and the things they need.",
  })
  const environments = await get(`/api/projects/${project.id}/environments`)
  const envID = environments.items[0].id

  const web = await post(`/api/environments/${envID}/apps`, {
    name: "storefront",
    source_type: "git",
    repo_url: "https://github.com/vercel/next.js",
    branch: "canary",
    builder: "railpack",
    port: 3000,
    health_path: "/api/health",
  })
  await post(`/api/environments/${envID}/apps`, {
    name: "api",
    source_type: "image",
    image: "ghcr.io/example/api:1.4.2",
    port: 8080,
    health_path: "/healthz",
  })
  await post(`/api/environments/${envID}/apps`, {
    name: "worker",
    source_type: "image",
    image: "ghcr.io/example/api:1.4.2",
  })
  await optional("post", `/api/environments/${envID}/databases`, {
    name: "storefront-db",
    engine: "postgres",
    storage_gb: 10,
  })
  await optional("post", `/api/apps/${web.id}/domains`, { hostname: "shop.example.com" })

  // Variables and scaling are the panel's own records and need nothing outside
  // it, so these are not optional: if they fail, the screenshot would be of a
  // page that is wrong rather than of a page that is empty.
  await request.put(`/api/apps/${web.id}/variables`, {
    headers,
    data: { key: "STRIPE_SECRET_KEY", value: "sk_test_not_a_real_key", secret: true },
  })
  await request.put(`/api/apps/${web.id}/variables`, {
    headers,
    data: { key: "NEXT_PUBLIC_SITE_NAME", value: "Storefront" },
  })
  await optional("put", `/api/apps/${web.id}/scaling`, {
    autoscale: true,
    min_replicas: 2,
    max_replicas: 6,
    cpu_target: 70,
  })

  // --- every page ---------------------------------------------------------

  // Templates and Settings are lazy chunks behind a Suspense fallback, and the
  // catalogue is nearly three hundred entries, so the heading arrives after the
  // navigation rather than with it.
  const go = async (path: string, heading: RegExp | string) => {
    await page.goto(path)
    await expect(page.getByRole("heading", { name: heading, level: 1 })).toBeVisible({
      timeout: 30_000,
    })
  }

  await go("/", "Overview")
  await shoot(page, "overview")

  // Another language, to show that five is not a token gesture. Taken on the
  // overview because that is the screen with the most words on it.
  await page.locator('[data-slot="language-switcher"]').click()
  await page.getByRole("menuitem", { name: "Русский" }).click()
  await page.waitForTimeout(400)
  await shoot(page, "overview-russian")
  await page.locator('[data-slot="language-switcher"]').click()
  await page.getByRole("menuitem", { name: "Bahasa Indonesia" }).click()
  await page.waitForTimeout(400)
  await shoot(page, "overview-indonesian")
  await page.locator('[data-slot="language-switcher"]').click()
  await page.getByRole("menuitem", { name: "English" }).click()
  await page.waitForTimeout(400)

  // Dark, because that is what most people will see it in.
  await page.locator('[data-slot="theme-toggle"]').click()
  await page.getByRole("menuitem", { name: /dark/i }).click()
  await expect(page.locator("html")).toHaveClass(/dark/)
  await shoot(page, "overview-dark")
  await page.locator('[data-slot="theme-toggle"]').click()
  await page.getByRole("menuitem", { name: /light/i }).click()
  await expect(page.locator("html")).not.toHaveClass(/dark/)

  // Settings in another language, because this is the densest page of words in
  // the panel and the one where anything still English is obvious.
  await page.goto("/settings")
  await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible({
    timeout: 30_000,
  })
  await page.locator('[data-slot="language-switcher"]').click()
  await page.getByRole("menuitem", { name: "Bahasa Indonesia" }).click()
  await page.waitForTimeout(600)
  await shoot(page, "settings-indonesian")
  await page.locator('[data-slot="language-switcher"]').click()
  await page.getByRole("menuitem", { name: "English" }).click()
  await page.waitForTimeout(400)

  await go("/projects", "Projects")
  await shoot(page, "projects")

  await page.goto(`/projects/${project.id}`)
  await expect(page.getByText("Storefront").first()).toBeVisible()
  await shoot(page, "project")

  await go("/databases", /databases/i)
  await shoot(page, "databases")

  await go("/servers", /servers/i)
  await shoot(page, "servers")

  await page.goto("/servers/new")
  await expect(page.getByLabel(/IP address or hostname/i)).toBeVisible()
  await shoot(page, "add-server")

  await go("/templates", /templates/i)
  await shoot(page, "templates")

  await go("/plugins", /plugins/i)
  await shoot(page, "plugins")

  // The store, which on a panel that cannot reach one says so. That is the
  // honest picture until something is published at plugins.skifity.com, and a
  // screenshot of it is better than none.
  await page.getByRole("tab", { name: /store/i }).click()
  await shoot(page, "plugins-store")

  await go("/activity", /activity/i)
  await shoot(page, "activity")

  await go("/account", /account/i)
  await shoot(page, "account")

  await page.goto(`/environments/${envID}/apps/new`)
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible()
  await shoot(page, "new-app")

  // --- the app, tab by tab -----------------------------------------------

  for (const tab of [
    "overview",
    "deployments",
    "logs",
    "console",
    "variables",
    "domains",
    "scaling",
    "storage",
    "firewall",
    "settings",
    "advanced",
  ]) {
    await page.goto(tab === "overview" ? `/apps/${web.id}` : `/apps/${web.id}?tab=${tab}`)
    await expect(page.getByRole("tab", { selected: true })).toBeVisible()
    await shoot(page, `app-${tab}`)
  }

  // --- settings, tab by tab ----------------------------------------------

  for (const [tab, name] of [
    ["panel", /^general$/i],
    ["git", /^git$/i],
    ["notifications", /notifications/i],
    ["components", /components/i],
    ["plugins", /plugins/i],
    ["members", /members/i],
    ["security", /security/i],
    ["audit", /audit/i],
  ] as const) {
    await page.goto("/settings")
    await page.getByRole("tab", { name }).click()
    await shoot(page, `settings-${tab === "panel" ? "general" : tab}`)
  }

  // --- signed out ---------------------------------------------------------

  // Clearing the cookies rather than calling logout: a fetch from inside the
  // page carries no CSRF header and is refused, correctly.
  await page.context().clearCookies()
  await page.goto("/")
  await expect(page.getByLabel(/^email$/i)).toBeVisible()
  await shoot(page, "sign-in")
})
