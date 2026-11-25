---
section: Changed
---
- **lock_requirement_not_declared** no longer warns when every writer is fully analyzable and either all writers hold one exact common registration lock or no writer reads the Attribute. Any form it cannot analyze keeps the warning.
