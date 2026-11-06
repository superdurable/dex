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

Go, Python, TypeScript, and Java API HTML is generated per release and served
under `/api/<language>/`. Rust links to exact releases on docs.rs. Go is hosted
here because pkg.go.dev does not display the SDK's documentation under its
license.

### Sources and tools

[`api-reference-versions.json`](api-reference-versions.json) lists the newest
patch of each of the five retained minors, newest first. Output uses the minor
in its URL: `/api/go/1.35/` comes from `sdk-go/v1.35.0`.

- Go, Python, and TypeScript are generated from the release tag. The generator
  extracts the SDK with `git archive` into a temporary directory and removes it
  afterwards. It never checks out or changes the working branch.
- Go uses gomarkdoc and renders its Markdown with marked. gomarkdoc links
  symbols to its own named anchors. Headings get lowercase ids so its
  Constants and Variables links resolve. Generated code, internal packages,
  examples, tests, and contributor scripts are excluded.
- Python uses pdoc with Google-style docstrings on the tagged source. The exact
  published SDK wheel supplies its dependencies and native extension.
- TypeScript uses TypeDoc on the tagged source with the tag's locked
  dependencies. It does not compile the native extension.
- Java uses the Javadoc JAR that Maven Central publishes for the release
  (`io.superdurable:dex-sdk:<version>:javadoc`). The generator checks the JAR
  against Central's SHA-512 checksum before extracting it.

All tool pins live in [`scripts/api-reference-tools/`](scripts/api-reference-tools/):

- `go-tools.json`: gomarkdoc.
- `package.json` and `package-lock.json`: the Node tools and their dependencies.
- `pdoc-requirements.txt`: pdoc and its dependencies, with hashes. pip installs
  it with `--require-hashes --only-binary=:all:`. Regenerate it with the `uv`
  command in its header. Use an `--exclude-newer` date at least two weeks old.

Adopt only tool versions published at least two weeks earlier.

### Generate and audit locally

Use Node.js 22, Go 1.24 or newer, and Python 3.12 with pip and venv. Java needs
`unzip` and network access to Maven Central. Fetch tags first.

```bash
cd docs
npm run generate:api -- go sdk-go/v1.35.0
npm run generate:api -- python sdk-python/v1.35.0
npm run generate:api -- typescript sdk-typescript/v1.35.0
npm run generate:api -- java sdk-java/v1.35.0
npm run build
node scripts/assemble-api-reference.mjs --allow-missing-versions
npm run test:site
npm run test:api
# Generate, assemble, and require the entire retained window:
npm run generate:api -- --all
node scripts/assemble-api-reference.mjs
npm run test:api -- build/api --all
```

Set `DEX_API_PYTHON` to choose the Python interpreter. Set `DEX_API_OUTPUT` to
change the generator's output root. By default it writes undecorated HTML to
`.cache/api-reference/<language>/<minor>/`, which Git and Docker ignore.
Generated HTML is never committed.

### Assembly and indexing

`scripts/assemble-api-reference.mjs` copies the generated trees into
`build/api/` after the Docusaurus build. Everything that depends on the
manifest happens there, so a manifest change does not invalidate generated
output:

- `/api/<language>/<minor>/` for each manifest version. These pages are
  `noindex,follow`. Their canonical URL points to the page itself. A canonical
  to the latest page would contradict `noindex`, so search engines get one
  clear signal.
- `/api/<language>/latest/`, a copy of the newest version. It is the only
  indexable API tree. Each page has `index,follow` and a self-referencing
  canonical URL.
- A banner on every page names the SDK and version and links to the Developer
  reference. Versioned pages also link to the same page under `/latest/`, or
  to its root when the page no longer exists.

API HTML is English only. It is neither a Docusaurus route nor a sitemap entry.
The SDK reference indexes link to it with `pathname:///api/...` links. The
site's link wrapper keeps those links in the same tab and outside the locale
prefix, so previews and local builds stay local. Docusaurus does not check
`pathname://` links. Instead, `npm run check` verifies that each index links to
`/latest/` first and then to exactly the manifest's versions, and that no index
links to javadoc.io or an absolute docs URL. Without API output it accepts the
declared `/api/` entry URLs. When `build/api/` exists, the site smoke test also
runs the API audit: layout, indexing policy, canonicals, banners, local links,
anchors, and size.

### CI

The deploy workflow generates the latest version of each language on pull
requests, and all versions on pushes to main and manual runs. Each job fetches
only its release tag. Java fetches no tag. The cache key is the language,
version, tag commit, and a hash of the generator and the tool pins. It does not
include the manifest. Every job assembles and audits its output, even on a
cache hit. The build job then assembles all artifacts into the site, runs the
smoke test, requires every retained version outside pull requests, and checks
the site size against the GitHub Pages 1 GB limit.

### Advancing the version window

1. Add the new release at the front of its language's list in
   `api-reference-versions.json`, and remove the oldest one.
2. Update the API reference section of `reference/<language>/index.mdx` to
   match. `npm run check` fails until the links match the manifest.
3. Deploy. Assembly no longer publishes the retired version, so its URLs
   return the site's 404 page. That page detects
   `/api/<language>/<minor>/...` for a minor that is not in the manifest. It
   sends the reader to the same page under `/api/<language>/latest/`, keeping
   the query and fragment, or to `/reference/<language>/` when that page does
   not exist in the latest release.

Do not add `/api/` entries to `redirects.json`. Its redirects must target
Docusaurus routes, and API pages are not routes.

### Known warnings

TypeDoc can report unresolved links to JavaScript built-in error types or
private source declarations. These render as text. The API audit checks every
emitted local link and anchor.

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
