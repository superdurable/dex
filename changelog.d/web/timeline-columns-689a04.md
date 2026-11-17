---
section: Changed
---
- The Timeline lane shows each event's label straight above its dot, wrapped at
  word boundaries in up to three lines, instead of slanted. Identifiers read as
  words, such as **Send User Message** for **send_user_message**, and the tooltip
  keeps the exact name. Columns are 64 to 160 px wide, and a page wider than the
  lane shows how many events are in view.
- The selected event's panel gives Input and Output equal width and Context a
  narrow column. Each value sits under its name; objects, including JSON stored
  as a string, are pretty-printed and capped at seven lines with **Copy** and
  **Expand**. Channel conditions read as counts such as "1 or more", and Context
  times show milliseconds without the date.
