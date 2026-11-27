# Control structure patterns

Part of the [design patterns](../patterns.md), which hold the pseudocode conventions and the runnable examples.

## Control structures

Map each control structure of a process to its Dex recipe. Each row states the rule that decides correctness; the sections below add skeletons for the last three recipes.

| Control structure | Dex recipe | Rules |
| --- | --- | --- |
| Sequence | Execute returns GoTo to the next Step | Each Step is one commit. Split Steps by recovery boundary, not by line of code. |
| Exclusive choice | Execute returns one GoTo, chosen from its input and Attributes | Execute picks exactly one branch. |
| Parallel split | GoToMany | Each branch is its own Step execution and runs concurrently. A branch ends with DeadEnd or a closing decision. |
| Fork and join | A Join's fork starts the branches and the join Step; each branch ends with endBranch; the join waits forAllBranches, forFirstBranch, or forBranches(n) | Every branch reports exactly one result on every exit path: success, early exit, and the failure recovery route. A canceled Step reports nothing and its failure route does not run, so a join that waits for every branch never fires. Cancel the join together with the branches, wait for the join and a Timer with AnyOf, or do not cancel branches the join counts. See [Await parallel Steps](parallel.md#await-parallel-steps). |
| Several waiters for one event | One message per waiter | One message satisfies one wait. It is never copied to other waiting Steps. Use a ChannelMap with one key per waiter and publish to every key, or publish one message per waiter. |
| First result of internal work | GoToMany of one Step type; the first to finish returns its result with a decision that cancels its sibling Steps | The sibling selector cancels executions of the same Step type started by the same parent. Losers must be safe to cancel or harmless to finish; cancellation does not undo an external effect. |
| First external event wins | One Step waits AnyOf over one Channel per event and a Timer for the deadline | Only the winning Condition consumes. A queued message wins at once; otherwise the first event to commit wins. Execute checks which Condition fired and moves to one branch. When a caller must learn whether its reply was accepted before the deadline, close the reply window with an Attribute lock. |
| Mutual exclusion inside one Execute | An Execute lock on a shared Attribute | A lock covers one Step method. It never covers a WaitFor or several Steps. |
| Critical section across Steps | A Channel that holds one token message | A handoff Step takes the token with ForOne and does no protected work. The last Step of the section publishes the token back, and so does every other exit: the ExecuteFailure route, an early exit, and a WaitForFailure policy. Never cancel a Step that does protected work. |
| Cancel one task | An RPC result or a decision cancels Steps by Step type and starts the replacement Step | A canceled Step is not retried, its failure route does not run, and Dex discards its later writes. Dex picks the Steps to cancel at commit time. |
| Cancel the case and its child Flows | The Step that starts the SubFlow waits AnyOf(cancel Channel, SubFlow) and, on cancel, stops the child with Client StopFlow before ForceFail | List the cancel condition first in every wait. Every later waiting Step also waits for the cancel Channel. A closing parent never stops its children, so give every child its own Flow timeout. |
| Loop | Execute returns GoTo to the same or an earlier Step with the next input | Each iteration is a new Step execution and a new commit. Bound the loop. Keep accumulated results in Attributes, not in a growing Step input. Wait between iterations with a Timer in WaitFor. To poll an external system, follow [Polling](polling-and-timers.md#polling). |
| Repeat on a schedule | A scheduler Step waits for a Timer to the next occurrence, then GoToMany starts the run Step and the next scheduler wait together | Starting the next wait with the run keeps a slow run from drifting the schedule. Make each run's external effect idempotent per occurrence. Stop the schedule with a Channel message or a closing decision. |
| Multiple instances | GoToMany of one Step type, one execution per item | Bound the fan-out and batch large inputs. When the Flow needs the results, fork a Join with one branch per item. A Join needs the instance count when it forks; instances added while others run need an explicit count and a loop. Use child Flows only for instances with their own lifecycle. |

### Decision with Deadline

Wait for several events and route on whichever happens first, such as accept, reject, or expiry of an offer. Go folder: `deferred-choice`.

```text
rpc acceptCustomerOffer(note), locks OfferResponseWindowClosed
    -> if OfferResponseWindowClosed: return windowClosed
       OfferAccepted.publish(note); OfferResponseWindowClosed.set(true); return recorded
rpc rejectCustomerOffer(note): the same, publishing to OfferRejected

AwaitOfferResponse.waitFor -> AnyOf(OfferAccepted.ForOne(), OfferRejected.ForOne(), Timer(3 days))
AwaitOfferResponse.execute, locks OfferResponseWindowClosed, short retry budget
    OfferResponseWindowClosed.set(true)
    winner = the Condition that fired
    if winner is OfferAccepted: goTo(FulfillOffer)
    if winner is OfferRejected: goTo(CloseRejectedOffer)
    if winner is the Timer:
        if OfferAccepted.size() + OfferRejected.size() > 0: goTo(RouteResponseBeforeExpiry)
        goTo(ExpireOffer)
RouteResponseBeforeExpiry.waitFor -> AnyOf(OfferAccepted.ForOne(), OfferRejected.ForOne())  // wins at once
```

- Only the winning Condition consumes. A losing response stays pending in its Channel.
- In Execute, check which Condition fired. Never treat "no Channel result" as "the deadline passed".
- When the wait is evaluated, Dex selects one ready Condition: a ready Timer first, otherwise the first ready Channel in declaration order. When Conditions become ready at different times, the first event to satisfy the wait wins. Declare the decision Channels in business priority order.
- Declaration order ranks only Channels that are ready when the wait is evaluated. When the business ranks responses, keep that rule and state which definition of "the same moment" the design uses; see [Arbitrate by business priority](#arbitrate-by-business-priority).
- A response can commit after the Timer won and before Execute runs. Without the window lock its RPC succeeds and the Flow still expires the offer. Two responses can also both be accepted before the wait takes one. With the window, the first response closes it: it is recorded and decides, and every later caller gets "window closed". See [Close a reply window with an Attribute lock](#close-a-reply-window-with-an-attribute-lock).
- The Timer branch's size check takes a response that committed before the locked Execute closed the window, even one that committed after the Timer won.
- The runnable deferred-choice examples show only this first-committed-response window. They have no ranked rounds, recovery route, or reply Step.
- Test the deadline branch by skipping the Timer with the Client, not by shortening the business deadline.
- Anti-patterns: one parallel Step per event that cancels the others; a Timer loop that checks an Attribute; a separate Flow or job that enforces the deadline.

### Close a reply window with an Attribute lock

Use this when a caller must learn whether its reply was accepted before the deadline: it must never be told a reply was accepted when the Flow ignored it. When the business accepts that a reply arriving just after the deadline is ignored, skip the window; the reply RPC only publishes and takes no lock. Two races need the window:

- The Timer wins, Execute is about to run, and a reply RPC publishes and returns success. Execute then escalates as if no reply came, and the reply is silently ignored.
- Two reply RPCs both publish and return success before the wait takes one. The wait consumes only one, so the other caller was told a reply was accepted that never decides.

A Channel size read in Execute is a snapshot, and a closing check such as ForceCompleteIfChannelsEmpty guards only the Flow close, so neither closes these gaps. Runnable examples: [Decision with Deadline](#decision-with-deadline).

```text
Attribute replyWindow: open | closed            // unset reads as open

rpc sendReply(reply), transactional, locks replyWindow
    if replyWindow is closed: return windowClosed   // a business result, not an error
    Reply.publish(reply)                            // commits with the handler
    replyWindow.set(closed)                         // the first reply closes the window
    return recorded

AwaitReply.waitFor -> AnyOf(Reply.ForOne(), Timer(deadline))     // no lock on WaitFor
AwaitReply.execute, locks replyWindow, short retry budget
    replyWindow.set(closed)
    if Reply won: goTo(HandleReply, the consumed reply)
    // the Timer won
    if Reply.size() > 0: goTo(TakeLateReply)      // committed before this Execute took the lock
    goTo(Escalate)
TakeLateReply.waitFor -> Until(Reply.ForOne())    // wins at once
TakeLateReply.execute -> goTo(HandleReply, the consumed reply)
```

Why it holds:

- Execute takes the window lock, closes the window, and checks the reply Channel size. A reply that committed before that is taken through `TakeLateReply`, and every later reply gets a lock conflict while Execute runs and "window closed" after it commits.
- A transactional RPC commits its handler, its publish, and its window write together. The first reply closes the window, so at most one reply is ever queued, and the wait or the size check takes exactly that one.
- The business deadline is the moment the locked Execute closes the window, not the instant the Timer fires. This is deliberate: every caller gets a truthful answer.

Rules:

- Every RPC that writes the reply Channel declares the same lock. Locks are cooperative; an RPC without it bypasses the window. Prefer one entry RPC per reply Channel.
- Lock only Execute. WaitFor and Execute have separate lock sets, and the wait itself holds no lock.
- The reply RPCs take this lock, so the locked Execute makes no external call: set the window, check the winner and the size, and route. Put side effects, such as escalation notices or provider calls, in the next Step. A locked Execute holds the lock through its retry backoff, and every reply meanwhile gets a lock conflict.
- Give the locked Execute an explicit short retry budget: a total retry duration, a maximum attempt count, and a per-attempt timeout. The default total budget is long, and waiting for the lock before the first attempt does not consume it. The defaults are in each SDK's [StepOptions reference](https://docs.superdurable.io/reference/go/step-options#defaults).
- Callers retry a lock conflict with bounded backoff. Keep the reply idempotent, for example with a business reply ID.
- After the Flow closes, a reply RPC fails before its handler runs. A caller that must answer "window closed" then reads the retained Flow to tell a closed Flow from one that never existed.
- The reply RPC may also record the admitted reply as the outcome in the same locked commit. Execute still checks which Condition fired; it never infers the Timer from an open window or an unset outcome.
- Close the window in the reply RPC and in the Execute on every branch, so a reply after the first reply or after the decision gets "window closed".
- To wait again, such as a new reply round, reopen the window under the same lock, in the Execute that starts the next wait.
- Test it: a reply before the deadline decides; two replies sent back to back give one "recorded" and one "window closed"; a reply after the window closed gets "window closed"; a reply that commits after the Timer won but before the locked Execute runs is taken through the size check. In that last test, confirm the Execute is waiting for its locks before the reply RPC releases the lock.

### Arbitrate by business priority

Use this when the business ranks events against each other, such as a withdrawal over a decision, or "a confirmation wins over a decline that arrives at the same moment". Keep the priority rule; never drop it as unenforceable. State in the design which definition of "the same moment" the code uses. Both are valid:

- **(a) Server commit order.** A reply window or lock admits replies one commit at a time. The first committed reply wins, so ties never occur.
- **(b) Competing Channels ready when the wait is evaluated.** Among the Channel Conditions of one AnyOf that are ready together, the first declared wins, and Execute uses the messages it consumed. This ranks Channels strictly at that moment. It does not rank a Channel against a Timer: a ready Timer is selected first.

When a Timer wins, Execute may still take a reply that committed before it closed the window; see [Close a reply window with an Attribute lock](#close-a-reply-window-with-an-attribute-lock).

To rank every event admitted before the decision, use a locked round instead: its closed window defines which events are eligible, and the arbitration Step ranks them. A reply window can be combined with ranked replies this way.

| Business rule | Recipe |
| --- | --- |
| Rank competing Channels ready when the wait is evaluated, definition (b) | Declare the Channels in rank order and use the messages the winning Condition consumed. A Timer in the same wait that is also ready is selected first, and the messages stay queued. |
| Rank every event admitted before the decision closes, a locked round | Keep each round's window and candidates in AttributeMaps keyed by a round ID that is never reused, not in Channels. Each RPC names its round and appends its candidate under that round's instance locks; the arbitration Step closes that round's window under the same locks and picks the winner from its candidates. See the skeleton below. |
| The first committed response wins and later callers get "window closed", definition (a) | [Close a reply window with an Attribute lock](#close-a-reply-window-with-an-attribute-lock). |

```text
// RoundWindow and RoundCandidates are AttributeMaps keyed by round ID; a round ID is never reused.
// Every writer of a round locks and loads both instances of that round: the round's locks.
OpenRound.execute(roundId), the round's locks
    RoundWindow[roundId] = open; goTo(Arbitrate, roundId)
rpc submitWithdrawal(roundId, w), the round's locks
    if RoundWindow[roundId] is not open: return closed               // an old or unknown round
    key = (roundId, withdrawal, w.eventId)
    if key is already a candidate: return received                   // idempotent retry
    RoundCandidates[roundId].append({key, ref: w.eventId, rank: 1, order: next})
    if the set was empty: RoundWakeups[roundId].publish()            // one data-free wake-up per round
    return received
rpc submitDecision(roundId, d): the same, with rank 2

Arbitrate.waitFor(roundId) -> AnyOf(Timer(deadline), RoundWakeups[roundId].ForOne())
Arbitrate.execute(roundId), the round's locks, short retry budget, failure route ArbitrationFailed
    if RoundWindow[roundId] is not open: return deadEnd()            // closed or missing: end the branch
    RoundWindow[roundId] = closed
    winner = the highest-ranked candidate (rank 1 first); admission order breaks a tie
    record the decision; mark the other candidates as not chosen
    answers = one immutable answer per candidate, keyed by the candidate key
    goTo(AnswerSenders, {decision, answers})   // with no candidate, the decision is the deadline branch
ArbitrationFailed.execute(roundId), the round's locks
    if RoundWindow[roundId] is not open: return deadEnd()            // closed or missing: end the branch
    in one commit: arbitrate again from RoundCandidates[roundId], or close the round
    and record the failure; build the answers the same way
    goTo(AnswerSenders, {decision or failure, answers})
AnswerSenders.execute({outcome, answers})      // no lock and no round read: the answers are its input
    send each answer through an idempotent path keyed by its candidate key
    route on the outcome
```

- The RPCs answer "received". The receipt confirms admission only; the arbitration Step decides.
- Every RPC and Step that reads or changes a round's window or candidates holds that round's locks. Both the arbitration Step and the recovery Step commit the outcome and one immutable answer per candidate under those locks, then pass them to an unlocked Step that sends the answers, because the RPCs take the same locks. Keep the Execute of the arbitration Step and the recovery Step SYNC, even when the Flow defaults to ASYNC, so the decision and the answers are stored before the answering Step runs; see [Durability](https://docs.superdurable.io/reference/go/step-options#durability). An RPC names its target round, so a late RPC for a closed round gets "closed" and never reaches a newer round.
- Each round's candidates hold everything that decides the outcome: a key of round ID, event type, and event ID, a payload reference, rank, and admission order. The key also deduplicates retries and keys the answers.
- Publish one wake-up, in the same commit, when a round's candidate set goes from empty to non-empty; later admissions only append. The wake-up carries no data and its ChannelMap key is the round ID. Never read a wake-up to decide candidates or the outcome. When the Timer wins, one wake-up may stay pending; delete it with the round's other state when a long-running Flow must reclaim state.
- The arbitration Step and the recovery Step both recheck the round's window under its locks. When the round is closed or missing, they end the branch with a dead end: no decision and no business route. Never return an empty decision for this case.
- Start only one arbitration wait per round. The decision derives only from the round's Attribute state, so a retried Execute reaches the same decision. Execute holds the locks through its retries and backoff, so no candidate joins meanwhile.
- The Timer is not a hard cutoff; the successful locked close is. A candidate admitted before it is eligible, even after the Timer fired.
- When Execute exhausts its retries, neither the close nor the decision commits, and the round stays open. Give Execute a failure route whose Step, under the same locks, arbitrates again from the stored candidates or closes the round and records the failure, with the answers, and moves to the same answering Step. It never relies on the wake-up. Without a failure route the Flow fails, and senders learn the outcome from its terminal state.
- Losing candidates stay in the record, and the answering Step answers their senders too.
- The runnable deferred-choice examples show only the first-committed-response window. This round skeleton has no runnable example.
- Never present an RPC that decides by lock order as business priority: lock order is commit order, definition (a).

### Critical section

Let parallel branches of one Flow take turns on a sequence of Steps, such as several reviewers editing one contract. Go folder: `critical-section`.

```text
OpenContractEdits.execute(edits)
    Token.publish("contract")                      // exactly one token, before any branch waits on it
    goToMany(for each edit: Enter(edit), Join(count))

Enter.waitFor  -> Until(Token.ForOne())             // options: ExecuteFailure -> Release
Enter.execute  -> goTo(Edit, edit)                  // handoff only, no protected work
Edit.execute   -> first protected work; goTo(Save, edit)   // options: ExecuteFailure -> Release
                  early exit: Done.publish(record); Token.publish("contract"); deadEnd()
Save.execute   -> last protected work               // options: ExecuteFailure -> Release
                  Done.publish(record); Token.publish("contract"); deadEnd()
Release.execute -> Done.publish(failed record); Token.publish("contract"); deadEnd()
Join.waitFor(count) -> Until(Done.ForN(count))
```

- Use an Attribute lock when the protected work fits in one Execute. Use a token when it spans Steps or a WaitFor.
- The Step that takes the token is a pure handoff. Its Execute does no protected work and only moves to the first working Step.
- Every exit of the section publishes the token back:
  - the last Step of the section, on success;
  - an ExecuteFailure route on every Step of the section, including the handoff, to a Step that publishes the token;
  - a WaitForFailure policy that proceeds to Execute on any later Step of the section that has its own WaitFor; that Execute publishes the token;
  - any Execute that leaves the section early with a business decision.
- Canceling the handoff Step is safe because it does no protected work. When Dex cancels it after its wait consumed the token but before its Execute committed, Dex returns the token to the front of the Channel, and another branch enters.
- Never cancel a Step that does protected work. A canceled Step publishes nothing and its failure route does not run. The canceller cannot publish the token for it: Dex picks the cancel set at commit time, the Step may already have published it, and a second token breaks mutual exclusion. To stop a branch early, set a stop request, such as an Attribute from an RPC, that the section's Steps check; the Step that sees it leaves through an early exit that publishes the token.
- Dex cannot fence the external writes of a canceled method. Cancellation is cooperative, so a canceled Execute can keep writing after Dex returns its token. The section stays exclusive only if no Step that does protected work is canceled.
- Closing the Flow (ForceComplete, ForceFail, a Flow timeout, or Client StopFlow) ends every branch. No token is needed then.
- Publications commit with the method's result, or not at all. A failed attempt never publishes, so a retry cannot create a second token.
- Dex does not promise which waiting branch gets the token next.
- Use the token Channel for nothing else. Keep the token value meaningless and the protected state in Attributes. Keep the section short.
- Anti-patterns: protected work in the Step that waits for the token; a boolean Attribute that Steps check and set; returning the token only on success; canceling a Step that does protected work; publishing a replacement token while the old one may still exist.

### Cancel with child Flows

Stop the child Flows a Flow started when the Flow is canceled, such as a credit check started by a loan application. Go folder: `cancel-case`.

```text
rpc cancelApplication(reason) -> ApplicationCanceled.publish(reason)

RunCreditCheck.waitFor -> AnyOf(ApplicationCanceled.ForOne(),     // cancel first
                                SubFlow(CreditCheckFlow, request, own Flow timeout))
RunCreditCheck.execute
    if ApplicationCanceled has a result:
        client.stopFlow(subFlowID())   // not found, not active, or closing counts as stopped
        forceFail("application canceled: " + reason)
    goTo(AwaitUnderwriterDecision, subFlowResult().output)

AwaitUnderwriterDecision.waitFor -> AnyOf(ApplicationCanceled.ForOne(), UnderwriterDecided.ForOne())
AwaitUnderwriterDecision.execute -> if canceled: forceFail(...); else gracefulComplete(decision)
```

- A closing parent never stops its children. A child keeps running when its parent completes, fails, times out, or is stopped.
- The recipe works only on the cancel-Channel path. A Client StopFlow on the parent, the parent's Flow timeout, or a ForceFail or ForceComplete from another Step closes the parent and leaves the children running.
- Set a Flow timeout on every child, so a child that is never stopped still ends. Clients cancel a case through the cancel RPC, not StopFlow.
- Every later Step that waits also waits on the cancel Channel, so a cancel after the child finished still closes the case.
- List the cancel condition first in every wait. When the cancel message and another condition are ready at once, the first declared condition wins, so a pending cancel is never swallowed by a normal completion.
- Declaration order ranks Channels that are both queued. A fallback or deadline Timer can win, and a cancel or withdrawal can still commit after it before the Timer branch runs. Make the Timer branch handle a cancel that may still be queued, for example by waiting on the cancel Channel in its next Step, or close the window as in [Close a reply window with an Attribute lock](#close-a-reply-window-with-an-attribute-lock).
- A cancel that arrives after the last decision was consumed, or after the Flow closed, cannot change the outcome.
- Once the decision is final, close without consuming the cancel Channel. Do not close with ForceCompleteIfChannelsEmpty on it and fall back to a cancel path: a late cancel would then override the decision. A cancel accepted after the decision stays pending until the Flow closes.
- Treat not found, not active, and closing from StopFlow as already stopped. Execute may retry, and the child may finish at the same moment.
- Inject a Client provider through the Flow constructor. Record the stopped child Flow ID in an Attribute when operators or tests need it.
- One cancel message satisfies one wait. When several Steps each hold a child, each needs its own message: a ChannelMap key per Step, or one publish per Step.
- With several children, do not ForceFail from the first Step that sees a cancel message; that cancels the other Steps before they stop their children. Every Step that holds a child stops its own child before anything closes the Flow.
