### How to run

* start a iWF server following the [instructions](https://github.com/superdurable/iwf#how-to-use)
* build and run `main.py`
* start a workflow: `http://localhost:8802/moneytransfer/start?fromAccount=test1&toAccount=test2&amount=100&notes=hello`
* watch in WebUI `http://localhost:8233/namespaces/default/workflows`
* modify the workflow code to try injecting some errors, and shorten the retry, to see what will happen
