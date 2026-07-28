This is the code that is [shown in iWF server as an example of microservice orchestration](https://github.com/superdurable/iwf#example-microservice-orchestration).

## How to test the APIs in browser

* start workflow: http://localhost:8803/microservice/start?workflowId=1234
* swap the data: http://localhost:8803/microservice/swap?workflowId=1234&data=122
* signal the workflow: http://localhost:8803/microservice/signal?workflowId=1234