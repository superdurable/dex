# Workflow Patterns mapping

This file maps the 43 control-flow patterns of the Workflow Patterns
Initiative (workflowpatterns.com, WCP-1 to WCP-43) to Dex constructs. Every
SDK example implements the same Flows from this table.

Naming rules:

- Flow type and Step type strings are identical in every language.
- Channel, Join, and Attribute names are durable string literals. They are
  byte-identical in every language and use the camelCase strings in this spec,
  such as `"fulfillmentBranchCompleted"`. Never re-case them.
- Only RPC handler names and code identifiers follow each language's casing
  convention, for example **approvePayment** in TypeScript and Java,
  **ApprovePayment** in Go (the Go SDK uses the exported method name), and
  **approve_payment** in Python and Rust.

Data, resource, and exception-handling patterns are out of scope.

## Rating scheme

- `+` direct support: one documented Dex construct satisfies every defining
  clause of the pattern, with no application bookkeeping. A construct is a
  Primitive, a Step decision, a wait, or an SDK API that all five SDKs provide
  and document as the standard form, such as the Join.
- `+/-` indirect support (the literature's partial support): the pattern needs application bookkeeping, such as
  Channel signals that branches publish, Attribute counters or flags, an RPC
  guard, a loop that waits again, a lock used as a mutex, or a Channel used as
  a token, or one defining clause holds only in a restricted form.
- `-` not supported. The example would be the closest approximation, not the pattern.
  Its test asserts the approximation's behavior, and the spec names the
  defining clause it fails.

How joins are rated: the Join is the join construct inside one Flow. It is a
ChannelMap of branch results. Fork starts the branches and the join Step in one
decision and gives them one JoinRound token `{roundKey, branchCount}`; the
round key is the forking Step execution ID. Each branch ends with EndBranch,
the join waits ForAllBranches, ForFirstBranch, or ForBranches(n), and the next
round's split Step resets the previous round with ForRemainingBranches. The
application keeps no branch count, round key, or reset Step, so these joins are
`+`. Joins whose instance count grows while instances run (WCP-15, WCP-36), the
per-branch queueing AND-join (WCP-33), and the reachability merge (WCP-38) stay
`+/-`. When the work is split into child Flows, a **SubFlow** AllOf wait is the
native join.

## Ratings

| WCP | Name | Rating | Dex construct | Flow type |
| --- | --- | --- | --- | --- |
| WCP-1 | Sequence | + | GoTo | SequenceFlow |
| WCP-2 | Parallel Split | + | GoToMany | ParallelSplitSynchronizationFlow |
| WCP-3 | Synchronization | + | Join: Fork starts the branches and the join Step; each branch ends with EndBranch; the join waits ForAllBranches | ParallelSplitSynchronizationFlow |
| WCP-4 | Exclusive Choice | + | Execute returns GoTo to exactly one Step | ExclusiveChoiceSimpleMergeFlow |
| WCP-5 | Simple Merge | + | Several Steps GoTo the same Step type | ExclusiveChoiceSimpleMergeFlow |
| WCP-6 | Multi-Choice | + | GoToMany with a computed subset of Steps | MultiChoiceSynchronizingMergeFlow |
| WCP-7 | Structured Synchronizing Merge | + | The Multi-Choice decision forks a Join with only the selected branches; the join waits ForAllBranches | MultiChoiceSynchronizingMergeFlow |
| WCP-8 | Multi-Merge | + | Each branch GoTo the same Step type; each arrival is a new Step execution | MultiMergeFlow |
| WCP-9 | Structured Discriminator | + | Join per round; the join waits ForBranches(1); the next round's split Step waits ForRemainingBranches(previous round, 1), which resets the join | StructuredPartialJoinFlow (requiredQuotes = 1) |
| WCP-10 | Arbitrary Cycles | + | GoTo any Step; the cycle has two entry points and two exit points | ArbitraryCyclesFlow |
| WCP-11 | Implicit Termination | + | GracefulComplete closes the Flow only when no Step is active | ImplicitTerminationFlow |
| WCP-12 | Multiple Instances without Synchronization | + | GoToMany N executions of one Step type; nothing waits for them | MultipleInstancesWithoutSynchronizationFlow |
| WCP-13 | Multiple Instances with a priori Design-Time Knowledge | + | Fork a Join with a fixed list of instances; the join waits ForAllBranches | DesignTimeMultipleInstancesFlow |
| WCP-14 | Multiple Instances with a priori Run-Time Knowledge | + | Fork a Join with one instance per input item; the join waits ForAllBranches | RunTimeMultipleInstancesFlow |
| WCP-15 | Multiple Instances without a priori Run-Time Knowledge | +/- | RPC result next Steps add instances; an Attribute counts them; the join re-waits ForOne until intake is closed and every instance reported | NoPriorKnowledgeMultipleInstancesFlow |
| WCP-16 | Deferred Choice | + | WaitFor AnyOf over Channels; Dex consumes only the selected condition, so the other branch never starts | DeferredChoiceFlow |
| WCP-17 | Interleaved Parallel Routing | +/- | Execute Attribute lock shared by every task; GoTo for the partial order. A lock covers one Execute only, not a Step with a WaitFor or a suspended task | InterleavedParallelRoutingFlow |
| WCP-18 | Milestone | +/- | Attribute marks the milestone state; an RPC checks it and returns a next Step only while it holds | MilestoneFlow |
| WCP-19 | Cancel Task | + | RPC result canceling Steps (Step type) plus next Steps | CancelTaskFlow |
| WCP-20 | Cancel Case | +/- | The Step that starts a SubFlow waits AnyOf(SubFlow, cancel Channel). On cancel, its Execute reads the child ID with SubFlow.getFlowId, stops the child with Client StopFlow, and returns ForceFail, which cancels every other active Step | CancelCaseFlow |
| WCP-21 | Structured Loop | + | A pre-test Step GoTo the loop body or the exit; the body GoTo the test | StructuredLoopFlow |
| WCP-22 | Recursion | + | A Step waits for a SubFlow of its own Flow type | FactorialRecursionFlow |
| WCP-23 | Transient Trigger | +/- | Channels persist messages, so an RPC guard under a shared Attribute lock accepts one trigger only while a Step waits and drops the rest | TransientTriggerFlow |
| WCP-24 | Persistent Trigger | + | Channel messages stay queued until a WaitFor consumes them | PersistentTriggerFlow |
| WCP-25 | Cancel Region | + | RPC result canceling several Step types at once | CancelRegionFlow |
| WCP-26 | Cancel Multiple Instance Activity | + | RPC result canceling the instance Step type; completed instances keep their results | CancelMultipleInstanceTaskFlow |
| WCP-27 | Complete Multiple Instance Activity | + | RPC result canceling the instances and the join, plus a next Step | CompleteMultipleInstanceTaskFlow |
| WCP-28 | Blocking Discriminator | + | Join per round; the join waits ForBranches(1); the next round's split Step waits ForRemainingBranches before it forks, so no round k + 1 branch exists before round k resets | BlockingPartialJoinFlow (requiredSignals = 1) |
| WCP-29 | Cancelling Discriminator | + | Join; the join waits ForBranches(1), then its decision cancels its sibling branch Steps | CancellingPartialJoinFlow (requiredBids = 1) |
| WCP-30 | Structured Partial Join | + | Join per round; the join waits ForBranches(n); the next round's split Step waits ForRemainingBranches(previous round, n), which consumes the other m-n results | StructuredPartialJoinFlow (requiredQuotes = 2) |
| WCP-31 | Blocking Partial Join | + | As WCP-28 with ForBranches(n) | BlockingPartialJoinFlow (requiredSignals = 2) |
| WCP-32 | Cancelling Partial Join | + | Join; the join waits ForBranches(n), then its decision cancels its sibling branch Steps | CancellingPartialJoinFlow (requiredBids = 2) |
| WCP-33 | Generalised AND-Join | +/- | WaitFor AllOf ForOne per Channel; extra signals stay queued for the next firing. Branches must publish the signals | GeneralizedAndJoinFlow |
| WCP-34 | Static Partial Join for Multiple Instances | + | Join per round over m instances; the join waits ForBranches(n); the next round's split Step waits ForRemainingBranches(previous round, n) | StaticPartialJoinForMultipleInstancesFlow |
| WCP-35 | Cancelling Partial Join for Multiple Instances | + | Join over m instances; the join waits ForBranches(n), then its decision cancels its sibling instance Steps | CancellingPartialJoinForMultipleInstancesFlow |
| WCP-36 | Dynamic Partial Join for Multiple Instances | +/- | RPC result next Steps add instances; the join re-waits ForOne and evaluates the completion condition; an Attribute disables new instances | DynamicPartialJoinForMultipleInstancesFlow |
| WCP-37 | Local Synchronizing Merge | + | Same as WCP-7: the diverging decision forks a Join with the active branches; the branch count lives in the JoinRound | MultiChoiceSynchronizingMergeFlow |
| WCP-38 | General Synchronizing Merge | +/- | An Attribute counts the threads that can still reach the merge, under one shared lock; the commit that brings it to 0 publishes a sentinel that the merge Step waits for | GeneralSynchronizingMergeFlow |
| WCP-39 | Critical Section | +/- | A single-message Channel used as a token; Attribute locks cover one Step method only, not a multi-Step section | CriticalSectionFlow |
| WCP-40 | Interleaved Routing | +/- | Execute Attribute lock shared by every task, then a Channel ForN join. A lock covers one Execute only | InterleavedRoutingFlow |
| WCP-41 | Thread Merge | + | Fork a Join with k executions of one Step type; the merge waits ForAllBranches | ThreadSplitMergeFlow |
| WCP-42 | Thread Split | + | GoToMany k executions of one Step type | ThreadSplitMergeFlow |
| WCP-43 | Explicit Termination | + | ForceComplete closes the Flow at once and cancels every other Step | ExplicitTerminationFlow |

Totals: 33 `+`, 10 `+/-`, 0 `-`.

### Rating notes

- **Round-based joins (WCP-9, WCP-28, WCP-30, WCP-31, WCP-34)** fork one Join
  round per split Step execution. The round key is that Step execution ID, so a
  late or next-round result cannot count toward the current round. The join
  passes its JoinRound to the next round's split Step, whose WaitFor waits
  ForRemainingBranches(previous round, n) and whose Execute records
  RemainingBranchResults in an Attribute before it forks the next round. The
  count n is the pattern parameter, not a counter. The last round's late
  results stay pending until the Flow closes.
- **WCP-28 and WCP-31 are `+`.** The definition says "Subsequent
  enablements of incoming branches are blocked until the Blocking
  Discriminator has reset" (WCP-31: "until the join has reset"). The next
  round's split Step waits for the reset before it forks, so no round k + 1
  branch exists until round k resets, and a next-round enablement is blocked by
  construction. The tests prove it has no effect before the reset.
- **Cancelling joins (WCP-29, WCP-32, WCP-35)** cancel with CancelSiblingSteps.
  Fork starts the branches and the join Step in one decision, so they are
  siblings, and the selector reaches exactly this round's branches.
- **Join limits.** Each branch reports exactly once on every exit path. A
  branch canceled from outside never reports, so a join that waits for every
  branch then waits forever; cancel the join with the branches or add a Timer.
  As in Petri nets and YAWL, this does not affect the rating.
- **WCP-38 is `+/-`.** The definition requires the merge to fire "when either
  (1) each active incoming branch has been enabled or (2) it is not possible
  that any branch that has not yet been enabled will be enabled at any future
  time". The pattern expects the merge itself to decide clause (2) from the
  process structure. In Dex, application code decides reachability at each
  GoTo: it counts the live threads in an Attribute and publishes a sentinel
  when the count reaches 0. That is bookkeeping, so the rating is `+/-`. The
  design follows the Drain Internal Channels sentinel pattern.
- **WCP-20 is `+/-`.** The definition says "This includes currently executing
  tasks, those which may execute at some future time and all sub-processes."
  Client StopFlow alone leaves running SubFlows alive. The example meets the
  definition with application bookkeeping: a cancel Channel that every
  SubFlow-starting Step also waits on, an explicit StopFlow of the child, and a
  follow-up watcher once no child runs. It uses only public APIs: the child ID
  comes from SubFlow.getFlowId in Execute. Several children of one Step use
  AnyCombinationOf({every SubFlow}, {cancel}) and stop each index.

## File layout

Flows are grouped by WCP category into ten source groups.

| Group | Flow types |
| --- | --- |
| basic control flow | SequenceFlow, ParallelSplitSynchronizationFlow, ExclusiveChoiceSimpleMergeFlow |
| multi-choice and merges | MultiChoiceSynchronizingMergeFlow, MultiMergeFlow, GeneralSynchronizingMergeFlow |
| partial joins | StructuredPartialJoinFlow, BlockingPartialJoinFlow, CancellingPartialJoinFlow |
| and-join and threads | GeneralizedAndJoinFlow, ThreadSplitMergeFlow |
| multiple instances | MultipleInstancesWithoutSynchronizationFlow, DesignTimeMultipleInstancesFlow, RunTimeMultipleInstancesFlow, NoPriorKnowledgeMultipleInstancesFlow, StaticPartialJoinForMultipleInstancesFlow, CancellingPartialJoinForMultipleInstancesFlow, DynamicPartialJoinForMultipleInstancesFlow |
| state based | DeferredChoiceFlow, InterleavedParallelRoutingFlow, MilestoneFlow, CriticalSectionFlow, InterleavedRoutingFlow |
| cancellation | CancelTaskFlow, CancelCaseFlow, BackgroundCheckFlow, CancelRegionFlow, CancelMultipleInstanceTaskFlow, CompleteMultipleInstanceTaskFlow |
| iteration | ArbitraryCyclesFlow, StructuredLoopFlow, FactorialRecursionFlow |
| termination | ImplicitTerminationFlow, ExplicitTerminationFlow |
| triggers | TransientTriggerFlow, PersistentTriggerFlow |

Planned paths (`<group>` is the group name in the language's file casing):

| Language | Source | Integration test |
| --- | --- | --- |
| TypeScript | `examples/typescript/src/patterns/workflow-patterns/<group-kebab>.ts`, plus `workflow-pattern-flows.ts` exporting every Flow instance | `examples/typescript/test/integ/workflow-patterns.test.ts` |
| Python | `examples/python/dex_examples/patterns/workflow_patterns/<group_snake>.py` | `examples/python/tests/integ/test_workflow_patterns.py` |
| Go | `examples/go/patterns/workflow-patterns/<group_snake>.go`, package `workflowpatterns` | `examples/go/integ/workflow_patterns_test.go` |
| Java | `examples/java/src/main/java/io/superdurable/dex/patterns/workflowpatterns/<FlowType>.java`, one file per Flow type | `examples/java/src/test/java/io/superdurable/dex/integ/WorkflowPatternsIntegTest.java` |
| Rust | `examples/rust/src/workflow_patterns/<group_snake>.rs`, plus the module root `examples/rust/src/workflow_patterns.rs` | `examples/rust/tests/workflow_patterns.rs` |

Group file names: `basic-control-flow`, `multi-choice-and-merges`,
`partial-joins`, `and-join-and-threads`, `multiple-instances`, `state-based`,
`cancellation`, `iteration`, `termination`, `triggers` (TypeScript), and the
same names with underscores for Python, Go, and Rust.

## Shared conventions

- Every Step type name below is unique across the whole catalog, so Go can keep
  every Step type in one package. Two Flows share Step types only where noted.
- Every RPC name is unique across the whole catalog and the rest of each
  example app, because one Client invokes RPCs by name for every registered
  Flow.
- A Step with no input takes the language's empty input (void, `None`,
  `struct{}`, `Void`, `()`). An RPC with no input or output does the same.
- **Trail**: a list of strings, the Step types run so far. A Step appends its
  own Step type before passing the trail on.
- **TaskInterval**: `{task: string, startedAtMillis: int64, finishedAtMillis: int64}`
  in Unix epoch milliseconds.
- "Parked" means a Step whose WaitFor waits for a Channel that the test never
  publishes. Reaching COMPLETED in a Flow whose other Steps used
  GracefulComplete proves the parked Step was withdrawn.
- Tests read `FlowResult.completions` (Step type plus decoded output). Every
  GracefulComplete and ForceComplete below returns an explicit output. A
  DeadEnd adds no completion. In TypeScript, string and number outputs travel
  as Dex scalars and lists and objects as JSON, so decode each with the
  matching codec.
- An RPC that declares an Attribute lock fails at once with an RPC lock
  conflict error while a Step holds that lock. Callers retry it.
- "Wait for X-1" means Client WaitForStepCompletion on Step execution
  `X`, number 1, with a bounded request timeout.
- Each test uses a unique Flow ID and bounded waits. Never sleep in a test to
  order events; order them with RPCs and WaitForStepCompletion.
- Interleaving tasks sleep 200 ms inside Execute so overlaps would show.
- A concurrency gate holds instances in flight: each instance's WaitFor
  marks it started in an AttributeMap and waits for one gate message. The
  test polls the started count through an RPC, then opens the gate.
- An absence check reads the unconsumed messages of a Channel through an RPC.
  Dex evaluates waits when a message is published, so a live waiting Step
  would already have consumed it.

## Examples and required test assertions

### basic control flow

**SequenceFlow** (WCP-1). Input: Trail (start with an empty list).
- Steps: ReceiveOrder -> CheckInventory -> ShipOrder, each by GoTo. ShipOrder
  returns GracefulComplete(trail).
- Test: COMPLETED; exactly one completion, from ShipOrder, with output
  `["ReceiveOrder", "CheckInventory", "ShipOrder"]`.

**ParallelSplitSynchronizationFlow** (WCP-2, WCP-3). Input: none.
- Join: fulfillmentBranchCompleted (string). Channel: paymentApproved (string).
- RPC approvePayment(): publishes `"approved"` to paymentApproved.
- Steps: SplitOrder (start) forks fulfillmentBranchCompleted with branches
  PackItems(round) and ChargePayment(round) and join
  SynchronizeFulfillment(round), returned as one GoToMany. PackItems returns
  EndBranch(round, `"PackItems"`). ChargePayment waits paymentApproved.ForOne,
  returns EndBranch(round, `"ChargePayment"`). SynchronizeFulfillment waits
  ForAllBranches(round), returns GracefulComplete(sorted BranchResults).
- Test: start; wait for PackItems-1; DescribeFlow is RUNNING; invoke
  approvePayment; COMPLETED; exactly one SynchronizeFulfillment completion with
  `["ChargePayment", "PackItems"]`.

**ExclusiveChoiceSimpleMergeFlow** (WCP-4, WCP-5). Input: claimAmount (int).
- Steps: RouteClaim: claimAmount >= 1000 -> GoTo(ManualReview,
  ["RouteClaim"]), else GoTo(AutoApprove, ["RouteClaim"]). ManualReview and
  AutoApprove each GoTo(CloseClaim, trail + self). CloseClaim returns
  GracefulComplete(trail + "CloseClaim").
- Test: 250 -> one completion, CloseClaim,
  `["RouteClaim", "AutoApprove", "CloseClaim"]`. 5000 -> CloseClaim,
  `["RouteClaim", "ManualReview", "CloseClaim"]`.

### multi-choice and merges

**MultiChoiceSynchronizingMergeFlow** (WCP-6, WCP-7, WCP-37). Input:
`{sendEmail: bool, sendSms: bool, sendPush: bool}`.
- Join: notificationSent (string).
- Steps: SelectNotificationChannels: selects the branch Steps whose flag is
  true. If none is selected, GracefulComplete(empty list); otherwise it forks
  notificationSent with only the selected branches and join
  SynchronizingMerge(round). SendEmail, SendSms, SendPush each return
  EndBranch(round, own Step type). SynchronizingMerge waits
  ForAllBranches(round), returns GracefulComplete(sorted BranchResults). The
  join input carries no count.
- Test: `{true, false, true}` -> COMPLETED; exactly one SynchronizingMerge
  completion with `["SendEmail", "SendPush"]`.

**MultiMergeFlow** (WCP-8). Input: none.
- Steps: StartReviews -> GoToMany(LegalReview, FinanceReview). Each GoTo
  RecordReview with its own Step type as input. RecordReview(reviewer string)
  returns GracefulComplete(reviewer).
- Test: COMPLETED; exactly two RecordReview completions, outputs
  `{"LegalReview", "FinanceReview"}`.

**GeneralSynchronizingMergeFlow** (WCP-38). A document review: after drafting,
an OR-split picks a legal check and/or a finance check. Finance can send the
draft back for rework, which re-drafts and triggers another legal check. The
finance branch can instead leave to archive and never reach the merge.
PublishDocument is the merge. The number of arrivals is unknown at the split.
- Input: DocumentReviewPlan `{isLegalCheckNeeded: bool, isFinanceCheckNeeded:
  bool, financeReworkRounds: int, isFinanceArchived: bool}`.
- DocumentDraft: `{plan: DocumentReviewPlan, revision: int}`.
- Attributes: liveBranchCount (int), the threads that can still reach the
  merge; mergedReviews (list of strings), the arrivals. Every Step that reads
  or writes them holds an Execute lock on liveBranchCount.
- Channel: mergeSentinel (string).
- Steps:
  - StartDocumentReview (start, input DocumentReviewPlan, locked): sets
    liveBranchCount to 1 -> GoToMany(DraftDocument(`{plan, revision: 0}`),
    PublishDocument).
  - DraftDocument(DocumentDraft, locked): revision 0 selects CheckLegalTerms
    when isLegalCheckNeeded and CheckFinanceTerms when isFinanceCheckNeeded; a
    rework revision (> 0) selects both. With n selected,
    liveBranchCount += n - 1 in the same commit; if it becomes 0, publish
    `"done"` to mergeSentinel. GoToMany(CheckLegalTerms(revision),
    CheckFinanceTerms(DocumentDraft)) for the selected ones, or DeadEnd when
    n is 0.
  - CheckLegalTerms(revision, locked): an arrival. Append
    `"CheckLegalTerms-" + revision` to mergedReviews, liveBranchCount -= 1,
    publish the sentinel if it becomes 0, DeadEnd.
  - CheckFinanceTerms(DocumentDraft, locked): revision <
    financeReworkRounds -> GoTo(DraftDocument, revision + 1), count unchanged;
    else isFinanceArchived -> liveBranchCount -= 1 (publish the sentinel if 0)
    and GoTo(ArchiveDocument); else an arrival: append
    `"CheckFinanceTerms-" + revision`, liveBranchCount -= 1, publish the
    sentinel if 0, DeadEnd.
  - ArchiveDocument returns GracefulComplete("ArchiveDocument").
  - PublishDocument, the merge, waits mergeSentinel.ForOne and returns
    GracefulComplete(sorted mergedReviews).
- Test (a) rework adds a review after the split: `{true, true, 1, false}`;
  COMPLETED; exactly one PublishDocument completion with `["CheckFinanceTerms-1",
  "CheckLegalTerms-0", "CheckLegalTerms-1"]`.
- Test (b) an archived branch does not block the merge: `{true, true, 0,
  true}`; COMPLETED; exactly one PublishDocument completion with
  `["CheckLegalTerms-0"]` and one ArchiveDocument completion.
- Test (c) the merge fires once, after the last thread that can reach it:
  `{false, true, 2, false}`; COMPLETED; exactly one PublishDocument completion,
  with `["CheckFinanceTerms-2", "CheckLegalTerms-1", "CheckLegalTerms-2"]`, so
  it includes the last arrival.

### partial joins

**StructuredPartialJoinFlow** (WCP-9 with requiredQuotes = 1, WCP-30 with 2).
Input: QuoteRound `{requiredQuotes, round, roundCount, previousRound?}`,
started with round 1, roundCount 2, and no previousRound.
- Channels: supplierAReleased, supplierBReleased, supplierCReleased (string).
- Join: quoteReceived (string).
- Attribute: ignoredQuotes (`{round, ignoredQuotes: sorted strings}`), the late
  quotes of the most recently reset round.
- RPC releaseSupplierQuote(supplier string `"A"`, `"B"`, or `"C"`): publishes
  to that supplier's Channel. Reject any other value with an input error.
- Steps: RequestQuotes(QuoteRound) WaitFor: no previousRound ->
  SkipImmediately; else ForRemainingBranches(previousRound, requiredQuotes),
  the reset. Execute: with a previousRound, sets ignoredQuotes to `{round - 1,
  sorted RemainingBranchResults(previousRound)}`; then forks quoteReceived with
  branches QuoteFromSupplierA/B/C(round) and join AcceptQuotes(`{quoteRound,
  round}`). QuoteFromSupplierX(round) waits supplierXReleased.ForOne, returns
  EndBranch(round, `"X"`). AcceptQuotes waits ForBranches(round,
  requiredQuotes); round < roundCount -> GoToMany(PlaceOrder(`{round,
  acceptedQuotes: sorted BranchResults}`), RequestQuotes(round + 1,
  previousRound = this JoinRound)); else GoTo(PlaceOrder), and the last round's
  late quotes stay pending. PlaceOrder returns GracefulComplete(its input).
  There is no reset Step and no `round-<k>` key.
- Test required = 1 (two full join cycles): release A; wait for PlaceOrder-1;
  release B; release C; wait for RequestQuotes-2; release B; wait for
  PlaceOrder-2; release A; release C; COMPLETED. PlaceOrder completions in
  execution order: `{round: 1, acceptedQuotes: ["A"]}`, `{round: 2,
  acceptedQuotes: ["B"]}`. Attribute ignoredQuotes: `{round: 1,
  ignoredQuotes: ["B", "C"]}`.
- Test required = 2: release A; wait for QuoteFromSupplierA-1; release B; wait
  for PlaceOrder-1; release C; wait for RequestQuotes-2; release C; wait for
  QuoteFromSupplierC-2; release A; wait for PlaceOrder-2; release B;
  COMPLETED. PlaceOrder: `{1, ["A", "B"]}`, `{2, ["A", "C"]}`.
  Attribute ignoredQuotes: `{1, ["C"]}`.

**BlockingPartialJoinFlow** (WCP-28 with requiredSignals = 1, WCP-31 with 2).
Input: BlockingJoinRound `{requiredSignals, round, previousRound?}` with
requiredSignals 1 or 2, started with round 1 and no previousRound. The Flow
runs two rounds.
- Channels: branchASignal, branchBSignal, branchCSignal (string), the
  enablement of each branch.
- Join: branchCompleted (string).
- RPC sendBranchSignal(branch `"A"`, `"B"`, or `"C"`): publishes the branch
  letter to that branch's Channel.
- RPC getQueuedBranchSignalCount() -> int, loads the three branch Channels:
  the total number of queued (unconsumed) branch signals.
- Steps: StartBlockingRound(BlockingJoinRound) (start) WaitFor: no
  previousRound -> SkipImmediately; else ForRemainingBranches(previousRound,
  requiredSignals). Execute forks branchCompleted with branches
  SignalFromBranchA/B/C(round) and join BlockingJoin(`{blockingRound, round}`).
  SignalFromBranchX(round) waits branchXSignal.ForOne, returns EndBranch(round,
  `"X"`). BlockingJoin waits ForBranches(round, requiredSignals); round < 2 ->
  GoToMany(ProcessRound(`{round, branches: sorted BranchResults}`),
  StartBlockingRound(round + 1, previousRound = this JoinRound)); else
  GoTo(ProcessRound). ProcessRound returns GracefulComplete(its input). There
  is no reset Step and no `round-<k>` key.
- Until round k resets, no round k + 1 branch exists, so a next-round signal
  stays queued in its branch Channel.
- Test required = 1: signal A; wait for ProcessRound-1; signal A again (a
  next-round enablement); getQueuedBranchSignalCount is 1, so it had no effect;
  signal B; signal C; wait for ProcessRound-2; signal B; signal C; COMPLETED;
  ProcessRound completions, in execution order: `{round: 1, branches: ["A"]}`,
  `{round: 2, branches: ["A"]}`.
- Test required = 2: signal A, B; wait for ProcessRound-1; signal A, B again;
  getQueuedBranchSignalCount is 2; signal C; wait for ProcessRound-2; signal C;
  COMPLETED; ProcessRound completions `{1, ["A", "B"]}`, `{2, ["A", "B"]}`.

**CancellingPartialJoinFlow** (WCP-29 with requiredBids = 1, WCP-32 with 2).
Input: requiredBids (int).
- Channels: carrierAReleased, carrierBReleased, carrierCReleased (string),
  carrierBookingConfirmed (string). Join: bidReceived (string).
- RPC releaseCarrierBid(carrier `"A"`, `"B"`, or `"C"`).
- RPC getQueuedCarrierReleaseCount() -> int, loads the three carrier release
  Channels: the total number of unconsumed release messages.
- RPC confirmCarrierBooking(): publishes to carrierBookingConfirmed.
- Steps: RequestBids forks bidReceived with branches BidFromCarrierA/B/C(round)
  and join AcceptBids(`{requiredBids, round}`). BidFromCarrierX waits
  carrierXReleased.ForOne, returns EndBranch(round, `"X"`). AcceptBids waits
  ForBranches(round, requiredBids), returns GoTo(ConfirmCarrierBooking, sorted
  BranchResults) with canceling sibling Steps BidFromCarrierA,
  BidFromCarrierB, BidFromCarrierC. ConfirmCarrierBooking
  waits carrierBookingConfirmed.ForOne, returns GracefulComplete(its input).
- Test required = 1: release B; wait for AcceptBids-1; release A; release C
  (late branches); getQueuedCarrierReleaseCount is 2, so the sibling cancel
  withdrew the other carriers and no late branch ran;
  confirmCarrierBooking; COMPLETED; the only completion is
  ConfirmCarrierBooking `["B"]`.
- Test required = 2: release A; wait for BidFromCarrierA-1; release C; wait for
  AcceptBids-1; release B (late); getQueuedCarrierReleaseCount is 1;
  confirmCarrierBooking; COMPLETED; the only completion is
  ConfirmCarrierBooking `["A", "C"]`.

### and-join and threads

**GeneralizedAndJoinFlow** (WCP-33). Input: `{expectedMatches, matchedCount}`,
started with matchedCount 0.
- Channels: invoiceReceived, paymentReceived (string).
- RPCs deliverInvoice(invoiceId string), deliverPayment(paymentId string).
- Steps: MatchInvoiceAndPayment(`{expectedMatches, matchedCount}`), start Step
  with matchedCount 0, waits AllOf(invoiceReceived.ForOne,
  paymentReceived.ForOne). Execute: GoToMany(RecordMatch(`{invoiceId,
  paymentId}`), and MatchInvoiceAndPayment(matchedCount + 1) only when
  matchedCount + 1 < expectedMatches). RecordMatch returns
  GracefulComplete(its input).
- Test expectedMatches = 2: deliverInvoice I1; deliverInvoice I2;
  deliverPayment P1; wait for RecordMatch-1; DescribeFlow is RUNNING;
  deliverPayment P2; COMPLETED; RecordMatch completions in execution order
  `{I1, P1}`, `{I2, P2}`.

**ThreadSplitMergeFlow** (WCP-41, WCP-42). Input: parcelCount (int).
- Join: parcelPacked (int).
- Steps: SplitShipment forks parcelPacked with parcelCount x
  PackParcel(`{parcelIndex, round}`) and join MergeParcels(round). PackParcel
  returns EndBranch(round, parcelIndex). MergeParcels waits
  ForAllBranches(round), returns GracefulComplete(sorted BranchResults).
- Test parcelCount = 3: COMPLETED; exactly one MergeParcels completion
  `[0, 1, 2]`.

### multiple instances

**MultipleInstancesWithoutSynchronizationFlow** (WCP-12). Input: attendee
names (list of strings).
- Channel: invitationReleased (string).
- RPC releaseInvitations(count int): publishes count messages.
- Steps: RegisterAttendees -> GoToMany(SendInvitation(name) for each name,
  ConfirmRegistration). SendInvitation waits invitationReleased.ForOne,
  returns GracefulComplete(name). ConfirmRegistration returns
  GracefulComplete("ConfirmRegistration").
- Test `["ana", "ben"]`: wait for ConfirmRegistration-1 while every invitation
  is still parked; DescribeFlow is RUNNING; releaseInvitations(2); COMPLETED;
  SendInvitation outputs `{"ana", "ben"}`; one ConfirmRegistration completion.

**DesignTimeMultipleInstancesFlow** (WCP-13). Input: none.
- AttributeMap: startedReviewers (bool, instance key = reviewer).
- Channel: reviewGateOpened (string). Join: reviewCompleted (string). Shares
  Step types ReviewDocument and CollectReviews, the AttributeMap, the Channel,
  and the Join with RunTimeMultipleInstancesFlow.
- RPC getStartedDesignTimeReviewCount() -> int, loads startedReviewers: its
  map size.
- RPC openDesignTimeReviewGate(count int): publishes count messages to
  reviewGateOpened.
- Steps: StartDesignTimeReviews forks reviewCompleted with
  ReviewDocument(`{reviewer, round}`) for "legal", "finance", and "security"
  and join CollectReviews(round). ReviewDocument WaitFor sets
  startedReviewers[reviewer] = true and waits reviewGateOpened.ForOne (the
  concurrency gate); Execute returns EndBranch(round, reviewer).
  CollectReviews waits ForAllBranches(round), returns GracefulComplete(sorted
  BranchResults).
- Test: poll getStartedDesignTimeReviewCount until 3 (all three instances are
  in flight at once, held by the gate); DescribeFlow is RUNNING;
  openDesignTimeReviewGate(3);
  COMPLETED; one CollectReviews completion `["finance", "legal", "security"]`.

**RunTimeMultipleInstancesFlow** (WCP-14). Input: reviewers (list of
strings).
- Steps: StartRunTimeReviews forks reviewCompleted with one ReviewDocument
  per reviewer and join CollectReviews(round). The same gate, AttributeMap,
  Channel, and Join as DesignTimeMultipleInstancesFlow.
- RPC getStartedRunTimeReviewCount() -> int and RPC
  openRunTimeReviewGate(count int), the same as the design-time RPCs.
- Test `["dee", "ana", "cy", "ben"]`: poll getStartedRunTimeReviewCount until
  4; DescribeFlow is RUNNING; openRunTimeReviewGate(4); COMPLETED; one
  CollectReviews
  completion `["ana", "ben", "cy", "dee"]`.

**NoPriorKnowledgeMultipleInstancesFlow** (WCP-15). Input: analyzed sample IDs
(list of strings), started empty.
- Attributes: startedSampleCount (int), isSampleIntakeClosed (bool).
- AttributeMap: analysisStartedSamples (bool, instance key = sampleId).
- Channels: sampleAnalyzed (string), sampleIntakeClosed (string),
  sampleGateOpened (string).
- RPC addSample(sampleId string) -> bool, locks startedSampleCount: closed ->
  false; else increment startedSampleCount and return true with next Step
  AnalyzeSample(sampleId).
- RPC closeSampleIntake(), locks startedSampleCount: sets
  isSampleIntakeClosed and publishes `"closed"` to sampleIntakeClosed.
- RPC getStartedAnalysisCount() -> int, loads analysisStartedSamples: its map
  size.
- RPC openSampleGate(count int): publishes count messages to sampleGateOpened.
- Steps: JoinSamples(analyzed list), the start Step with an empty list. WaitFor:
  closed and analyzed count == started -> SkipImmediately; closed ->
  sampleAnalyzed.ForOne; open -> AnyOf(sampleAnalyzed.ForOne,
  sampleIntakeClosed.ForOne). Execute (no lock: the count cannot change
  once intake is closed): append consumed samples; closed and count == started ->
  GoTo(ReportSamples, analyzed); else GoTo(JoinSamples, analyzed).
  AnalyzeSample WaitFor sets analysisStartedSamples[sampleId] = true and waits
  sampleGateOpened.ForOne; Execute publishes its sampleId to sampleAnalyzed,
  DeadEnd. ReportSamples returns GracefulComplete(sorted).
- Test: addSample s1 and s2 return true; poll getStartedAnalysisCount until 2;
  addSample s3 returns true while s1 and s2 are still running; poll until 3;
  closeSampleIntake; addSample s4 returns false; DescribeFlow is RUNNING;
  openSampleGate(3); COMPLETED; ReportSamples `["s1", "s2", "s3"]`.

**StaticPartialJoinForMultipleInstancesFlow** (WCP-34). Input: InspectionRound
`{inspectionCount, requiredInspections, round, roundCount, previousRound?}`,
started with round 1, roundCount 2, and no previousRound.
- Channel: inspectionReleased (string).
- Join: unitInspected (int).
- Attribute: ignoredInspections (`{round, unitIndexes: sorted ints}`), the late
  inspections of the most recently reset round.
- RPC releaseInspections(count int): publishes count messages.
- Steps: StartInspections(InspectionRound) WaitFor: no previousRound ->
  SkipImmediately; else ForRemainingBranches(previousRound,
  requiredInspections). Execute: with a previousRound, sets ignoredInspections
  to `{round - 1, sorted RemainingBranchResults(previousRound)}`; then forks
  unitInspected with inspectionCount x InspectUnit(`{unitIndex, round}`) and
  join AcceptInspections(`{inspection, round}`). InspectUnit waits
  inspectionReleased.ForOne, returns EndBranch(round, unitIndex).
  AcceptInspections waits ForBranches(round, requiredInspections); round <
  roundCount -> GoToMany(ApproveBatch(`{round, unitIndexes: sorted
  BranchResults}`), StartInspections(round + 1, previousRound = this
  JoinRound)); else GoTo(ApproveBatch). ApproveBatch returns
  GracefulComplete(its input). There is no reset Step and no `round-<k>` key.
- Test `{3, 2, 1, 2}` (two full join cycles): releaseInspections(2); wait for
  ApproveBatch-1; DescribeFlow is RUNNING; releaseInspections(1); wait for
  StartInspections-2; releaseInspections(2); wait for ApproveBatch-2;
  releaseInspections(1); COMPLETED. Two ApproveBatch completions, rounds 1
  and 2, each with 2 distinct indexes; Attribute ignoredInspections is round 1
  with 1 index; the round-1 ApproveBatch and ignoredInspections indexes
  together are {0, 1, 2}.

**CancellingPartialJoinForMultipleInstancesFlow** (WCP-35). Input:
`{inspectionCount, requiredInspections}`.
- Channels: quorumInspectionReleased (string), quorumCertified (string).
  Join: quorumUnitInspected (int).
- RPC releaseQuorumInspections(count int).
- RPC getQueuedQuorumReleaseCount() -> int, loads quorumInspectionReleased:
  its unconsumed message count.
- RPC certifyQuorum(): publishes to quorumCertified.
- Steps: StartQuorumInspections forks quorumUnitInspected with
  inspectionCount x InspectQuorumUnit(`{unitIndex, round}`) and join
  AcceptQuorumInspections(`{requiredInspections, round}`). InspectQuorumUnit
  waits quorumInspectionReleased.ForOne, returns EndBranch(round, unitIndex).
  AcceptQuorumInspections waits ForBranches(round, requiredInspections),
  returns GoTo(CertifyQuorum, sorted BranchResults) with canceling sibling
  Steps InspectQuorumUnit.
  CertifyQuorum waits quorumCertified.ForOne, returns GracefulComplete(its
  input).
- Test `{3, 2}`: releaseQuorumInspections(2); wait for
  AcceptQuorumInspections-1; releaseQuorumInspections(1) (late);
  getQueuedQuorumReleaseCount is 1, so the sibling cancel withdrew the last
  instance and it never ran; certifyQuorum;
  COMPLETED; the only completion is CertifyQuorum with 2 distinct indexes.

**DynamicPartialJoinForMultipleInstancesFlow** (WCP-36). Input:
`{requiredAcceptedBids, acceptedBids}`, started with an empty acceptedBids list.
- Attribute: isBidAdmissionClosed (bool).
- AttributeMap: evaluationStartedBids (bool, instance key = the amount as a
  decimal string).
- Channels: bidEvaluated (`{amount: int, isAccepted: bool}`), bidGateOpened
  (string), contractSigned (string).
- RPC addBid(amount int) -> bool, locks isBidAdmissionClosed: closed -> false;
  else true with next Step EvaluateBid(amount).
- RPC getStartedEvaluationCount() -> int, loads evaluationStartedBids: its map
  size.
- RPC openBidGate(count int): publishes count messages to bidGateOpened.
- RPC signContract(): publishes to contractSigned.
- Steps: JoinBids(`{requiredAcceptedBids, acceptedBids: int list}`), the start
  Step with an empty list, waits bidEvaluated.ForOne. Execute, with an Execute
  lock on isBidAdmissionClosed: append accepted amounts; count >= required ->
  set isBidAdmissionClosed, GoTo(AwardContract, accepted); else GoTo(JoinBids,
  updated). EvaluateBid WaitFor sets evaluationStartedBids[amount] = true and
  waits bidGateOpened.ForOne; Execute publishes `{amount, isAccepted: amount <=
  1000}` to bidEvaluated, DeadEnd. AwardContract waits contractSigned.ForOne,
  returns GracefulComplete(sorted amounts).
- Test required = 2 (retry any RPC that fails with the RPC lock conflict
  error): addBid 500 and 1500 return true; poll getStartedEvaluationCount until
  2; addBid 700 returns true while the others run; poll until 3;
  openBidGate(3); poll the Client Attribute read until isBidAdmissionClosed is
  true; addBid 400 returns false; signContract; COMPLETED; AwardContract
  `[500, 700]`.

### state based

**DeferredChoiceFlow** (WCP-16). Input: none.
- Channels: offerPresented, offerAccepted, offerDeclined (string).
- RPCs presentOffer(), acceptOffer(), declineOffer().
- Steps: PresentOffer (start) waits offerPresented.ForOne ->
  GoTo(AwaitCustomerResponse). AwaitCustomerResponse waits
  AnyOf(offerAccepted.ForOne, offerDeclined.ForOne); accepted results non-empty
  -> GoTo(FulfillOffer), else GoTo(ArchiveOffer). Each returns
  GracefulComplete(its own Step type).
- Test single response: presentOffer; wait for PresentOffer-1; declineOffer;
  COMPLETED; the only completion is ArchiveOffer.
- Test competing responses: acceptOffer and declineOffer both before
  presentOffer, so both are queued when the choice resolves; presentOffer;
  COMPLETED; exactly one completion, FulfillOffer or ArchiveOffer; the other
  branch never ran.

**InterleavedParallelRoutingFlow** (WCP-17). Input: none.
- Attribute: onboardingTaskLock (bool), used only as a lock key.
- Channel: onboardingTaskFinished (TaskInterval).
- Steps: StartOnboardingTasks -> GoToMany(PrepareContract, RunCreditCheck,
  CompleteOnboardingTasks(3)). PrepareContract, SignContract, and
  RunCreditCheck each hold an Execute lock on onboardingTaskLock, sleep 200 ms,
  and publish their TaskInterval. PrepareContract then GoTo(SignContract); the
  others DeadEnd. CompleteOnboardingTasks(count) waits ForN(count), returns
  GracefulComplete(intervals).
- Restriction: each task is one Execute. The lock does not cover a WaitFor or
  a task that suspends across several Steps.
- Test: COMPLETED; 3 intervals for PrepareContract, SignContract,
  RunCreditCheck; no two overlap; PrepareContract finishes no later than
  SignContract starts.

**MilestoneFlow** (WCP-18). Input: none.
- Attribute: isChangeWindowOpen (bool).
- Channels: releaseApproved, releaseFinalized (string).
- RPC requestConfigChange(change string) -> bool: window open -> true with next
  Step ApplyConfigChange(change); else false.
- RPCs approveRelease(), finalizeRelease().
- Steps: OpenChangeWindow (start) sets isChangeWindowOpen true ->
  GoTo(AwaitReleaseApproval). AwaitReleaseApproval waits
  releaseApproved.ForOne, sets isChangeWindowOpen false ->
  GoTo(AwaitReleaseFinalization). AwaitReleaseFinalization waits
  releaseFinalized.ForOne, returns GracefulComplete("AwaitReleaseFinalization").
  ApplyConfigChange returns GracefulComplete(change).
- Test: poll the Client Attribute read until isChangeWindowOpen is true;
  requestConfigChange("raise-limit") returns true; approveRelease; poll until
  isChangeWindowOpen is false; requestConfigChange("late-change") returns
  false; finalizeRelease; COMPLETED; ApplyConfigChange outputs exactly
  `["raise-limit"]`.

**CriticalSectionFlow** (WCP-39). Input: none.
- Channels: criticalSectionToken (string), criticalSectionFinished
  (TaskInterval).
- Steps: StartCriticalSections publishes `"token"` to criticalSectionToken ->
  GoToMany(ReserveStock, RecalculatePrice, FinishCriticalSections(2)).
  Section `"inventory"`: ReserveStock waits criticalSectionToken.ForOne, records
  its start time, sleeps 100 ms -> GoTo(CommitStock, start time). CommitStock
  sleeps 100 ms, publishes TaskInterval{"inventory", start, now}, publishes the
  token back, DeadEnd. Section `"pricing"`: RecalculatePrice -> PublishPrice,
  the same way. FinishCriticalSections(count) waits ForN(count), returns
  GracefulComplete(intervals).
- Test: COMPLETED; 2 intervals, inventory and pricing, that do not overlap.

**InterleavedRoutingFlow** (WCP-40). Input: none.
- Attribute: auditLock (bool), used only as a lock key.
- Channel: auditFinished (TaskInterval).
- Steps: StartAudits -> GoToMany(AuditPayroll, AuditExpenses, AuditTaxes,
  CompleteAudits(3)). Each audit holds an Execute lock on auditLock, sleeps
  200 ms, publishes its TaskInterval, DeadEnd. CompleteAudits(count) waits
  ForN(count), returns GracefulComplete(intervals).
- Restriction: the same single-Execute restriction as WCP-17.
- Test: COMPLETED; 3 intervals, one per audit; no two overlap.

### cancellation

**CancelTaskFlow** (WCP-19). Input: none.
- Channel: courierArrived (string).
- RPCs confirmCourierPickup() publishes; cancelCourierPickup() returns
  canceling Step AwaitCourierPickup and next Step ArrangeSelfDelivery.
- Steps: AwaitCourierPickup (start) waits courierArrived.ForOne, returns
  GracefulComplete("AwaitCourierPickup"). ArrangeSelfDelivery returns
  GracefulComplete("ArrangeSelfDelivery").
- Test: cancelCourierPickup; COMPLETED; the only completion is
  ArrangeSelfDelivery.

**CancelCaseFlow** (WCP-20) with child **BackgroundCheckFlow**.
- CancelCaseFlow input: none.
- Attribute: stoppedChildFlowIds (list of strings).
- Channels: applicationReviewed (string), cancelCase (string).
- RPC cancelCase(reason string): publishes the reason to cancelCase.
- Steps: StartApplication (start) -> GoToMany(ReviewApplication,
  RunBackgroundCheck). ReviewApplication waits applicationReviewed.ForOne,
  returns GracefulComplete("ReviewApplication").
- RunBackgroundCheck waits AnyOf(SubFlow.run(BackgroundCheckFlow, none),
  cancelCase.ForOne). Execute:
  - cancelCase results non-empty: childFlowId = SubFlow.getFlowId(context, 0);
    stop it with Client StopFlow (cancel type), treating a not-found or
    already closed Flow as success; set stoppedChildFlowIds to
    `[childFlowId]`; return ForceFail("case canceled: " + reason). ForceFail
    cancels every other active Step, such as ReviewApplication.
  - otherwise the child completed: GoTo(WatchCaseCancellation).
- WatchCaseCancellation waits cancelCase.ForOne, returns ForceFail("case
  canceled: " + reason). It covers a cancel after the child finished, so
  exactly one Step waits on cancelCase at a time.
- Only public APIs: the child ID comes from SubFlow.getFlowId, not from a
  derived string. If cancelCase is queued before the WaitFor commits, the
  WaitFor still starts the child and AnyOf selects the cancel at once, so the
  child is stopped.
- BackgroundCheckFlow input: none. Channel backgroundCheckCompleted (string).
  Step AwaitBackgroundCheck (start) waits backgroundCheckCompleted.ForOne,
  returns GracefulComplete("AwaitBackgroundCheck").
- Test: start; wait for StartApplication-1; cancelCase("applicant withdrew");
  the parent reaches FAILED and has no ReviewApplication completion; the
  Client Attribute read of stoppedChildFlowIds has one ID; poll DescribeFlow
  of that child until it is CANCELED. The child parks forever, so CANCELED
  proves a running child was stopped.

**CancelRegionFlow** (WCP-25). Input: none.
- Channels: identityVerified, creditHistoryChecked, sanctionsScreened (string).
- RPC rejectApplication(): canceling Steps VerifyIdentity and
  CheckCreditHistory, next Step NotifyRejection.
- RPC completeSanctionsScreening(): publishes to sanctionsScreened.
- Steps: StartApplicationChecks -> GoToMany(VerifyIdentity,
  CheckCreditHistory, ScreenSanctions). Each waits its own Channel and returns
  GracefulComplete(its own Step type). NotifyRejection returns
  GracefulComplete("NotifyRejection").
- Test: wait for StartApplicationChecks-1; rejectApplication; wait for
  NotifyRejection-1; DescribeFlow is RUNNING; completeSanctionsScreening;
  COMPLETED; completion Step types are exactly {NotifyRejection,
  ScreenSanctions}.

**CancelMultipleInstanceTaskFlow** (WCP-26). Input: translationCount (int).
- Attribute: finishedTranslationCount (int).
- Channel: translationReleased (string).
- RPC releaseTranslation(): publishes one message. RPC cancelTranslations():
  canceling Step TranslateDocument, next Step ReportTranslations.
- Steps: StartTranslations -> GoToMany(translationCount x
  TranslateDocument(index)). TranslateDocument waits translationReleased.ForOne;
  Execute, with an Execute lock on finishedTranslationCount, increments it and
  returns GracefulComplete(index). ReportTranslations returns
  GracefulComplete(finishedTranslationCount).
- Test translationCount = 3: wait for StartTranslations-1; releaseTranslation;
  poll the Client Attribute read until finishedTranslationCount is 1;
  cancelTranslations; COMPLETED; exactly one TranslateDocument completion;
  ReportTranslations output 1.

**CompleteMultipleInstanceTaskFlow** (WCP-27). Input: reviewerCount (int).
- Channels: peerReviewReleased (string), peerReviewSubmitted (int).
- RPC releasePeerReview(): publishes one message.
- RPC getSubmittedPeerReviewCount() -> int, loads peerReviewSubmitted: its
  pending message count.
- RPC completePeerReviewsEarly(), loads peerReviewSubmitted: canceling Steps
  SubmitPeerReview and CollectPeerReviews, next Step
  PublishReviewResults(sorted pending indexes).
- Steps: StartPeerReviews -> GoToMany(reviewerCount x SubmitPeerReview(index),
  CollectPeerReviews(reviewerCount)). SubmitPeerReview waits
  peerReviewReleased.ForOne, publishes its index to peerReviewSubmitted,
  DeadEnd. CollectPeerReviews(count) waits ForN(count) ->
  GoTo(PublishReviewResults, sorted indexes). PublishReviewResults returns
  GracefulComplete(its input).
- Test reviewerCount = 3: wait for StartPeerReviews-1; releasePeerReview; poll
  getSubmittedPeerReviewCount until 1; completePeerReviewsEarly; COMPLETED;
  exactly one completion, PublishReviewResults, with one index.

### iteration

**ArbitraryCyclesFlow** (WCP-10). Input: `{hasDraft: bool, shouldAbandon:
bool}`.
- The Draft and Edit cycle has two entry points (DraftArticle, EditArticle)
  and two exit points (EditArticle -> PublishArticle, DraftArticle ->
  AbandonArticle).
- Steps: RouteSubmission: revision = `{revision: 0, shouldAbandon, trail:
  ["RouteSubmission"]}`; hasDraft -> GoTo(EditArticle, revision), else
  GoTo(DraftArticle, revision). DraftArticle: shouldAbandon and revision >= 1
  -> GoTo(AbandonArticle, trail + self), else GoTo(EditArticle, trail + self).
  EditArticle: revision >= 1 -> GoTo(PublishArticle, trail + self), else
  GoTo(DraftArticle, `{revision + 1, shouldAbandon, trail + self}`).
  PublishArticle and AbandonArticle return GracefulComplete(trail + self).
- Test `{hasDraft: false, shouldAbandon: false}`: PublishArticle
  `["RouteSubmission", "DraftArticle", "EditArticle", "DraftArticle",
  "EditArticle", "PublishArticle"]`.
- Test `{hasDraft: true, shouldAbandon: false}`: PublishArticle
  `["RouteSubmission", "EditArticle", "DraftArticle", "EditArticle",
  "PublishArticle"]`.
- Test `{hasDraft: false, shouldAbandon: true}`: AbandonArticle
  `["RouteSubmission", "DraftArticle", "EditArticle", "DraftArticle",
  "AbandonArticle"]`.

**StructuredLoopFlow** (WCP-21). Input: `{remainingBatches, processedBatches}`
where processedBatches is a list of strings, started empty.
- Steps: CheckBatches (start), the pre-test: remainingBatches == 0 ->
  GoTo(FinishBatches, processedBatches), else GoTo(ProcessBatch, same input).
  ProcessBatch, the loop body, appends `"batch-" + remainingBatches` and
  GoTo(CheckBatches, `{remainingBatches - 1, appended list}`). FinishBatches
  returns GracefulComplete(processedBatches).
- Test `{3, []}`: FinishBatches `["batch-3", "batch-2", "batch-1"]`.
- Test `{0, []}`: FinishBatches `[]`, so ProcessBatch never ran.

**FactorialRecursionFlow** (WCP-22). Input: n (int).
- Steps: ComputeFactorial: WaitFor n <= 1 -> SkipImmediately, else
  SubFlow.run(FactorialRecursionFlow, n - 1). Execute: n <= 1 ->
  GracefulComplete(1), else GracefulComplete(n x the SubFlow single output).
- Test n = 4: COMPLETED; ComputeFactorial output 24.

### termination

**ImplicitTerminationFlow** (WCP-11). Input: none.
- Channel: profileCompleted (string). RPC completeProfile().
- Steps: StartOnboarding -> GoToMany(CreateAccount, SendWelcomeEmail,
  AwaitProfileCompletion). CreateAccount and SendWelcomeEmail return
  GracefulComplete(own Step type). AwaitProfileCompletion waits
  profileCompleted.ForOne, returns GracefulComplete(own Step type).
- Test: wait for CreateAccount-1 and SendWelcomeEmail-1; DescribeFlow is
  RUNNING; completeProfile; COMPLETED; completion Step types are exactly
  {CreateAccount, SendWelcomeEmail, AwaitProfileCompletion}.

**ExplicitTerminationFlow** (WCP-43). Input: none.
- Channels: diagnosticsCollected, onCallAcknowledged, incidentResolved
  (string). RPC resolveIncident().
- Steps: StartIncident -> GoToMany(CollectDiagnostics, NotifyOnCall,
  ResolveIncident). CollectDiagnostics and NotifyOnCall are parked on their
  Channels. ResolveIncident waits incidentResolved.ForOne, returns
  ForceComplete("ResolveIncident").
- Test: wait for StartIncident-1; resolveIncident; COMPLETED; the only
  completion is ResolveIncident.

### triggers

**TransientTriggerFlow** (WCP-23). Input: none.
- Attribute: isAwaitingMotionEvent (bool).
- Channels: motionSensorArmed (string), motionEvent (int),
  motionMonitoringStopped (string).
- RPC armMotionSensor(). RPC stopMotionMonitoring() publishes to
  motionMonitoringStopped. RPC sendMotionEvent(value int) -> bool, locks
  isAwaitingMotionEvent: when it is true, set it false, publish, and return
  true; else return false and drop the trigger. Clearing the guard in the same
  locked commit makes the first accepted trigger the only one.
- Steps: AwaitMotionSensorArmed (start) waits motionSensorArmed.ForOne ->
  GoTo(AwaitMotionEvent). AwaitMotionEvent holds a WaitFor lock on
  isAwaitingMotionEvent; WaitFor sets it true and waits motionEvent.ForOne;
  Execute returns GoTo(AwaitMonitoringStop, value). AwaitMonitoringStop(value)
  waits motionMonitoringStopped.ForOne, returns GracefulComplete(value). The
  Flow stays open after the accepted event, so a later trigger reaches the
  RPC guard instead of a closed Flow.
- Test (retry an RPC that fails with the RPC lock conflict error):
  sendMotionEvent(1) returns false; armMotionSensor; poll the Client Attribute
  read until isAwaitingMotionEvent is true; sendMotionEvent(2) returns true;
  sendMotionEvent(3) returns false; stopMotionMonitoring; COMPLETED;
  AwaitMonitoringStop output 2.

**PersistentTriggerFlow** (WCP-24). Input: none.
- Channels: carrierReady (string), shipmentLabel (string).
- RPCs markCarrierReady(), sendShipmentLabel(labelId string), which always
  publishes.
- Steps: AwaitCarrierReady (start) waits carrierReady.ForOne ->
  GoTo(AwaitShipmentLabel). AwaitShipmentLabel waits shipmentLabel.ForOne,
  returns GracefulComplete(labelId).
- Test: sendShipmentLabel("label-1") before markCarrierReady; then
  markCarrierReady; COMPLETED; AwaitShipmentLabel output `"label-1"`.
