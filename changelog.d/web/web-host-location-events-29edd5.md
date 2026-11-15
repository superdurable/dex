---
section: Added
---
- **web/embedded**: With **Target.HasHostNavigation**, Dex Web in a frame posts a **dex-web:location** message to its parent window after every route change. The message names the page's path relative to **BasePath** and its query string, so your page can keep Dex Web's page in its own URL and open it again after a refresh or from a shared link. See [Keep the page in the host URL](/reference/web/embed-dex-web#host-location).
