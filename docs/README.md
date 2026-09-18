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

Application code samples must use `<SdkTabs>` / `<SdkSnippet>` so readers can
switch Python, Go, Java, TypeScript, and Rust when `examples/rust` has the same
sample. Do not use per-language headings or stacked fenced blocks. `bash` /
`text` fences are exempt.

Product docs ship in **English** and **Simplified Chinese** (`zh-Hans`). The
navbar language switcher (top right) persists the choice in the browser. When you
change a page in `content/`, update the matching file under
`i18n/zh-Hans/docusaurus-plugin-content-docs/current/`.

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
* [ContinueAsNew in Temporal (or Cadence)](design/ContinueAsNew-in-Temporal-(or-Cadence)-workflow.md)
* Plans under [`design/plan/`](design/plan/)

## Archive

Legacy iWF-era wiki and case studies (source material only):

* [`archive/old-iwfwiki/`](archive/old-iwfwiki/)
* [`archive/old-iwf-case-study/`](archive/old-iwf-case-study/)

## Hosting

GitHub Pages via [`.github/workflows/deploy-docs.yml`](../.github/workflows/deploy-docs.yml).
