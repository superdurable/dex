---
section: Added
---
- **Settings > Codec endpoint** in **Custom** mode takes an optional bearer token for codec servers that require one, such as **dexcli codec-server**. Dex Web sends it as **Authorization: Bearer** only to the codec server's **/decode** and **/encode**, and only while the Custom URL is the one it was saved with. With **Remember on this browser**, on by default, all tabs of this browser share the token until you delete it; without it, the token is kept for this tab. The small delete button next to the token field deletes it, and other open tabs follow. It works together with **Send cookies**. A 401 from the codec server names the bearer token as the likely cause. [Reference](/reference/server/payload-encryption#dex-web).
