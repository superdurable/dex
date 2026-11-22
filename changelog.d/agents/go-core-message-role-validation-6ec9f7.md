---
section: Fixed
---
- A Go runner's message with a role other than user, assistant, or tool now fails its turn at once, with the model call's usage recorded and no retry, instead of being stored and breaking **get_agent_snapshot** and **list_agent_messages** for the rest of the session.
