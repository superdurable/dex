---
section: Added
---
- **Client.describe_flow** and **Client.get_attributes** accept keyword-only **run_id** to read one exact run, including a retained closed run. **AsyncClient** supports the same calls.
- **Client.wait_for_flow** accepts keyword-only **run_id**; a closed run returns its result at once. **AsyncClient** supports the same call.
- **Client.stop_flow** accepts keyword-only **run_id**. It never stops a newer run and raises **FlowNotActiveOrNotFoundError** when the tracked run has closed. **AsyncClient** supports the same call.
