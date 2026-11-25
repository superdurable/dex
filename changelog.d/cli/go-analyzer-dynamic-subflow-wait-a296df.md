---
section: Fixed
---
- **dexcli visualize** draws one SubFlow condition and start edge for range-built Go SubFlow waits. Other spreads previously drew an unknown condition without a diagnostic. They now report **dynamic_wait_condition** as an error if they can hold SubFlow conditions, or as a warning otherwise.
