# Docs Rules

The repository root `CLAUDE.md` rules also apply. Read
`examples/CLAUDE.md` (FDG 2.0 Step Explanations) before changing the Flow
Definition Graph sources that docs diagrams use.

### Developer Reference Versions

This is the target layout. Phases P2 and P4 of the docs and skills cleanup
build it.

- Each component has its own versioned reference: the Go, Python, Java,
  TypeScript, and Rust SDKs, the server, cli, web, and the agents packages.
- The left sidebar of each reference has a version selector. It defaults to
  the latest version.
- Only the current version is edited by hand. Each minor release snapshots it
  automatically. Older versions change only to fix a factual error or a
  broken link.
- The site keeps the last 5 versions of each reference.
- When a version leaves the site, redirect each of its URLs to the same page
  in the latest version. If that page no longer exists, redirect to the
  component's reference index. Docs URLs stay permanent.
- Each SDK, cli, web, and agents minor version has one reference version.
  Patch releases update that minor's reference. Before agents 1.0, each
  `0.N` agents minor is one version. Until server-zd 1.0, each
  `0.1.N` server-zd release is one version. From 1.0 on, server-zd is
  versioned by minor release.

### Docs Writing, Languages, and Markdown Export

These rules add to `.cursor/rules/docs-writing.mdc`,
`.cursor/rules/docs-i18n.mdc`, and `.cursor/rules/docs.mdc`.

- Page structure, voice, and the page rewrite steps follow the "Page structure
  and voice" section of `.cursor/rules/docs-writing.mdc`. The reasons are in
  `docs/design/plan/docs-revisit.md`. The Components pages sit in the
  sidebar's Run Dex group.
- Concept pages are the Concepts, Components, and Design pattern pages. Use
  short sentences there. Put one idea in each sentence.
- Concept pages contain no version numbers and no compatibility notes for
  older servers or SDKs.
- Say each thing once. Link to its single home instead of repeating it.
- Version differences appear only in the Developer reference. Mark each one
  "Since <component version>" or "Changed in <component version>". Use the
  version of the component the page documents, such as "Since 1.35" for an
  SDK, cli, or web, and "Since 0.1.17" for server-zd.
- Every changed page outside the Developer reference must be read in a
  browser before merge. This includes Intro, Quick start, Concepts,
  Components, Design patterns, and Use cases. Run `cd docs && npm start`,
  which builds and serves both locales. Read every such page from top to
  bottom as a reader, once in English and once in Simplified Chinese. Fix what
  you find. List the pages you read in the pull request's Testing section.
- Every docs change must work on a phone as well as a desktop. This covers
  content, components, styles, diagrams, and the sidebar. Check each changed
  page, or each page type a style or component change affects, at a 390px wide
  viewport. The page must not scroll sideways. Text stays readable without
  zooming. Wide tables and code scroll inside their own box. Diagrams fit or
  scroll inside their own box. The docs menu opens and reaches every page.
  List the phone checks in the pull request's Testing section.
- Every docs page outside the Developer reference ships in English and
  Simplified Chinese (`zh-Hans`). This includes Intro, Quick start,
  Concepts, Components, Design patterns, and Use cases. Keep both locales in
  sync.
- Chinese pages translate their title, sidebar label, and headings. Each
  Chinese heading keeps the English anchor as an explicit `{#anchor}`. Sidebar
  group labels are translated in the zh-Hans `current.json`, and each group's
  Chinese overview page uses that label as its title. `npm run lint:structure`
  checks the group labels.
- The Developer reference is English only. It means the versioned
  `reference-*` docs instances and the generated API references. When a page
  moves into the Developer reference, it drops its Chinese copy. Redirect the
  old `zh-Hans` URL like any other moved page.
- Every docs page has a Markdown twin, built by the local docs exporter. Each
  new MDX component needs an export rule in that exporter. The build fails on
  a component without one. Phase P1 of the docs and skills cleanup adds the
  exporter; this rule applies from then on.

## Maintainability (docs rules)

- Product docs in `docs/content/` must present application code in
  `<SdkTabs>` / `<SdkSnippet>` (Python, Go, Java, TypeScript; add Rust when
  `examples/rust` has the same sample). Do not use
  per-language headings or stacked fenced blocks. `bash` / `text` fences
  are exempt.
- Treat every published docs URL as permanent. Before moving, renaming, or
  deleting a page, add direct entries to `docs/redirects.json` for every
  affected locale. Redirect to the closest successor, never an unrelated page
  or the home page. Do not create redirect chains or loops. Update internal
  links to use the destination directly.
- Docs pages never link to a README in the repository. User-facing content,
  including reference, lives in the docs; a README keeps developer notes and
  links to the docs. Link an example's folder, not its README. Clicking a sidebar
  group header opens that group's overview page, which is not listed again
  under it, and that page's title is the group's name; items keep their own
  titles, never "Overview". A group overview is a wayfinder: it links to
  child pages and sections and does not repeat their caveats, limits, API
  names, or term definitions; terms go in the Glossary. Headings are
  statements, not questions. Section openings keep only essential links;
  pointers elsewhere and caveats go at the end of the section or in Next.
  Charts use short labels, not sentences; green is Dex, blue a third-party
  service or framework, grey your own code. Every page in the reading sequence (not a group
  overview, the home page, or the Glossary) ends with one last
  section named Next (下一步 in Chinese); following its first link gives a
  linear reading order. The home page links straight to the one page that
  does each job, with no ordered reading lists. The Quick Start group is the
  one exception to the wayfinder rule: its header opens the Quick Start
  tutorial, which owns the install. `npm run lint:structure` enforces these rules.
- Each paragraph, list item, and callout opens with what is true. A negation
  comes second, as a clarification. Cut a negation that only restates what the
  positive already implies, such as "dexcli does not run on Windows" after a
  macOS or Linux prerequisite. A real warning whose point is the negation keeps it:
  list its opening words under `positive_first_allow:` in the page's front
  matter, with a YAML comment that says why.
  `npm run lint:structure` (tests/docs-positive-first.mjs) enforces this.
- Every docs change must preserve all four search-index integrity gates:
  routes and internal links resolve without accidental 404s; only declared
  legacy URLs redirect; each indexable page has one unique, self-referencing
  canonical URL; and robots directives, noindex/nofollow metadata, internal
  links, and the sitemap keep canonical pages crawlable.
- After any change under `docs/`, run `cd docs && npm run check`. This command
  must typecheck, build both locales, reject broken links, anchors, and duplicate
  routes, and pass the generated-site route, redirect, canonical, robots, and
  sitemap audit. Do not merge docs changes when it fails.
- Application snippets must be copied from runnable files under `examples/`.
  Link the example (and the examples playground when the sample has HTTP and
  is catalogued). Do not invent APIs; add the example first.
  See `.cursor/rules/docs-examples.mdc`.
- Whenever runnable code for a documented design pattern or product example is
  added or changed, run `cd docs && npm run generate:flow-definitions`. Review
  and commit the affected JSON under `docs/src/data/flow-definitions/` in the
  same change, even when the MDX import is unchanged.
- Draw every Flow design diagram in docs with `DocsFlowDefinitionGraph`. It
  renders Flow Definition Graph 2.0 JSON the way Dex Web's Design tab does.
  Generate that JSON from a runnable file under `examples/` by adding the source
  to `docs/scripts/generate-flow-definitions.sh`. Do not add Step graph images
  or hand-written definitions. Every Step in such a source declares one
  `dex:group` and one `dex:explanation` directive.
