# Channel primitive

This Flow can list and delete pending messages by ID. Its move RPC atomically
deletes from one Channel and publishes to another. The Step explicitly loads
pending messages for Execute and deletes the first queued message with its decision.

HTTP:

- `GET /primitives/channel/start?workflowId=...&inputNum=5`
- `GET /primitives/channel/approve?workflowId=...`
- `GET /primitives/channel/enqueue?workflowId=...&value=hello`
- `GET /primitives/channel/messages?workflowId=...`
- `GET /primitives/channel/delete?workflowId=...&messageId=...`
- `GET /primitives/channel/move?workflowId=...&messageId=...`
