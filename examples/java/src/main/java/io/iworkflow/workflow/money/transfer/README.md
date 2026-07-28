### How to run
* start a iWF server following the [instructions](https://github.com/superdurable/iwf#how-to-use)
* run this project by using gradle task `bootRun`.
* start a workflow: `http://localhost:8803/moneytransfer/start?fromAccount=test1&toAccount=test2&amount=100&notes=hello`
* watch in WebUI `http://localhost:8233/namespaces/default/workflows`
* modify the workflow code to try injecting some errors, and shorten the retry, to see what will happen
