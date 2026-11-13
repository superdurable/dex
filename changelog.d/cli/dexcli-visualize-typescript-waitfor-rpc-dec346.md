---
section: Added
---
- **dexcli visualize** reads TypeScript **waitFor** methods, RPC handlers,
  and **handleTimeout** in the same graph shape as Go. A Wait built directly
  from **Wait** factories shows its Timer, Channel, ChannelMap, Join, and
  SubFlow conditions, and a **waitFor** that only throws shows a failure
  Wait. An RPC's **RPCResult** shows its **nextSteps** and **cancelingSteps**.
  The graph also records resource reads, writes, and publishes in each
  handler and in the same-file helpers it calls, and warns about unused
  resources. A Wait or RPC result that the analyzer cannot read, a Stream
  write in an RPC or timeout handler, and several Flows in one file are
  blocking diagnostics. Such a source no longer reports
  **typescript_wait_for_unsupported**.
  [Reference](/reference/web/flow-visualization#supported-source-profile).
