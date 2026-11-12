---
section: Added
---
- **dexcli agents-tui** also runs the **sessions** and **connectors** modes of
  terminal UI contract 1. The **sessions** mode is a full-screen, paged list of
  a command's sessions. It shows the title, last activity, and turn count when
  the sessions have them, and only the ID, status, and start time otherwise.
  **/** filters the loaded sessions and searches titles in Dex. Enter on a
  running session hands it back to the command to resume. The
  **connectors** mode lists the connections of the connector tools, refreshes
  them with **r**, and opens the Dex Web Connectors page with **a**.
- In the agents chat, **/sessions** opens the same session list and switches
  the chat to the picked session with its history. Before **/sessions** or
  **/new** switches, the chat asks whether to drop attached images, and it
  waits for the current session's requests to finish.
