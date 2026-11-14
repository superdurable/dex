---
section: Changed
---
- The **Events** switch on the run page has a third option, **Design**, when the
  run's Flow type has a loaded design. **?view=design** opens it.
- **Graph** and **Design** draw left to right in the strip's place, with the
  same selected event panel below. Switching views keeps the panel, the pager,
  and the selected event in place.
- Every run has the tabs **Events**, **State**, **Conversation**, **Actions**,
  and **Streams**, in that order, without the word **Experimental**. When
  **Conversation** or **Actions** has nothing to show, its panel says why and
  links to the docs. A Dex Agents session still opens on **Conversation**.
- Buttons, switches, and tabs change smoothly, and the switch's indicator
  slides. Reduced motion turns this off.
- **State** uses two columns on wide screens: live state on the left, and
  **Configuration**, **Identity**, **Start input**, **Completed outputs**, and
  **Queued steps** on the right. **Details** is now **Identity**.
- **Configuration** replaces **History settings**. It shows the run's settings in
  plain words, and while the run is active **Edit** changes the retention
  window, step durability, active step search, or Attribute stores.
- The selected event's related events read as **From** and **To**, and each
  link names only the other event, such as
  `ResumeTurn-1 · WaitFor completed · #8 · 44ms before`.
