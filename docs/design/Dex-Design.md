<!---
---

---
--->

<!---## DOCHUB-PATH: get-started/high-level-design.mdx :DOCHUB-PATH ##--->

# High-level design

An Dex application is composed of several Dex workflow workers. These workers host REST APIs as "worker APIs" for server to callback. The Worker APIs are exposed by the SDKs. E.g. the [Java SDK](../../sdk-java/src/main/java/io/dex/core/WorkerService.java#L37).

An application also perform actions on workflow executions, such as starting, stopping, signaling, and retrieving results by calling Dex service APIs as "service APIs". The service APIs are provided by the "API service" in Dex server. 

The API between client/worker and server is defined in [`protos/dex.proto`](../../protos/dex.proto) (see [IDL renames](idl-renames.md)).

Internally, this API service communicates with the Cadence/Temporal service as its backend. The Dex server runs the Cadence/Temporal workers as "worker service". The worker service hosts [an interpreter workflow](../../server/service/interpreter/workflowImpl.go). This workflow implements all the core features as described above, and also things like "Auto ContinueAsNew" to let you use Dex without any scaling limitation.

![Screenshot 2023-07-17 at 10 22 10 AM](https://github.com/indeedeng/iwf/assets/4523955/f85011ee-6f2a-4a7c-9215-459c3630d4b4)

Users define their workflow code with a new SDK “Dex SDK” and the code is running in  workers that talk to the Dex interpreter engine. 

The user workflow code defines a list of [WorkflowState](https://github.com/longquanzheng/dex/blob/main/src/com/dex/WorkflowState.java) and kicks off a workflow execution.

At any workflow state, the interpreter will call back the user workflow code to invoke some APIs (waitUntil or execute).
Calling the `waitUntil` API will return some command requests. When the command requests are finished, the interpreter will then call user workflow code to invoke the “execute” API to return a decision. The decision will decide how to complete or transitioning to other workflow states.

At any API, workflow code can mutate the data/search attributes or publish to internal channels. 

![Screenshot 2023-07-17 at 10 26 32 AM](https://github.com/indeedeng/iwf/assets/4523955/f83bfec3-b544-498d-8b2a-63e64700ba66)

# Interpreter workflow pseudo code 
Below is the signature of the interpreter workflow in Java. 
Notes 
Config will store configuration of the execution so that the workflow knows the end point of dex user worker to callback user workflow code(“waitUntil” and “execute”). 
Input/output of this workflow are all binaries as the workflow doesn’t need to deserialize them. Similar as the activity input/output
Here uses Java to demonstrate the signature because Java is more declarative. But we will use Golang to implement the workflow because the Cadence/Temporal Golang SDK is more powerful than Java SDK. Workflow thread in Golang SDK is based on goroutines which are lightweight and efficient(memory/CPU)

<!---
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

<div style={{
    "border": "1px darkgray solid",
    "borderRadius": "1rem",
    "padding": "0.5rem"
}}>
<Tabs>
    <TabItem value="java" label="Java">
--->
```java
public interface InterpreterWorkflow{
   @WorkflowMethod
   void start(Config config, State startState, []byte startInput);
   @QueryMethod
   byte[] query(String key)    
   @SignalMethod
   void signal(String name, byte[] value)
}
```

Below is the implementation of the start method in “Java like” pseudo code by omitting lots of details. 

```java
public void start( State startState, byte[] startInput){
	currentStates = new Queue( new StateExecution(startState, startInput))
	while (currentStates.isNotEmpty()){
		statesToExecute = currentStates.popAll()
                for( stateExecutioin in statesToExecute){
				Async.procedure( ()->{
					decision = processStateExecution( stateExecution )
					if(decision.hasCompletedState){
						return decision.completedResult
					}else{
			 		currentStates.pushAll(decision.allNextStateExecutions)
					}
				})
                }
		
     }
}

private void processStateExecution(StateExecution currentState){
	commandRequest = executeActivity( currentState.waitUntil, input)
	commandResults = new Array()
	foreach timerCommand = commandRequest.timerCommands {
		commandResults.add ( scheduleTimer( timerCommand ) ) 
	}
	foreach signalCommand = commandRequest.signalCommands {
		commandResults.add ( waitFor( signalCommand ) ) 
	}
	Workflow.await( commandRequest.isReady(commandResults))
	return executeActivity( currentState.execute, input)      
}

private boolean timerReady;

private void scheduleTimer( TimerCommand timerCommand){
    timerReady = false
    Workflow.sleep( timerCommand.fireTimestamp - workflow.now() )
    timerReady = true
} 

private boolean isTimerReady(){
   return timerReady
}


```

Note, this is the pseudo code to outline the high-level idea. The actual implementation is probably 100x more complicated. 

<!---
</TabItem>
</Tabs>
</div>
--->