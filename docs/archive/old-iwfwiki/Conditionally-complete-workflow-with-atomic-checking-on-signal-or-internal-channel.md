<!---
---
title: Completing Workflow with Channel Check
---
--->

<!---## DOCHUB-PATH: advanced-concepts/completing-workflow-with-channel-check.mdx :DOCHUB-PATH ##--->

<!---
# Conditionally complete workflow with atomic checking on signal or internal channel
--->

One of the [StateDecision](WorkflowState.md#statedecision-from-execute) can be conditional checking on signal/internal channel, like:
* [forceCompleteIfInternalChannelEmptyOrElse](../../sdk-java/src/main/java/io/dex/core/StateDecision.java#L93)
* [forceCompleteIfSignalChannelEmptyOrElse](../../sdk-java/src/main/java/io/dex/core/StateDecision.java#L132C33-L132C72)

The main scenario/use case of this feature is to keep the workflow execution as short as possible while the workflow is receiving requests from external to process (via Signal or RPC + internalChannel). Keeping the workflow short helps reduce the cost of using Cadence/Temporal, especially if the number of workflows is large. And it's generally easier to maintain a short workflow than a long one, for [versioning](%5BVersioning%5DHow-to-modify-workflow-code-without-breaking-changes.md) workflow. 

The atomic checking of channel being empty is performed on Dex server. This is to safely ensure no racing conditions, leading to messages being unprocessed when completing a workflow. 

However, you must be sure that only one state is consuming the signal or internal channel being checked. Otherwise, there could still be racing conditions. 

For example, the below is a wrong usage:
```java
InitState implements WorkflowState { // starting State
   execute(...) {
      return StateDecision.multiNextStates(State1.class, State2.class);
   }   
}

State1 implements WorkflowState {

   waitUntil(...) {
      return CommandRequest.forAnyCommandCompleted( InternalChannelCommand.create("TEST_CHANNEL"));
   }

   execute(...) {
      return forceCompleteIfInternalChannelEmptyOrElse("TEST_CHANNEL", State1.class);
   }
}

State2 implements WorkflowState {

   waitUntil(...) {
      return CommandRequest.forAnyCommandCompleted( InternalChannelCommand.create("TEST_CHANNEL"));
   }

   execute(...) {
      return forceCompleteIfInternalChannelEmptyOrElse("TEST_CHANNEL", State2.class);
   }
}
```
The problem is that when the channel has one message, it may happen that State1 consumes the message and also State2 checks the emptiness. Therefore, State2 will see an empty channel and complete the workflow, while State1 doesn't have a chance to process the last message.