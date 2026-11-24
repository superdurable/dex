---
section: Fixed
---
- Documentation: **Attribute.get** and **AttributeMap.get** no longer say that a read of an unset value raises **KeyError**. They never raised it. An unset value reads as `False`, `0`, or `0.0` for a `bool`, `int`, or `float` value type, and as `None` for any other value type. **AttributeMap.get** now also lists **AttributeMapNotLoadedError** for an instance the handler did not load. Behavior is unchanged.
