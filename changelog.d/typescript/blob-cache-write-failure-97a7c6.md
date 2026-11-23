---
section: Fixed
---
- A failed blob cache write no longer fails a Step, RPC, or Client call. The SDK logs the error with **console.warn** and uses the value it loaded from Dex, as the Go, Java, and Python SDKs do (#2185). [Reference](/reference/typescript/workers#blob-cache).
- **openBlobCache** now says that only one open cache may use a directory at a time. Two Worker processes on one directory could fail every attempt of a Flow with **blob ID content mismatch** (#2185). [API reference](pathname:///api/typescript/latest/functions/openBlobCache.html).
