# Workflow Patterns mapping

This file maps the 43 control-flow patterns of the Workflow Patterns
Initiative (workflowpatterns.com, WCP-1 to WCP-43) to Dex constructs. Every
SDK example implements the same Flows from this table. Each language uses the
same Flow type, Step type, Channel, Attribute, and RPC names, adjusted only
for its casing convention (for example **approvePayment** in TypeScript, Java,
and Go, and **approve_payment** in Python and Rust).

Data, resource, and exception-handling patterns are out of scope.

## Rating scheme

- `+` direct support: one documented Dex construct satisfies every defining
  clause of the pattern, with no application bookkeeping.
- `+/-` partial support: the pattern needs application state (Attribute
  counters or flags), an RPC guard, an extra reset Step, a Channel used as a
  token, or a SubFlow scope, or one defining clause holds only in a restricted
  form.
- `-` not supported.

## Ratings

| WCP | Name | Rating | Dex construct | Flow type |
| --- | --- | --- | --- | --- |
| WCP-1 | Sequence | + | GoTo | SequenceFlow |
| WCP-2 | Parallel Split | + | GoToMany | ParallelSplitSynchronizationFlow |
| WCP-3 | Synchronization | + | Branches publish to a Channel; the join Step waits Channel ForN(branch count) | ParallelSplitSynchronizationFlow |
| WCP-4 | Exclusive Choice | + | Execute returns GoTo to exactly one Step | ExclusiveChoiceSimpleMergeFlow |
| WCP-5 | Simple Merge | + | Several Steps GoTo the same Step type | ExclusiveChoiceSimpleMergeFlow |
| WCP-6 | Multi-Choice | + | GoToMany with a computed subset of Steps | MultiChoiceSynchronizingMergeFlow |
| WCP-7 | Structured Synchronizing Merge | + | The Multi-Choice decision starts the join Step with the selected branch count as input; it waits Channel ForN(count) | MultiChoiceSynchronizingMergeFlow |
| WCP-8 | Multi-Merge | + | Each branch GoTo the same Step type; each arrival is a new Step execution | MultiMergeFlow |
| WCP-9 | Structured Discriminator | +/- | Channel ForN(1) join plus an extra reset Step that waits ForN(n-1) for the remaining branches | StructuredPartialJoinFlow (requiredQuotes = 1) |
| WCP-10 | Arbitrary Cycles | + | GoTo any Step, including Steps already run; cycles may have several entry points | ArbitraryCyclesFlow |
| WCP-11 | Implicit Termination | + | GracefulComplete closes the Flow only when no Step is active | ImplicitTerminationFlow |
| WCP-12 | Multiple Instances without Synchronization | + | GoToMany N executions of one Step type; nothing waits for them | MultipleInstancesWithoutSynchronizationFlow |
| WCP-13 | Multiple Instances with a priori Design-Time Knowledge | + | GoToMany with a fixed count plus Channel ForN(count) join | DesignTimeMultipleInstancesFlow |
| WCP-14 | Multiple Instances with a priori Run-Time Knowledge | + | GoToMany with a count computed from input plus Channel ForN(count) join | RunTimeMultipleInstancesFlow |
| WCP-15 | Multiple Instances without a priori Run-Time Knowledge | +/- | RPC result next Steps add instances; an Attribute counts started instances; the join Step re-waits ForOne in a loop until intake is closed and every instance reported | NoPriorKnowledgeMultipleInstancesFlow |
| WCP-16 | Deferred Choice | + | WaitFor AnyOf over Channels; Dex consumes only the selected condition | DeferredChoiceFlow |
| WCP-17 | Interleaved Parallel Routing | + | Execute Attribute lock shared by every task; GoTo for the partial order | InterleavedParallelRoutingFlow |
| WCP-18 | Milestone | +/- | Attribute marks the milestone state; an RPC checks it and returns a next Step only while it holds | MilestoneFlow |
| WCP-19 | Cancel Task | + | RPC result canceling Steps (Step type) plus next Steps | CancelTaskFlow |
| WCP-20 | Cancel Case | +/- | Client StopFlow cancels every active Step and records the run as canceled; running SubFlows keep running and must be stopped separately | CancelCaseFlow |
| WCP-21 | Structured Loop | + | Step GoTo itself with a pre-test condition | StructuredLoopFlow |
| WCP-22 | Recursion | + | A Step waits for a SubFlow of its own Flow type | FactorialRecursionFlow |
| WCP-23 | Transient Trigger | +/- | Channels persist messages, so an RPC guard drops a trigger unless an Attribute written by WaitFor says a Step is waiting | TransientTriggerFlow |
| WCP-24 | Persistent Trigger | + | Channel messages stay queued until a WaitFor consumes them | PersistentTriggerFlow |
| WCP-25 | Cancel Region | + | RPC result canceling several Step types at once | CancelRegionFlow |
| WCP-26 | Cancel Multiple Instance Activity | + | RPC result canceling the instance Step type; completed instances keep their results | CancelMultipleInstanceTaskFlow |
| WCP-27 | Complete Multiple Instance Activity | + | RPC result canceling the instances and the join, plus a next Step | CompleteMultipleInstanceTaskFlow |
| WCP-28 | Blocking Discriminator | +/- | One Channel per branch, join waits AnyOf; a reset Step waits AllOf the remaining branches; later arrivals stay queued until the reset | BlockingPartialJoinFlow (requiredSignals = 1) |
| WCP-29 | Cancelling Discriminator | + | Join waits ForN(1), then its decision cancels the other branches | CancellingPartialJoinFlow (requiredBids = 1) |
| WCP-30 | Structured Partial Join | +/- | Channel ForN(n) join plus an extra reset Step that waits ForN(m-n) | StructuredPartialJoinFlow (requiredQuotes = 2) |
| WCP-31 | Blocking Partial Join | +/- | One Channel per branch, join waits AnyCombinationOf every n-subset; reset Step waits AllOf the rest | BlockingPartialJoinFlow (requiredSignals = 2) |
| WCP-32 | Cancelling Partial Join | + | Join waits ForN(n), then its decision cancels the other branches | CancellingPartialJoinFlow (requiredBids = 2) |
| WCP-33 | Generalised AND-Join | + | WaitFor AllOf ForOne per Channel; extra messages stay queued for the next firing | GeneralizedAndJoinFlow |
| WCP-34 | Static Partial Join for Multiple Instances | +/- | Channel ForN(n) join plus an extra reset Step that waits ForN(m-n) | StaticPartialJoinForMultipleInstancesFlow |
| WCP-35 | Cancelling Partial Join for Multiple Instances | + | Join waits ForN(n), then its decision cancels the instance Step type | CancellingPartialJoinForMultipleInstancesFlow |
| WCP-36 | Dynamic Partial Join for Multiple Instances | +/- | RPC result next Steps add instances; the join re-waits ForOne and evaluates the completion condition; an Attribute disables new instances | DynamicPartialJoinForMultipleInstancesFlow |
| WCP-37 | Local Synchronizing Merge | + | Same as WCP-7: the diverging decision passes the branch count to the join Step | MultiChoiceSynchronizingMergeFlow |
| WCP-38 | General Synchronizing Merge | +/- | No in-Flow construct; wrap the region in a SubFlow, whose GracefulComplete fires once no Step in it can run | GeneralSynchronizingMergeFlow |
| WCP-39 | Critical Section | +/- | A single-message Channel used as a token; Attribute locks cover one Step method only, not a multi-Step section | CriticalSectionFlow |
| WCP-40 | Interleaved Routing | + | Execute Attribute lock shared by every task, then a Channel ForN join | InterleavedRoutingFlow |
| WCP-41 | Thread Merge | + | Channel ForN(k) join of k executions of one Step type | ThreadSplitMergeFlow |
| WCP-42 | Thread Split | + | GoToMany k executions of one Step type | ThreadSplitMergeFlow |
| WCP-43 | Explicit Termination | + | ForceComplete closes the Flow at once and cancels every other Step | ExplicitTerminationFlow |

