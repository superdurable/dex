# Draining External Channel Publishing

## Endpoints

- `GET /patterns/drain-channels/external-publishing/start-or-publish?workflowId={workflowId}&messageId={messageId}`: pass a stable `messageId` so a retried call reuses the same start and publish request IDs; without it, the controller generates one. A success means the run accepted the message, not that it was handled. HTTP 409 means the outcome is unknown: send the message again with the same `messageId`.

Call twice in a row to start, then publish another message.
