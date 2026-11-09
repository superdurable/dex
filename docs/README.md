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

`npm start` and `npm run serve` use `docusaurus serve`, which returns 404 for
the index of an archived Developer Reference version, such as
`/reference/web/1.29.x/`. It treats `1.29.x` as a file name with an extension.
Other archived pages and GitHub Pages are not affected. To review archived
indexes, serve the build with a static server that redirects directories to
their trailing slash, as GitHub Pages does:

```bash
npm run build
python3 -m http.server 3000 --directory build
```

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

Feature guides include [durable SubFlows](content/concepts/primitives/subflow.mdx).

## Reference versions and release snapshots

Edit only the current reference content under `reference/<component>/`.
`reference/versions.json` records the latest released version for each component.
SDKs, CLI, and Web use minor versions such as `1.35.x`. Server uses each
`0.1.N` release before 1.0, then minor versions. Release train numbers can skip
for a component; never invent a missing minor.

The left sidebar selector uses Docusaurus's version metadata. Choosing a
version opens it immediately. The
selector keeps the document ID when switching versions and falls back to the
target version's index when that document is absent. It keeps the URL anchor
when the target page has that anchor, and drops it otherwise. The selector also
appears in the mobile docs drawer. Archived pages link back to the latest
matching page in a banner. Reference instances hide the Docusaurus version
badge, because the selector already shows the version. Current routes stay at
`/reference/<component>/`; archived routes add the version, such as
`/reference/web/1.32.x/`. The Chinese shell serves the same English reference
content with translated selector labels.

To capture a released component, run from this directory:

```bash
node scripts/snapshot-reference.mjs web 1.36.0
node scripts/snapshot-reference.mjs server 0.1.21
```

The script runs `docusaurus docs:version:reference-<component>`, updates the
latest label, and keeps five released snapshots, including the latest. It
then points the new snapshot's links to its own component at the same version:
`/reference/server/workers` becomes `/reference/server/0.1.20/workers`. A
reader of an archived version stays in that version. Anchors and trailing
slashes are kept. Links to other components, generated API references, and
other site routes still point to their current pages. The
snapshot matching the latest label is stored but hidden: current content serves
that release at the permanent unversioned route. When a newer release arrives,
that stored snapshot becomes an archived version. A patch release within the
latest minor, or a repeated run, replaces the hidden snapshot and its sidebar
with the current content. The script rejects versions older than the latest
label.

### Bootstrap: the hidden latest snapshot

A new version needs the hidden snapshot of the current latest label, because
that snapshot becomes the archived version. Current content already describes
the new release, so the script cannot rebuild the old one from it. When the
hidden snapshot is missing, the script fails and names the command that creates
it. Running the script with the latest label itself creates the hidden snapshot:

```bash
node scripts/snapshot-reference.mjs web 1.35.x
```

Every component needs a hidden snapshot equal to its label in
`reference/versions.json`, made from that release's reference content. The
historical backfill created these snapshots together with the older versions.
A new component needs one before its first release snapshot. Until then, the
release snapshot job fails for that component without affecting the release.

### Editing archived snapshots

Snapshots use Docusaurus's `reference-<component>_versions.json`,
`reference-<component>_versioned_docs/`, and
`reference-<component>_versioned_sidebars/` files. Archived snapshots change
only to fix a factual error or a broken link. Backfill must use the actual
releases rather than relabeling current content.

Moving or renaming a page can break links inside archived snapshots. The new
redirect does not fix them: client redirects are not routes, and the build
fails on broken links. After adding the redirect, retarget every existing
redirect whose destination is the old URL, in both locales. Pruning redirects
often point at current pages. Then rewrite the moved URL in every retained
snapshot and run `npm run check`:

```bash
node scripts/snapshot-reference.mjs --rewrite-links /concepts/old-page /concepts/new-page
```

The rewrite matches whole paths in Markdown, MDX, and versioned sidebar files.
It keeps anchors and trailing slashes and leaves current content unchanged. The
site audit accepts a link on an archived page when it resolves to a route or a
declared redirect. Current pages must still link to routes directly.

Snapshots taken by the script already have version-local links. To apply the
same rewrite to every stored snapshot, including each hidden latest snapshot,
run:

```bash
node scripts/snapshot-reference.mjs --localize-links
```

It prints the number of rewritten links per component. Links that already name
a version stay unchanged, so a rerun rewrites nothing. A hidden snapshot links
to its own version route, which exists once a newer release archives it.

A version-local link to a page or anchor that the version lacks fails
`npm run check`. Fix the snapshot text: drop the link or the anchor. Do not
point it back to the current page, which describes a different release.

### Pruning and search indexing

