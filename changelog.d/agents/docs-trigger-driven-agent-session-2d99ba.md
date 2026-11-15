---
section: Added
---
- Docs: a TypeScript session can be started and fed by Connector Triggers. A new Slack thread starts a session through a Flow start target, whose request ID is the event ID. Each reply calls **send_user_message** through an RPC target, which sends one request ID for every delivery of an event, so a redelivered reply queues one message while Dex Server keeps that request ID for the session run. A runner with a durable inbox replays the events recorded before a crash. It needs **@superdurable/connectors-sdk** 0.10.1 and **@superdurable/dex** 1.45.4 or later. The new Slack thread agent example runs without a Slack workspace. [Reference](/reference/agents/connector-triggers).
