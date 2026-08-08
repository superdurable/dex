<!---
---

---
--->

<!---## DOCHUB-PATH: advanced-concepts/signal-and-internal-channels.mdx :DOCHUB-PATH ##--->

<!---
# Signal and Internal Channels
--->

<!--- ## GITHUB-ONLY ## --->
## TL; DR:
<!--- ## END-GITHUB-ONLY ## --->

SignalChannel can be replaced by InternalChannel + RPC. However, this replacement has a slight overhead in performance (latency) and cost (two Temporal Cloud actions vs. one).

As a recommendation, if the signal is enough, we should just use the signal, but some complex features may need internal channels. E.g. must use an RPC to return some results, or need an RPC call can send multiple internal channel messages atomically which signal cannot achieve.

## Details
Signal is created first, it’s mapped directly to Cadence/Temporal’s signal feature. Because of that, you can use Cadence/Temporal WebUI to send signal directly. Because of that, a signal is mapped directly to a Cadence/Temporal history event of “WorkflowExecutionSignaled” which is very nice to read.

There are two reasons that we introduced InternalChannel later:
* Signal is like sending a message to MessageQueue without a response. Sometimes users want to get results as part of the write operation. So RPC is created. In order to let RPC wake up some WorkflowStates running in the background, we need a message channel.
* The WorkflowStates executions (especially when executing in parallel) may need some synchronization. E.g. 1 -> 2, 3, 4 -> 5 where 2, 3, 4 need to be completed together before 5 is executed. This is an internal communication as well, so we call it “internal channel”.
<!--- ## GITHUB-ONLY ## --->
## See more
* RPC.md#signal-channel-vs-rpc
* WorkflowState.md#internalchannel-async-message-queue
<!--- ## END-GITHUB-ONLY ## --->