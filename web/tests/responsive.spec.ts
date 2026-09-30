import { test, expect, type APIRequestContext, type Page } from "@playwright/test"
import { readFileSync } from "node:fs"
import { join, resolve } from "node:path"

/**
 * Every page, at a phone's width and at a desktop's.
 *
 * The one thing a layout test can check without becoming a second copy of the
 * panel is whether anything is off the side of the screen. A horizontal
 * scrollbar on a phone is the most common responsive bug and the most annoying
 * one: the page moves sideways under a thumb that meant to scroll down.
 *
 * 375px is an iPhone SE, which is the narrowest screen worth supporting and the
 * one that finds every overflow. 1440px is a laptop.
 */

const EMAIL = "owner@example.test"
const PASSWORD = "a reasonable passphrase"

const PAGES: Array<[string, string]> = [
  ["/", "Overview"],
  ["/projects", "Projects"],
  ["/databases", "Databases"],
  ["/servers", "Servers"],
  ["/servers/new", "Add a server"],
  ["/templates", "Templates"],
  ["/activity", "Activity"],
  ["/account", "Account"],
  ["/settings", "Settings"],
]

/**
 * The pages that only exist once there is something in the panel.
 *
 * An empty install has no tables, no tab strips and no long identifiers, which
 * is exactly where a layout breaks — so the list above would pass on a panel
 * whose app page is unusable on a phone. Seeded through the API rather than
 * through the forms: this test is about widths, and driving eight forms to
 * reach a detail page would fail for reasons that have nothing to do with one.
 */
let seeded: Array<[string, string]> = []

