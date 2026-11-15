---
section: Fixed
---
- Reading a SubFlow result with an error type this SDK version does not recognize no longer throws `TypeError`. `FlowResult.errorType` is then `undefined`.
