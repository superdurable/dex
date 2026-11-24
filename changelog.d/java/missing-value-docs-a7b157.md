---
section: Fixed
---
- Documentation: **Attribute.get** and **AttributeMap.get** no longer say that every unset value reads as `null`. An unset value reads as `false` or zero for a Boolean or numeric value type, and as `null` for any other value type. **AttributeMap.get** now also lists **AttributeMapNotLoadedException** for an instance the invocation did not load. Behavior is unchanged.
