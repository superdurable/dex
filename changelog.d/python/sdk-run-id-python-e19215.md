---
section: Added
---
- **Client.describe_flow** and **Client.get_attributes** accept a keyword-only `run_id` to read one exact run of a Flow ID, even when a newer run exists. A closed run returns its final status and Attribute values. **AsyncClient** has the same parameters.
- **Client.wait_for_flow** and **Client.stop_flow** accept a keyword-only `run_id` to target one exact run, as **Client.invoke_rpc** already does. A wait on a closed run returns its result at once. A stop with a run ID never affects a newer run and raises **FlowNotActiveOrNotFoundError** when that run has already closed. **AsyncClient** has the same parameters.
