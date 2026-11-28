# Client

The Client is the application's boundary to Dex. It starts, stops, inspects, and searches Flows, reads committed Attributes, invokes typed RPCs, waits for state, and reads or writes Streams. It needs only the Dex Server address and the same registry and payload configuration as the Workers. See [application.md](application.md) for wiring.

Inside a Step or RPC handler, the Context reads the method's snapshot, and its writes commit with the method ([Commits](../../SKILL.md#commits-and-isolation)). A Client call from a handler is outside that commit: it reads newer committed state, an RPC it invokes fails at once with a lock conflict when the method holds a lock that RPC needs ([Locks](../../SKILL.md#repeated-execution-snapshots-and-locks)), and the call repeats when the method retries. So usually read and write the handler's own Flow through the Context. A Step uses an injected Client to act on another Flow: start a top-level Flow, call its RPCs, stop it, or read its state and result, or stop a SubFlow by the ID its Context supplies.

## The Flow owns decisions and status

The only hard boundary is that a Client never writes Flow state directly; it has no such API. Within that boundary, prefer that the Flow owns the decisions and status of its process. Pass business facts as start input or initial Attributes and let Steps and RPC handlers apply the business rules and write the resulting state. When an initial status depends on those rules, compute it in the first Step. Dex Web and later reads use the committed state.

- The caller, the application code that uses the Client, chooses the Flow ID and Request ID, starts Flows, invokes RPCs, waits, and reads.
- Then the rules live in one place, and status changes commit together with the Flow's progress.
- A caller that precomputes a rule-dependent decision, such as "needs a second approval", and passes it as start input spreads the rule across two places. The two copies drift apart.

## Operations

