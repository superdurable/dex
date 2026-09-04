# Channel primitive

Minimal Flow that waits on a Channel, lists pending messages by ID, deletes them,
and transactionally moves one message between Channels.
The Step explicitly loads pending messages for Execute and deletes the first queued message with its decision.

HTTP:

- `GET /primitives/channel/start?workflowId=...&inputNum=5`
- `GET /primitives/channel/approve?workflowId=...`
- `GET /primitives/channel/enqueue?workflowId=...&value=hello`
- `GET /primitives/channel/messages?workflowId=...`
- `GET /primitives/channel/delete?workflowId=...&messageId=...`
- `GET /primitives/channel/move?workflowId=...&messageId=...`
