# Subscription

The Flow sends a welcome email, waits through the trial and billing timers, and charges for a bounded number of periods. Concurrent Steps accept typed charge updates and cancellation messages. `Describe` is a typed Flow RPC.

With the sample server running:

```text
http://localhost:8080/products/subscription/start
http://localhost:8080/products/subscription/describe?workflowId=<flow-id>
http://localhost:8080/products/subscription/updateChargeAmount?workflowId=<flow-id>&newChargeAmount=250
http://localhost:8080/products/subscription/cancel?workflowId=<flow-id>
```
