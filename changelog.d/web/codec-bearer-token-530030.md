---
section: Added
---
- **Settings > Codec endpoint** in **Custom** mode takes an optional bearer token for codec servers that require one, such as **dexcli codec-server**. Dex Web sends it as **Authorization: Bearer** only to the codec server's **/decode** and **/encode**, never to the Dex Web server. The token stays in the tab's session storage, so a reload keeps it and a new tab does not; **Clear token** removes it. It works together with **Send cookies**. A 401 from the codec server now names the bearer token as the likely cause. [Reference](/reference/server/payload-encryption#dex-web).
