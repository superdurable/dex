# Dex documentation

Product docs are a **Docusaurus** site in this directory, published to
[https://docs.superdurable.io](https://docs.superdurable.io).

## Local preview

```bash
cd docs
npm install
npm start
```

`npm start` builds and serves both English and Simplified Chinese. Do not run
`docusaurus start` for review: it only serves English.

Published pages live under [`content/`](content/). Site config:
`docusaurus.config.ts`, `sidebars.ts`, `src/`.

The Developer Reference lives under [`reference/`](reference/). Each component
(`server`, `cli`, `web`, `go`, `python`, `java`, `typescript`, `rust`) is its
own docs plugin instance with the id `reference-<component>`, the route
`/reference/<component>/`, and the sidebar file `reference/sidebars/<component>.ts`.
The landing page with the compatibility matrix is `content/reference/index.mdx`.
The reference pages are English only; the `zh-Hans` build serves the English
text at `/zh-Hans/reference/...`. The old `/production/server-zd-operations` and
`/production/pull-workers` pages are transition pages that keep their old
section ids and link each section to its new location. Do not add content to
them.

Application code samples must use `<SdkTabs>` / `<SdkSnippet>` so readers can
switch Python, Go, Java, TypeScript, and Rust when `examples/rust` has the same
sample. Do not use per-language headings or stacked fenced blocks. `bash` /
`text` fences are exempt.

Product docs ship in **English** and **Simplified Chinese** (`zh-Hans`). The
navbar language switcher (top right) persists the choice in the browser. When you
change a page in `content/`, update the matching file under
`i18n/zh-Hans/docusaurus-plugin-content-docs/current/`. The Developer reference
is the exception: it is English only. See `../.cursor/rules/docs-i18n.mdc`.

Runnable application samples live under [`examples/`](../examples/); see
[`examples/README.md`](../examples/README.md) and the playground
([`examples/playground/`](../examples/playground/)).

Flow diagrams render checked-in JSON with the shared
[`flow-definition-renderer`](../packages/flow-definition-renderer). Regenerate
the JSON after changing a referenced Python Flow:

```bash
cd docs
npm run generate:flow-definitions
```

Set `DEX_FLOW_PYTHON` when Python 3.11+ is not the default interpreter.

Feature guides include [durable SubFlows](content/primitives/subflow.mdx).

## Route and search-index integrity

Published documentation URLs are permanent. When moving, renaming, or deleting
a page, add a direct redirect for every affected locale to
[`redirects.json`](redirects.json). Point each old URL to the closest successor,
not the home page, and update internal links to use the destination directly.

Run the full production-site check after every docs change:

```bash
cd docs
npm run check
```

The check typechecks and builds both locales, rejects broken links, anchors, and
duplicate routes, and audits generated routes, redirects, canonical URLs,
robots directives, and the sitemap.

## Generated SDK API references

Go, Python, and TypeScript API HTML is generated from release tags. Java and
Rust link to exact releases on javadoc.io and docs.rs. Go is hosted here because
pkg.go.dev does not display the SDK's documentation under its license.

[`api-reference-versions.json`](api-reference-versions.json) lists the newest
patch in each of the five retained minors. Output uses the minor in its URL:
`/api/go/1.35/` is generated from `sdk-go/v1.35.0`. When advancing the window,
update this manifest and the SDK reference indexes together. Preserve retired
URLs with direct redirects before removing their output.

The generator uses `git archive` into a temporary directory and removes it on
success or failure. It never checks out or changes the working branch. It runs
these pinned tools, all published more than two weeks before their adoption:

| Tool | Version | Published |
| --- | --- | --- |
| gomarkdoc | 1.1.0 | 2023-06-17 |
| marked | 15.0.12 | 2025-05-20 |
| github-slugger | 2.0.0 | 2022-10-27 |
| pdoc | 15.0.4 | 2025-06-04 |
| TypeDoc | 0.28.14 | 2025-10-11 |
| TypeScript | 5.9.3 | 2025-09-30 |

The Node tools and their dependencies are locked in
[`scripts/api-reference-tools/package-lock.json`](scripts/api-reference-tools/package-lock.json).
The Go generator renders public packages, excluding generated code, internal
packages, examples, tests, and contributor scripts. Markdown heading IDs use
GitHub's slug convention so gomarkdoc's table of contents works in static HTML.
Python renders the public `dex` exports with Google-style docstrings. It installs
the exact published SDK wheel for its dependencies and native extension, then
documents the Python source from the tag. TypeScript reads the tagged source
with the tag's locked dependencies; it does not compile the native extension.

Use Node.js 22, Go 1.24 or newer, and Python 3.12 with pip and venv. Fetch tags
first. To build and audit locally:

```bash
cd docs
npm run generate:api -- go sdk-go/v1.35.0
npm run generate:api -- python sdk-python/v1.35.0
npm run generate:api -- typescript sdk-typescript/v1.35.0
npm run test:api
# Build and require the entire retained window:
npm run generate:api -- --all
npm run test:api -- .cache/api-reference --all
```

Set `DEX_API_PYTHON` to choose the Python interpreter. Set `DEX_API_OUTPUT` to
change the output root. By default HTML goes under `.cache/api-reference/`,
which is ignored by Git. Generated HTML is never committed.

`npm run check` skips generation and accepts only the declared API entry URLs.
This keeps routine docs checks independent of SDK toolchains and downloads.
The deploy workflow validates the latest tag of each language on pull requests,
and all 15 versions on pushes to main or manual runs. Each version is cached by
language, tag, tag commit, manifest, and generator/tool lockfile hash. Even cache
hits run the API audit. The workflow copies the artifacts into `build/api/`
after Docusaurus builds both locales, then audits the assembled site and checks
its complete size against the GitHub Pages 1 GB limit.

API HTML is English only and is not a Docusaurus route or a sitemap entry.
The site smoke test audits it separately when present. That audit checks local
links and anchors, unique self-referencing canonicals, version markers, output
size, and `noindex,follow` on older versions. The latest API pages allow indexing.
The normal docs route, redirect, canonical, hreflang, robots, and sitemap gates
continue to apply to every Docusaurus page. Absolute API links in the reference
indexes intentionally bypass Docusaurus's route checker; the smoke test checks
them against the manifest instead.

External hosts can lag publication. In particular, javadoc.io may redirect a
new version to its versions page even while Maven Central has its Javadoc JAR.
The Java index includes a tagged-source fallback. TypeDoc can report unresolved
links to JavaScript built-in error types or private source declarations; these
render as text, and the generated local HTML links are audited separately.

## Contributor design notes

Engineering design docs (not in the public sidebar):

* [Dex Design](design/Dex-Design.md)
* [Compact workflow-history wire names](design/compact-workflow-history-names.md)
* [IDL renames (OpenAPI → dex.proto)](design/idl-renames.md)
* Plans under [`design/plan/`](design/plan/)

## Archive

Legacy iWF-era wiki and case studies (source material only):

* [`archive/old-iwfwiki/`](archive/old-iwfwiki/)
* [`archive/old-iwf-case-study/`](archive/old-iwf-case-study/)
* [`archive/design/`](archive/design/): Temporal-based Go Server design notes

## Hosting

GitHub Pages via [`.github/workflows/deploy-docs.yml`](../.github/workflows/deploy-docs.yml).
