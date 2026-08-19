# Subscription

The Flow sends a welcome email, waits through the trial and billing timers, and
charges for a bounded number of periods. Concurrent Steps accept typed charge
updates and cancellation messages. `describe` is a typed Flow RPC.

`ChargeCurrentBill` decides in `wait_for` whether the subscription is over and
passes that decision to `execute` through a Step execution local, so the timer
is only scheduled while billing periods remain.

`Subscription` stores its periods as seconds rather than `timedelta` so the
whole `Customer` value round-trips through the SDK's JSON codec.
`subscription_billing` holds the pure billing helpers, which are easy to unit
test on their own.

With the sample server running:

```text
http://localhost:8080/products/subscription/start
http://localhost:8080/products/subscription/describe?workflowId=<flow-id>
http://localhost:8080/products/subscription/updateChargeAmount?workflowId=<flow-id>&newChargeAmount=250
http://localhost:8080/products/subscription/cancel?workflowId=<flow-id>
```
