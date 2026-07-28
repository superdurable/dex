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
* WorkflowAsCode
* Natural to represent business
* Builtin & rich support for operation tooling

It's so simple & easy  to implement [business logic code in a single file](./UserSignupWorkflow.java). 

And the [application code](../../controller/SignupWorkflowController.java) will be simply interacting with the workflow.

### How to run
* submit API: http://localhost:8803/signup/submit?username=test1&email=abc@c.com
* verify API: http://localhost:8803/signup/verify?username=test1
