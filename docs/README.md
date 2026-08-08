# Dex documentation

Product docs are a **Docusaurus** site in this directory, published to
[https://docs.superdurable.io](https://docs.superdurable.io).

## Local preview

```bash
cd docs
npm install
npm start
```

Build:

```bash
npm run build
npm run serve
```

Published pages live under [`content/`](content/). Site config:
`docusaurus.config.ts`, `sidebars.ts`, `src/`.

## Contributor design notes

Engineering design docs (not in the public sidebar):

* [Dex Design](design/Dex-Design.md)
* [IDL renames (OpenAPI → dex.proto)](design/idl-renames.md)
* [ContinueAsNew in Temporal (or Cadence)](design/ContinueAsNew-in-Temporal-(or-Cadence)-workflow.md)
* Plans under [`design/plan/`](design/plan/)

## Archive

Legacy iWF-era wiki and case studies (source material only):

* [`archive/old-iwfwiki/`](archive/old-iwfwiki/)
* [`archive/old-iwf-case-study/`](archive/old-iwf-case-study/)

## Hosting

GitHub Pages via [`.github/workflows/deploy-docs.yml`](../.github/workflows/deploy-docs.yml).
DNS / GoDaddy steps: [Hosting on GitHub Pages](content/references/hosting-github-pages.mdx).
