---
section: Added
---
- The **dexcli agents-tui** chat opens a tool's whole stored output in a
  pager with **Ctrl-O**, or picks one of the latest turn's tools with
  **/tools**. **/history** pages through the messages stored before the ones
  a resumed chat shows. The pager scrolls with **j**/**k** and the page keys,
  searches with **/**, and closes with **q**.
- Replies render tables, strikethrough, and task lists. Tool rows count the
  images a tool returned.
- The approval popup shows what a call acts on, what it does, and the diff
  of a file write, when the tool's source describes them; otherwise it shows
  the concrete arguments.
- Pasting the path of an image file, as a terminal does for a dropped file,
  asks whether to attach it. **Tab** completes the path after **/image** and
  keeps its quotes or backslash escapes; **/image** reads them the same way.
- The welcome area shows the Dex logo when the terminal is large enough.
- **/run** shows as unavailable in the command list, with the reason, until
  the session waits for approval of a draft plan.
