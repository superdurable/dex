# Order processing

Charge the buyer, wait for seller approval (with a reminder timer), then ship.
If shipping exhausts retries, refund.

With the sample server running:

```text
http://localhost:8080/products/order-processing/start
http://localhost:8080/products/order-processing/wait-charged?workflowId=<flowID>
http://localhost:8080/products/order-processing/approve?workflowId=<flowID>
http://localhost:8080/products/order-processing/describe?workflowId=<flowID>
```

Pass `failShip=true` on start to exercise the refund path after approval.
