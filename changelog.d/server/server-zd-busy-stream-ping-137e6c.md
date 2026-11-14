---
section: Fixed
---
- A busy pull Worker stream now gets an empty keepalive response every **matching.pingInterval**, as an idle stream gets a **Ping**. A load balancer that counts idleness only in the server-to-client direction, such as an AWS ALB, no longer resets a stream while a long Step or RPC runs. Every released SDK ignores the keepalive and sends no reply. [Reference](/reference/server/workers#server-and-network-setup).
