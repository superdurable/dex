# Draining External Channel Publishing

This Flow processes messages published from outside the Worker and completes when the Channel is empty. The endpoint publishes to an active Flow; when no execution is active, it starts a Flow with the first message.

Use `GET /patterns/drain-channels/external-publishing/start-or-publish?workflowId={workflowId}&messageId={messageId}`. Pass a stable `messageId` so a retried call reuses the same start request ID; without it, the controller generates one. A success means the run accepted the message, not that it was handled. HTTP 409 means the outcome is unknown: send the message again with the same `messageId`.
