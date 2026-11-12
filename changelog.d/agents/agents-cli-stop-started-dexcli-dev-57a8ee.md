---
section: Changed
---
- Dex Agents CLI commands: a **dexcli dev** that a command started for **dex.server: auto** now stops, with its Dex Web, when the command exits, including on SIGTERM and SIGHUP. A reused **dexcli dev** keeps running. To share one Dex server across commands, run **dexcli dev** yourself first or set **dex.server**. Sessions stay on disk, so a later command can **--resume** them. A command killed with SIGKILL leaves its **dexcli dev** running; stop it with **kill**. [The local Dex server](/reference/agents/cli#dex-server).
