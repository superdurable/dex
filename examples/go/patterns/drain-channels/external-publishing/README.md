# Draining External Channel Publishing

## Endpoints

- `GET /patterns/drain-channels/external-publishing/start-or-publish?workflowId={workflowId}&messageId={messageId}`: pass a stable `messageId` so a retried call reuses the same start request; without it, the controller generates one.

Call twice in a row to start, then publish another message.
