# Microservice orchestration

The starting Step calls API 1 and schedules API 2 and API 3 concurrently. API 3
waits for either a typed `Ready` Channel message or a 24-hour timer, and
`context.has_timer_fired()` tells the two outcomes apart. `swap` is a typed Flow
RPC that atomically returns and replaces the persisted `data` Attribute.

With the sample server running:

```text
http://localhost:8080/products/microservices/start?workflowId=microservice-1
http://localhost:8080/products/microservices/swap?workflowId=microservice-1&data=updated
http://localhost:8080/products/microservices/signal?workflowId=microservice-1
```
