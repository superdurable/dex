# Idle Timer with Resolution

- `GET /patterns/inbound-event-loop/start?workflowId={workflowId}`
- `GET /patterns/inbound-event-loop/reply?workflowId={workflowId}&text={text}`
- `GET /patterns/inbound-event-loop/resolve?workflowId={workflowId}&reason={reason}`
- `GET /patterns/inbound-event-loop/transcript?workflowId={workflowId}` returns the current transcript chunk.

`SupportTicketConversationFlow` starts without a Flow timeout, so only the 24-hour inactivity Timer closes a quiet ticket. `AwaitTicketMessage` records each batch of replies and loops back, which restarts the inactivity Timer. A resolution moves to `CloseResolvedTicket`; inactivity moves to `CloseInactiveTicket`. Both close with `force_complete_if_channels_empty` and fall back to `AwaitTicketMessage` when a message is still queued.

See the [Idle Timer with Resolution design pattern](https://docs.superdurable.io/design-patterns/external-interaction/idle-timer-with-resolution).
