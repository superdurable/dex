# Product documentation voice

Applies to `docs/content/` and matching Simplified Chinese pages under
`docs/i18n/zh-Hans/docusaurus-plugin-content-docs/current/`.

- Write plain, direct English (or natural 简体中文). Short sentences; no
  marketing or chatbot filler.
- Use Dex terms exactly: Flow, Step, Attribute, Channel, RPC, Timer.
- **Do not** wrap API names, method names, types, or identifiers in inline
  backticks in prose. Use **bold** instead.
- Fenced code blocks and `bash` / `text` fences are exempt.
- Enforced by `make docs-prose-check` and git/Cursor/Codex hooks on commit.
- Application snippets must use `<SdkTabs>` / `<SdkSnippet>`. Copy from
  runnable files under `examples/`. See `.cursor/rules/docs-examples.mdc`.
- Keep English and `zh-Hans` locales in sync. See `.cursor/rules/docs-i18n.mdc`.

Cursor mirror: `.cursor/rules/docs-writing.mdc`.
