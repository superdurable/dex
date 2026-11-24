---
section: Fixed
---
- Documentation: **Attribute.get** and **AttributeMap.get** now state that an unset read returns false, 0n, 0, or undefined by codec wire kind, even though the return type is **T**. **AttributeMap.get** now documents **AttributeMapNotLoadedError**. Behavior is unchanged.
