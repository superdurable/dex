# Dex Java examples

These examples target `io.superdurable:dex-sdk:0.0.3` (`io.superdurable.dex`).

The sample process hosts one gRPC Worker on `127.0.0.1:8803` and an HTTP
controller on port `8080`. One Registry and disk BlobCache are shared by its
Worker and Client.

## Run locally

Start Dex, ensure Temporal search attributes exist, then run the examples:

```bash
dexcli dev --temporal-db-filename /tmp/dex-examples.db

# Required once per Temporal namespace (dexcli usually creates FlowType /
# ActiveStepTypes; CustomKeywordField is used by engagement):
temporal --address 127.0.0.1:7233 operator search-attribute create \
  --name ActiveStepTypes --type KeywordList
temporal --address 127.0.0.1:7233 operator search-attribute create \
  --name CustomKeywordField --type Keyword

./gradlew bootRun
```

Use JDK 17 (Spring Boot 2.7). The Gradle Java toolchain targets 17 when it is
installed. Defaults connect to Dex at `localhost:8801`. Override with:

- `DEX_FLOW_SERVICE_ADDRESS`: Dex gRPC target
- `DEX_WORKER_BIND_ADDRESS`: WorkerService bind address
- `DEX_WORKER_TARGET`: address advertised to Dex when it differs from the bind
  address
- `DEX_BLOB_CACHE_DIR`: shared Client/Worker blob-cache directory

If host port `7233` is already taken (for example by OrbStack), pass alternate
Temporal ports to `dexcli`, for example
`--temporal-port 17233 --temporal-ui-port 18233`, and point `temporal ...`
commands at that address.

When Dex runs in Docker, set `DEX_WORKER_TARGET=host.docker.internal:8803`.

## Try each example

With `bootRun` up:

```bash
# Money transfer saga
curl 'http://127.0.0.1:8080/moneytransfer/start?fromAccount=a&toAccount=b&amount=42&notes=hi'

# Microservice orchestration
curl 'http://127.0.0.1:8080/microservice/start?workflowId=ms-1'
curl 'http://127.0.0.1:8080/microservice/swap?workflowId=ms-1&data=new'
curl 'http://127.0.0.1:8080/microservice/signal?workflowId=ms-1'

# Engagement
curl 'http://127.0.0.1:8080/engagement/start'
curl 'http://127.0.0.1:8080/engagement/describe?workflowId=<id>'
curl 'http://127.0.0.1:8080/engagement/decline?workflowId=<id>&notes=no'
curl 'http://127.0.0.1:8080/engagement/accept?workflowId=<id>&notes=yes'
curl 'http://127.0.0.1:8080/engagement/optout?workflowId=<id>'

# Subscription
curl 'http://127.0.0.1:8080/subscription/start'
curl 'http://127.0.0.1:8080/subscription/describe?workflowId=<id>'
curl 'http://127.0.0.1:8080/subscription/updateChargeAmount?workflowId=<id>&newChargeAmount=250'
curl 'http://127.0.0.1:8080/subscription/cancel?workflowId=<id>'

# Polling
curl 'http://127.0.0.1:8080/polling/start?workflowId=poll-1&pollingCompletionThreshold=3'
curl 'http://127.0.0.1:8080/polling/complete?workflowId=poll-1&channel=task-a-completed'
curl 'http://127.0.0.1:8080/polling/complete?workflowId=poll-1&channel=task-b-completed'
```

## Examples

- [Money transfer saga](./src/main/java/io/superdurable/dex/workflow/money/transfer)
- [Microservice orchestration](./src/main/java/io/superdurable/dex/workflow/microservices)
- [Employer/job-seeker engagement](./src/main/java/io/superdurable/dex/workflow/engagement)
- [Subscription](./src/main/java/io/superdurable/dex/workflow/subscription)
- [Polling and channel coordination](./src/main/java/io/superdurable/dex/workflow/polling)
