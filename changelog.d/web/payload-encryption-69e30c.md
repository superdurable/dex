---
section: Added
---
- **GET /api/codec-server** returns the endpoint and token of a local codec server when the host configures one with **Config.LocalCodecServer**, as **dexcli dev -codec-key-file** does. Otherwise its **configured** field is false.
