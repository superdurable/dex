### Problem
![1](https://github.com/indeedeng/iwf/assets/4523955/e0c7001e-2c8f-4a93-92d7-37e50a248c26)

As above diagram, you want to:
* Orchestrate 4 APIs as a workflow
* Each API needs backoff retry
* The data from topic 1 needs to be passed through
* API2 and API3+4 need to be in different parallel threads
* Need to wait for a signal from topic 2 for a day before calling API3
* If not ready after a day, call API4

This is a very abstracted example. It could be applied into any real-world scenario like refund process:
* API1: create a refund request object in DB
* API2: notify different users refund is created
* topic2: wait for approval
* API3: process refund after approval
* API4: notify timeout and expired

### Some existing solutions

With some other existing technologies, you solve it using message queue(like SQS which has timer) + Database like below:
![2](https://github.com/indeedeng/iwf/assets/4523955/babfca50-c605-4fae-b146-18d2aad79c6e)

* Using visibility timeout for backoff retry
  * Need to re-enqueue the message for larger backoff
* Using visibility timeout for durable timer
  * Need to re-enqueue the message for once to have 24 hours timer
* Need to create one queue for every step
* Need additional storage for waiting & processing ready signal
* Only go to 3 or 4 if both conditions are met
* Also need DLQ and build tooling around

It's complicated and hard to maintain and extend.   

### Dex solution
![3](https://github.com/indeedeng/iwf/assets/4523955/3428523e-c3d9-4fd6-8d10-c19b91ac7ecd)

The solution with Dex:
* All in one single place without scattered business logic
* Natural to represent business
* Builtin & rich support for operation tooling

It's so simple & easy to do that the code can be shown here!

See the running code in [Java samples](../../examples/java/src/main/java/io/dex/workflow/microservices), [Golang samples](../../examples/go/workflows/microservices). 
```java
public class OrchestrationWorkflow implements ObjectWorkflow {

    public static final String DA_DATA1 = "SomeData";
    public static final String READY_SIGNAL = "Ready";

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new State1()),
                StateDef.nonStartingState(new State2()),
                StateDef.nonStartingState(new State3()),
                StateDef.nonStartingState(new State4())
        );
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(String.class, DA_DATA1)
        );
    }
    
    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                SignalChannelDef.create(Void.class, READY_SIGNAL)
        );
    }
}

class State1 implements WorkflowState<String> {

    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    @Override
    public StateDecision execute(final Context context, final String input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        persistence.setDataAttribute(DA_DATA1, input);
        System.out.println("call API1 with backoff retry in this method..");
        return StateDecision.multiNextStates(State2.class, State3.class);
    }
}

class State2 implements WorkflowState<Void> {

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        String someData = persistence.getDataAttribute(DA_DATA1, String.class);
        System.out.println("call API2 with backoff retry in this method..");
        return StateDecision.deadEnd();
    }
}

class State3 implements WorkflowState<Void> {

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, final Persistence persistence, final Communication communication) {
        return CommandRequest.forAnyCommandCompleted(
                TimerCommand.createByDuration(Duration.ofHours(24)),
                SignalCommand.create(READY_SIGNAL)
        );
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        if (commandResults.getAllTimerCommandResults().get(0).getTimerStatus() == TimerStatus.FIRED) {
            return StateDecision.singleNextState(State4.class);
        }
        
        String someData = persistence.getDataAttribute(DA_DATA1, String.class);
        System.out.println("call API3 with backoff retry in this method..");
        return StateDecision.gracefulCompleteWorkflow();
    }
}

class State4 implements WorkflowState<Void> {

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        String someData = persistence.getDataAttribute(DA_DATA1, String.class);
        System.out.println("call API4 with backoff retry in this method..");
        return StateDecision.gracefulCompleteWorkflow();
    }
}
```

And the [application code](../../examples/java/src/main/java/io/dex/controller/MicroserviceWorkflowController.java) simply interacts with the workflow like below:
```java
    @GetMapping("/start")
    public ResponseEntity<String> start(
            @RequestParam String workflowId
    ) {
        try {
            client.startWorkflow(OrchestrationWorkflow.class, workflowId, 3600, "some input data, could be any object rather than a string");
        } catch (WorkflowAlreadyStartedException e) {
            // ignore
        }
        return ResponseEntity.ok("success");
    }

    @GetMapping("/signal")
    ResponseEntity<String> receiveSignalForApiOrchestration(
            @RequestParam String workflowId) {
        client.signalWorkflow(OrchestrationWorkflow.class, workflowId, "", OrchestrationWorkflow.READY_SIGNAL, null);
        return ResponseEntity.ok("done");
    }
```