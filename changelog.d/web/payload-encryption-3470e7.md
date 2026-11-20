---
section: Changed
---
- Web API JSON marks encrypted stored Values as **__dexEnvelope** objects and wraps application JSON objects with keys that start with **__dex** as **__dexEscapedObject**, so data cannot look like a Dex Web marker. Failed requests whose gRPC status message is encrypted return it in **encodedError** for the browser to decrypt.
