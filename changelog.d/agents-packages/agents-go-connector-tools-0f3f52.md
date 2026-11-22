---
section: Connectors as tools in the Go core
---
- The Go core offers Dex Connector operations to an agent as tools, with the
  behavior of the Python and TypeScript cores' **ConnectorToolSource**. The new
  package `connectortools` takes an operation's `sdkgo.AgentTool` from
  `github.com/superdurable/dex-connectors-library/sdkgo` v0.39.0:
  `NewConnectorTool` reads its name, description, argument schema, effect, and
  approval from the reviewed agent metadata, and `NewConnectorToolSource` offers
  the tools. Pass the source to the new `builtinloop.WithToolSources`. Each call
  runs one operation invocation in its own tool Step through
  `RunAttemptWithOptions` with the tool Step's `dex.Context`, so the connector's
  Call ID, idempotency key, and single-dispatch fence apply on every attempt.
  The source reads that Step from the new `DeferredToolExecution.StepContext`,
  so a runner may pass it a context derived from the Step's.
- A Go runner routes a call that a tool source built first, through
  `ToolSourceRouter.ExecuteDeferredToolCall`, once per tool Step attempt and
  before the framework's tool node, tool middleware, and tool handlers, as the
  Python and TypeScript adapters do. The session enforces it: a second router
  call in one attempt runs no source, and the session records the source's
  outcome and error, whatever the runner returns, so a retry middleware cannot
  resend a write past its fence and an error handler cannot record an unknown
  outcome as **succeeded**.
- The arguments are checked against the `InputSchema` before the call, branches
  map as in the other cores (a success branch is **succeeded**, **uncertain**
  and a branch listed **unknown** are **outcome_unknown**, every other branch is
  **failed**), and results are compact JSON with sorted keys, cut at 64 KiB with
  `[Dex] truncated, <n> bytes`. A connector Retry fails the attempt, after its
  delay in whole seconds. The Go test runs every case of
  `agents/conformance/connector_tools.json`.
- A Mutation whose `RepeatOnUnknown` is `limited` pins its send cap, the
  metadata's `MaxSends` or the new `ToolPolicy.MaxSends` (1 to 10), in its tool
  source reference, `{"tool": "<name>", "maxSends": <n>}`, and passes it with
  `sdkgo.WithMaxSends` on every attempt; its `CallSingleAttemptTool` gets the
  operation's attempts, at least the cap. Connector writes of one model response
  run one at a time in the model's order, and the later ones record **failed**
  with `skipped` set once one does not succeed.
- Core additions any Go runner can use: `ToolSource`, `ToolSourceRouter`, and
  `SourceToolListing`, which list tools at run time and store the source name in
  the envelope the other cores store; `DeferredToolCall.Ordered`;
  `ToolOutcomeUnknownError`; `ToolExecutionResult.TruncatedOutputBytes`; and
  `ToolCallStepOptionsProvider` with `ToolCallStepOptions`, whose
  `BoundedResendExecuteRetry` gives a call more than one attempt in
  `CallSingleAttemptTool`. `ResolveToolPolicy` keeps an explicit policy's
  `MaxSends`. A call that waits for approval carries no approval display yet.
- Upgrade rule: stop or drain every Go Worker of a Flow type built from 0.6.15
  or earlier before a Worker with tool sources serves that Flow type, and do
  not roll such a Flow type back below 0.6.16 while a session has a queued
  connector write or a connector call in flight. An older Go Worker waits
  forever for a queued write that only a 0.6.16 `RunModel` starts, and it
  records a later attempt of a connector write **failed** instead of
  **outcome_unknown**, so the model may send the write again.