Totals: 31 `+`, 12 `+/-`, 0 `-`.

## File layout

Flows are grouped by WCP category into ten source groups.

| Group | Flow types |
| --- | --- |
| basic control flow | SequenceFlow, ParallelSplitSynchronizationFlow, ExclusiveChoiceSimpleMergeFlow |
| multi-choice and merges | MultiChoiceSynchronizingMergeFlow, MultiMergeFlow, GeneralSynchronizingMergeFlow, ReviewRegionFlow |
| partial joins | StructuredPartialJoinFlow, BlockingPartialJoinFlow, CancellingPartialJoinFlow |
| and-join and threads | GeneralizedAndJoinFlow, ThreadSplitMergeFlow |
| multiple instances | MultipleInstancesWithoutSynchronizationFlow, DesignTimeMultipleInstancesFlow, RunTimeMultipleInstancesFlow, NoPriorKnowledgeMultipleInstancesFlow, StaticPartialJoinForMultipleInstancesFlow, CancellingPartialJoinForMultipleInstancesFlow, DynamicPartialJoinForMultipleInstancesFlow |
| state based | DeferredChoiceFlow, InterleavedParallelRoutingFlow, MilestoneFlow, CriticalSectionFlow, InterleavedRoutingFlow |
| cancellation | CancelTaskFlow, CancelCaseFlow, CancelRegionFlow, CancelMultipleInstanceTaskFlow, CompleteMultipleInstanceTaskFlow |
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
| Rust | `examples/rust/src/patterns/workflow_patterns/<group_snake>.rs`, plus `mod.rs` | `examples/rust/tests/workflow_patterns.rs` |

