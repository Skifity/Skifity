// The browser's folder filter against the cases the Go packer is held to.
//
// `skifity up` and the panel's folder picker decide what is sent with two
// implementations of one rule: Go for the CLI, TypeScript for the browser.
// internal/cli/testdata/ignore-cases.json is the rule, and both are checked
// against it, so the two cannot drift apart without a build failing.
import { readFileSync } from "node:fs"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

import { ignoreFileNames, makeFolderFilter } from "../src/lib/ignore.ts"

const here = dirname(fileURLToPath(import.meta.url))
const fixture = JSON.parse(
  readFileSync(join(here, "../../internal/cli/testdata/ignore-cases.json"), "utf8"),
)

let failed = 0
for (const c of fixture.cases) {
  const ignoreFiles = new Map()
  for (const [path, body] of Object.entries(c.files)) {
    const slash = path.lastIndexOf("/")
    const dir = slash < 0 ? "" : path.slice(0, slash)
    const name = path.slice(slash + 1)
    const index = ignoreFileNames.indexOf(name)
    if (index < 0) continue
    const texts = ignoreFiles.get(dir) ?? new Array(ignoreFileNames.length).fill("")
    texts[index] = body
    ignoreFiles.set(dir, texts)
  }
  const keep = makeFolderFilter(ignoreFiles)
  const sent = Object.keys(c.files).filter(keep).sort()
  const want = [...c.sent].sort()
  if (JSON.stringify(sent) !== JSON.stringify(want)) {
    failed++
    console.error(`ignore: "${c.name}"\n  sent ${JSON.stringify(sent)}\n  want ${JSON.stringify(want)}`)
  }
}
if (failed > 0) process.exit(1)
console.log(`ignore: ${fixture.cases.length} cases agree with the Go packer.`)
