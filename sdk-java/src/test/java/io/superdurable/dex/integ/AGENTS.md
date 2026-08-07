# Java Integration Test Suite

- Keep each workflow in its own `.java` file beside its owning test.
- Prefix each workflow class and filename with the owning test name, such as
  `SkipWaitUntilWorkflow` and `SkipWaitUntilMixedWaitWorkflow` for
  `SkipWaitUntilTest`.
- Let each test instantiate its workflows directly. Do not add fixture
  aggregators or registries such as `IwfFlows`.
- Never use anonymous `Step` implementations. Define named, non-public step
  classes in the workflow file so the default `stepType` is stable.
- Reuse one workflow across tests only when they intentionally verify the same
  contract. Do not duplicate a workflow solely to satisfy the naming rule.
