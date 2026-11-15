---
section: Fixed
---
- Reading a SubFlow result with an error type this SDK version does not recognize no longer raises `ValueError`. `FlowResult.error_type` is then `None`.
