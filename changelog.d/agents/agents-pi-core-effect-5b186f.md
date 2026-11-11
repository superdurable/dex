---
section: Added
---
- **@superdurable/dex-agents-pi**: a Pi tool's **prepareArguments** runs before the schema check, so an old argument shape is converted first. A plain error a Pi tool throws follows the effect the session uses after tool policies: it fails the call for **read** and **idempotent_write**, and records **outcome_unknown** for **non_idempotent_write** and **unclassified**.
