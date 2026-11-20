---
section: Fixed
---
- **SetAttributes**, **DeleteChannelMessage**, and **InvokeRPC** now compare only the request ID, as **StartFlow** does. A repeat with a recorded request ID gets the first request's reply and applies none of its own changes, even when its content differs. It no longer fails with INVALID_ARGUMENT "request ID ... was already used for a different request". Concurrent transactional or locking RPC calls with the same request ID share one handler execution whatever their inputs. [Reference](/reference/server/runtime-data#rpc-request-ids)
