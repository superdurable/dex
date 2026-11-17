---
section: Fixed
---
- The Run page offers **Clone fork…** and **Clone fork from here…** on every run when the server supports clone fork, including a Flow whose type has no uploaded Flow definition. Before, Dex Web hid them unless a loaded definition showed the Flow was not an agent session.
- A clone fork of an agent session applies the session rules: it deletes **SessionTerminalLease** and **SessionLastTerminalEnvironment**, and a session on its own session task queue moves the new Flow to the task queue derived from the new Flow ID. Dex Web identifies a session by its Flow definition or by its own session task queue.