Group file names: `basic-control-flow`, `multi-choice-and-merges`,
`partial-joins`, `and-join-and-threads`, `multiple-instances`, `state-based`,
`cancellation`, `iteration`, `termination`, `triggers` (TypeScript), and the
same names with underscores for Python, Go, and Rust.

## Shared conventions

- Every Step type name below is unique across the whole catalog, so Go can keep
  every Step type in one package. Two Flows share Step types only where noted.
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

## Examples and required test assertions

### basic control flow

**SequenceFlow** (WCP-1). Input: Trail (start with an empty list).
- Steps: ReceiveOrder -> CheckInventory -> ShipOrder, each by GoTo. ShipOrder
  returns GracefulComplete(trail).
- Test: COMPLETED; exactly one completion, from ShipOrder, with output
  `["ReceiveOrder", "CheckInventory", "ShipOrder"]`.

**ParallelSplitSynchronizationFlow** (WCP-2, WCP-3). Input: none.
- Channels: fulfillmentBranchCompleted (string), paymentApproved (string).
- RPC approvePayment(): publishes `"approved"` to paymentApproved.
- Steps: SplitOrder (start) -> GoToMany(PackItems, ChargePayment,
  SynchronizeFulfillment). PackItems publishes `"PackItems"` to
  fulfillmentBranchCompleted, DeadEnd. ChargePayment waits
  paymentApproved.ForOne, publishes `"ChargePayment"`, DeadEnd.
  SynchronizeFulfillment waits fulfillmentBranchCompleted.ForN(2), returns
  GracefulComplete(sorted branch names).
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
- Channel: notificationSent (string).
- Steps: SelectNotificationChannels: selects the branch Steps whose flag is
  true; GoToMany(SynchronizingMerge(selected count), selected branches). If
  none is selected, GracefulComplete(empty list). SendEmail, SendSms, SendPush
  each publish their own Step type to notificationSent, DeadEnd.
  SynchronizingMerge(count) waits notificationSent.ForN(count), returns
  GracefulComplete(sorted names).
- Test: `{true, false, true}` -> COMPLETED; exactly one SynchronizingMerge
  completion with `["SendEmail", "SendPush"]`.

**MultiMergeFlow** (WCP-8). Input: none.
- Steps: StartReviews -> GoToMany(LegalReview, FinanceReview). Each GoTo
  RecordReview with its own Step type as input. RecordReview(reviewer string)
  returns GracefulComplete(reviewer).
