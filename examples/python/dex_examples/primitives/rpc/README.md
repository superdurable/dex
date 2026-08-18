# RPC primitive

Minimal Flow where an RPC unblocks a waiting Step.

HTTP:
- `GET /primitives/rpc/start?workflowId=...`
- `GET /primitives/rpc/trigger?workflowId=...&message=hello`
