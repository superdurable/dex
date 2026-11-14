---
section: Fixed
---
- A busy pull Worker stream now gets an empty keepalive response every **matching.pingInterval**, as an idle stream gets a **Ping**. A load balancer or proxy that counts idleness only in the server-to-client direction no longer resets a stream while a long Step or RPC runs; busy streams behind an AWS ALB were observed being reset this way. Every released SDK ignores the keepalive and sends no reply. [Reference](/reference/server/workers#server-and-network-setup).
