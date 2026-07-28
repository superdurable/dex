# Parent Child Design Pattern (Option 2)

Here we demonstrate how to start a child workflow and wait for its completion.

The pattern is particular useful when you need to fan out the workflow executions for parallelism, or for any other reasons that a parent workflow need to wait for a child to complete.

There are two different ways in iWF.

## Option 1: Let child workflow send back signal on completion
This is the option that we implemented in [scalable parallel](../scalableparallel) design pattern. It's the most efficient but also a bit complex.

1. Parent should use `ignoreAlreadyStarted=true` with `requestId` when starting childWorkflow
2. Parent must use dynamic channel name to wait for each child workflow to complete.

Additionally, each child can only notify one single parent on completion. If there could be multiple parents, this approach will not work. This means that multiple parent executions can be mapped to multiple(MtoM) child executions. For real production example, a billing refund request(as a parent workflow) may contain multiple invoices, each invoice will be a child workflow, but different billing refund requests may contain some overlapping invoices, and each refund parent workflow will have to wait for its associated children to complete. 

## Option2: Let parent workflow wait for child workflow completion via client API

In the above MtoM case, there is a even simpler way to start and wait for child workflow to complete, via `iwfClient.waitForWorkflowCompletion(...)` API. 

You need to catch the known exception of "LongPollTimeoutException" if the child workflow could take more than 10 seconds to complete. Because `waitForWorkflowCompletion` only wait for 10s by default(due to default Envoy setting).

And potentially, use a timer command to wait with some interval if it is expected to take more than 10 seconds. 

This approach may look simpler for some people, without the overhead of understanding `ignoreAlreadyStarted + requestId` and "dynamic channel".  

So we will implement it here.

Compared to Option1, this option is less efficient in terms of Temporal actions usage -- it will consume Temporal actions for every iteration of AwaitChildWorkflowCompletionState until the child completed, if the child workflow takes very long time (like days), 
it's not recommended to use this option to wait for child workflow. You should use option1, although a little more code to write. 

But if child workflow can normally complete within minutes, this is probably the easiest way to deal with child workflow, and it's more flexible to support MtoM relationship of parent&child.