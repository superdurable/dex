---
section: Added
---
- **dexcli visualize** reports two graph warnings for every source language. **channel_without_consumer** marks a Channel that receives messages that no Step waits on or reads. **no_completion_path** marks a Flow where no decision can complete or fail it. The Java analyzer now follows a Channel, Attribute, or Stream passed to a helper method, so the helper's reads and writes appear in the graph. A Java RPC or timeout handler that passes a Stream to a helper that writes it now reports the blocking **step_progress_outside_step** error, as a direct write already did.
