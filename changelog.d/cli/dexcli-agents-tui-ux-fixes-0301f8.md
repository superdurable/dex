---
section: Changed
---
- In the **dexcli agents-tui** chat, an idle **Ctrl-C** asks for a second
  press within two seconds before it exits. A first press still clears a
  draft. **Esc** on an empty input cancels the running turn, as **Ctrl-C**
  does; the status line names it.
- An approval or question that waited before the chat loaded the session
  stays in the pending bar until **Tab** opens it. One that arrives while the
  user types opens once the input stays empty for a second.
- In the pending bar, **Up** and **Down** pick the queued message that **s**
  and **x** act on. Any other letter returns to the input and is typed there.
- Typing in a question's choice list starts a free-text answer, when the
  question allows one.
- **write_todos** rows list the plan's steps as a checklist, and **Ctrl-P**
  shows the whole plan. **ask_user** rows show no raw result, and a waiting
  question is called a question.
- **/model** without an ID shows the current model and leaves **/model** in
  the input to type one; an ID with spaces is refused.
- The status line says "writing reply" while text streams. A stopped Worker
  dims the input. The welcome area keeps the session the chat started with,
  and the exit line names it after a switch.
- Paths under the home directory show as **~**. **/web** with the browser
  switched off shows the link as information.