Pruning removes obsolete snapshot files and adds direct redirects in both
locale shells. Each dropped page goes to the latest page with the same document
ID, or to the component index if that document no longer exists. Existing
redirects are retargeted to avoid chains. Reference sidebars have no generated
category pages, so every route of a version is a document route and has a
redirect. Redirects are not routes, so the build still rejects a link to a
dropped version. Change logs link to older versions, for example. Pruning
therefore rewrites every such link in current content, the docs and i18n
sources, and the retained snapshots to the same redirect destination, without
its anchor. A `test:reference` case simulates the next release of every
component on the real tree and fails when a link to a pruned version remains,
so `npm run check` catches the problem before a release. Archived pages have self-canonical
URLs and Docusaurus's noindex metadata, and are excluded from the sitemap and
`llms.txt`. Their Markdown twins still exist. Current pages remain indexable.

### Release automation

After creating release tags, `release-changed-components.yml` snapshots the
selected SDK, CLI, and Web components. `server-zd-binary-release.yml` snapshots
the server after publishing a stable release (each `0.1.N`, then minor
versions from 1.0), starting from main rather than the tag. Prereleases are
skipped. Each job runs `npm run check` and pushes one
`automation/reference-snapshots-<run_id>-<attempt>` branch. It never pushes to
main. The job is `continue-on-error`, so a snapshot failure never fails the
release run.

The jobs cannot open the pull request themselves. The repository setting that
allows GitHub Actions to create pull requests is off. A pull request created
with `GITHUB_TOKEN` would also not trigger the required Check Gate CI. A GitHub
App token would remove both limits.

Until then, a person opens each pull request:

1. Open the release run's summary. It names the snapshot branch and gives a
   compare link.
2. Open the pull request from the compare link with the Summary, Why, and
   Testing sections.
3. Merge snapshot branches in release order. Each branch starts from main as
   it was at its release, so a later branch may miss or conflict with an
   earlier one. Merge the earlier branch first. If the later branch then
   conflicts, run its snapshot command by hand on the updated main instead.
   Rerunning the `release-changed-components.yml` job does not help, because it
   checks out the original release commit. Rerunning the
   `server-zd-binary-release.yml` job checks out the current main, so it picks
   up the merged earlier branch.

If a snapshot job fails, run the snapshot command above by hand and open its
pull request.

### Tests and upgrade risks

`npm run test:reference` checks version boundaries, retention, file pruning,
page fallback, locale redirects, patch replacement, the missing-bootstrap error,
link rewrites (including angle-bracket destinations), version-local links in
archived and hidden snapshots, and isolation between instances. `npm run check` also builds both
locale shells and audits the selectors, archived routes, banners, canonicals,
robots metadata, redirects, sitemaps, and `llms.txt`.

The snapshot script and the site audit match Docusaurus's own document IDs and
slugs. They import internal modules of `@docusaurus/plugin-content-docs`:
`lib/slug.js` and `lib/numberPrefix.js`. The selector imports
`useNavbarMobileSidebar` from `@docusaurus/theme-common/internal`. These are not
public APIs. Check them, and run `npm run check` and the snapshot tests, when
upgrading Docusaurus.

## Route and search-index integrity

