This [Subscription](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/dex/workflow/subscription)

To start a subscription workflow:

* Open http://localhost:8803/subscription/start

It will return you a **workflowId**.

The controller is hard coded to start with 20s as trial period, 10s as billing period, $100 as period charge amount for
10 max billing periods

To update the period charge amount :

* Open http://localhost:8803/subscription/updateChargeAmount?workflowId=<TheWorkflowId>&newChargeAmount=<The new amount>

To cancel the subscription:

* Open http://localhost:8803/subscription/cancel?workflowId=<TheWorkflowId>


To describe the subscription:
* Open http://localhost:8803/subscription/describe?workflowId=<TheWorkflowId>

It's recommended to use an Dex state diagram to visualize the workflow design like this:
![Subscription workflow Dex state diagram](https://user-images.githubusercontent.com/4523955/216396635-1c46df3c-e087-415a-996e-16ce47e7ccb2.png)