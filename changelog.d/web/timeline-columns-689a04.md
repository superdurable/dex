---
section: Changed
---
- The Timeline lane has two label modes, picked from **Dense** and **Tidy** tabs
  at the left end of the lane. **Dense**, the default, slants each event's label
  at 45 degrees. **Tidy** shows each label straight above its dot, wrapped at
  word boundaries in up to three lines; identifiers read as words, such as
  **Send User Message** for **send_user_message**, and the tooltip keeps the
  exact name. The browser remembers the mode, and a short note introduces both
  modes the first time the Timeline opens.
- A page wider than the Timeline lane shows which events are in view. A page
  that fits beside the run's end note shows every event from the first one, and
  an event scrolled past the lane's left edge shows no cut-off label.
- The run page opens on the tab chosen last, on any run. An agent session opens
  on **Conversation** only when no tab was chosen yet.
- The selected event's panel gives Input and Output equal width and Context a
  narrow column. Each value sits under its name; short values wrap beside it,
  and objects, including JSON stored as a string, are pretty-printed and capped
  at seven lines with **Copy** and **Expand**. Channel conditions read as counts
  such as "exactly 1" or "1 or more". Context times show milliseconds, add the
  date only when it differs from the event's, and show the full timestamp on
  hover.
