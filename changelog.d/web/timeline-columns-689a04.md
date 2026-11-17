---
section: Changed
---
- The Timeline lane has two label modes, picked from **Dense** and **Tidy** tabs
  at the left end of the lane. **Dense**, the default, slants each event's label
  at 45 degrees; each label's width follows its own text, up to 280 px, and
  not the width of its event's slot. **Tidy** shows each label straight above its dot, wrapped at
  word boundaries in up to three lines; identifiers read as words, such as
  **Send User Message** for **send_user_message**, and the tooltip keeps the
  exact name. The browser remembers the mode, and a short note introduces both
  modes the first time the Timeline opens.
- A page wider than the Timeline lane shows which events are in view.
- The run page opens on the tab chosen last, on any run. An agent session opens
  on **Conversation** only when no tab was chosen yet.
- The selected event's panel gives Input and Output equal width and Context a
  narrow column. Each value sits under its name; objects, including JSON stored
  as a string, are pretty-printed and capped at seven lines with **Copy** and
  **Expand**. Channel conditions read as counts such as "1 or more", and Context
  times show milliseconds without the date.
