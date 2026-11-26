# Child Flows with Business Identity

An order starts one top-level `ShipmentFlow` per shipment. Each shipment's Flow
ID is its shipment number, so people and systems can look it up or stop it by
that number. The shipment reports its outcome to the order through an RPC; the
order also reads the shipment at each reconcile interval and records how it
ended. Both writes take the same lock, and the first outcome wins. The order
starts and reads shipments in unlocked Steps, so a report never meets a lock
held across a Client call. A report that arrives before the order records the
started run waits in `pendingShipmentReports`, grouped by shipment number and
run ID, until the claim or the rejection settles it. An order
that repeats a shipment number fails before it starts any shipment.

- `GET /patterns/child-flows-business-identity/start?workflowId={orderNumber}`
- `GET /patterns/child-flows-business-identity/shipment?shipmentNumber={shipmentNumber}`
- `GET /patterns/child-flows-business-identity/cancel-shipment?shipmentNumber={shipmentNumber}`
