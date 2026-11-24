---
section: Fixed
---
- Documentation: **Attribute.get** and **AttributeMap.get** now state what an unset value reads as: `false`, `0n`, or `0` for a `bool`, `int64`, or `double` codec, and `undefined` for any other codec, even though the return type is `T`. **AttributeMap.get** now also lists **AttributeMapNotLoadedError** for an instance the handler did not load. Behavior is unchanged.
