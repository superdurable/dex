---
section: Changed
---
- **dexcli dev** and **dexcli web** bundle **dex-server-zd** 0.1.37, where **SetAttributes**, **DeleteChannelMessage**, and **InvokeRPC** compare only the request ID, so retried encrypted requests get the first reply.
