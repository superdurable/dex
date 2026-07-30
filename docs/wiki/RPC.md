<!---
---

---
--->

<!---## DOCHUB-PATH: advanced-concepts/RPC.mdx :DOCHUB-PATH ##--->

## Overview

RPC stands for "Remote Procedure Call". Allows external systems to interact with the workflow execution.

It's invoked by client, executed in workflow worker, and then respond back the results to client. 

RPC can have access to not only persistence read/write API, but also interact with WorkflowStates using InternalChannel, 
or trigger a new WorkflowState execution in a new thread.

## Atomicity of RPC APIs

It's important to note that in addition to read/write persistence fields, a RPC can **trigger new state executions, and publish message to InternalChannel, all atomically.**

Atomically sending internal channel, or triggering state executions is an important pattern to ensure consistency across dependencies for critical business – this 
solves a very common problem in many existing distributed system applications. Because most RPCs (like REST/gRPC/GraphQL) don't provide a way to invoke 
background execution when updating persistence. People sometimes have to use complicated design to acheive this. 

**But in Dex, it's all builtin, and user application just needs a few lines of code!** 

![flow with RPC](https://user-images.githubusercontent.com/4523955/234930263-40b98ca7-4401-44fa-af8a-32d5ae075438.png)

Note that by default, read and write are atomic separately.
To ensure the atomicity of the whole RPC for read+write, you should use `PARTIAL_WITH_EXCLUSIVE_LOCK` persistence loading policy for the RPC options.
The `PARTIAL_WITH_EXCLUSIVE_LOCK` for RPC is only supported by Temporal as backend with enabling synchronous update feature (by `frontend.enableUpdateWorkflowExecution:true` in Dynamic Config).
See the [wiki](RPC-locking-What-does-the-atomicity-of-RPC-really-mean.md) for further details.

## Signal Channel vs RPC

There are two major ways for external clients to interact with workflows: Signal and RPC. 

Historically, signal was created first as the only mechanism for external application to interact with workflow. However, it's a "write only"
which is limited. RPC is the new way and much more powerful and flexible. 

Here are some more details:
* Signal is sent to Dex service without waiting for response of the processing
* RPC will wait for worker to process the RPC request synchronously
* Signal will be held in a signal channel until a workflow state consumes it
* RPC will be processed by worker immediately

![signals vs rpc](https://user-images.githubusercontent.com/4523955/234932674-b0d062b2-e5dd-4dbe-93b5-1b9863acc5e0.png)

## SDKs 
An RPC belongs to a workflow definition(instance of ObjectWorkflow) as a method of the instance. You can define as many RPCs as needed for a workflow definition. 

An RPC definition can also include optional parameters:
* RPC timeout: maximum time that server will wait for RPC execution
* data attribute loading policy: how to load the data attribute(default to all without locking)
* search attribute loading policy: how to load the data attribute(default to all without locking)

Also, there are some rules to make a method an RPC, which are different based on SDKs:

<!---
import { FaJava, FaPython } from "react-icons/fa";
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

<div style={{
    "border": "1px darkgray solid",
    "borderRadius": "1rem",
    "padding": "0.5rem"
}}>
<Tabs>
    <TabItem value="java" label={<><FaJava size={26} /> Java</>}>
--->
<!--- ## GITHUB-ONLY ## --->
### Java
<!--- ## END-GITHUB-ONLY ## --->
* Using the [RPC annotation](../../sdk-java/src/main/java/io/dex/core/RPC.java) can make a method an RPC
* The method must be [one of the four forms](../../sdk-java/src/main/java/io/dex/core/RpcDefinitions.java)

Example
```java
// Java
public class UserSignupWorkflow implements ObjectWorkflow {
//...
    @RPC
    public String verify(Context context, String input, Persistence persistence, Communication communication) {
        String status = persistence.getDataAttribute(DA_Status, String.class);
        if (status == "verified") {
            return "already verified";
        }
        persistence.setDataAttribute(DA_Status, "verified");
        communication.publishInternalChannel(VERIFY_CHANNEL, null);
        return "done";
    }
}

// Kotlin 
open class UserSignupWorkflow : ObjectWorkflow {
//...

    // "open" is required for RPC methods in Kotlin!!!!
    // "open" means "non final" in Java
    @RPC 
    open fun verify(context: Context,str: String,persistence: Persistence,communication: Communication): String {
        val status = persistence.getDataAttribute(DA_Status, String::class.java)
        if (status === "verified") {
            return "already verified"
        }
        persistence.setDataAttribute(DA_Status, "verified")
        communication.publishInternalChannel(VERIFY_CHANNEL, null)
        return "done"
    }
}
```
To invoke an RPC from external, using client API:
```java
final UserSignupWorkflow rpcStub = client.newRpcStub(UserSignupWorkflow.class, workflowId);
String output = client.invokeRPC(rpcStub::verify, input);
```
The RPC stub is for providing an strongly typing experience.

NOTE there there is a restriction to use RPC in Java/Kotlin:
* RPC methods must be [public and not final](https://github.com/indeedeng/iwf-java-sdk/issues/286).
  * In Java, a method is not final by default. But in Kotlin, you MUST add `open` to make it non final.
  * This is to make the ByteBuddy work for intercepting the call.

<!---
</TabItem>
<TabItem value="python" label={<><FaPython size={26} /> Python</>}>
--->
<!--- ## GITHUB-ONLY ## --->
### Python
<!--- ## END-GITHUB-ONLY ## --->
* Using [`rpc` decorator factory](../../sdk-python/dex/rpc.py) to annotate a method will make it an RPC. 
* Because it's decorator factory, parentheses are required even there are not parameters : `@rpc()`
* An RPC must have at most 5 params: self, context:WorkflowContext, input:Any, persistence:Persistence, communication:Communication, where input can be any type (the order doesn't matter, but it's recommended for convention)

```python
class UserSignupWorkflow(ObjectWorkflow):
...
...

    @rpc()
    def verify(
        self, source: str, persistence: Persistence, communication: Communication
    ) -> str:
        status = persistence.get_data_attribute(data_attribute_status)
        if status == "verified":
            return "already verified"
        persistence.set_data_attribute(data_attribute_status, "verified")
        persistence.set_data_attribute(data_attribute_verified_source, source)
        communication.publish_to_internal_channel(verify_channel)
        return "done"
```

Invoke an RPC is very simple in python using client:
```python
output = client.invoke_rpc(username, UserSignupWorkflow.verify, input)
```

<!---
</TabItem>
<TabItem value="go" label="Golang">
--->
<!--- ## GITHUB-ONLY ## --->
### Golang
<!--- ## END-GITHUB-ONLY ## --->
Golang doesn't have equivalence to Java's annotation or Python's decorator. An RPC must be registered under CommunicationSchema.

```go
type MyWorkfow struct{
   dex.WorkflowDefaults
}

func (e MyWorkflow) GetCommunicationSchema() []dex.CommunicationMethodDef {
	return []dex.CommunicationMethodDef{
		dex.RPCMethodDef(e.MyRPC, nil),
	}
}

func (e MyWorkflow) MyRPC(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (interface{}, error) {

	var oldData string
	persistence.GetDataAttribute(keyData, &oldData)
	var newData string
	input.Get(&newData)
	persistence.SetDataAttribute(keyData, newData)

	return oldData, nil
}
```

To invoke an RPC from external, using client API:
```go
var output string
err := client.InvokeRPC(context.Background(), wfId, "", wf.MyRPC, input, &output)
```
<!---
</TabItem>
</Tabs>
</div>
--->
