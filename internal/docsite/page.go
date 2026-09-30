package docsite

import "time"

// versionTime is the modification time reported for embedded files. Embedded
// files have no timestamp of their own, and a zero time makes http.ServeContent
// skip conditional requests entirely, which is the behaviour we want: the
// content changes only when the binary does, and the ETag does the rest.
var versionTime = time.Time{}

// pageTemplate wraps rendered Markdown.
//
// The styling is inline and deliberately small. This has to render on a machine
// that cannot reach the internet, in a browser that may be the only one on the
// server, and it must not depend on the panel's own JavaScript bundle: the most
// likely reader is someone whose panel is not working.
const pageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>%s</title>
<style>
:root {
  --bg: #ffffff; --fg: #0f172a; --muted: #64748b; --line: #e2e8f0;
  --accent: #2563eb; --code-bg: #f1f5f9;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #0b1120; --fg: #e2e8f0; --muted: #94a3b8; --line: #1e293b;
    --accent: #60a5fa; --code-bg: #131c2e;
  }
}
* { box-sizing: border-box; }
body {
  margin: 0; background: var(--bg); color: var(--fg);
  font: 16px/1.65 ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto,
        "Noto Sans", "Noto Sans Devanagari", "Noto Sans SC", sans-serif;
}
header {
  border-bottom: 1px solid var(--line); padding: 0 16px;
  position: sticky; top: 0; background: var(--bg);
}
header div { max-width: 46rem; margin: 0 auto; padding: 14px 0; }
header a { color: var(--fg); font-weight: 600; text-decoration: none; }
main { max-width: 46rem; margin: 0 auto; padding: 32px 16px 96px; }
h1 { font-size: 1.9rem; line-height: 1.2; margin: 0 0 1rem; letter-spacing: -0.02em; }
h2 { font-size: 1.3rem; margin: 2.5rem 0 0.75rem; letter-spacing: -0.01em; }
h3 { font-size: 1.05rem; margin: 2rem 0 0.5rem; }
h2, h3 { scroll-margin-top: 4rem; }
p, ul, ol { margin: 0 0 1rem; }
a { color: var(--accent); }
code {
  background: var(--code-bg); padding: 0.1em 0.35em; border-radius: 4px;
  font: 0.875em/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
}
pre {
  background: var(--code-bg); padding: 14px 16px; border-radius: 8px;
  overflow-x: auto; margin: 0 0 1.25rem;
}
pre code { background: none; padding: 0; }
table { border-collapse: collapse; width: 100%%; margin: 0 0 1.25rem; font-size: 0.925rem; }
th, td { border: 1px solid var(--line); padding: 8px 10px; text-align: left; vertical-align: top; }
th { background: var(--code-bg); font-weight: 600; }
blockquote {
  margin: 0 0 1rem; padding-left: 1rem; border-left: 3px solid var(--line);
  color: var(--muted);
}
img { max-width: 100%%; height: auto; border-radius: 8px; border: 1px solid var(--line); }
hr { border: 0; border-top: 1px solid var(--line); margin: 2.5rem 0; }
.lead { color: var(--muted); font-size: 1.05rem; }
ul.index { list-style: none; padding: 0; }
ul.index li { border-top: 1px solid var(--line); padding: 14px 0; }
ul.index a { font-weight: 600; text-decoration: none; }
ul.index span { display: block; color: var(--muted); font-size: 0.925rem; }
</style>
</head>
<body>
<header><div><a href="%[3]s">%[2]s</a></div></header>
<main>
%[4]s
</main>
</body>
</html>
`
