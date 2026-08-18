# Channel primitive

Minimal Flow that waits on a Channel until an approval RPC publishes.

HTTP:
- `GET /primitives/channel/start?workflowId=...&inputNum=5`
- `GET /primitives/channel/approve?workflowId=...`
