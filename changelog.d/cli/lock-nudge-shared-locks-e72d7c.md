---
section: Changed
---
- **lock_requirement_not_declared** no longer warns when every writer registers the same exact lock, of any Attribute, or when no writer reads the Attribute. It keeps the warning when it cannot read a writer's locks or reads.
