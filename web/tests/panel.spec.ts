import { test, expect, type Page } from "@playwright/test"
import { createHmac } from "node:crypto"
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"

/**
 * One pass through the panel, against the real binary.
 *
 * Deliberately small: first-run setup, the recovery key gate, the shell, and
 * the language switcher in all five languages. That last one is the part most
 * likely to break silently, because an untranslated page still renders.
 */

const EMAIL = "owner@example.test"
const PASSWORD = "a reasonable passphrase"

/** The five languages the panel ships, named as the switcher names them. */
const LANGUAGES = [
  { code: "en", name: "English" },
  { code: "id", name: "Bahasa Indonesia" },
  { code: "hi", name: "हिन्दी" },
  { code: "ru", name: "Русский" },
  { code: "zh-CN", name: "简体中文" },
] as const

/** Reads a locale file, so the test never guesses at a translation. */
function localeStrings(code: string): Record<string, Record<string, unknown>> {
  const path = resolve(import.meta.dirname, "..", "src", "locales", `${code}.json`)
  return JSON.parse(readFileSync(path, "utf8"))
}

function setupToken(): string {
  const workdir = readFileSync(resolve(import.meta.dirname, "..", ".e2e-workdir"), "utf8").trim()
  return readFileSync(join(workdir, "setup-token"), "utf8").trim()
}

test.describe.configure({ mode: "serial" })

test("first-run setup creates an account and shows the recovery key once", async ({ page }) => {
  await page.goto("/")

  // With no account, every route is the setup screen.
  await expect(page.getByLabel(/setup token/i)).toBeVisible()

  // The installer prints a link with the token in it, because copying a
  // forty-character string out of a terminal is the most annoying minute of
  // installing anything. It travels in the fragment, which a browser never
  // sends to a server, and the page takes it out of the address bar once it
  // has read it — so a bookmark or a screen share does not keep it.
  const token = setupToken()
  await page.goto(`/setup#token=${token}`)
  await expect(page.getByLabel(/setup token/i)).toHaveValue(token)
  expect(new URL(page.url()).hash, "the token is still in the address bar").toBe("")

  await page.getByLabel(/^email$/i).fill(EMAIL)
  await page.getByLabel(/your name/i).fill("Owner")
  await page.getByLabel(/^password$/i).fill(PASSWORD)
  await page.getByLabel(/team name/i).fill("Acme")
  await page.getByRole("button", { name: /create account/i }).click()

  // The recovery key is shown once, and the panel does not move on until it is
  // acknowledged. That gate is the whole point of the screen.
  const recoveryKey = page.getByText(/^SKIFITY-RECOVERY-v1-/)
  await expect(recoveryKey).toBeVisible()
  expect((await recoveryKey.textContent())?.trim().length).toBeGreaterThan(30)

  const done = page.getByRole("button", { name: /done|continue|finish/i })
  await expect(done).toBeDisabled()

  await page.getByRole("checkbox").check()
  await expect(done).toBeEnabled()
  await done.click()

  await expect(page.getByRole("heading", { name: "Overview", level: 1, exact: true })).toBeVisible()
})

test("the shell is there, and the empty states say what to do next", async ({ page }) => {
  await signIn(page)

  // Every navigation target should exist and land somewhere. The heading is
  // matched at level 1 and exactly: "Servers" would otherwise also match the
  // empty state's "No servers yet".
  for (const name of ["Projects", "Servers", "Databases"] as const) {
    await page.getByRole("link", { name, exact: true }).click()
    await expect(page.getByRole("heading", { name, level: 1, exact: true })).toBeVisible()
  }

  // A panel with no servers must not just say "none": it has to offer the
  // next step.
  await page.getByRole("link", { name: "Servers", exact: true }).click()
  await expect(page.getByRole("link", { name: /add a server/i }).first()).toBeVisible()

  await page
    .getByRole("link", { name: /add a server/i })
    .first()
    .click()
  await expect(page.getByLabel(/IP address or hostname/i)).toBeVisible()
  // The promise that the password is not kept has to be on the screen where
  // the password is typed.
  await expect(page.getByText(/never stored/i)).toBeVisible()
})

// The overview's first answer is whether anything is wrong, and on a fresh
// panel with no servers it still gives it: a team with no notification
// channel is told that nobody would hear about a failure, here and on the
// bell, and once there is a channel, that nothing needs attention.
test("the overview and the bell say what needs attention", async ({ page }) => {
  await signIn(page)
  await expect(page.getByText("No servers yet")).toBeVisible()

  const nobody = page.getByRole("link", { name: /Nobody is told when something breaks/ })
  await expect(nobody).toBeVisible()
  await expect(page.getByTestId("attention-count")).toHaveText("1")

  // The bell opens the same list, which links to where the fix is.
  await page.getByRole("button", { name: "1 thing needs attention" }).click()
  await expect(page.getByRole("dialog").getByRole("link", { name: /Nobody is told/ })).toBeVisible()
  await page.keyboard.press("Escape")

  const cookies = await page.context().cookies()
  const me = await (await page.request.get("/api/me")).json()
  const created = await page.request.post(`/api/teams/${me.teams[0].id}/notifications`, {
    headers: {
      "Content-Type": "application/json",
      "X-Skifity-CSRF": cookies.find((c) => c.name === "skifity_csrf")?.value ?? "",
    },
    data: {
      kind: "telegram",
      name: "ops",
      config: { bot_token: "123456:fake-bot-token-for-e2e", chat_id: "-100" },
      events: ["deploy.failed"],
    },
  })
  expect(created.ok(), await created.text()).toBeTruthy()

  await page.reload()
  await expect(page.getByText("Nothing needs attention")).toBeVisible()
  await expect(page.getByText("No servers yet")).toBeVisible()
  await expect(page.getByTestId("attention-count")).toHaveCount(0)
})

