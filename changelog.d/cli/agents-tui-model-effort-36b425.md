---
section: Added
---
- **dexcli agents-tui**: **/model** without an ID opens a list of the
  command's suggested models with the current one marked; **/model ID** still
  takes any model ID. The new **/effort** opens the reasoning effort values the
  command offers, and **/effort LEVEL** checks the value against them;
  **default** returns to the model's default. Both change the session's
  settings from the next turn. The status line shows the reasoning effort when
  the session sets one. The lists come from dex-agents 0.7.0; with an older
  command, **/model** lists only its configured model and **/effort** says the
  command has no reasoning effort setting.