- Test: COMPLETED; exactly two RecordReview completions, outputs
  `{"LegalReview", "FinanceReview"}`.

**GeneralSynchronizingMergeFlow** (WCP-38) with child **ReviewRegionFlow**.
- GeneralSynchronizingMergeFlow input: none. Steps: RunReviewRegion (start)
  waits SubFlow.run(ReviewRegionFlow, 0); Execute decodes every completion
  output of the SubFlow result as a string and GoTo PublishDecision(sorted
  outputs). PublishDecision returns GracefulComplete(its input).
- ReviewRegionFlow input: revision (int). Steps: DraftProposal(revision) ->
  GoToMany(ReviewProposal(revision), TranslateProposal(revision)).
  ReviewProposal: revision < 1 -> GoTo(DraftProposal, revision + 1) (the
  cycle), else GracefulComplete("ReviewProposal-" + revision).
  TranslateProposal returns GracefulComplete("TranslateProposal-" + revision).
- Test: COMPLETED; exactly one PublishDecision completion with
  `["ReviewProposal-1", "TranslateProposal-0", "TranslateProposal-1"]`.

### partial joins

**StructuredPartialJoinFlow** (WCP-9 with requiredQuotes = 1, WCP-30 with 2).
Input: requiredQuotes (int).
- Channels: supplierAReleased, supplierBReleased, supplierCReleased (string),
  quoteReceived (string).
- RPC releaseSupplierQuote(supplier string `"A"`, `"B"`, or `"C"`): publishes
  to that supplier's Channel. Reject any other value with an input error.
- Steps: RequestQuotes -> GoToMany(QuoteFromSupplierA, QuoteFromSupplierB,
  QuoteFromSupplierC, AcceptQuotes(requiredQuotes)). QuoteFromSupplierX waits
  supplierXReleased.ForOne, publishes `"X"` to quoteReceived, DeadEnd.
  AcceptQuotes(required) waits quoteReceived.ForN(required) ->
  GoToMany(PlaceOrder(accepted list), ResetQuoteJoin(3 - required)).
  PlaceOrder returns GracefulComplete(sorted accepted). ResetQuoteJoin(n) waits
  quoteReceived.ForN(n), returns GracefulComplete(sorted ignored).
- Test required = 1: release A; wait for PlaceOrder-1; release B; release C;
  COMPLETED; exactly one PlaceOrder completion `["A"]`; ResetQuoteJoin
  `["B", "C"]`.
- Test required = 2: release A; wait for QuoteFromSupplierA-1; release B; wait
  for PlaceOrder-1; release C; COMPLETED; one PlaceOrder `["A", "B"]`;
  ResetQuoteJoin `["C"]`.

**BlockingPartialJoinFlow** (WCP-28 with requiredSignals = 1, WCP-31 with 2).
Input: `{requiredSignals, round}` with requiredSignals 1 or 2 and round 1.
- Channels: branchASignal, branchBSignal, branchCSignal (string).
- RPC sendBranchSignal(branch `"A"`, `"B"`, or `"C"`): publishes the branch
  letter to that branch's Channel.
- Steps: BlockingJoin(`{requiredSignals, round}`), the start Step with
  round 1. requiredSignals = 1: WaitFor AnyOf(A.ForOne, B.ForOne, C.ForOne).
  requiredSignals = 2: AnyCombinationOf({A, B}, {A, C}, {B, C}) with condition
  IDs `ab-a`, `ab-b`, `ac-a`, `ac-c`, `bc-b`, `bc-c`. Execute: fired = sorted
  letters whose Channel results are non-empty; GoToMany(ProcessRound(`{round,
  branches: fired}`), ResetBlockingJoin(`{requiredSignals, round,
  pendingBranches: the other letters}`)). ProcessRound returns
  GracefulComplete(its input). ResetBlockingJoin waits AllOf(ForOne on every
  pending branch); round < 2 -> GoTo(BlockingJoin, round + 1), else
  GracefulComplete(round).