async function seed(page: Page, request: APIRequestContext) {
  if (seeded.length) return

  const cookies = await page.context().cookies()
  const headers = {
    "Content-Type": "application/json",
    "X-Skifity-CSRF": cookies.find((c) => c.name === "skifity_csrf")?.value ?? "",
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

  const teams = await get("/api/teams")
  const project = await post(`/api/teams/${teams.items[0].id}/projects`, {
    name: "Layout",
    description: "Seeded so the detail pages have something on them.",
  })
  const environments = await get(`/api/projects/${project.id}/environments`)
  const envID = environments.items[0].id
  // Creating an app answers with an object holding the app, not with the app:
  // the reply also carries the first deployment and whether the webhook was
  // registered.
  const created = await post(`/api/environments/${envID}/apps`, {
    name: "storefront",
    source_type: "git",
    // A long URL on purpose: a repository address is the longest unbreakable
    // string the panel shows, and it is what pushes a card past the screen.
    repo_url: "https://github.com/an-organisation-with-a-long-name/a-repository-with-a-long-name",
    branch: "main",
    builder: "railpack",
    port: 3000,
    health_path: "/api/health",
  })
  const app = created.app
  await request.put(`/api/apps/${app.id}/variables`, {
    headers,
    // Long, unbreakable and secret: the three things that break a variables
    // table, together.
    data: {
      key: "A_VERY_LONG_ENVIRONMENT_VARIABLE_NAME_THAT_SOMEBODY_WILL_USE",
      value: "an-equally-long-value-with-no-spaces-in-it-at-all-whatsoever",
      secret: true,
    },
  })

  seeded = [
    [`/projects/${project.id}`, "Project"],
    [`/environments/${envID}/apps/new`, "New app"],
    ...[
      "overview",
      "deployments",
      "logs",
      "variables",
      "domains",
      "scaling",
      "storage",
      "settings",
      "advanced",
    ].map(
      (tab) =>
        [tab === "overview" ? `/apps/${app.id}` : `/apps/${app.id}?tab=${tab}`, `App: ${tab}`] as [
          string,
          string,
        ],
    ),
  ]
}

/** overflow returns how far past the viewport the document reaches. */
async function overflow(page: Page): Promise<number> {
  return page.evaluate(() => {
    const doc = document.documentElement
    return doc.scrollWidth - doc.clientWidth
  })
}

/** widest names the element that sticks out, so a failure is actionable. */
async function widest(page: Page): Promise<string> {
  return page.evaluate(() => {
    const limit = document.documentElement.clientWidth
    let worst = ""
    let by = 0
    for (const element of Array.from(document.querySelectorAll("body *"))) {
      const box = element.getBoundingClientRect()
      const over = Math.round(box.right - limit)
      if (over > by) {
        by = over
        const el = element as HTMLElement
        worst = `${el.tagName.toLowerCase()}.${el.className?.toString().slice(0, 80)} sticks out ${over}px`
      }
    }
    return worst || "nothing individually: the page itself is wider than the viewport"
  })
}

test.describe("layout", () => {
  test.describe.configure({ mode: "serial" })
  test.setTimeout(5 * 60 * 1000)

  for (const [width, height, label] of [
    [375, 812, "a phone"],
    [768, 1024, "a tablet"],
    [1440, 900, "a laptop"],
  ] as const) {
    test(`nothing is off the side of the screen on ${label} (${width}px)`, async ({
      page,
      request,
    }) => {
      await page.setViewportSize({ width, height })
      await signIn(page)
      await seed(page, request)

      for (const [path, name] of [...PAGES, ...seeded]) {
        await page.goto(path)
        await expect(
          page.getByRole("heading", { level: 1 }).first(),
          `${name} did not render at ${width}px`,
        ).toBeVisible({ timeout: 30_000 })
        // The sidebar collapses on a narrow screen and the transition moves
        // things; measuring mid-animation reports an overflow that is not
        // there a moment later.
        await page.waitForTimeout(400)

        const over = await overflow(page)
        expect(
          over,
          `${name} at ${width}px: ${over}px of horizontal scroll. ${await widest(page)}`,
        ).toBeLessThanOrEqual(1)
      }
    })
  }

  test("the sidebar is a drawer on a phone and fixed on a laptop", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 812 })
    await signIn(page)
    await page.goto("/")

    // On a phone the navigation is behind the toggle: the links are not on
    // screen until it is opened, or the content has no room.
    //
    // Scoped to the sidebar's own links: the Overview page links to Projects
    // too, and that one is on screen at every width.
    const projects = page
      .locator('[data-slot="sidebar-menu-button"]')
      .and(page.getByRole("link", { name: "Projects", exact: true }))
    await expect(projects).toBeHidden()
    await page
      .getByRole("button", { name: /show or hide the sidebar/i })
      .first()
      .click()
    await expect(projects).toBeVisible()

    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto("/")
    await expect(projects).toBeVisible()
  })

  test("every tap target on a phone is big enough to hit", async ({ page, request }) => {
    await page.setViewportSize({ width: 375, height: 812 })
    await signIn(page)
    await seed(page, request)

    // 24px is the WCAG 2.2 minimum (AA, 2.5.8). Anything smaller is a target
    // somebody misses, and on a panel that deletes things that matters.
    const tooSmall: string[] = []
    for (const [path, name] of [...PAGES, ...seeded]) {
      await page.goto(path)
      await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible({ timeout: 30_000 })
      await page.waitForTimeout(300)
      const small = await page.evaluate(() => {
        const out: string[] = []
        for (const element of Array.from(document.querySelectorAll("button, a, [role=button]"))) {
          const box = element.getBoundingClientRect()
          if (box.width === 0 || box.height === 0) continue // not on screen
          const style = getComputedStyle(element)
          if (style.visibility === "hidden") continue
          // A skip link is clipped to nothing until it has focus, but it still
          // has a box the size of its padding. It is not a target until then.
          if (style.clipPath !== "none" || style.clip !== "auto") continue
          if (box.height < 24 || box.width < 24) {
            const el = element as HTMLElement
            out.push(
              `${el.tagName.toLowerCase()} "${(el.innerText || el.getAttribute("aria-label") || "").slice(0, 30)}" ` +
                `is ${Math.round(box.width)}x${Math.round(box.height)}`,
            )
          }
        }
        return out
      })
      for (const one of small) tooSmall.push(`${name}: ${one}`)
    }
    expect(tooSmall, "tap targets under 24px, which WCAG 2.2 AA calls a failure").toEqual([])
  })

  test("a tab strip that wraps stays inside its own box", async ({ page, request }) => {
    await page.setViewportSize({ width: 375, height: 812 })
    await signIn(page)
    await seed(page, request)

    // Settings has seven tabs and the app page has nine; a phone fits four. The
    // strip is meant to wrap, and it did — but its height was fixed at one row,
    // so the rows below it were drawn on top of the panel underneath.
    const escaped: string[] = []
    for (const [path, name] of [["/settings", "Settings"], ...seeded] as Array<[string, string]>) {
      await page.goto(path)
      await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible({ timeout: 30_000 })
      await page.waitForTimeout(300)
      const out = await page.evaluate(() => {
        const bad: string[] = []
        for (const list of Array.from(document.querySelectorAll('[data-slot="tabs-list"]'))) {
          const box = list.getBoundingClientRect()
          for (const tab of Array.from(list.children)) {
            const it = tab.getBoundingClientRect()
            if (it.bottom > box.bottom + 1 || it.right > box.right + 1) {
              bad.push(`"${(tab as HTMLElement).innerText}" is outside its tab strip`)
            }
          }
        }
        return bad
      })
      for (const one of out) escaped.push(`${name}: ${one}`)
    }
    expect(escaped, "tabs drawn outside the strip they belong to").toEqual([])
  })
})

