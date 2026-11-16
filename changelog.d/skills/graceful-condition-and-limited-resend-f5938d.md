---
bump: patch
---
- Add the **graceful completion** Condition to `dex-sdk` `step.md`: it completes when the Flow has a graceful completion request, from a **GracefulComplete** decision or a Flow stop with graceful complete. **AnyOf** checks it after Timers, Channels, and SubFlows. Its result reports completed whenever a request exists, so **Execute** handles consumed messages first and returns **DeadEnd** only when there are none.
- Correct the Mutation resend rule in `dex-sdk` `connector.md`: a Connector operation that declares a bounded resend limit may resend within that limit when the outcome is unknown, reading the write back first when the provider supports it. It selects the uncertainty branch only when the outcome is still unknown. Such a Mutation stays a non-idempotent write, and application code never resends on its own.
