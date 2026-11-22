---
section: Fixed
---
- A Go session at the server's **limits.maxFlowStateBytes** now ends with the reason and status **failed**, as Python and TypeScript sessions do, instead of going on after a model answer or tool result that did not fit. The Go **AgentSessionClient** reports the failure even when the session had no room to store it, and a message sent to the failed session returns **AgentSessionFailedError** with the reason.
