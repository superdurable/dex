---
bump: patch
---
- A transactional or locking RPC answers a repeated request ID with its recorded reply, including a refusal. A business retry after a refusal is a new logical call with a new request ID. The back-pressure submit Step relies on the SDK default of a new request ID per retry.
- List queued messages per Channel among the Server limits. A long-lived parent's waiting list in one Channel must fit that limit; split it or raise the limit.
- TypeScript: a Step that needs the Client gets a Client provider through its Flow constructor. Flow and Step modules never import the Client or a module-level getter for it.
