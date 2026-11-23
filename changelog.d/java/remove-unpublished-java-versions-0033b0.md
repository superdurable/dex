---
section: Fixed
---
- Workers accept omitted input for inputless RPCs, including calls from TypeScript Clients before 1.45.8 that failed with **Value has no concrete kind**. RPCs that declare input still reject omissions with **ValueMappingException**. (#2034)