| Operation | Purpose | Notes |
| --- | --- | --- |
| StartFlow | Start a run with typed input, options, and initial Attributes | Returns once Dex accepts the start. See [error-handling.md](error-handling.md#start-identity-and-duplicate-starts). |
| InvokeRPC | Call a typed Flow RPC | The only application write path for Flow state. See [rpc.md](rpc.md). |
| Client Attribute read | Read committed Attributes and exact map instances | No Worker. See below. |
| WaitForAttributeMatch, WaitForAttributeMapInstanceMatch | Wait until a stored value matches | Needs an active Flow. |
| WaitForStepCompletion | Wait until a named Step execution completes | Returns no Step output. |
| WaitForFlow | Wait for terminal status and output | Can return a long-poll timeout while still open; call again. |
| DescribeFlow | Current or latest run status, type, start time | Does not wait. With a run ID, describes that run. |
| SearchFlows | Find Flows by query over indexed fields | Eventually consistent. |
| StopFlow | Close a run: cancel (default), fail with a reason, terminate, force-complete, or request a graceful completion; the complete types take an output | Cancel, fail, and terminate close as soon as the stop commits; force-complete begins closing and cancels active Steps; a graceful completion waits for active Steps. See [Flow close](../../SKILL.md#flow-timeout-completion-and-cancellation). |
| SkipTimer | Make a pending Timer fire | See [timer.md](timer.md#skip-a-timer). |
| TimeTravel | Reset a run to a recorded point as a new run attempt | Operator recovery. See [error-handling.md](error-handling.md#operator-recovery). |
| UpdateFlowConfig | Merge supplied Flow configuration fields | Accepted asynchronously. |
| ReadStream, ListStreamMessages, WriteStream | Stream access | Best effort. See [stream.md](stream.md). |

The Client has no direct Attribute write, Channel publish, or pending-message method. Write through typed RPCs. Handle the typed errors listed in [error-handling.md](error-handling.md#error-families-an-application-handles).

## Read Attributes from outside a Flow

The Client Attribute read returns committed values of known Attributes and exact AttributeMap instances.

- **One server read.** All requested keys go in one request and come from the same commit.
- **No Worker.** Dex serves it directly. It works while no Worker runs and costs less latency than an RPC.
- **Closed runs.** It reads the current run, or the latest run when none is open. A closed run stays readable until purged. It takes no run ID.
- **Committed state.** It never sees writes that a running Step or RPC has staged, hard-delete values included.
- **Batch only.** There is no single-Attribute method. Separate calls can read different commits, so usually pass every Attribute and map instance the response needs in one call. Some SDKs cap the number of reads per call; separate calls can read different commits. When one response needs more values from one commit, use a read-only RPC.
- **Exact keys.** Each read is a singleton Attribute or one exact map instance. It cannot enumerate a map and does not read Channels.
- **Registered definitions.** Each definition must be registered by some Flow in the Client's registry. Dex reads by name and does not check that the target Flow declares it, so a same-named Attribute of another type fails to decode.
- **Large values.** The Client hydrates blob-backed values through its BlobCache before decoding.
- **Not a wait, not a status.** Use Attribute match to wait, and DescribeFlow for status.
- **Unset values.** Each SDK has its own representation for an unset Attribute or instance. See the language file.

Errors:

- A missing Flow returns Flow not found, never the combined missing-or-inactive error. At a business Get, return not found directly.
- An unregistered or mismatched definition returns a Flow definition error before any request.
- A value that cannot be decoded returns a value mapping error.
- An empty Flow ID, no reads, or a duplicate read fails before any request.
- On any error the call returns no values.

| Choose | When |
| --- | --- |
| Client Attribute read | stored values of known Attributes or exact instances, as they are: an entity snapshot API, a status page, a read after closure, a read that must not depend on Workers |
| Read-only RPC | Flow code must compute or reshape the response; it needs pending messages or every map instance; it must exclude concurrent writers with a lock (active Flow only); it needs more values from one commit than one read call allows |

## Read-after-write

After an RPC write, a direct read of the same state sees it; do not poll. Search and Attribute Store tables lag. A direct read does not prove that triggered Steps finished. A returned call, a completed future, or a nil error proves only that the API's own response boundary was reached. The table below is a reference for less common pairs.

Three different guarantees:

1. **Acceptance.** Dex accepted the request. It may not have processed it.
2. **Read-after-write.** A read started after the write returns sees the write's direct effects or a later state. The read may block until the Server catches up.
3. **Business completion.** The Steps, provider calls, and cleanup the write triggered have finished. A fresh read does not prove this.

| Write | Later read | Guarantee |
| --- | --- | --- |
| RPC that writes Attributes or map instances | Read-only RPC that loads the same state | Strong. Read once; do not poll. |
| RPC that writes Attributes or map instances | Client Attribute read of the same keys | Strong. Read once; do not poll. |
| RPC that publishes or deletes pending messages | Read-only RPC that loads the Channel | Strong for the pending queue. Another consumer can remove a just-published message. |
| Transactional or locked RPC | Read-only RPC | Strong. Handler and effects commit together. Does not wait for triggered Steps or projections. |
| Hard-delete Attribute write from a Step or RPC | Client Attribute read or read-only RPC | Strong, like an ordinary Attribute write: visible only after its method commits. |
| Indexed Attribute write | SearchFlows, CLI search, Dex Web search | Eventual. A miss does not prove the write failed. Confirm with a direct read, or bound a search retry when the index itself is needed. |
| Attribute Store-synced write | External table query | Eventual latest-state projection; never the input of a Flow decision. See [Attribute Store sync](attribute.md#attribute-store-sync). |
| RPC that moves to Steps or cancels | Step-completion wait, Attribute match, or terminal result | Asynchronous business completion. A direct read is not a scheduling barrier. |
| SkipTimer | Timer and Step state; Step completion for the result | The Timer change is readable; Execute runs later. |
| StopFlow | WaitForFlow or DescribeFlow | Closed as soon as it commits. Cleanup and pending projection writes may continue. |
| UpdateFlowConfig | Server or CLI configuration read | Applied once the Flow processes it. Does not recall dispatched work. |

- StartFlow and TimeTravel are not state writes. Their returned run identifies an accepted Flow run. When the response must prove business acceptance, wait for a named Step or the result.
- Strong read-after-write needs a successful write, a read started afterward, the same Flow, and the right selections. It adds no isolation, does not make a sequence of RPCs atomic, and does not protect a value from a later competing write.
- Set a read deadline that allows the Server to catch up.

## Waits

Attribute-match and Step-completion waits observe durable state. They are not an application timeout and not a final result.

- **Request timeout** is the caller's budget for the whole SDK call, across every transparent reattachment. Zero waits until cancellation or a result. Expiry returns the SDK's typed request-timeout error. A later call waits again from current state.
- The Server caps each transport wait internally; the SDK reattaches with the same Request ID without resetting the request timeout.
- A wait's Request ID is optional. A wait reads current committed state, so repeating it is safe.
- Each waiting call holds a wait slot on the Flow only while it waits. The Server rejects waits beyond its per-Flow limit; see [default limits](../../SKILL.md#default-limits).
- Attribute waits target the active run. If the Flow can close before the wait attaches, handle the error through authoritative state.

Attribute match:

- Missing Attributes never match. Cross-type comparisons never match.
- Strings and Booleans support equal and not equal. Integers and floats support all six comparisons.
- Object, bytes, null, blob-backed, non-finite float, and invalid operands fail.
- Hard-delete Attributes cannot be matched.
- A revision watermark coalesces: a wait for greater than 0 after `0 -> 1 -> 2 -> 3` may return 3. It is not an event stream.

Step completion confirms that one Step execution committed. It returns no Step output and does not wait for the Flow.

WaitForFlow returns the terminal status and output. Use it when the caller needs engine completion.

Step outputs live only in history, and only a COMPLETED Flow keeps an output; see [Runs, reads, and outputs](../../SKILL.md#runs-reads-and-outputs). So a report that must survive a failure or stay readable while the Flow runs goes to an Attribute, not only to the output.

## Lifecycle APIs

- StopFlow cancel closes the run as canceled. Fail closes it as failed with a reason. Terminate closes it as terminated. Each records a different terminal status. None proves provider cleanup or projection completion.
- A cancel, fail, or terminate stop starts no more application code, and a handler that ignores cancellation keeps making external calls; see [Flow timeout, completion, and cancellation](../../SKILL.md#flow-timeout-completion-and-cancellation). So whoever waits for the stopped Flow reconciles from its terminal state and accepts an earlier report idempotently.
- A later write to a closed run is rejected. Write what the Flow needs before you stop it.
- DescribeFlow on a missing ID, or an ID that is not a Flow, returns Flow not found.
- SearchFlows excludes internal Flow runs. A generic-slot query constrains FlowType; see [indexed Attributes](attribute.md#indexed-attributes-and-search). Keep the query unchanged across pages.

## Self-check

- Does every business decision and status write live in a Step or RPC, with the caller only supplying facts and reading results?
- Is a direct read after an RPC write read once, with polling kept for search and Attribute Store tables?
- Does each response read all its values in one Client Attribute read call or one read-only RPC?
- Is every wait bounded by a request timeout at an HTTP boundary?
- Is search used only for discovery, never as proof of a write?
