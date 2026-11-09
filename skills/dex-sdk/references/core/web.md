# Web (Dex Web)

Dex Web is the operator UI for Dex. It finds Flows, follows a run, shows its inputs, Attributes, and failures, and acts on it. It runs as its own process and uses the same Server API as Workers and Clients.

## What it shows

| Area | Use |
| --- | --- |
| Search and Summary | Find Flows and show their state. |
| Run page | A run's active Steps, Attributes, and Channels, and a Timeline of its events. The selected event shows inputs, outputs, Attribute changes, and failures with stack traces. |
| Flow canvas | The Flow Definition Graph from `dexcli visualize`. |
| Start Flow | Start a Flow. It runs on your Workers. Dex Web does not check Worker registration first. |
| Actions | Call RPCs on a selected run. |
| Connectors | Configure official Connectors in local development only. See [connector.md](connector.md#local-connector-credentials). |

## Diagnose with Dex Web

- **Pending reason.** A pending WaitFor or Execute attempt shows why it waits: waiting for locks, waiting for a Worker, or waiting for a Worker that supports this Step type. See [error-handling.md](error-handling.md#diagnosis).
- **Routing.** Steps show the serving Worker ID and whether the sticky queue or the task queue of the Flow type served them. Compare with the sticky ID captured for that dispatch.
- **Run attempts.** Time travel keeps the run ID and creates a new run attempt. Dex Web shows and switches attempts.
- **History window.** The run page loads only the newest window of events and pages back for older ones. The canvas and overview read only the newest window, so their per-Step counts are lower bounds. Page back, or use `dexcli flow history`, before you conclude that an older Step never ran.

## Run it

| Form | Use |
| --- | --- |
| `dexcli dev` | Local development, with a local server-zd |
| `dex-web` binary or image | Production, against an existing Server |
| `dexcli web` | Against an existing Server, for example over an SSH tunnel |

- Dex Web has no end-user authentication. Anyone who reaches its port acts with its Server credential. Put it behind TLS and an authenticating reverse proxy. Never expose it to the public Internet.
- With Server authentication on, give Dex Web a bearer-token file for its own Server calls. That credential does not authenticate users.
- Use its readiness endpoint for traffic gating and its health endpoint for liveness, so a Server outage does not restart Web.

Settings, access control, and each page are in the [Web reference](https://docs.superdurable.io/reference/web/dex-web).
