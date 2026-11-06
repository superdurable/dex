# Client

The Client is the application's boundary to Dex. It starts, stops, inspects, and searches Flows, reads committed Attributes, invokes typed RPCs, waits for state, and reads or writes Streams. It needs only the Dex Server address and the same registry and payload configuration as the Workers. See [application.md](application.md) for wiring.

Inside a Step or RPC handler, read and write through the Context, never through a Client. A Step may hold an injected Client only to start or call another top-level Flow.

## Operations

| Operation | Purpose | Notes |
| --- | --- | --- |
| StartFlow | Start a run with typed input, options, and initial Attributes | Returns once Dex accepts the start. See [error-handling.md](error-handling.md#start-identity-and-duplicate-starts). |
| InvokeRPC | Call a typed Flow RPC | The only application write path for Flow state. See [rpc.md](rpc.md). |
| Client Attribute read | Read committed Attributes and exact map instances | No Worker. See below. |
| WaitForAttributeMatch, WaitForAttributeMapInstanceMatch | Wait until a stored value matches | Needs an active Flow. |
| WaitForStepCompletion | Wait until a named Step execution completes | Returns no Step output. |
| WaitForFlow | Wait for terminal status and output | Can return a long-poll timeout while still open; call again. |
| DescribeFlow | Current or latest run status, type, start time | Does not wait. |
| SearchFlows | Find Flows by query over indexed fields | Eventually consistent. |
| StopFlow | Close a run: cancel (default), fail with a reason, or terminate | Closes as soon as it commits. |
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
- **Committed state.** It never sees writes that a running Step or RPC has staged. Hard-delete values are the exception: they are stored before commit, so the read can return a value from an attempt that later failed.
- **Batch only.** There is no single-Attribute method. Pass every Attribute and map instance the response needs in one call. Never issue one call per Attribute. Some SDKs cap the number of reads per call; separate calls can read different commits. When one response needs more values from one commit, use a read-only RPC.
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

Classify every write API and read API pair. A returned call, a completed future, or a nil error proves only that the API's own response boundary was reached.

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
| Hard-delete Attribute write from a Step or RPC | Any read or a retried attempt | Visible before commit; can reflect a failed attempt. |
| Indexed Attribute write | SearchFlows, CLI search, Dex Web search | Eventual. A miss does not prove the write failed. Confirm with a direct read, or bound a search retry when the index itself is needed. |
| Attribute Store-synced write | External table query | Eventual latest-state projection. Never read it to make a Flow decision. |
| RPC that moves to Steps or cancels | Step-completion wait, Attribute match, or terminal result | Asynchronous business completion. A direct read is not a scheduling barrier. |
| SkipTimer | Timer and Step state; Step completion for the result | The Timer change is readable; Execute runs later. |
| StopFlow | WaitForFlow or DescribeFlow | Closed as soon as it commits. Cleanup and pending projection writes may continue. |
| UpdateFlowConfig | Server or CLI configuration read | Applied once the Flow processes it. Does not recall dispatched work. |

- StartFlow and TimeTravel are not state writes. Their returned run identifies an accepted execution. Wait for a named Step or the result to prove business acceptance.
- Strong read-after-write needs a successful write, a read started afterward, the same Flow, and the right selections. It adds no isolation, does not make a sequence of RPCs atomic, and does not protect a value from a later competing write.
- Set a read deadline that allows the Server to catch up. Do not weaken a strong path into polling to hide unavailability.
- Never use a fixed sleep as confirmation. Do not attach a callback to every write. Add an application acknowledgement only when the contract needs a fact public observation cannot express.

## Waits

Attribute-match and Step-completion waits observe durable state. They are not an application timeout and not a final result.

- **Request timeout** is the caller's budget for the whole SDK call, across every transparent reattachment. Zero waits until cancellation or a result. Expiry returns the SDK's typed request-timeout error. A later call waits again from current state.
- The Server caps each transport wait internally; the SDK reattaches with the same Request ID without resetting the request timeout.
- A wait's Request ID is optional. A wait reads current committed state, so repeating it is safe.
- Each waiting call holds a wait slot on the Flow only while it waits. Keep concurrent waiters per Flow bounded; the Server rejects waits beyond its limits with RESOURCE_EXHAUSTED.
- Leave any deprecated internal handler timeout unset.
- Attribute waits target the active run. If the Flow can close before the wait attaches, handle the error through authoritative state.

Attribute match:

- Missing Attributes never match. Cross-type comparisons never match.
- Strings and Booleans support equal and not equal. Integers and floats support all six comparisons.
- Object, bytes, null, blob-backed, non-finite float, and invalid operands fail.
- Hard-delete Attributes cannot be matched.
- A revision watermark coalesces: a wait for greater than 0 after `0 -> 1 -> 2 -> 3` may return 3. It is not an event stream.

Step completion confirms that one Step execution committed. It returns no Step output and does not wait for the Flow.

WaitForFlow returns the terminal status and output. Use it when the caller needs engine completion, not as a business read.

## Lifecycle APIs

- StopFlow cancel asks the Flow to stop cooperatively. Fail closes it as failed with a reason. Terminate closes it at once. Each records a different terminal status. None proves provider cleanup or projection completion.
- A later write to a closed run is rejected. Write what the Flow needs before you stop it.
- DescribeFlow on a missing ID, or an ID that is not a Flow, returns Flow not found.
- SearchFlows excludes internal executions. A generic-slot query must constrain FlowType. Keep the query unchanged across pages.

## Self-check

- Is each read after a write classified by the matrix above, with no polling on strong pairs?
- Does each response read all its values in one Client Attribute read call or one read-only RPC?
- Is every wait bounded by a request timeout at an HTTP boundary?
- Is search used only for discovery, never as proof of a write?