- Test required = 1: signal A; wait for ProcessRound-1; signal A again (held
  for round 2); signal B; signal C; wait for ProcessRound-2; signal B; signal
  C; COMPLETED; ProcessRound completions, in execution order:
  `{round: 1, branches: ["A"]}`, `{round: 2, branches: ["A"]}`.
- Test required = 2: signal A, B; wait for ProcessRound-1; signal A, B (held);
  signal C; wait for ProcessRound-2; signal C; COMPLETED; ProcessRound
  completions `{1, ["A", "B"]}`, `{2, ["A", "B"]}`.

**CancellingPartialJoinFlow** (WCP-29 with requiredBids = 1, WCP-32 with 2).
Input: requiredBids (int).
- Channels: carrierAReleased, carrierBReleased, carrierCReleased (string),
  bidReceived (string).
- RPC releaseCarrierBid(carrier `"A"`, `"B"`, or `"C"`).
- Steps: RequestBids -> GoToMany(BidFromCarrierA, BidFromCarrierB,
  BidFromCarrierC, AcceptBids(requiredBids)). BidFromCarrierX waits
  carrierXReleased.ForOne, publishes `"X"` to bidReceived, DeadEnd.
  AcceptBids(required) waits bidReceived.ForN(required), returns
  GracefulComplete(sorted accepted) with canceling Steps BidFromCarrierA,
  BidFromCarrierB, BidFromCarrierC.
- Test required = 1: release B; COMPLETED; the only completion is AcceptBids
  `["B"]`.
- Test required = 2: release A; wait for BidFromCarrierA-1; release C;
  COMPLETED; the only completion is AcceptBids `["A", "C"]`.

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
- Channel: parcelPacked (int).
- Steps: SplitShipment -> GoToMany(parcelCount x PackParcel(index),
  MergeParcels(parcelCount)). PackParcel publishes its index, DeadEnd.
  MergeParcels(count) waits ForN(count), returns GracefulComplete(sorted
  indexes).
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
- Channel: reviewCompleted (string). Shares Step types ReviewDocument and
  CollectReviews with RunTimeMultipleInstancesFlow.
- Steps: StartDesignTimeReviews -> GoToMany(ReviewDocument("legal"),
  ReviewDocument("finance"), ReviewDocument("security"), CollectReviews(3)).
  ReviewDocument publishes its reviewer, DeadEnd. CollectReviews(count) waits
  ForN(count), returns GracefulComplete(sorted reviewers).
- Test: COMPLETED; one CollectReviews completion
  `["finance", "legal", "security"]`.

**RunTimeMultipleInstancesFlow** (WCP-14). Input: reviewers (list of
strings).
- Steps: StartRunTimeReviews -> GoToMany(ReviewDocument per reviewer,
  CollectReviews(reviewer count)).
- Test `["ana", "ben", "cy", "dee"]`: one CollectReviews completion with all
  four, sorted.

**NoPriorKnowledgeMultipleInstancesFlow** (WCP-15). Input: analyzed sample IDs
(list of strings), started empty.
- Attributes: startedSampleCount (int), isSampleIntakeClosed (bool).
- Channels: sampleAnalyzed (string), sampleIntakeClosed (string).
- RPC addSample(sampleId string) -> bool, locks startedSampleCount: closed ->
  false; else increment startedSampleCount and return true with next Step
  AnalyzeSample(sampleId).
- RPC closeSampleIntake(), locks startedSampleCount: sets
  isSampleIntakeClosed and publishes `"closed"` to sampleIntakeClosed.
- Steps: JoinSamples(analyzed list), the start Step with an empty list. WaitFor:
  closed and analyzed count == started -> SkipImmediately; closed ->
  sampleAnalyzed.ForOne; open -> AnyOf(sampleAnalyzed.ForOne,
  sampleIntakeClosed.ForOne). Execute (no lock: the count cannot change
  once intake is closed): append consumed samples; closed and count == started ->
  GoTo(ReportSamples, analyzed); else GoTo(JoinSamples, analyzed).
  AnalyzeSample publishes its sampleId, DeadEnd. ReportSamples returns
  GracefulComplete(sorted).
