---
section: Changed
---
- **dex-agents-pi** keeps Pi's own **read**, **grep**, **find**, **ls**, **write**, **edit**, and **bash** with Pi's behavior, each call in its own tool Step, and adds the Connector, web fetch, browser, and **ask_user** tools. **grep** and **find** run **rg** and **fd**, which Pi downloads when they are missing. Connector writes always wait for approval. A Connector tool whose name a Pi tool has gets the prefix **connector_**. [Reference](/reference/agents/cli#harness-tools).
