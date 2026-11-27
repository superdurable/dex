---
bump: patch
---
- A transactional or locking RPC answers a repeated request ID with its recorded reply, including a refusal. A business retry after a refusal is a new logical call with a new request ID. The back-pressure submit Step relies on the SDK default of a new request ID per retry.
- List queued messages per Channel among the Server limits. A long-lived parent's waiting list in one Channel must fit that limit; a longer waiting list is split across Channels.
- A reply RPC may record the admitted reply as the outcome under the reply-window lock. Execute still checks which Condition fired and never infers the Timer from an open window or an unset outcome.
- "Keep trying for up to X" is the Step retry total duration plus an Execute failure route; the Step computes no deadline or budget guard of its own. Work that must happen at the deadline uses a deadline watchdog.
- Drop the caveats that the retry total duration never ends a running attempt and that a wait can end well after it; the StepOptions reference keeps the exact semantics.
- TypeScript: a Step that needs the Client gets a Client provider through its Flow constructor. Flow and Step modules never import the Client or a module-level getter for it.