Published documentation URLs are permanent. When moving, renaming, or deleting
a page, add a direct redirect for every affected locale to
[`redirects.json`](redirects.json). Point each old URL to the closest successor,
not the home page, and update internal links to use the destination directly.
Links inside archived reference snapshots follow
[Editing archived snapshots](#editing-archived-snapshots) instead.

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
  published SDK wheel supplies its native extension. The wheel and its runtime
  dependencies come from the hashed lock for that version.
- TypeScript uses TypeDoc on the tagged source with the tag's locked
  dependencies. It does not compile the native extension.
- Java uses the Javadoc JAR that Maven Central publishes for the release
  (`io.superdurable:dex-sdk:<version>:javadoc`). The generator checks the JAR
  against Central's SHA-512 checksum before extracting it.

All tool pins live in [`scripts/api-reference-tools/`](scripts/api-reference-tools/):

- `go-tools.json`: gomarkdoc.
- `package.json` and `package-lock.json`: the Node tools and their dependencies.
- `pdoc-requirements.in`: the pdoc pin.
- `python/<version>.txt`: one hashed lock per Python SDK version. Each lock
  covers pdoc, the SDK wheel, and all their dependencies. pip installs it in
  one step with `--require-hashes --only-binary=:all:`. Regenerate every lock
  with `lock-python-dependencies.sh <cutoff date>`, using a cutoff at least two
  weeks old. The cutoff applies to every package except the SDK release, which
  is pinned exactly. `npm run check` fails when the locks do not match the
  manifest or a requirement has no hash.

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
3. For Python, run `scripts/api-reference-tools/lock-python-dependencies.sh`
   to regenerate the per-version locks.
4. Deploy. Assembly no longer publishes the retired version, so its URLs
   return the site's 404 page. That page detects
   `/api/<language>/<minor>/...` for a minor that is not in the manifest. It
   sends the reader to the same page under `/api/<language>/latest/`, or to
   `/reference/<language>/` when that page does not exist in the latest
   release. Both keep the query and fragment. `tests/api-reference-redirect.mjs`
   covers this logic in `src/apiReferenceRedirect.mjs`.

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

## Markdown exports

The local [`markdown-export`](plugins/markdown-export/) plugin captures all loaded
content-docs instances in `allContentLoaded` and writes Markdown in `postBuild`.
It reads MDX source and loaded document metadata, including each version's routes
and each locale's effective source file. Missing translations use the same
English fallback as the HTML page. Instance IDs and route prefixes are unrestricted.

Every document has a sibling `.md` file: `/concepts/primitives/attribute/` becomes
`/concepts/primitives/attribute.md`, section indexes become `/concepts.md`, and the locale
home pages become `/index.md` and `/zh-Hans/index.md`. The exporter, the robot link,
and the site audit share this mapping in [`markdownPath.mjs`](src/markdownPath.mjs).
The robot link follows each page title and opens that file in a new tab. React-only
custom pages, such as `/cloud/`, are outside the content-docs export.

Each locale's `llms.txt` starts with the site title and a one-line summary. Its
sections follow the docs sidebar: one section per top-level category, in sidebar
order, and pages outside every sidebar come last. Each entry has the page title,
the absolute Markdown URL, and the page description. Only the current version of
each versioned docs instance is listed; archived reference versions keep their
Markdown twins but stay out of `llms.txt`.

Exports start with YAML containing `title`, `description`, `canonical_url` for the
HTML page, repository-relative `source`, and `locale`. The description is the HTML
meta description: the front matter `description`, or the excerpt Docusaurus
derives from the page. Internal document links point to absolute Markdown URLs,
preserve fragments, and resolve relative MDX source links using the loaded
version. Custom heading IDs become explicit anchor elements. Image URLs are
absolute.

Component expansion rules:

- `SdkTabs` and `SdkSnippet` emit a labelled fenced block for each language in
  the page tab order (Python, Go, Java, TypeScript, Rust), with example links.
  A fence keeps its own language; a fence without one gets the SDK language.
  `SdkTabs` language props such as `go="..."` must be string literals.
- `DocsFlowDefinitionGraph` reads its imported JSON. It lists the start Step, each
  Step with its WaitFor conditions and its terminal decisions, and every edge.
  Decision conditions appear on the decision. It ends with the source example link.
- The six diagram components require a `markdownDescription` at each use. Write
  one to three plain sentences describing what the diagram draws, in both locales.
  The component also uses this description as its accessible image label.
- `DexSkillHostTabs` and the exporter read
  [`dex-skill-hosts.json`](src/data/dex-skill-hosts.json). Update this shared data
  when changing host installation instructions or invocation examples. Host
  headings sit one level below the surrounding section. Both sides accept the
  same content types: `p`, `h1` to `h6`, `ol`, `ul`, `li`, `code`, and
  `invocations`.
- Admonitions become blockquotes with a localized bold label. All Docusaurus
  keywords are supported: `note`, `tip`, `info`, `warning`, `danger`, `caution`,
  `important`, `secondary`, and `success`. Any other keyword, a stray `:::`, or an
  unclosed admonition fails the build.
- Image elements, links, explicit anchors (`<a id>` keeps its text), line breaks,
  and supported formatting wrappers become Markdown.

The exporter ignores unknown attributes on known tags; it reads only the
attributes listed above. Unknown JSX, unevaluated MDX expressions (including
`{/* */}` comments), ESM other than imports, and non-literal attribute values
fail the build with the source file and line. Relative `.mdx` links must resolve
to a loaded document, and root-relative links must resolve to a route or a
static file. Plugins run `postBuild` concurrently, and other output, such as
generated API reference, may be written by another plugin or a CI step. Links
under the prefixes in `unverifiedLinkPrefixes` in
[`export.mjs`](plugins/markdown-export/export.mjs), currently `/api/`, are
accepted without that check. Add a prefix there only for output that the docs
build does not produce itself. To support a new component, add an explicit expansion in
[`export.mjs`](plugins/markdown-export/export.mjs), preserve its meaningful text
and links, and add an integration scenario in
[`markdown-export.mjs`](tests/markdown-export.mjs). Do not evaluate arbitrary MDX
JavaScript or silently omit a component. Run `npm run check`; its site audit
verifies every generated docs twin, SDK snippet order and fence languages,
descriptions, absolute links, the robot link, both `llms.txt` indexes, and the
existing search-index gates.

Markdown files are static output, not Docusaurus routes. They stay out of the
sitemap, remain crawlable in `robots.txt`, and do not add HTML alternate links.
GitHub Pages cannot supply per-file canonical or noindex headers; the export
records the HTML canonical in front matter. Pages may serve `.md` as
`text/markdown`, which some browsers download. Keep the `.md` extension and
verify the deployed response when reviewing hosting behavior.