- Test: addSample s1, s2, s3 each return true; closeSampleIntake; COMPLETED;
  ReportSamples `["s1", "s2", "s3"]`.

**StaticPartialJoinForMultipleInstancesFlow** (WCP-34). Input:
`{inspectionCount, requiredInspections}`.
- Channels: inspectionReleased (string), unitInspected (int).
- RPC releaseInspections(count int): publishes count messages.
- Steps: StartInspections -> GoToMany(inspectionCount x InspectUnit(index),
  AcceptInspections(`{requiredInspections, remainingInspections}`)).
  InspectUnit waits inspectionReleased.ForOne, publishes its index, DeadEnd.
  AcceptInspections waits ForN(required) -> GoToMany(ApproveBatch(indexes),
  ResetInspectionJoin(remaining)). ApproveBatch returns GracefulComplete(sorted
  indexes). ResetInspectionJoin(n) waits ForN(n), returns
  GracefulComplete(sorted indexes).
- Test `{3, 2}`: releaseInspections(2); wait for ApproveBatch-1; DescribeFlow
  is RUNNING; releaseInspections(1); COMPLETED; ApproveBatch has 2 indexes;
  ResetInspectionJoin has 1; together they are `{0, 1, 2}`.

**CancellingPartialJoinForMultipleInstancesFlow** (WCP-35). Input:
`{inspectionCount, requiredInspections}`.
- Channels: quorumInspectionReleased (string), quorumUnitInspected (int).
- RPC releaseQuorumInspections(count int).
- Steps: StartQuorumInspections -> GoToMany(inspectionCount x
  InspectQuorumUnit(index), AcceptQuorumInspections(required)).
  InspectQuorumUnit waits quorumInspectionReleased.ForOne, publishes its index,
  DeadEnd. AcceptQuorumInspections waits ForN(required), returns
  GracefulComplete(sorted indexes) with canceling Step InspectQuorumUnit.
- Test `{3, 2}`: releaseQuorumInspections(2); COMPLETED; the only completion is
  AcceptQuorumInspections with 2 distinct indexes.

**DynamicPartialJoinForMultipleInstancesFlow** (WCP-36). Input:
`{requiredAcceptedBids, acceptedBids}`, started with an empty acceptedBids list.
- Attribute: isBidAdmissionClosed (bool).
- Channel: bidEvaluated (`{amount: int, isAccepted: bool}`).
- RPC addBid(amount int) -> bool, locks isBidAdmissionClosed: closed -> false;
  else true with next Step EvaluateBid(amount).
- Steps: JoinBids(`{requiredAcceptedBids, acceptedBids: int list}`), the start
  Step with an empty list, waits bidEvaluated.ForOne. Execute, with an Execute
  lock on isBidAdmissionClosed: append accepted amounts; count >= required ->
  set isBidAdmissionClosed, GoTo(AwardContract, accepted); else GoTo(JoinBids,
  updated). EvaluateBid publishes `{amount, isAccepted: amount <= 1000}`,
  DeadEnd. AwardContract returns GracefulComplete(sorted amounts).
- Test required = 2: addBid 500, 1500, 700 each return true (retry an RPC
  that fails with the RPC lock conflict error while JoinBids holds the lock); COMPLETED;
  AwardContract `[500, 700]`.

### state based

**DeferredChoiceFlow** (WCP-16). Input: none.
- Channels: offerAccepted, offerDeclined (string).
- RPCs acceptOffer(), declineOffer().
- Steps: AwaitCustomerResponse (start) waits AnyOf(offerAccepted.ForOne,
  offerDeclined.ForOne); accepted results non-empty -> GoTo(FulfillOffer), else
  GoTo(ArchiveOffer). Each returns GracefulComplete(its own Step type).
- Test decline: declineOffer; COMPLETED; the only completion is ArchiveOffer.
- Test accept: acceptOffer; COMPLETED; the only completion is FulfillOffer.

