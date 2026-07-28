# iWF Python examples

samples for [iWF Python SDK](https://github.com/superdurable/iwf/tree/main/sdk-python) (`pip install iwf-sdk`)

## Running Requirements

* Python 3.9+
* [iWF server](https://github.com/superdurable/iwf#how-to-use)

## How to run locally
1. Start iWF server, using [docker](https://github.com/superdurable/iwf?tab=readme-ov-file#using-all-in-one-docker-image) or [docker-compose](https://github.com/superdurable/iwf?tab=readme-ov-file#using-docker-image--docker-compose)
2. Install dependencies and run the sample code
```
poetry install
# Run case 1
poetry run python moneytransfer/main.py
# or, run case 2
poetry run python signup/main.py
```

## Case1: [Money transfer workflow/SAGA Patten](./moneytransfer)

This example shows how to transfer money from one account to another account.
The transfer involves multiple steps. When any step fails, the whole transfer is canceled with some compensation steps.

## Case 2: User sign-up workflow

A common use case that is almost everywhere -- new user sign-up/register a new account in a website/system.
E.g. Amazon/Linkedin/Google/etc...

### Use case requirements

* User fills a form and submit to the system with email
* System will send an email for verification
* User will click the link in the email to verify the account
* If not clicking, a reminder will be sent every X hours

<img width="303" alt="user case requirements" src="https://github.com/superdurable/iwf-python-sdk/assets/4523955/356a4284-b816-42d3-9e44-b371a91834e4">

### Some old solution

With some other existing technologies, you solve it using message queue(like SQS which has timer) + Database like below:

<img width="309" alt="old solution" src="https://github.com/superdurable/iwf-python-sdk/assets/4523955/49ef8846-9589-4a28-91bd-c575daf37dcf">

* Using visibility timeout for backoff retry
* Need to re-enqueue the message for larger backoff
* Using visibility timeout for durable timer
* Need to re-enqueue the message for once to have 24 hours timer
* Need to create one queue for every step
* Need additional storage for waiting & processing ready signal
* Only go to 3 or 4 if both conditions are met
* Also need DLQ and build tooling around

**It's complicated and hard to maintain and extend.**

### New solution with iWF

The solution with iWF:
<img width="752" alt="iwf solution" src="https://github.com/superdurable/iwf-python-sdk/assets/4523955/4cec7742-a965-4a2d-868b-693ffba372fa">
* All in one single dependency
* Natural to represent business
* Builtin & rich support for operation tooling

It's so simple & easy to do that the [business logic code like this](./signup/signup_workflow.py) can be shown here!

```python
class SubmitState(WorkflowState[Form]):
    def execute(self, ctx: WorkflowContext, input: Form, command_results: CommandResults, persistence: Persistence,
                communication: Communication,
                ) -> StateDecision:
        persistence.set_data_attribute(data_attribute_form, input)
        persistence.set_data_attribute(data_attribute_status, "waiting")
        print(f"API to send verification email to {input.email}")
        return StateDecision.single_next_state(VerifyState)


class VerifyState(WorkflowState[None]):
    def wait_until(self, ctx: WorkflowContext, input: T, persistence: Persistence, communication: Communication,
                   ) -> CommandRequest:
        return CommandRequest.for_any_command_completed(
            TimerCommand.timer_command_by_duration(
                timedelta(seconds=10)
            ),  # use 10 seconds for demo
            InternalChannelCommand.by_name(verify_channel),
        )

    def execute(self, ctx: WorkflowContext, input: T, command_results: CommandResults, persistence: Persistence,
                communication: Communication,
                ) -> StateDecision:
        form = persistence.get_data_attribute(data_attribute_form)
        if (
                command_results.internal_channel_commands[0].status
                == ChannelRequestStatus.RECEIVED
        ):
            print(f"API to send welcome email to {form.email}")
            return StateDecision.graceful_complete_workflow("done")
        else:
            print(f"API to send the a reminder email to {form.email}")
            return StateDecision.single_next_state(VerifyState)


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

And the [application code](signup/main.py) will be simply interacting with the workflow like below:

```python
@flask_app.route("/signup/submit")
def signup_submit():
    username = request.args["username"]
    form = Form(
        ...
    )
    client.start_workflow(UserSignupWorkflow, username, 3600, form)
    return "workflow started"


@flask_app.route("/signup/verify")
def signup_verify():
    username = request.args["username"]
    source = request.args["source"]
    return client.invoke_rpc(username, UserSignupWorkflow.verify, source)
```

### development tips

When updating iwf-sdk if the dependency is not updated:

`poetry cache clear pypi --all && poetry update`
