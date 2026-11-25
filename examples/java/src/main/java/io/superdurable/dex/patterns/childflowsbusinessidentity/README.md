# Child Flows with Business Identity

An order starts one top-level `ShipmentFlow` per shipment. Each shipment's Flow
ID is its shipment number, so people and systems can look it up or stop it by
that number. The shipment reports its outcome to the order through an RPC; the
order also reads the shipment at each reconcile interval and records how it
ended. Both writes take the same lock, and the first outcome wins. An order
that repeats a shipment number fails before it starts any shipment.

- `GET /patterns/child-flows-business-identity/start?workflowId={orderNumber}`
- `GET /patterns/child-flows-business-identity/shipment?shipmentNumber={shipmentNumber}`
- `GET /patterns/child-flows-business-identity/cancel-shipment?shipmentNumber={shipmentNumber}`