// Signs in, doing first-run setup when this file is run on its own.
//
// panel.spec.ts creates the account in its first test, and Playwright runs the
// files in order — so this works as part of the suite and hangs on the setup
// screen when somebody runs `-- responsive` by itself, which is exactly when
// somebody is looking at layout.
async function signIn(page: Page) {
  await page.goto("/")

  const setupToken = page.getByLabel(/setup token/i)
  const email = page.getByLabel(/^email$/i)
  const accountMenu = page.locator('[data-slot="account-menu"]')
  await expect(setupToken.or(email).or(accountMenu).first()).toBeVisible()

  if (await setupToken.isVisible()) {
    const workdir = readFileSync(resolve(import.meta.dirname, "..", ".e2e-workdir"), "utf8").trim()
    await setupToken.fill(readFileSync(join(workdir, "setup-token"), "utf8").trim())
    await email.fill(EMAIL)
    await page.getByLabel(/your name/i).fill("Owner")
    await page.getByLabel(/^password$/i).fill(PASSWORD)
    await page.getByLabel(/team name/i).fill("Acme")
    await page.getByRole("button", { name: /create account/i }).click()
    await page.getByRole("checkbox").check()
    await page.getByRole("button", { name: /done|continue|finish/i }).click()
  } else if (await email.isVisible()) {
    await email.fill(EMAIL)
    await page.getByLabel(/^password$/i).fill(PASSWORD)
    await page.getByRole("button", { name: /sign in/i }).click()
  }
  await expect(accountMenu).toBeVisible()
}

// A breadcrumb that links to the not-found page is worse than one that does not
// link at all. The ids are dropped from the trail, so the path has to be rebuilt
// from what is left, and /environments/env_x/apps/new used to rebuild into
// /environments and /environments/apps — neither of which is a page.
test("no breadcrumb points at a page that does not exist", async ({ page, request }) => {
  await signIn(page)
  await seed(page, request)

  for (const [path] of seeded) {
    await page.goto(path)
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible()

    const hrefs = await page
      .locator('nav[aria-label="breadcrumb"] a')
      .evaluateAll((links) => links.map((link) => link.getAttribute("href") ?? ""))

    for (const href of hrefs) {
      if (!href || href === "/") continue
      await page.goto(href)
      await expect(
        page.getByRole("heading", { level: 1 }),
        `${path} has a breadcrumb pointing at ${href}`,
      ).not.toHaveText(/not found/i)
    }
  }
})
