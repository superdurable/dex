# Benchmark Worker

This package contains the benchmark worker application and its deployment
artifacts.

## Contents

- `cmd/benchmarkworker/`
  - The Go application that:
    - runs an SDK worker against dex matching/run gRPC services
    - exposes an HTTP endpoint for triggering benchmark runs
    - logs benchmark run completion so end-to-end validation can confirm success
- `Dockerfile`
  - Container build for the benchmark worker.
- `helm/dex-benchmark/`
  - Helm chart for deploying the benchmark worker to Kubernetes.

## HTTP Endpoint

The worker exposes:

- `GET /healthz`
- `GET /trigger?mode=sequential|parallel&runs=N&numSteps=M&stateSize=K`
- `GET /publish?runId=...&channel=...&value=...` — publish a string value
  to the named channel on the running run.
- `GET /agentTrigger?maxConcurrentSubAgents=N` — start one
  `mainAgentFlow` run for the multi-agent demo. See
  [AGENT_WORKFLOW.md](./AGENT_WORKFLOW.md).
- `GET /agentHumanMessage?runId=...&kind=start_subagents|start_llm_loop|complete&num=N`
  — publish a typed `mainAgentMessage` to the multi-agent flow's
  `MainAgentMessageCh` channel.
- `GET /agentInterruptLLM?runId=...&reason=...` — publish to the
  multi-agent flow's `InterruptLLMCh` to demo sibling cancellation.

Example:

```bash
curl "http://127.0.0.1:9123/trigger?mode=parallel&runs=10&numSteps=20&stateSize=4096"
```

## Local Development

Run the package tests:

```bash
cd benchmark
go test ./...
```

Build the image:

```bash
docker build -f benchmark/Dockerfile -t dex-benchmark-worker:local .
```

Deploy locally to kind through the canonical validation script:

```bash
./deploy/scripts/e2e-kind-setup-and-test.sh
```
