# Design patterns

A pattern combines several [facts](../../SKILL.md#facts) for a common business shape: when the business needs X, a shape that works is Y, because of the facts it names. Patterns are suggestions. Each pattern names its facts in a `Derived from:` line; when the business differs, derive a variant from the same facts. The [Must rules](../../SKILL.md#must) hold in every variant.

Choose the smallest Flow shape that meets the requirement. Prefer the nearest pattern below to an ad hoc coordination loop. Keep its Flow shape and replace the domain and integrations. Then read the selected language's pattern guidance and copy API usage from the runnable example.

Skeletons in the pattern files are language-neutral pseudocode. `A.waitFor` and `A.execute` are the two methods of Step `A`.

This page is the index. Each pattern lives in one file under `patterns/`; open only the file of the pattern you need.

## Runnable examples

Most patterns have a runnable example in each SDK; a section says when its example is partial, simplified, or missing:

| Language | Pattern root | Folder naming |
| --- | --- | --- |
| Go | [examples/go/patterns](https://github.com/superdurable/dex/tree/main/examples/go/patterns) | kebab-case |
| Python | [examples/python/dex_examples/patterns](https://github.com/superdurable/dex/tree/main/examples/python/dex_examples/patterns) | kebab-case |
| TypeScript | [examples/typescript/src/patterns](https://github.com/superdurable/dex/tree/main/examples/typescript/src/patterns) | kebab-case |
| Java | [examples/java/src/main/java/io/superdurable/dex/patterns](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/superdurable/dex/patterns) | lowercase, no separators |
| Rust | [examples/rust/src/patterns](https://github.com/superdurable/dex/tree/main/examples/rust/src/patterns) | snake_case |

Each section names its Go folder. The other languages use the same folder name in their own style. A few patterns live under `primitives/` or `products/`; the section says so. In Rust, Decision with Deadline (Deferred choice), Critical section, and Cancel with child Flows live under [examples/rust/src/control_patterns](https://github.com/superdurable/dex/tree/main/examples/rust/src/control_patterns).

## Choose a pattern

| Requirement | Pattern | File |
| --- | --- | --- |
| Define and version workflows as data at runtime using a shared set of actions and external conditions | [Workflow DSL](patterns/flow-shapes-and-state.md#workflow-dsl) | [flow-shapes-and-state.md](patterns/flow-shapes-and-state.md) |
| Start one business operation from an API | [Root Flow admission](patterns/flow-shapes-and-state.md#root-flow-admission) | [flow-shapes-and-state.md](patterns/flow-shapes-and-state.md) |
| Run known, distinct branches in parallel | [Static parallel Steps](patterns/parallel.md#static-parallel-steps) | [parallel.md](patterns/parallel.md) |
| Run one Step per input item | [Dynamic parallel Steps](patterns/parallel.md#dynamic-parallel-steps) | [parallel.md](patterns/parallel.md) |
| Proceed only after every branch finishes | [Await parallel Steps](patterns/parallel.md#await-parallel-steps) | [parallel.md](patterns/parallel.md) |
| Proceed after the first branch or n of m branches, optionally in rounds | [Partial join and reset](patterns/parallel.md#partial-join-and-reset) | [parallel.md](patterns/parallel.md) |
| Keep the first result, cancel the rest | [First-win parallel Steps](patterns/parallel.md#first-win-parallel-steps) | [parallel.md](patterns/parallel.md) |
| Child Flow runs that need their own lifecycle, history, or retries, or a very large fan-out | [Parallel SubFlows](patterns/parallel.md#parallel-subflows) | [parallel.md](patterns/parallel.md) |
| Child jobs with their own business identity, looked up, retried, or stopped by their own number | [Top-level child Flows](flow.md#child-jobs-with-their-own-identity) | [flow.md](flow.md) |
| Wait for an external system to become ready or finish | [Polling](patterns/polling-and-timers.md#polling) | [polling-and-timers.md](patterns/polling-and-timers.md) |
| Recurring work at a fixed interval | [Cron schedule](patterns/polling-and-timers.md#cron-schedule) | [polling-and-timers.md](patterns/polling-and-timers.md) |
| Repeat until the recipient opts out | [Reminder](patterns/polling-and-timers.md#reminder) | [polling-and-timers.md](patterns/polling-and-timers.md) |
| Act after a period without activity | [Idle Timer](patterns/polling-and-timers.md#idle-timer) (Inactivity tracker) | [polling-and-timers.md](patterns/polling-and-timers.md) |
| Take inbound messages or replies until a resolution or inactivity | [Idle Timer with Resolution](patterns/polling-and-timers.md#idle-timer-with-resolution) (Inbound event loop) | [polling-and-timers.md](patterns/polling-and-timers.md) |
| Recover when WaitFor fails | [WaitFor failure recovery](patterns/failure-handling.md#waitfor-failure-recovery) | [failure-handling.md](patterns/failure-handling.md) |
| Compensate after Execute retries are exhausted | [Execute failure recovery](patterns/failure-handling.md#execute-failure-recovery) | [failure-handling.md](patterns/failure-handling.md) |
| Stop retrying a provider error confirmed permanent | [Fail fast on permanent errors](patterns/failure-handling.md#fail-fast-on-permanent-errors) | [failure-handling.md](patterns/failure-handling.md) |
| Escalate a failure to a person | [Manual recovery](patterns/failure-handling.md#manual-recovery) | [failure-handling.md](patterns/failure-handling.md) |
| Handle an overall Flow deadline | [Graceful timeout](patterns/failure-handling.md#graceful-timeout) | [failure-handling.md](patterns/failure-handling.md) |
| Finish only after internal producers' messages are handled | [Drain internal Channels](patterns/channels-and-updates.md#drain-internal-channels) | [channels-and-updates.md](patterns/channels-and-updates.md) |
| Process bursty external messages in a short Flow | [Drain an external Channel](patterns/channels-and-updates.md#drain-an-external-channel) | [channels-and-updates.md](patterns/channels-and-updates.md) |
| Change or stop long repeating work on request, such as its batch size | [Control Long-Running Work](patterns/channels-and-updates.md#control-long-running-work) (formerly Stop Long-Running Work and Interruptible execution) | [channels-and-updates.md](patterns/channels-and-updates.md) |
| Route on whichever of several events happens first, such as a decision or its deadline | [Decision with Deadline](patterns/control-structures.md#decision-with-deadline) | [control-structures.md](patterns/control-structures.md) |
| Decide "no reply before the deadline" without silently ignoring a reply that an RPC accepted | [Close a reply window with an Attribute lock](patterns/control-structures.md#close-a-reply-window-with-an-attribute-lock) | [control-structures.md](patterns/control-structures.md) |
| Let parallel branches take turns on a sequence of Steps | [Critical section](patterns/control-structures.md#critical-section) | [control-structures.md](patterns/control-structures.md) |
| Stop child Flows when the case is canceled | [Cancel with child Flows](patterns/control-structures.md#cancel-with-child-flows) | [control-structures.md](patterns/control-structures.md) |
| Map a control structure to its Dex recipe | [Control structures](patterns/control-structures.md#control-structures) | [control-structures.md](patterns/control-structures.md) |
| Return to the caller at a durable milestone | [Responsive update](patterns/channels-and-updates.md#responsive-update) | [channels-and-updates.md](patterns/channels-and-updates.md) |
| One Flow per entity, queried with SQL | [Entity Flow with Attribute Sync](patterns/flow-shapes-and-state.md#entity-flow-with-attribute-sync) (formerly Entity store / Entity Flow with SQL Projection) | [flow-shapes-and-state.md](patterns/flow-shapes-and-state.md) |
| One long-lived Flow per business entity, without SQL queries | The plain Entity Flow in [Entity Flow with Attribute Sync](patterns/flow-shapes-and-state.md#entity-flow-with-attribute-sync) | [flow-shapes-and-state.md](patterns/flow-shapes-and-state.md) |
| Append-only history that grows | [Sequentially chunked AttributeMap](patterns/flow-shapes-and-state.md#sequentially-chunked-attributemap) | [flow-shapes-and-state.md](patterns/flow-shapes-and-state.md) |
| Keyed records with direct lookup | [Hash-partitioned AttributeMap](patterns/flow-shapes-and-state.md#hash-partitioned-attributemap) | [flow-shapes-and-state.md](patterns/flow-shapes-and-state.md) |
| Durable AI agent | [Durable AI agents](patterns/ai-agents.md#durable-ai-agents) | [ai-agents.md](patterns/ai-agents.md) |

Use Channels for durable commands, Attributes for durable state, and Streams for best-effort progress.

## Self-check

- Does the graph expose every success, wait, retry exhaustion, cancellation, timeout, and terminal path?
- Is every runtime-sized fan-out bounded?
- Are losing or abandoned branches safe?
- Is progress classified as durable or best effort?
- Is every loop and external wait bounded, or given an explicit stop path?
- Does each pattern you used still fit the business, checked against the facts its `Derived from:` line names?
