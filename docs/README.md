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

Every document has a sibling `.md` file: `/primitives/attribute/` becomes
`/primitives/attribute.md`, section indexes become `/primitives.md`, and the locale
home pages become `/index.md` and `/zh-Hans/index.md`. The robot link beside each
title opens that file in a new tab. Each locale's `llms.txt` lists document titles,
front matter descriptions, and absolute Markdown URLs. React-only custom pages,
such as `/cloud/`, are outside the content-docs export.

Exports start with YAML containing `title`, optional `description`, `canonical_url`
for the HTML page, repository-relative `source`, and `locale`. Internal document
links point to absolute Markdown URLs, preserve fragments, and resolve relative
MDX source links using the loaded version. Custom heading IDs become explicit
anchor elements. Image URLs are absolute.

Component expansion rules:

- `SdkTabs` and `SdkSnippet` emit a labelled fenced block for each language in
  source order, retaining example links and captions.
- `DocsFlowDefinitionGraph` reads its imported JSON and lists Steps and edges,
  with the source example link.
- The six diagram components require a `markdownDescription` at each use. Write
  one to three plain sentences describing the diagram, in both locales. The
  component also uses this description as its accessible image label.
- `DexSkillHostTabs` and the exporter read
  [`dex-skill-hosts.json`](src/data/dex-skill-hosts.json). Update this shared data
  when changing host installation instructions or invocation examples.
- Admonitions become labelled blockquotes. Image elements, links, explicit
  anchors, line breaks, and supported formatting wrappers become Markdown.

Unknown JSX and unevaluated MDX expressions fail the build with the source file
and line. To support a new component, add an explicit expansion in
[`export.mjs`](plugins/markdown-export/export.mjs), preserve its meaningful text
and links, and add an integration scenario in
[`markdown-export.mjs`](tests/markdown-export.mjs). Do not evaluate arbitrary MDX
JavaScript or silently omit a component. Run `npm run check`; its site audit
verifies every generated docs twin, source SDK snippets, absolute links, both
`llms.txt` indexes, and the existing search-index gates.

Markdown files are static output, not Docusaurus routes. They stay out of the
sitemap, remain crawlable in `robots.txt`, and do not add HTML alternate links.
GitHub Pages cannot supply per-file canonical or noindex headers; the export
records the HTML canonical in front matter. Pages may serve `.md` as
`text/markdown`, which some browsers download. Keep the `.md` extension and
verify the deployed response when reviewing hosting behavior.
