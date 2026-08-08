Note that users can wait for a state execution to complete in the new feature: How-to-wait-for-a-workflow-state-to-complete.md 

The page is using Java as example, but should be the same idea for other SDKs.

You can call a Client API after starting workflow, to wait for the completion of the workflow -- getSimpleResultsWithWait  will let you wait for the completion.
Some details about it:

* The API will throw an WorkflowUncompletedException if the workflow doesn’t complete eventually. This include failure, timeout, cancel, terminate etc. You could catch it to do other business.
* It will throw `NoRunningWorkflowException`  if the workflow Id is not valid.
* Since it's a long poll API and HTTP request has timeout(default to 60s), it will throw `LongPollTimeoutException` with  
 LONG_POLL_TIME_OUT_SUB_STATUS if exceeds the wait time. You should just retry on that if needs continue waiting. 
* You may just call this API in a State API and let State API keep on retrying for you automatically. 
* You can pass Void.class as result class, If you just need to wait for the result, without any real results to return.


