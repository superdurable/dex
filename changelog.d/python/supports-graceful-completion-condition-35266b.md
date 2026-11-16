---
section: Added
---
- The function **supports_graceful_completion_condition** reports whether the connected Server supports **graceful_completion_requested**, from the protocol version the Worker negotiated, without a request. **wait_for** can add the condition only when it returns True and keep working with Servers older than protocol 7. It returns False for a Context that Dex did not create. [Reference](/reference/python/waits#graceful-completion-requests).
