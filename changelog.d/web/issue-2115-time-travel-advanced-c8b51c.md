---
section: Changed
---
- Time travel from Dex Web now skips write reapply by default. RPC calls, Channel publications, and Attribute writes that reached the run after the chosen point are skipped, so a Step that waits for such input, such as a human approval, waits again. Turn on **Apply later writes again** under **Advanced** in the time travel dialog for the earlier behavior. A Client and **dexcli flow time-travel** still apply later writes again by default. **POST /api/flows/time-travel** accepts an optional **skipWritesReapply**; an absent value means false. [Reference](/reference/web/dex-web#time-travel-dialog).
- The time travel and Clone fork dialogs show only their common fields. **SubFlow reset policy** moves under a collapsed **Advanced** section in both, and Clone fork also moves **Task queue** and **ID reuse policy** there. [Reference](/reference/web/dex-web#clone-fork).
