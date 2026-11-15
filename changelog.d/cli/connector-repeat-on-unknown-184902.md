---
section: Added
---
- Flow analysis accepts the **agent.repeatOnUnknown** and **agent.maxSends** fields of a Connector package's **dex-connector.json**, and the published manifest schema declares them.
- **dexcli agents-tui** reads a session whose tool call has the **bounded_write** effect from dex-agents 0.6.12, and labels it a write.
