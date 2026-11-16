---
section: Added
---
- **StopType.GRACEFUL_COMPLETE**, **StopType.FORCE_COMPLETE**, and the **StopFlowOptions(StopType, String, Object)** constructor let **Client.stopFlow** complete a Flow with a result, like the **gracefulComplete** and **forceComplete** Step decisions. They need Dex Server 0.1.34 or newer.
