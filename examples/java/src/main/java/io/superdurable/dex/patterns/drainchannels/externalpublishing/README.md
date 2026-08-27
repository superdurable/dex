# Draining External Channel Publishing

This Flow processes messages published from outside the Worker and completes when the Channel is empty. The endpoint publishes to an active Flow; when no execution is active, it starts a Flow with the first message.

Use `GET /patterns/drain-channels/external-publishing/start-or-publish?workflowId={workflowId}`.
