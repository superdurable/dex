# Slack Thread Agent (TypeScript)

An agent session that Slack Connector Triggers drive: one session per thread.
A new thread in a channel starts a session with the thread's first message. Each
reply in the thread becomes the session's next user message. The docs explain
the pattern in
[Start and feed sessions from Connector Triggers](https://docs.superdurable.io/agent-frameworks/sessions#connector-triggers).

| File | What it holds |
| --- | --- |
| [`thread-agent-flow.ts`](thread-agent-flow.ts) | The session Flow on the built-in agent loop, with a scripted model that echoes each message. |
| [`thread-session-triggers.ts`](thread-session-triggers.ts) | The session ID of a thread, the two Trigger targets, and one durable Slack Trigger runner for both. |
| [`test/slack-thread-triggers.test.ts`](test/slack-thread-triggers.test.ts) | Delivers root and reply events to the targets against a real Dex server, with redeliveries and with restarts that replay a durable inbox. |

This folder is the tested Trigger wiring. It has no Worker entry point of its own.

- The root target starts the session with `newDexFlowTriggerTarget`. The Slack
  event ID is the start's request ID, so a redelivered root starts no second
  session.
- The reply target calls the session's `send_user_message` RPC with
  `newDexRPCTriggerTarget`. Each delivery of one event sends the same request
  ID, and `send_user_message` is transactional, so a redelivered reply queues
  one message while Dex still keeps that request ID for the session run.
- Both targets resolve the session ID from the team, channel, and thread
  timestamp, so a reply finds its thread's session.

The files run directly with Node.js type stripping, so there is no build step.
Use Node.js 22.18 or later, or Node.js 24. The folder is its own npm package. It
needs `@superdurable/dex` 1.45.4 or later and `@superdurable/connectors-sdk`
0.10.1 or later. Its `overrides` keep one copy of each, because
`@superdurable/dex-agents` 0.6.8 has an exact peer dependency on
`@superdurable/dex` 1.45.3, and `@superdurable/connector-slack` 0.6.0 depends on
`@superdurable/connectors-sdk` 0.10.0 exactly.

## Connect a Slack workspace

`newThreadSessionTriggerRunner` runs both bindings of one Slack connection on
one runner, from `newProjectMessageTriggerRunner`. The project configuration
must declare the `thread-session-start` binding of `channelThreadCreated` and
the `thread-session-reply` binding of `threadReplyCreated`, each with its
channel and members. Each binding records an event in its durable project
inbox before the runner acknowledges it to Slack. After a crash, the next
`run` replays the recorded events. Do not pass the plain targets to
`newMessageTriggerRunner` in production: it acknowledges Slack before
delivery, so a crash in between loses the event.

Call `run` next to a Worker that registers the session Flow. The
[Slack thread approval example](https://github.com/superdurable/dex-connectors-library/tree/main/connectors/slack/typescript/examples/thread-approval)
shows the project configuration, the Worker, the runner, and a clean
shutdown. The live Slack path of this example is not tested.

## Run the test

The test needs no Slack workspace and no model key. Start Dex in one terminal:

```bash
dexcli dev
```

Run the test in a second terminal:

```bash
npm install
npm test
```

Set `DEX_FLOW_SERVICE_ADDRESS` when Dex listens on another address than
`127.0.0.1:8801`. The restart tests keep the durable inbox in memory and call
`prepareTrigger` and `replayTriggerDeliveries`, the way the runner does
before it acknowledges Slack and after it restarts.
