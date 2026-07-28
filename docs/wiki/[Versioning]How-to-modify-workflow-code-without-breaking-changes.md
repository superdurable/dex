So there are three types of “breaking changes” to discuss here:

* Non-deterministic errors – this won’t happen in Dex user application workflow 
* Business in-compatible changes 
* Technical in-compatible changes (sorry, naming is very hard here)

## Non-deterministic errors
First of all, there are [no “non-deterministic-errors” in Dex user workflow code](Compare-with-Cadence-Temporal.md#determinism-and-versioning) because Dex workflow is not running in a “replay model” like Temporal does. The Dex workflow code is serving as  RESTful APIs, meaning that the modification can take effect immediately once deployed, for current and future states.  

For example, if we have a workflow with these 4 steps/states in sequence
![image](https://github.com/indeedeng/iwf/assets/4523955/0828ea23-5ef4-44e9-bd5b-4f9eaaa78448)



And we want to add a new step/state before S3, then the code will then become
![image](https://github.com/indeedeng/iwf/assets/4523955/0e0931b9-90d0-4278-9318-261fa8ffb72f)



This is not safe to do in native Temporal workflow applications, because it breaks the replay determinism. But this is totally safe in Dex:

* For any workflow execution that is already in S3 or after, it will continue without problems
* For any workflow execution that is in S2 or before (both existing and new), it will use the new flow – execute S31 before S3.

## Business in-compatible changes 
Then we move to the second type: business in-compatible changes. For the above example, what if we want the existing workflow executions to keep using the old flow, if it started with the old one?

Essentially, the workflow logic should become:
![image](https://github.com/indeedeng/iwf/assets/4523955/27c35bd1-1bfb-4955-8019-012819c9d51e)



To implement such a behavior, the user workflow can just set a custom flag/version in the startingState of the workflow. Update the flag/version value when updating the workflow code. Then based on the flag/version to make the decision on going to S31 or S3 like in the above diagram. 

## Technical in-compatible changes
Finally, maybe the most common breaking changes are technical in-compabible changes. These are exactly the same as all the API breaking changes in microservices, because the Dex workflow code is serving as RESTful APIs for Dex server callback. The callback will fail if the API is “in-compatible”:

* Rename or remove a Workflow/WorkflowState, but the Workflow/WorkflowState is still running 
* Making breaking changes to any data model in the workflow, while the data model is still being used with old data values. E.g. change a field from optional to required, rename a field, add a new required field, etc. 


Essentially, users should keep in mind that all the workflow code is running as a RESTful service, and use the standard approaches to avoid those breaking changes. 

Some tips/best practices, for examples:

* In Java, using Optional to add a new field is always safe when changing data models
* In Java, Make sure to set “ignore unknown fields” in Jackson (default in Dex already) for removing old fields
* Use duplicated Workflow/WorkflowState when renaming, and remove the duplicated after all old workflows are completed. 
* By default, the WorkflowType and StateId are the class simple names. But you can override the method to use a different name to make it different from the class name. 
* Use system search attributes “DexExecutingStateIds” and “DexWorkflowType” to check if there are any old workflows running, when rename/removing the old StateId or WorkflowType. 
NOTE: DexExecutingStateIds is disabled by default because of cost. To enable it by setting "disableSystemSearchAttributes" to false for WorkflowConfig when starting a workflow. We are working on [this](https://github.com/indeedeng/iwf/issues/411) as a more cost effective way – it only records the SearchAttribute for states that have waitUntil. And also comes with several [optimizations](https://github.com/indeedeng/iwf/issues/454). 

![Screenshot 2024-10-24 at 8 17 13 AM](https://github.com/user-attachments/assets/3de7c550-0343-4011-8dba-f72866237847)

