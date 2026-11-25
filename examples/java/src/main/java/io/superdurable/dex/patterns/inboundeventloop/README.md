# Inbound Event Loop

- `GET /patterns/inbound-event-loop/start?workflowId={workflowId}`
- `GET /patterns/inbound-event-loop/reply?workflowId={workflowId}&text={text}`
- `GET /patterns/inbound-event-loop/resolve?workflowId={workflowId}&reason={reason}`
- `GET /patterns/inbound-event-loop/transcript?workflowId={workflowId}`

`AwaitTicketMessage` records each batch of replies and loops back, which restarts the inactivity Timer. A resolution moves to `CloseResolvedTicket`; inactivity moves to `CloseInactiveTicket`. Both close with `forceCompleteIfChannelsEmpty` and fall back to `AwaitTicketMessage` when a message is still queued.

See the [Inbound Event Loop design pattern](https://docs.superdurable.io/design-patterns/inbound-event-loop).
