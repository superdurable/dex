---
section: Fixed
---
- **dexcli visualize** accepts TypeScript Attributes and AttributeMaps declared with **.plaintext()**, as it accepts **.syncToAttributeStore()** and **.hardDeleteOnNull()**. Before, a Step or RPC that read or wrote such an Attribute failed with **dynamic_resource_access**.
