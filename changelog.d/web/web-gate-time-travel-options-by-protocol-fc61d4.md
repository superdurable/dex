---
section: Fixed
---
- Dex Web no longer offers or sends options the connected server does not support. **Clone fork**, the session fork actions, the **Event ID** time travel point, and the **SubFlow reset policy** choice appear only when the server reports protocol version 6 or later, as server-zd 0.1.33 and later do. On an older server, time travel sends no **subFlowResetPolicy**, and **POST /api/flows/clone-fork** and **POST /api/flows/time-travel** reject these options with HTTP 400 **SERVER_PROTOCOL_UNSUPPORTED** instead of a silent no-op or a misleading error. [Reference](/reference/web/dex-web#clone-fork).
- **GET /api/server-info** returns **serverEngine** and **currentProtocolVersion**, and **GET /api/v2/catalog** adds **currentProtocolVersion**.
