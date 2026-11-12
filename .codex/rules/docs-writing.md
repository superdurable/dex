# Product documentation voice

Applies to `docs/content/` and matching Simplified Chinese pages under
`docs/i18n/zh-Hans/docusaurus-plugin-content-docs/current/`.

- Write plain, direct English (or natural 简体中文). Short sentences; no
  marketing or chatbot filler.
- Use Dex terms exactly: Flow, Step, Attribute, Channel, RPC, Timer.
- Write API names, method names, types, and identifiers in inline code in
  prose. Use **bold** for a term's first definition and for UI labels.
- Application snippets must use `<SdkTabs>` / `<SdkSnippet>`. Copy from
  runnable files under `examples/`. See `.cursor/rules/docs-examples.mdc`.
- Each paragraph, list item, and callout opens with what is true. A negation
  comes second, as a clarification. Cut a negation that only restates what the
  positive already implies, such as "dexcli does not run on Windows" after a
  macOS or Linux prerequisite. A real warning whose point is the negation keeps it:
  list its opening words under `positive_first_allow:` in the page's front
  matter, with a YAML comment that says why.
  `npm run lint:structure` (tests/docs-positive-first.mjs) enforces this.
- Docs pages never link to a README in the repository; link an example's folder.
  Clicking a sidebar group header opens that group's overview page, which is not
  listed again under it and is titled with the group's name; no item is labelled
  "Overview". Every page in the reading sequence (not a group overview, the home
  page, Get Started, or the Glossary) ends with one section named Next (下一步),
  and following its first link gives a linear reading order. The home page and
  Get Started link straight to the one page that does each job, with no ordered
  reading lists. `npm run lint:structure` (tests/docs-structure.mjs) enforces this.
- Keep English and `zh-Hans` locales in sync. See `.cursor/rules/docs-i18n.mdc`.

Cursor mirror: `.cursor/rules/docs-writing.mdc`.
