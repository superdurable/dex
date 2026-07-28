The iWF top level concept is `WorkflowDefinition`which consists of the components shown below:

| Name                                                    | Description                                                                                                                                       | 
|:--------------------------------------------------------|:--------------------------------------------------------------------------------------------------------------------------------------------------| 
| [WorkflowState](WorkflowState.md)                        | A basic asyn/background execution unit as a "big step" of a "workflow". A State actually consists of one or two small steps: *waitUntil* (optional) and *execute*     |
| [RPC](RPC.md)                                             | API for application to interact with the workflow. It can access to persistence, internal channel, and state execution                            |
| [Persistence](Persistence.md)                             | A Kev-Value storage out-of-box to storing data. Can be accessed by RPC/WorkflowState implementation.                                              |
| [DurableTimer](WorkflowState.md#commands-from-waituntil)                | The waitUntil API can return a timer command to wait for certain time as a durable timer -- it is persisted by server and will not be lost.       |
| [InternalChannel](WorkflowState.md#internalchannel-async-message-queue) | The waitUntil API can return some command for "Internal Channel" -- An internal message queue workflow                                            |
| [Signal Channel](RPC.md#signal-channel-vs-rpc)            |  Async message queue for the workflowState to receive messages from external sources. Can be replaced by RPC+InternalChannel |


Above Workflow/WorkflowState concepts will have their instance as "Definitions" and "Executions". Below are the concepts of them:

| Name                                                    | Description                                                                                                                                       | 
|:--------------------------------------------------------|:--------------------------------------------------------------------------------------------------------------------------------------------------| 
| WorkflowDefinition| The code instance that implemented the interface "ObjectWorkflow". See below examples for Java/Golang/Python|
| WorkflowType| The identifier of the WorkflowDefinition within an application. By default it's the class/struct name in the code|
| WorkflowExecution| An execution instance of the workflow definition. It's initiated by StartWorkflow API to start running. |
| WorkflowID| The business identifier to start a WorkflowExecution. It's like "Primary Key" of designing iWF workflow. There cannot be more than one WorkflowExecutions running in-parallel with the same WorkflowID. It must be provided for any interactions after a workflow started. |
| WorkflowRunID| An internal identifier in UUID. It's returned by startWorkflow API to help identifier different workflowExecution. Because WorkflowID is allowed to reused based on [IDReusePolicy](WorkflowOptions.md#idreusepolicy-for-workflowid), the identifier of WorkflowExecution is WorkflowRunID. It's returned from iWF server by StartWorkflow API. However, due to the [AutoContinueAsNew](https://medium.com/@qlong/guide-to-continueasnew-in-cadence-temporal-workflow-using-iwf-as-an-example-part-1-c24ae5266f07) in iWF to workaround the Cadence/Temporal history limitation, the WorkflowRunID could be changed. But it's stable within in StateExecution (It won't change during the backoff retry).|
| StateID | It's the identifier of a WorkflowState definition. By default it's the className/structName in the code. |
| StateExecution| The instance of an execution of a WorkflowState. It includes the StateID to load the StateDefinition, the input of the State to invoke the code of StateDefinition|
| StateExecutionID| Given a WorkflowExecution, the same StateDefinition(StateID) can be executed multiple times. The StateExecutionID is to identifier the different StateExecutions. It's in a format of "StateID-<number>", where "<number>" is incremental counter maintained by iWF server. This is not an UUID |
| RPCName| The identifier of an RPC definition within a WorkflowDefinition. By default it's the method name in the code|

For example of this [UserSignupWorkflow](../../examples/java/src/main/java/io/iworkflow/workflow/signup/UserSignupWorkflow.java): 

* UserSignupWorkflow is the WorkflowType, which identify the definition of the workflow
* This [startWorkflow](../../examples/java/src/main/java/io/iworkflow/controller/SignupWorkflowController.java#L38) API will start a workflowExecution, by providing a workflowID as business identifier. The API will return a workflowRunID (UUID) as internal identifier.
* The startingState is SubmitState, which is also the StateID.
* The first execution of the State will be `SubmitState-1`. 
* The Another State is VerifyState, and it could run as a [loop](../../examples/java/src/main/java/io/iworkflow/workflow/signup/UserSignupWorkflow.java#L136). Hence the StateExecutionIDs will be VerifyState-1, VerifyState-2, ... etc. 
* An RPCName is [verify](../../examples/java/src/main/java/io/iworkflow/workflow/signup/UserSignupWorkflow.java#L72).

More notes:
* WorkflowType & StateID & RPCName are key elements in SDK. A StateExecution API callback from iWF server contains WorkflowType+StateID for SDK to invoke the associate StateDefinition. A RPC API call back from iWF server contains WorkflowType + RPCName. 
* The runtime values of WorkflowID, WorkflowRunID, StateExecutionID are accessible by "Context". E.g. [in Java SDK](../../sdk-java/src/main/java/io/iworkflow/core/Context.java#L8) (StateExecutionID will be empty for RPC because RPC doesn't below to any WorkflowState).

### SDK

A user application defines an ObjectWorkflow by implementing:
* [Java Interface](../../sdk-java/src/main/java/io/iworkflow/core/ObjectWorkflow.java)
* [Golang Interface](../../sdk-go/iwf/workflow.go) 
* [Python Base Class](../../sdk-python/iwf/workflow.py)

Once workflow is implemented, register the workflows into `Registry` of SDK, and expose an RESTful endpoint for iWF server to call using `WorkerService` of the SDK.

Underneath, SDK will invoke the corresponding Workflow/WorkflowState/RPC code when being called by iWF server:
* Java Example to use [Spring to register workflow beans](../../examples/java/src/main/java/io/iworkflow/config/IwfConfig.java), and set up [WorkerControllers](../../examples/java/src/main/java/io/iworkflow/controller/IwfWorkerApiController.java)
* Golang Example to [register workflows](../../examples/go/workflows/registry.go), and use [Golang Gin server to start worker controller](../../examples/go/cmd/server/iwf/iwf.go#L72).
* Python example to [register workflows](../../examples/python/signup/iwf_config.py), and use Flask to set up [WorkerControllers](../../examples/python/signup/main.py#L54).

#### Java

The Java interface has default implementation of all methods. So you can skip if you don't need any of them.
For example, if a workflow doesn't need persistence, then just skip the persistenceSchema.

An [example](../../examples/java/src/main/java/io/iworkflow/workflow/signup/UserSignupWorkflow.java) of Java workflow definition:
```java
public class UserSignupWorkflow implements ObjectWorkflow {

    public static final String DA_FORM = "Form";

    public static final String DA_Status = "Status";
    public static final String VERIFY_CHANNEL = "Verify";

    private MyDependencyService myService;

    public UserSignupWorkflow(MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new SubmitState(myService)),
                StateDef.nonStartingState(new VerifyState(myService))
        );
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(SignupForm.class, DA_FORM),
                DataAttributeDef.create(String.class, DA_Status)
        );
    }

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                InternalChannelDef.create(Void.class, VERIFY_CHANNEL)
        );
    }

    // Atomically read/write/send message in RPC
    @RPC
    public String verify(Context context, Persistence persistence, Communication communication) {
        String status = persistence.getDataAttribute(DA_Status, String.class);
        if (status == "verified") {
            return "already verified";
        }
        persistence.setDataAttribute(DA_Status, "verified");
        communication.publishInternalChannel(VERIFY_CHANNEL, null);
        return "done";
    }
}
```

#### Python
The Python base class has default implementation of all methods. So you can skip if you don't need any of them.
For example, if a workflow doesn't need persistence, then just skip the persistenceSchema.

[Example](../../examples/python/signup/signup_workflow.py) in Python:
```python
class UserSignupWorkflow(ObjectWorkflow):
    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(SubmitState(), VerifyState())

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.create(
            PersistenceField.data_attribute_def(data_attribute_form, Form),
            PersistenceField.data_attribute_def(data_attribute_status, str),
            PersistenceField.data_attribute_def(data_attribute_verified_source, str),
        )

    def get_communication_schema(self) -> CommunicationSchema:
        return CommunicationSchema.create(
            CommunicationMethod.internal_channel_def(verify_channel, None)
        )

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

#### Golang
Golang interface doesn't have default method implementation. So to make it "skippable", you just need to add the default implementation `iwf.WorkflowDefaults` of all:
```golang
type MyWorkflow struct {
	iwf.WorkflowDefaults
}
```

Also, Golang doesn't have equivalence to Java's annotation or Python's decorator. An RPC must be registered under CommunicationSchema.

This is an [example](../../examples/go/workflows/microservices/workflow.go) of a Golang workflow definition:
```golang
type OrchestrationWorkflow struct {
	iwf.WorkflowDefaults

	svc service.MyService
}

func (e OrchestrationWorkflow) GetWorkflowStates() []iwf.StateDef {
	return []iwf.StateDef{
		iwf.StartingStateDef(NewState1(e.svc)),
		iwf.NonStartingStateDef(NewState2(e.svc)),
		iwf.NonStartingStateDef(NewState3(e.svc)),
		iwf.NonStartingStateDef(NewState4(e.svc)),
	}
}

func (e OrchestrationWorkflow) GetPersistenceSchema() []iwf.PersistenceFieldDef {
	return []iwf.PersistenceFieldDef{
		iwf.DataAttributeDef(keyData),
	}
}

func (e OrchestrationWorkflow) GetCommunicationSchema() []iwf.CommunicationMethodDef {
	return []iwf.CommunicationMethodDef{
		iwf.SignalChannelDef(SignalChannelReady),

		iwf.RPCMethodDef(e.MyRPC, nil),
	}
}

func (e OrchestrationWorkflow) MyRPC(ctx iwf.WorkflowContext, input iwf.Object, persistence iwf.Persistence, communication iwf.Communication) (interface{}, error) {

	var oldData string
	persistence.GetDataAttribute(keyData, &oldData)
	var newData string
	input.Get(&newData)
	persistence.SetDataAttribute(keyData, newData)

	return oldData, nil
}
```