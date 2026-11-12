---
section: Fixed
---
- The **dexcli agents-tui** chat shows the first reply after **/new** or
  **/sessions**, and counts the new session's turns and usage from zero.
- A reply that follows a tool call prints below its tool row, whole, even
  when the session reports the finished row after the reply's text.
- A finished reply keeps single line breaks, as it showed while streaming,
  so it no longer jumps when the turn ends.
- A resumed chat no longer labels the user's own queued messages as from
  another client, and marks the cancelled turns of its history.
- A cancelled turn says that the reply is incomplete only when part of it
  was shown.
- When the Dex server becomes unreachable, the chat says so once, instead
  of reporting that the session has not loaded.
- **Ctrl-Z** suspends the chat once. Before, the stop signal could come back
  and release the terminal a second time, and keys typed then were lost.
