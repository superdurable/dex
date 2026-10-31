# Attribute primitive

HTTP:

- `GET /primitives/attribute/start?workflowId=...&message=hello`
- `GET /primitives/attribute/read?workflowId=...` reads the status, the payment
  progress, and the email from outside the Flow with one `get_attributes` call.
  The read invokes no RPC and no Worker, and it also works on a closed run.
  The Flow never sets the email, so it returns `null`.
