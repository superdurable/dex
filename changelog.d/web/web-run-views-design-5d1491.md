---
section: Changed
---
- The **Events** switch on the run page has a third option, **Design**, on
  every run. Without a loaded design for the Flow type it says why and links to
  the docs. **?view=design** opens it.
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
  **ResumeTurn-1 · WaitFor completed · #8 · 44ms before**.
- **Configuration** edits open on the current value and keep **Save** off until
  the value changes. A smaller retention window warns before it deletes older
  events, and Attribute sync turns off only through an explicit choice.
  **Start input** no longer repeats the start-time Flow configuration.
- The rail's Dex mark says **Dex Server unreachable** in red when Dex Web cannot
  reach the server, and keeps a faint dot otherwise.
- The **Conversation** tab drops its Working as banner and selector. Controls
  that Working as blocks offer **Work as** in place, and a line by the message
  box reads **Acting as** with **Change**.
- The horizontal Timeline gives each event its own label again, slanted at 30
  degrees, and draws the overview above the lane. A page that fits the strip
  puts each overview bar under its event dot; a longer page packs events
  closer, and its overview window is tinted, outlined, and dims the rest.
- The Timeline's label band is as tall as the page's longest label needs.
