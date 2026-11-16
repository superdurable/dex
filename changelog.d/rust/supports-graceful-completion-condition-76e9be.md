---
section: Added
---
- **Context::supports_graceful_completion_condition** reports whether the connected Server supports **Condition::graceful_completion_requested**, from the protocol version the Worker negotiated, without a request. **wait_for** can add the condition only when it returns true and keep working with Servers older than protocol 7. [Reference](/reference/rust/waits#graceful-completion-requests).
