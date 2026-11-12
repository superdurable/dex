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
- Keep English and `zh-Hans` locales in sync. See `.cursor/rules/docs-i18n.mdc`.

Cursor mirror: `.cursor/rules/docs-writing.mdc`.
