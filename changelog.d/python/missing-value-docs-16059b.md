---
section: Fixed
---
- Documentation: **Attribute.get** and **AttributeMap.get** no longer claim that an unset read raises **KeyError**. It returns False, 0, 0.0, or None by value type. **AttributeMap.get** now documents **AttributeMapNotLoadedError**. Behavior is unchanged.
