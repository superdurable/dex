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

## Reference versions and release snapshots

Edit only the current reference content under `reference/<component>/`.
`reference/versions.json` records the latest released version for each component.
SDKs, CLI, and Web use minor versions such as `1.35.x`. Server uses each
`0.1.N` release before 1.0, then minor versions. Release train numbers can skip
for a component; never invent a missing minor.

The left sidebar selector uses Docusaurus's version metadata. It preserves the
document ID when switching versions and falls back to the target version's
index when that document is absent. The selector also appears in the mobile
docs drawer. Archived pages link back to the latest matching page in a banner.
Current routes stay at `/reference/<component>/`; archived routes add the
version, such as `/reference/web/1.32.x/`. The Chinese shell serves the same
English reference content with translated selector labels.

To capture a released component, run from this directory:

```bash
node scripts/snapshot-reference.mjs web 1.36.0
node scripts/snapshot-reference.mjs server 0.1.20
```

The script runs `docusaurus docs:version:reference-<component>`, updates the
latest label, and keeps five released snapshots, including the latest. The
snapshot matching the latest label is stored but hidden: current content serves
that release at the permanent unversioned route. When a newer release arrives,
that stored snapshot becomes an archived version. Repeating a release or
releasing a patch within the same minor does not duplicate its snapshot.

Snapshots use Docusaurus's `reference-<component>_versions.json`,
`reference-<component>_versioned_docs/`, and
`reference-<component>_versioned_sidebars/` files. Older snapshots change only
to correct factual errors. The initial site has no historical snapshots;
backfill must use the actual releases rather than relabeling current content.

Pruning removes obsolete snapshot files and adds direct redirects in both
locale shells. Each dropped page goes to the latest page with the same document
ID, or to the component index if that document no longer exists. Existing
redirects are retargeted to avoid chains. Archived pages have self-canonical
URLs and Docusaurus's noindex metadata, and are excluded from the sitemap.
Current pages remain indexable.

After creating release tags, `release-changed-components.yml` snapshots only
the selected SDK, CLI, and Web components, runs `npm run check`, and opens one
pull request with the snapshots and latest-version updates. It pushes an
`automation/reference-snapshots-*` branch and never pushes to main. The
repository must allow GitHub Actions to create pull requests. Server releases
use a separate workflow; run the server snapshot command above when preparing
its reference update. The automation does not alter component publishing.

`npm run test:reference` checks version boundaries, retention, file pruning,
page fallback, locale redirects, idempotence, and isolation between instances.
`npm run check` also builds both locale shells and audits the selectors,
archived routes, banners, canonicals, robots metadata, redirects, and sitemaps.

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