test("every language is complete on the pages a new user sees", async ({ page }) => {
  await signIn(page)

  // The expected words come from the locale files themselves. The completeness
  // check already proves those files have every key; what this proves is that
  // the running panel actually resolves to the language that was chosen, which
  // is a different failure and one the files cannot show.
  const languages = LANGUAGES.map((language) => ({
    ...language,
    overview: localeStrings(language.code).dashboard.title as string,
  }))

  for (const language of languages) {
    // Selected by data-slot rather than by name: the button's accessible name
    // is itself translated, so it stops matching the moment it is used.
    await page.locator('[data-slot="language-switcher"]').click()
    await page.getByRole("menuitem", { name: language.name }).click()

    await expect(
      page.getByRole("heading", { name: language.overview, level: 1, exact: true }),
      `the overview heading in ${language.name}`,
    ).toBeVisible()

    // The sidebar must be translated too, not just the page.
    await expect(
      page.locator("nav").getByText(language.overview).first(),
      `the sidebar in ${language.name}`,
    ).toBeVisible()

    // Nothing may be left as a raw key, which is what a missing translation
    // looks like when the fallback is turned off.
    const body = (await page.locator("body").innerText()).trim()
    expect(body, `untranslated key visible in ${language.name}`).not.toMatch(
      /\b(nav|dashboard|common|servers|apps)\.[a-zA-Z]+\b/,
    )
  }

  // The choice has to survive a reload, or it is not a setting. The last
  // language switched to is still selected.
  const last = languages[languages.length - 1]
  await page.reload()
  await expect(
    page.getByRole("heading", { name: last.overview, level: 1, exact: true }),
  ).toBeVisible()
})

test("an error speaks the language the panel is set to", async ({ page }) => {
  await signIn(page)

  // The completeness check proves the catalogue has every error in every
  // language. What it cannot prove is that a real failure, coming off the wire
  // as English from a Go file, is looked up by its code and comes out in the
  // reader's language with the server's own values still in it. Until Phase 59
  // it did not: ninety-odd errors were English on every page in every language.
  const russian = LANGUAGES.find((language) => language.code === "ru")!
  await page.locator('[data-slot="language-switcher"]').click()
  await page.getByRole("menuitem", { name: russian.name }).click()

  // An id that cannot exist. The API answers resource.not_found, which carries
  // the kind — "app" — as a value rather than inside the sentence.
  await page.goto("/apps/app_does_not_exist")

  const catalogue = localeStrings("ru").errors as {
    catalogue: Record<string, Record<string, string>>
  }
  const entry = catalogue.catalogue["resource_not_found"]
  const title = entry.title.replace("{{0}}", "app")
  const impact = entry.impact

  await expect(page.getByText(title), "the error title in Russian").toBeVisible()
  await expect(page.getByText(impact), "what it means, in Russian").toBeVisible()

  // And the English it replaced is nowhere on the page.
  const english = localeStrings("en").errors as {
    catalogue: Record<string, Record<string, string>>
  }
  await expect(
    page.getByText(english.catalogue["resource_not_found"].impact),
    "the English the server sent is still showing",
  ).toHaveCount(0)

  // The language is the account's, not only this browser's: signing in again
  // with nothing stored in the browser comes back in Russian.
  await page.context().clearCookies()
  await page.evaluate(() => localStorage.clear())
  await signIn(page)
  await expect(
    page.getByRole("heading", {
      name: localeStrings("ru").dashboard.title as string,
      level: 1,
      exact: true,
    }),
    "the account's language after signing in again",
  ).toBeVisible()

  // English again, saved to the account, so every test after this one starts
  // in the language it reads.
  const saved = page.waitForResponse(
    (response) => response.url().endsWith("/api/me") && response.request().method() === "PATCH",
  )
  await page.locator('[data-slot="language-switcher"]').click()
  await page.getByRole("menuitem", { name: "English" }).click()
  await saved
})

// Every error the panel can show links into this, and an operator whose panel
// is broken may have no other browser and no way out to the internet.
// The catalogue is the first page anybody browses, and it was three hundred
// grey squares with a letter in them. The logos are served out of the binary,
// so a broken one is a file missing from a build rather than a CDN having a bad
// day — which is exactly the kind of thing that is never noticed until somebody
// opens the page.
test("every logo the catalogue claims is actually there", async ({ page }) => {
  await signIn(page)

  const cookies = await page.context().cookies()
  const jar = cookies.map((c) => `${c.name}=${c.value}`).join("; ")
  const headers = { Cookie: jar }

  const list = await (await page.request.get("/api/templates", { headers })).json()
  const withIcon = list.items.filter((template: { icon?: string }) => template.icon)
  expect(
    withIcon.length,
    "no template has a logo, so hack/fetch_icons.py has stopped working",
  ).toBeGreaterThan(150)

  const broken: string[] = []
  for (const template of withIcon) {
    const response = await page.request.get(`/api/templates/${template.id}/icon`, { headers })
    if (!response.ok() || (await response.body()).length === 0) {
      broken.push(`${template.id} answered ${response.status()}`)
    }
  }
  expect(broken, "logos the catalogue promises and the panel cannot serve").toEqual([])

  // And a template with no logo must not ask for one: an <img> pointing at a
  // 404 on every card is worse than the letter it replaced.
  const without = list.items.find((template: { icon?: string }) => !template.icon)
  if (without) {
    const response = await page.request.get(`/api/templates/${without.id}/icon`, { headers })
    expect(response.status(), "a template with no logo served one anyway").toBe(404)
  }
})

// The catalogue is three hundred and seventy-four apps on one page, so what
// makes it usable is being able to narrow it: a category, a search, or both.
// The one thing that goes wrong quietly is the two disagreeing — a category
// chosen, then a search that nothing in it matches, leaving a filter nobody can
// see and a page that says there is nothing.
test("the template catalogue narrows by category and by search", async ({ page }) => {
  await signIn(page)
  await page.goto("/templates")

  const sections = page.locator('section[aria-labelledby^="templates-"]')
  const chips = page.getByRole("group", { name: "Category" })
  const search = page.getByPlaceholder("Search templates")
  await expect(chips).toBeVisible()
  await expect.poll(() => sections.count()).toBeGreaterThan(5)

  await chips.getByRole("button", { name: /^AI \d+$/ }).click()
  await expect(sections).toHaveCount(1)
  await expect(sections.first().getByRole("heading", { level: 2 })).toContainText("AI")

  // Only the website templates mention WordPress, so the AI category has
  // nothing left, and the page shows what matched rather than an empty AI.
  await search.fill("wordpress")
  await expect(sections).toHaveCount(1)
  await expect(sections.first().getByRole("heading", { level: 2 })).toContainText(
    "Websites and publishing",
  )
  // One category left is not a choice, so there is no row of chips to choose from.
  await expect(chips).toBeHidden()

  await search.fill("no-such-template-anywhere")
  await expect(page.getByText("Nothing matches that")).toBeVisible()
  await expect(sections).toHaveCount(0)

  // On a phone the same choice is one dropdown, not five rows of chips.
  await page.setViewportSize({ width: 375, height: 800 })
  await search.fill("")
  await expect(chips).toBeHidden()
  const picker = page.getByRole("combobox", { name: "Category" })
  await picker.click()
  await page.getByRole("option", { name: /^All · \d+$/ }).click()
  await expect.poll(() => sections.count()).toBeGreaterThan(5)
  await picker.click()
  await page.getByRole("option", { name: /^AI · \d+$/ }).click()
  await expect(sections).toHaveCount(1)
})

