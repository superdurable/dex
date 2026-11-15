---
section: Changed
---
- **dexcli agents-tui**: pasting image paths, or dropping image files on the terminal, attaches them at once, without the **Attach NAME?** question, also when the input holds text. One paste may name several files, quoted or with backslash-escaped spaces. A pasted path that does not attach stays as text. When the paste also names an image, or the path ends in an image extension, a notice says why: not a PNG, JPEG, GIF, or WebP image, missing, or unreadable. A paste while a panel or list has focus closes it and goes to the input instead of being lost. [Reference](/reference/agents/cli#images).
- **dexcli agents-tui**: the notice for an image over the size limit gives both sizes in exact bytes, such as "an image may have at most 3,750,000", instead of a rounded "3.6 MB".