**InterleavedParallelRoutingFlow** (WCP-17). Input: none.
- Attribute: onboardingTaskLock (bool), used only as a lock.
- Channel: onboardingTaskFinished (TaskInterval).
- Steps: StartOnboardingTasks -> GoToMany(PrepareContract, RunCreditCheck,
  CompleteOnboardingTasks(3)). PrepareContract, SignContract, and
  RunCreditCheck each hold an Execute lock on onboardingTaskLock, sleep 200 ms,
  and publish their TaskInterval. PrepareContract then GoTo(SignContract); the
  others DeadEnd. CompleteOnboardingTasks(count) waits ForN(count), returns
  GracefulComplete(intervals).
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
- Attribute: auditLock (bool), used only as a lock.
- Channel: auditFinished (TaskInterval).
- Steps: StartAudits -> GoToMany(AuditPayroll, AuditExpenses, AuditTaxes,
  CompleteAudits(3)). Each audit holds an Execute lock on auditLock, sleeps
  200 ms, publishes its TaskInterval, DeadEnd. CompleteAudits(count) waits
  ForN(count), returns GracefulComplete(intervals).
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

**CancelCaseFlow** (WCP-20). Input: none.
- Channel: applicationReviewed (string).
- Steps: ReviewApplication (start) waits applicationReviewed.ForOne, returns
  GracefulComplete("ReviewApplication").
- Test: StopFlow with the cancel stop type and a reason; the Flow reaches
  CANCELED; no completions.

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

**ArbitraryCyclesFlow** (WCP-10). Input: `{hasDraft: bool}`.
- Steps: RouteSubmission: hasDraft -> GoTo(EditArticle, `{revision: 0, trail:
  ["RouteSubmission"]}`), else GoTo(DraftArticle, same). DraftArticle ->
  GoTo(EditArticle, trail + self). EditArticle: revision >= 1 ->
  GoTo(PublishArticle, trail + self), else GoTo(DraftArticle, `{revision + 1,
  trail + self}`). PublishArticle returns GracefulComplete(trail + self).
- Test hasDraft = false: PublishArticle `["RouteSubmission", "DraftArticle",
  "EditArticle", "DraftArticle", "EditArticle", "PublishArticle"]`.
- Test hasDraft = true: PublishArticle `["RouteSubmission", "EditArticle",
  "DraftArticle", "EditArticle", "PublishArticle"]`.

**StructuredLoopFlow** (WCP-21). Input: `{remainingBatches, processedBatches}`.
- Steps: ProcessBatches (start), pre-test: remainingBatches == 0 ->
  GoTo(FinishBatches, processedBatches), else GoTo(ProcessBatches, `{remaining -
  1, processed + 1}`). FinishBatches returns GracefulComplete(processed).
- Test `{3, 0}` -> FinishBatches 3. Test `{0, 0}` -> FinishBatches 0.

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
- Channels: motionSensorArmed (string), motionEvent (int).
- RPC armMotionSensor(). RPC sendMotionEvent(value int) -> bool: when
  isAwaitingMotionEvent is true, publish and return true; else return false and
  drop the trigger.
- Steps: AwaitMotionSensorArmed (start) waits motionSensorArmed.ForOne ->
  GoTo(AwaitMotionEvent). AwaitMotionEvent WaitFor sets isAwaitingMotionEvent
  true and waits motionEvent.ForOne; Execute sets it false and returns
  GracefulComplete(value).
- Test: sendMotionEvent(1) returns false; armMotionSensor; poll the Client
  Attribute read until isAwaitingMotionEvent is true; sendMotionEvent(2)
  returns true; COMPLETED; AwaitMotionEvent output 2.

**PersistentTriggerFlow** (WCP-24). Input: none.
- Channels: carrierReady (string), shipmentLabel (string).
- RPCs markCarrierReady(), sendShipmentLabel(labelId string), which always
  publishes.
- Steps: AwaitCarrierReady (start) waits carrierReady.ForOne ->
  GoTo(AwaitShipmentLabel). AwaitShipmentLabel waits shipmentLabel.ForOne,
  returns GracefulComplete(labelId).
- Test: sendShipmentLabel("label-1") before markCarrierReady; then
  markCarrierReady; COMPLETED; AwaitShipmentLabel output `"label-1"`.