// An install is all or nothing, and what would stop one is said before Install
// is pressed: a second Homer in the same environment would have the first
// one's name, and the dialog says so beside the name rather than after a click.
test("the install dialog says a name is taken before Install is pressed", async ({ page }) => {
  await signIn(page)
  const environment = await firstEnvironment(page)
  const installed = await page.request.post("/api/templates/homer/install", {
    headers: await csrfHeaders(page),
    data: { environment_id: environment },
  })
  expect(installed.ok(), await installed.text()).toBeTruthy()

  await page.goto("/templates")
  await page.getByPlaceholder("Search templates").fill("homer")
  await page.getByRole("button", { name: "Install Homer" }).click()
  const dialog = page.getByRole("dialog")
  const install = dialog.getByRole("button", { name: "Install", exact: true })
  await expect(dialog.getByText("That name is already used here")).toBeVisible()
  await expect(install).toBeDisabled()

  await dialog.getByLabel("App name").fill("homer-two")
  await expect(dialog.getByText("That name is already used here")).toHaveCount(0)
  await expect(install).toBeEnabled()
})

// WordPress starts as root, and the environment first-run setup made is at the
// strictest level, which refuses that. The dialog says so before anything is
// made, and the owner can accept root images for it in the same step: Install
// waits for the box. Nothing is installed here, so the level stays as it was
// for every test after this one.
test("a template that starts as root asks the owner before it is installed", async ({ page }) => {
  await signIn(page)
  await page.goto("/templates")
  await page.getByPlaceholder("Search templates").fill("wordpress")
  await page.getByRole("button", { name: "Install WordPress", exact: true }).click()

  const dialog = page.getByRole("dialog")
  await expect(dialog.getByText("This image starts as root")).toBeVisible()
  const accept = dialog.getByRole("checkbox", { name: /Accept root images in Production/ })
  const install = dialog.getByRole("button", { name: "Install", exact: true })
  await expect(accept).not.toBeChecked()
  await expect(install).toBeDisabled()

  await accept.check()
  await expect(install).toBeEnabled()
  await dialog.getByRole("button", { name: "Cancel" }).click()
  await expect(dialog).toBeHidden()
})

test("the documentation is served from the binary", async ({ page }) => {
  await page.goto("/docs/")
  await expect(page.getByRole("heading", { level: 1 })).toContainText("documentation")

  await page.getByRole("link", { name: "Troubleshooting" }).click()
  await expect(page.getByRole("heading", { name: "Troubleshooting", level: 1 })).toBeVisible()

  // A link written for the repository has to work here too.
  await page.getByRole("link", { name: "Configuration" }).first().click()
  await expect(page.getByRole("heading", { name: "Configuration", level: 1 })).toBeVisible()

  // And the images have to load, not 404.
  await page.goto("/docs/quick-start")
  const image = page.getByRole("img").first()
  await expect(image).toBeVisible()
  expect(await image.evaluate((element: HTMLImageElement) => element.naturalWidth)).toBeGreaterThan(
    0,
  )
})

test("the theme can be changed and is remembered", async ({ page }) => {
  await signIn(page)

  await page.locator('[data-slot="theme-toggle"]').click()
  await page.getByRole("menuitem", { name: /dark/i }).click()
  await expect(page.locator("html")).toHaveClass(/dark/)

  await page.reload()
  await expect(page.locator("html")).toHaveClass(/dark/)
})

// Every page in the navigation, opened.
//
// The Templates page threw on every install from the day the catalogue grew
// past the eight hand-written entries — `"databases": null` for the 157
// templates that have none, iterated, thrown — and rendered the error boundary
// instead of the catalogue. Nothing caught it, because nothing had ever opened
// the page: the Go tests read the catalogue's structure rather than its JSON,
// and the tests here checked the shell and the empty states.
//
// This opens each one and fails on two things: an uncaught error in the page,
// and the error boundary being on screen. It is deliberately not an assertion
// about what each page contains — that would be a second copy of the panel,
// out of date within a week. It asserts only that the page renders at all,
// which is the thing that was not true.
test("every page in the navigation renders", async ({ page }) => {
  await signIn(page)

  const crashes: string[] = []
  page.on("pageerror", (error) => crashes.push(`${page.url()}: ${error.message}`))

  for (const [path, label] of [
    ["/", "Overview"],
    ["/projects", "Projects"],
    ["/databases", "Databases"],
    ["/servers", "Servers"],
    ["/servers/new", "Add a server"],
    ["/templates", "Templates"],
    ["/activity", "Activity"],
    ["/account", "Account"],
    ["/settings", "Settings"],
    ["/settings?tab=accounts", "Accounts"],
  ] as const) {
    await page.goto(path)
    // The heading, not networkidle: two of these are lazy chunks behind a
    // Suspense fallback, and an idle network says nothing about whether the
    // chunk rendered.
    await expect(
      page
        .getByRole("heading", { level: 1 })
        .or(page.getByRole("heading", { level: 2 }))
        .first(),
      `${label} (${path}) showed no heading`,
    ).toBeVisible({ timeout: 30_000 })
    await expect(
      page.getByText(/this page stopped working/i),
      `${label} (${path}) rendered the error boundary`,
    ).toHaveCount(0)
  }

  expect(crashes, "a page threw while rendering").toEqual([])
})

test("a new database says why it cannot be backed up, and where that is fixed", async ({
  page,
}) => {
  await signIn(page)

  // This panel has no bucket, which is the state a new panel is in.
  const storage = await (await page.request.get("/api/backup-storage")).json()
  expect(storage.configured).toBe(false)

  const me = await (await page.request.get("/api/me")).json()
  const projects = await (await page.request.get(`/api/teams/${me.teams[0].id}/projects`)).json()
  await page.goto(`/projects/${projects.items[0].id}`)
  await page.getByRole("button", { name: "New database" }).first().click()

  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("switch", { name: "Back up daily" })).toBeDisabled()
  await expect(dialog.getByText("Not until this panel has backup storage.")).toBeVisible()
  // The panel's administrator is sent to the page it is set on.
  await expect(
    dialog.getByRole("link", { name: "Set it up under Settings → General → Backup storage." }),
  ).toHaveAttribute("href", "/settings?tab=panel")
})

