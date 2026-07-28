### How to run
* Start a iWF server following the [instructions](https://github.com/superdurable/iwf#how-to-use)
  * The easiest way is to run `docker run -p 8801:8801 -p 7233:7233 -p 8233:8233 -e AUTO_FIX_WORKER_URL=host.docker.internal --add-host host.docker.internal:host-gateway -it superdurable/iwf-server-lite:latest`
* Build and run this project `make bins && ./iwf-samples start`
* Start a workflow: `http://localhost:8803/polling/start?workflowId=test1&pollingCompletionThreshold=100`
  * pollingCompletionThreshold means how many times the workflow will poll before complete the polling task C
* Signal the workflow to complete task A and B:
  * complete task A: `http://localhost:8803/polling/complete?workflowId=test1&channel=taskACompleted`
  * complete task B: `http://localhost:8803/polling/complete?workflowId=test1&channel=taskBCompleted`
  * alternatively you can signal the workflow in WebUI manually 
* Watch in WebUI `http://localhost:8233/namespaces/default/workflows`
* Modify the pollingCompletionThreshold and see how the workflow complete task C automatically


### Screenshots
* The workflow should automatically continue As New after every 100 actions
<img width="773" alt="Screenshot 2024-01-01 at 10 06 11 PM" src="https://github.com/superdurable/iwf-golang-samples/assets/4523955/bca7e02c-f24c-4288-9fc6-1cca74a7c1d3">
* You can use query handler to look at the current data like this
<img width="618" alt="Screenshot 2024-01-01 at 10 08 41 PM" src="https://github.com/superdurable/iwf-golang-samples/assets/4523955/2909b494-5b05-404a-a047-31394eb4b43c">