test("a folder on this computer becomes an app, with no terminal", async ({ page }) => {
  await signIn(page)

  // A folder the way an assistant leaves one: the code, a .env with a real
  // key in it, node_modules, and build output .gitignore already names.
  const folder = join(mkdtempSync(join(tmpdir(), "skifity-e2e-")), "notes-app")
  const files: Record<string, string> = {
    "package.json": JSON.stringify({ name: "notes", dependencies: { pg: "8.13.0" } }),
    "index.js": "require('http').createServer((q, r) => r.end('hi')).listen(process.env.PORT)",
    ".gitignore": "dist\n",
    ".env": "STRIPE_SECRET_KEY=sk_test_e2e\n",
    "dist/bundle.js": "left behind by .gitignore",
    "node_modules/pg/index.js": "left behind always",
  }
  for (const [name, body] of Object.entries(files)) {
    mkdirSync(dirname(join(folder, name)), { recursive: true })
    writeFileSync(join(folder, name), body)
  }

  const me = await (await page.request.get("/api/me")).json()
  const projects = await (await page.request.get(`/api/teams/${me.teams[0].id}/projects`)).json()
  const envs = await (
    await page.request.get(`/api/projects/${projects.items[0].id}/environments`)
  ).json()
  await page.goto(`/environments/${envs.items[0].id}/apps/new`)

  await page.getByRole("button", { name: "A folder on my computer" }).click()
  await page.getByTestId("folder-input").setInputFiles(folder)

  // Read by the panel, packed in the browser: three files go, and what stays
  // behind is counted rather than silently dropped.
  await expect(page.getByText(/3 files/)).toBeVisible({ timeout: 30_000 })
  await expect(page.getByText(/3 left out/)).toBeVisible()
  await expect(page.getByText(/PostgreSQL/).first()).toBeVisible()
  // The .env is offered in the box, visibly, rather than uploaded.
  await expect(page.getByLabel(/settings/i).last()).toHaveValue(/STRIPE_SECRET_KEY=sk_test_e2e/)

  // No cluster here to make a database in.
  await page.getByRole("checkbox", { name: /PostgreSQL/ }).uncheck()
  // The button says what it will do, and deploying straight away is the default.
  await page.getByRole("button", { name: /^create and deploy$/i }).click()

  // The page opens on the build it started, with that deployment open, not on
  // an Overview that says nothing is running.
  await expect(page).toHaveURL(/\/apps\/app_\w+\?tab=deployments&deployment=dep_/, {
    timeout: 30_000,
  })
  await expect(page.getByRole("button", { name: /^#1\b/ })).toHaveAttribute("aria-expanded", "true")
  await expect(page.getByRole("button", { name: "Send a new version" })).toBeVisible()
  await page.getByRole("tab", { name: "Overview" }).click()
  await expect(page.getByText("An uploaded folder")).toBeVisible()

  const appID = page.url().split("/apps/")[1].split(/[/?#]/)[0]
  const variables = await (await page.request.get(`/api/apps/${appID}/variables`)).json()
  const keys = variables.items.map((v: { key: string }) => v.key)
  expect(keys, "the .env's value did not become a variable").toContain("STRIPE_SECRET_KEY")
  expect(JSON.stringify(variables)).not.toContain("sk_test_e2e")

  const deployments = await (await page.request.get(`/api/apps/${appID}/deployments`)).json()
  expect(deployments.items[0]?.commit_sha, "the deploy does not name the upload").toMatch(
    /^[a-f0-9]{64}$/,
  )
})

test("a first deployment that failed is said on the Overview, with its reason", async ({
  page,
}) => {
  await signIn(page)

  const cookies = await page.context().cookies()
  const headers = {
    "Content-Type": "application/json",
    "X-Skifity-CSRF": cookies.find((c) => c.name === "skifity_csrf")?.value ?? "",
  }
  const me = await (await page.request.get("/api/me")).json()
  const projects = await (await page.request.get(`/api/teams/${me.teams[0].id}/projects`)).json()
  const envs = await (
    await page.request.get(`/api/projects/${projects.items[0].id}/environments`)
  ).json()
  // Deployed at once, on a panel with no cluster to deploy to: the deployment
  // fails, and nothing ever goes live.
  const created = await page.request.post(`/api/environments/${envs.items[0].id}/apps`, {
    headers,
    data: { name: "never-live", source_type: "image", image: "nginx:1.27", port: 80, deploy: true },
  })
  expect(created.ok(), await created.text()).toBeTruthy()
  const appID = (await created.json()).app.id
  await expect
    .poll(
      async () =>
        (await (await page.request.get(`/api/apps/${appID}/deployments`)).json()).items[0]?.status,
      { timeout: 30_000 },
    )
    .toBe("failed")
  const failed = (await (await page.request.get(`/api/apps/${appID}/deployments`)).json()).items[0]

  await page.goto(`/apps/${appID}`)
  const en = localeStrings("en")
  const apps = en.apps as Record<string, string>
  const notice = page.getByRole("alert").filter({ hasText: "Deployment #1 failed" })
  await expect(notice).toBeVisible()
  // Its reason in the words the Deployments tab uses: the catalogue's title
  // for its code, or what the server recorded when the title needs values the
  // record does not keep.
  const catalogue = (en.errors as Record<string, Record<string, { title?: string }>>).catalogue
  const title = catalogue[String(failed.error_code).replaceAll(".", "_")]?.title
  await expect(notice).toContainText(title && !title.includes("{{") ? title : failed.error_message)
  // Not the empty state that sends people to start another deploy as though
  // none had been tried.
  await expect(page.getByText(apps.noInstancesTitle)).toBeVisible()
  await expect(page.getByText(/builds the app and starts the first one/)).toHaveCount(0)
  await expect(page.getByText(apps.liveNone)).toBeVisible()

  await page.getByRole("link", { name: apps.viewBuildLog }).click()
  await expect(page).toHaveURL(/\?tab=deployments&deployment=dep_/)
  await expect(page.getByRole("button", { name: /^#1\b/ })).toHaveAttribute("aria-expanded", "true")
})

test("an app can be put behind a password, and the password never comes back", async ({ page }) => {
  await signIn(page)

  const cookies = await page.context().cookies()
  const headers = {
    "Content-Type": "application/json",
    "X-Skifity-CSRF": cookies.find((c) => c.name === "skifity_csrf")?.value ?? "",
  }
  const me = await (await page.request.get("/api/me")).json()
  const projects = await (await page.request.get(`/api/teams/${me.teams[0].id}/projects`)).json()
  const envs = await (
    await page.request.get(`/api/projects/${projects.items[0].id}/environments`)
  ).json()
  const created = await page.request.post(`/api/environments/${envs.items[0].id}/apps`, {
    headers,
    data: { name: "staging", source_type: "image", image: "nginx:1.27", port: 80 },
  })
  expect(created.ok(), await created.text()).toBeTruthy()
  const appID = (await created.json()).app.id

  await page.goto(`/apps/${appID}?tab=firewall`)
  await expect(page.getByText(/Ask everyone who opens this app/)).toBeVisible()

  await page.getByLabel("Username", { exact: true }).fill("client")
  await page.getByLabel("Password", { exact: true }).fill("correct horse battery")
  await page.getByRole("button", { name: "Turn on" }).click()
  await expect(page.getByText(/signs in as client/)).toBeVisible()

  // Only a hash is kept, and not even that is ever sent back.
  const view = await (await page.request.get(`/api/apps/${appID}/password`)).text()
  expect(view).toContain('"username":"client"')
  expect(view).not.toContain("correct horse battery")
  expect(view).not.toContain("$2")
  await expect(page.getByText("correct horse battery")).toHaveCount(0)

  await page.getByRole("button", { name: "Remove password" }).click()
  await page.getByRole("alertdialog").getByRole("button", { name: "Remove password" }).click()
  await expect(page.getByRole("button", { name: "Turn on" })).toBeVisible()
  expect(await (await page.request.get(`/api/apps/${appID}/password`)).text()).toContain(
    '"enabled":false',
  )
})

test("a variable is changed in place, and Edit as text sends only what changed", async ({
  page,
}) => {
  await signIn(page)
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"])

  const cookies = await page.context().cookies()
  const headers = {
    "Content-Type": "application/json",
    "X-Skifity-CSRF": cookies.find((c) => c.name === "skifity_csrf")?.value ?? "",
  }
  const me = await (await page.request.get("/api/me")).json()
  const projects = await (await page.request.get(`/api/teams/${me.teams[0].id}/projects`)).json()
  const envs = await (
    await page.request.get(`/api/projects/${projects.items[0].id}/environments`)
  ).json()
  const created = await page.request.post(`/api/environments/${envs.items[0].id}/apps`, {
    headers,
    data: { name: "configured", source_type: "image", image: "nginx:1.27", port: 80 },
  })
  expect(created.ok(), await created.text()).toBeTruthy()
  const appID = (await created.json()).app.id
  for (const variable of [
    { key: "GREETING", value: "hello there", is_secret: false },
    { key: "API_TOKEN", value: "tok_e2e_not_a_real_token", is_secret: true },
  ]) {
    const set = await page.request.put(`/api/apps/${appID}/variables`, { headers, data: variable })
    expect(set.ok(), await set.text()).toBeTruthy()
  }

  const en = localeStrings("en")
  const words = en.variables as Record<string, string>
  const common = en.common as Record<string, string>
  const named = (sentence: string, values: Record<string, string>) =>
    sentence.replace(/\{\{(\w+)\}\}/g, (_, name: string) => values[name])
  // Every batch the page sends, which is what "nothing changed" is checked by.
  const batches: string[] = []
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().endsWith(`/apps/${appID}/variables/batch`)) {
      batches.push(request.postData() ?? "")
    }
  })

  await page.goto(`/apps/${appID}?tab=variables`)

  // A plain value is copied whole.
  await page.getByRole("button", { name: named(common.copyValue, { name: "GREETING" }) }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("hello there")

  // Edited in place, from what it is, without typing its name again.
  await page.getByRole("button", { name: named(words.editKey, { key: "GREETING" }) }).click()
  // The form opens in the variable's own row.
  const form = page.getByRole("row").filter({ has: page.locator("#variable-value") })
  await expect(form.locator("#variable-key")).toHaveValue("GREETING")
  await expect(form.locator("#variable-value")).toHaveValue("hello there")
  await form.locator("#variable-value").fill("hello again")
  await form.getByRole("button", { name: common.save, exact: true }).click()
  await expect(page.getByText((en.deploy as Record<string, string>).noRebuildNeeded)).toBeVisible()
  await expect(page.getByRole("button", { name: "hello again", exact: true })).toBeVisible()

  // A secret's value is never sent back, so its box starts empty.
  await page.getByRole("button", { name: named(words.editKey, { key: "API_TOKEN" }) }).click()
  await expect(form.locator("#variable-value")).toHaveValue("")
  await form.getByRole("button", { name: common.cancel, exact: true }).click()

  // The box opens with what is there: the plain line, and a comment for the
  // secret it leaves alone.
  await page.getByRole("button", { name: words.bulkEdit }).click()
  const box = page.locator("#bulk-variables")
  const bulk = page.locator('[data-slot="card"]').filter({ has: box })
  await expect(box).toHaveValue(/^GREETING=hello again$/m)
  const comment = `# ${named(words.textSecret, { key: "API_TOKEN" })}`
  const escaped = comment.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
  await expect(box).toHaveValue(new RegExp(`^${escaped}$`, "m"))

  // Saved as it opened: nothing to send, so nothing is sent.
  await bulk.getByRole("button", { name: common.save, exact: true }).click()
  await expect(page.getByText(words.nothingChanged)).toBeVisible()
  expect(batches).toEqual([])

  // One line changed: that line, and only it.
  await page.getByRole("button", { name: words.bulkEdit }).click()
  await box.fill((await box.inputValue()).replace("GREETING=hello again", "GREETING=hello, text"))
  await bulk.getByRole("button", { name: common.save, exact: true }).click()
  await expect.poll(() => batches.length).toBe(1)
  expect(JSON.parse(batches[0])).toEqual({ set: [{ key: "GREETING", value: "hello, text" }] })
  await expect(page.getByRole("button", { name: "hello, text", exact: true })).toBeVisible()

  const after = await (await page.request.get(`/api/apps/${appID}/variables`)).json()
  const secret = after.items.find((v: { key: string }) => v.key === "API_TOKEN")
  expect(secret?.is_secret, "the secret was lost or made plain").toBe(true)
  expect(JSON.stringify(after)).not.toContain("tok_e2e_not_a_real_token")
})

// The folder deployed above failed, with no cluster to build it on, and so did
// every app the tests before made. The overview says so, and a line opens its
// app's deployments.
test("a failed deploy is on the overview, and opens its deployments", async ({ page }) => {
  await signIn(page)
  const failed = page.getByRole("link", { name: /Deployment #\d+ of .+ failed/ }).first()
  await expect(failed).toBeVisible({ timeout: 60_000 })
  await failed.click()
  await expect(page).toHaveURL(/\/apps\/app_[^/?#]+\?tab=deployments/)
  await expect(page.getByRole("tab", { name: "Deployments" })).toHaveAttribute(
    "aria-selected",
    "true",
  )
})

// Links sent before the tab moved into the query string put it in the path:
// notifications, commit statuses, pull request comments. Those are in inboxes
// and on commits for good, so they still open the tab they name, and anything
// else under an app opens the app rather than "Page not found".
test("an old link to an app's tab opens that tab, and nonsense opens the app", async ({ page }) => {
  await signIn(page)
  const me = await (await page.request.get("/api/me")).json()
  const apps = await (await page.request.get(`/api/teams/${me.teams[0].id}/apps`)).json()
  const appID: string = apps.items[0].id

  await page.goto(`/apps/${appID}/deployments`)
  await expect(page).toHaveURL(new RegExp(`/apps/${appID}\\?tab=deployments$`))
  await expect(page.getByRole("tab", { name: "Deployments" })).toHaveAttribute(
    "aria-selected",
    "true",
  )

  await page.goto(`/apps/${appID}/nonsense`)
  await expect(page).toHaveURL(new RegExp(`/apps/${appID}$`))
  await expect(page.getByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true")
  await expect(page.getByText("That page does not exist")).toHaveCount(0)
})

// A failed deploy's notification links to that deployment. It opens, at the
// first line of its build that went wrong, even with another one running;
// clicking another opens that one instead; and a link to one the list does
// not have opens the tab as it would have opened anyway.
test("a link to a deployment opens its build at the line that went wrong", async ({ page }) => {
  await signIn(page)
  const me = await (await page.request.get("/api/me")).json()
  const apps = await (await page.request.get(`/api/teams/${me.teams[0].id}/apps`)).json()
  const appID: string = apps.items[0].id
  const at = new Date().toISOString()
  const deployment = (id: string, number: number, status: string) => ({
    id,
    app_id: appID,
    number,
    status,
    trigger: "manual",
    commit_sha: "",
    commit_message: `Release ${number}`,
    commit_author: "",
    image: "",
    build_fingerprint: "",
    created_by: "",
    created_at: at,
  })
  const log = (lines: string[], finished: boolean) => ({
    lines: lines.map((line, index) => ({ seq: index + 1, stream: "stdout", line, at })),
    finished,
  })
  await page.route(`**/api/apps/${appID}/deployments`, (route) =>
    route.fulfill({
      json: {
        items: [
          deployment("dep_e2e_running", 3, "building"),
          deployment("dep_e2e_failed", 2, "failed"),
        ],
        total: 2,
      },
    }),
  )
  await page.route(`**/api/apps/${appID}/deployments/dep_e2e_failed/logs*`, (route) =>
    route.fulfill({
      json: log(
        ["Preparing the builder.", "> shop@1.0.0 build", "npm ERR! missing script: build", "Done."],
        true,
      ),
    }),
  )
  await page.route(`**/api/apps/${appID}/deployments/dep_e2e_running/logs*`, (route) =>
    route.fulfill({ json: log(["Preparing the builder.", "Installing the dependencies."], false) }),
  )

  await page.goto(`/apps/${appID}?tab=deployments&deployment=dep_e2e_failed`)
  await expect(page.getByTestId("first-error-line")).toHaveText("npm ERR! missing script: build")
  await expect(page.getByText("Installing the dependencies.")).toHaveCount(0)

  await page.getByRole("button", { name: /Release 3/ }).click()
  await expect(page.getByText("Installing the dependencies.")).toBeVisible()
  await expect(page.getByTestId("first-error-line")).toHaveCount(0)

  await page.goto(`/apps/${appID}?tab=deployments&deployment=dep_e2e_gone`)
  await expect(page.getByText("Installing the dependencies.")).toBeVisible()
  await expect(page.getByText(/this page stopped working/i)).toHaveCount(0)
})

// The apps are on the overview once it is past first-run setup, each with the
// project and environment it lives in. This panel has no servers, so one is
// put in the answer to make it past that.
test("the overview lists the team's apps with where they live", async ({ page }) => {
  await page.route("**/api/teams/*/servers", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: "srv_e2e",
            name: "node-1",
            host: "203.0.113.9",
            role: "control-plane",
            status: "ready",
            cpu_cores: 2,
            memory_mb: 4096,
          },
        ],
        total: 1,
      },
    }),
  )
  await signIn(page)
  const me = await (await page.request.get("/api/me")).json()
  const apps = await (await page.request.get(`/api/teams/${me.teams[0].id}/apps`)).json()
  expect(apps.items.length, "the suite has made apps by now").toBeGreaterThan(0)

  await page.goto("/")
  await expect(page.getByRole("heading", { name: "Your apps", level: 2 })).toBeVisible()
  for (const app of apps.items as {
    name: string
    project_name: string
    environment_name: string
  }[]) {
    await expect(
      page
        .getByRole("link")
        .filter({ hasText: app.name })
        .filter({ hasText: `${app.project_name} · ${app.environment_name}` })
        .first(),
      `${app.name} with where it lives`,
    ).toBeVisible()
  }
})

test("Settings asks about new releases only when the button is pressed", async ({ page }) => {
  // Every request the page makes is watched, so the promise is checked rather
  // than read: the panel never asks which release is the newest on its own.
  const asked: string[] = []
  page.on("request", (request) => {
    if (request.url().includes("/api/upgrade/check")) asked.push(request.method())
  })

  await signIn(page)
  await page.goto("/settings")
  const check = page.getByRole("button", { name: "Check for updates" })
  await expect(check).toBeVisible()
  await expect(page.getByText(/never checks for updates on its own/)).toBeVisible()
  expect(asked).toEqual([])

  // This panel was not installed by the installer, so it has no repository to
  // ask about, and says so instead of guessing.
  await check.click()
  await expect(page.getByText("This panel does not know where its releases are")).toBeVisible()
  expect(asked).toEqual(["POST"])
})

// A recovery code on the sign-in page.
//
// The second factor was six boxes for digits, which cut a fourteen-character
// recovery code short and sent what was left as a wrong guess, so the codes the
// panel hands out "for when you lose your phone" could not be used. This is a
// second account, made here through an invitation, because turning two-factor
// on for the setup account would lock every test after this one out.
test("a recovery code signs in when the phone is gone", async ({ page }) => {
  const email = "phoneless@example.test"
  const password = "another reasonable passphrase"
  const codes = await accountWithTwoFactor(page, email, password)

  const attempts: string[] = []
  page.on("request", (request) => {
    if (request.url().endsWith("/api/auth/login")) attempts.push(request.method())
  })

  // The way to it is there in another language too, not only in English.
  const russian = localeStrings("ru").auth as Record<string, string>
  await page.goto("/")
  await page.evaluate(() => localStorage.setItem("skifity-language", "ru"))
  await page.reload()
  await page.locator("#email").fill(email)
  await page.locator("#password").fill(password)
  await page.locator('form button[type="submit"]').click()
  await expect(page.getByRole("button", { name: russian.useRecoveryCode })).toBeVisible()

  await page.evaluate(() => localStorage.setItem("skifity-language", "en"))
  await page.reload()
  await page.getByLabel(/^email$/i).fill(email)
  await page.getByLabel(/^password$/i).fill(password)
  await page.getByRole("button", { name: /^sign in$/i }).click()

  // Six digits in the boxes still send themselves.
  const boxes = page.locator('[data-slot="input-otp"]')
  await expect(boxes).toBeVisible()
  const before = attempts.length
  await boxes.pressSequentially("000000")
  await expect.poll(() => attempts.length, "six digits did not send themselves").toBe(before + 1)
  await expect(page.getByText("Email or password is not correct")).toBeVisible()

  // A recovery code waits for the button, however many characters are in.
  await page.getByRole("button", { name: "Use a recovery code instead" }).click()
  const field = page.getByLabel("Recovery code", { exact: true })
  await field.pressSequentially(codes[0].slice(0, 6))
  await page.waitForTimeout(1000)
  expect(attempts.length, "six characters of a recovery code were sent on their own").toBe(
    before + 1,
  )
  await field.pressSequentially(codes[0].slice(6))
  await page.getByRole("button", { name: /^sign in$/i }).click()

  await expect(page.locator('[data-slot="account-menu"]')).toBeVisible()
  await expect(page.getByText(`${codes.length - 1} codes are left`, { exact: false })).toBeVisible()
})

// Settings → Accounts, in every language, and the reason it exists: somebody
// who left could be taken out of each team, one at a time, and still sign in.
test("an administrator disables an account, and it cannot sign in", async ({ page, request }) => {
  const email = "leaver@example.test"
  const password = "a passphrase for leaving"
  const token = await invite(page, email)
  // Accepted on a request context of its own, so this page stays signed in as
  // the setup account.
  const accepted = await request.post(`/api/invitations/${token}/accept`, {
    data: { name: "Leaver", password },
  })
  expect(accepted.ok(), await accepted.text()).toBeTruthy()

  await page.goto("/settings?tab=accounts")
  for (const language of LANGUAGES) {
    await page.locator('[data-slot="language-switcher"]').click()
    await page.getByRole("menuitem", { name: language.name }).click()
    const words = localeStrings(language.code).accounts as Record<string, string>
    await expect(page.getByText(words.description), `the tab in ${language.name}`).toBeVisible()
    const body = (await page.locator("body").innerText()).trim()
    expect(body, `untranslated key visible in ${language.name}`).not.toMatch(
      /\baccounts\.[a-zA-Z]+\b/,
    )
  }
  // The last switch was to Chinese; English again, saved to the account, so
  // what follows reads it.
  const saved = page.waitForResponse(
    (response) => response.url().endsWith("/api/me") && response.request().method() === "PATCH",
  )
  await page.locator('[data-slot="language-switcher"]').click()
  await page.getByRole("menuitem", { name: "English" }).click()
  await saved

  const row = page.locator(`tr[data-account="${email}"]`)
  await row.getByRole("button", { name: /^Actions for/ }).click()
  await page.getByRole("menuitem", { name: "Disable" }).click()
  await page.getByRole("alertdialog").getByRole("button", { name: "Disable" }).click()
  await expect(row.getByText("Disabled", { exact: true })).toBeVisible()

  // The session the invitation started is over, and the password opens
  // nothing: refused the way a wrong one is.
  expect((await request.get("/api/me")).status()).toBe(401)
  const refused = await request.post("/api/auth/login", { data: { email, password } })
  expect(refused.status(), await refused.text()).toBe(401)
})

/**
 * Makes a second account through an invitation from the setup account, turns
 * two-factor on for it, and leaves the browser signed out. Answers the
 * recovery codes.
 */
async function accountWithTwoFactor(page: Page, email: string, password: string) {
  const token = await invite(page, email)
  await page.context().clearCookies()
  const accepted = await page.request.post(`/api/invitations/${token}/accept`, {
    data: { name: "Phoneless", password },
  })
  expect(accepted.ok(), await accepted.text()).toBeTruthy()

  // Signed in a moment ago, so turning two-factor on does not ask again.
  const headers = await csrfHeaders(page)
  const started = await page.request.post("/api/me/totp", { headers, data: {} })
  expect(started.ok(), await started.text()).toBeTruthy()
  const setup = (await started.json()) as { secret: string; recovery_codes: string[] }
  const confirmed = await page.request.post("/api/me/totp/confirm", {
    headers,
    data: { code: totp(setup.secret) },
  })
  expect(confirmed.ok(), await confirmed.text()).toBeTruthy()

  await page.context().clearCookies()
  return setup.recovery_codes
}

/** Invites an address to the setup account's team, and answers the link's token. */
async function invite(page: Page, email: string): Promise<string> {
  await signIn(page)
  const me = await (await page.request.get("/api/me")).json()
  const invited = await page.request.post(`/api/teams/${me.teams[0].id}/invitations`, {
    headers: await csrfHeaders(page),
    data: { email, role: "member" },
  })
  expect(invited.ok(), await invited.text()).toBeTruthy()
  const { url } = (await invited.json()) as { url: string }
  return url.split("/invite/").pop()!
}

/** What a request that changes something carries, for this browser's session. */
async function csrfHeaders(page: Page) {
  const cookies = await page.context().cookies()
  return {
    "Content-Type": "application/json",
    "X-Skifity-CSRF": cookies.find((c) => c.name === "skifity_csrf")?.value ?? "",
  }
}

/** The six-digit code an authenticator app would show now, RFC 6238. */
function totp(secret: string): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
  let bits = ""
  for (const char of secret.replace(/[\s=]/g, "").toUpperCase()) {
    bits += alphabet.indexOf(char).toString(2).padStart(5, "0")
  }
  const key = Buffer.from((bits.match(/.{8}/g) ?? []).map((byte) => parseInt(byte, 2)))
  const counter = Buffer.alloc(8)
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30_000)))
  const mac = createHmac("sha1", key).update(counter).digest()
  const offset = mac[mac.length - 1] & 0x0f
  return String((mac.readUInt32BE(offset) & 0x7fffffff) % 1_000_000).padStart(6, "0")
}
test("a version that crashed says why first, and opens the tab that fixes it", async ({ page }) => {
  await signIn(page)

  // No cluster here to crash on, so the deployment is the panel's answer for
  // one: the history an out-of-memory crash leaves, as the API serves it.
  const cookies = await page.context().cookies()
  const headers = {
    "Content-Type": "application/json",
    "X-Skifity-CSRF": cookies.find((c) => c.name === "skifity_csrf")?.value ?? "",
  }
  const me = await (await page.request.get("/api/me")).json()
  const projects = await (await page.request.get(`/api/teams/${me.teams[0].id}/projects`)).json()
  const envs = await (
    await page.request.get(`/api/projects/${projects.items[0].id}/environments`)
  ).json()
  const created = await page.request.post(`/api/environments/${envs.items[0].id}/apps`, {
    headers,
    data: { name: "crashes", source_type: "image", image: "nginx:1.27", port: 80 },
  })
  expect(created.ok(), await created.text()).toBeTruthy()
  const appID = (await created.json()).app.id

  const at = new Date().toISOString()
  const crashed = {
    id: "dep_e2e_crashed",
    app_id: appID,
    number: 1,
    status: "failed",
    trigger: "manual",
    commit_sha: "",
    commit_message: "",
    commit_author: "",
    image: "nginx:1.27",
    build_fingerprint: "image:nginx:1.27",
    error_code: "deploy.crashed",
    error_message:
      "The new version stopped as it started: The new version of crashes stopped as it started. Stops so far: 1. The last ended with exit code 137 (OOMKilled).",
    error_hint: "The lines it printed before it stopped are in the deployment log.",
    created_by: "",
    created_at: at,
    started_at: at,
    finished_at: at,
    can_rollback: false,
    problem: {
      code: "deploy.crashed",
      title: "The new version stopped as it started",
      cause:
        "The new version of crashes stopped as it started. Stops so far: 1. The last ended with exit code 137 (OOMKilled).",
      args: { cause: ["crashes", "1", "137", "OOMKilled"] },
      severity: "error",
      retryable: false,
      at,
      context: {
        kind: "out_of_memory",
        exit_code: "137",
        reason: "OOMKilled",
        stops: "1",
        log_tail: "allocating the cache\nFATAL: out of memory",
      },
    },
  }
  await page.route(
    (url) => url.pathname === `/api/apps/${appID}/deployments`,
    (route) => route.fulfill({ json: { items: [crashed], total: 1 } }),
  )

  for (const language of LANGUAGES) {
    await page.goto(`/apps/${appID}?tab=deployments`)
    await page.locator('[data-slot="language-switcher"]').click()
    await page.getByRole("menuitem", { name: language.name }).click()

    const words = localeStrings(language.code)
    const failure = words.deploy.failure as Record<string, Record<string, string> | string>
    const common = words.common as Record<string, string>
    const shown = page.locator('[data-slot="deploy-failure"]')

    // The cause comes first after the headline, in the reader's language,
    // and says what it was rather than "did not start".
    await expect(shown.locator("p").nth(1), `the cause in ${language.name}`).toHaveText(
      (failure.crashed as Record<string, string>).out_of_memory,
    )
    await expect(
      shown.getByRole("link", { name: (failure.open as Record<string, string>).scaling }),
      `the button to Scaling in ${language.name}`,
    ).toBeVisible()
    await expect(
      shown.getByRole("button", {
        name: common.copyValue.replace("{{name}}", failure.diagnosis as string),
      }),
      `Copy diagnosis in ${language.name}`,
    ).toBeVisible()
  }

  // In English again, saved to the account, for every test after this one;
  // then the button goes where the memory limit is.
  const saved = page.waitForResponse(
    (response) => response.url().endsWith("/api/me") && response.request().method() === "PATCH",
  )
  await page.locator('[data-slot="language-switcher"]').click()
  await page.getByRole("menuitem", { name: "English" }).click()
  await saved

  await page.getByRole("link", { name: "Open Scaling" }).click()
  await expect(page).toHaveURL(/[?&]tab=scaling/)
})

test("an app from a public repository says pushes will not deploy it, and where to fix that", async ({
  page,
}) => {
  await signIn(page)

  // The quick start's own path: a public repository, no Git connection, and
  // deploy on push on by default. Nothing registers a webhook for it, and
  // that is said when the app is made rather than after a push does nothing.
  await page.goto(`/environments/${await firstEnvironment(page)}/apps/new`)
  await page.getByLabel("Repository", { exact: true }).fill("https://github.com/acme/public-site")
  await page.getByRole("button", { name: /^create and deploy$/i }).click()

  await expect(page.getByText("Pushes will not deploy this app yet")).toBeVisible({
    timeout: 30_000,
  })
  await expect(page).toHaveURL(/\/apps\/app_/)

  // The app's settings say the same where it is fixed, with the connection
  // to choose.
  const appID = page.url().split("/apps/")[1].split(/[/?#]/)[0]
  await page.goto(`/apps/${appID}?tab=settings`)
  await expect(page.getByText("No webhook is registered for this app")).toBeVisible()
  await expect(page.getByLabel("Git connection", { exact: true })).toBeVisible()
})

/** The environment first-run setup made: Production of the first project. */
async function firstEnvironment(page: Page): Promise<string> {
  const me = await (await page.request.get("/api/me")).json()
  const projects = await (await page.request.get(`/api/teams/${me.teams[0].id}/projects`)).json()
  const envs = await (
    await page.request.get(`/api/projects/${projects.items[0].id}/environments`)
  ).json()
  return envs.items[0].id
}

/** Signs in, unless this context already has a session. */
async function signIn(page: Page) {
  await page.goto("/")

  const email = page.getByLabel(/^email$/i)
  const accountMenu = page.locator('[data-slot="account-menu"]')

  // Wait for the app to decide which of the two it is showing. Checking
  // visibility without waiting races the first render and always says "no".
  await expect(email.or(accountMenu).first()).toBeVisible()

  if (await email.isVisible()) {
    await email.fill(EMAIL)
    await page.getByLabel(/^password$/i).fill(PASSWORD)
    await page.getByRole("button", { name: /^sign in$/i }).click()
  }
  await expect(accountMenu).toBeVisible()
}
