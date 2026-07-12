# GKE Production Deployment

This guide describes the production-oriented deployment flow for dex on
GKE with MongoDB Atlas and the companion benchmark worker.

## Artifacts

- Server image build: `server/Dockerfile`
- Benchmark worker image build: `benchmark/Dockerfile`
- Server Helm chart: `deploy/helm/dex`
- Benchmark Helm chart: `benchmark/helm/dex-benchmark`
- Example server config: `docs/examples/dex-server.production.yaml`
- Example server values: `deploy/helm/dex/values.yaml`
- Example benchmark values: `benchmark/helm/dex-benchmark/values.yaml`

## Build Images

Build from the repository root so the local `protocol-grpc` and `sdk-go`
replacements remain valid during Docker build.

```bash
docker build -f server/Dockerfile -t dex-server:local .
docker build -f benchmark/Dockerfile -t dex-benchmark-worker:local .
```

## Convenience Scripts

To make image refresh + redeploy easier, these helper scripts are available:

- `deploy/scripts/build-and-push-images.sh`
- `deploy/scripts/deploy-helm.sh`
- `deploy/scripts/release.sh`

Common examples:

```bash
# Build and push both images with a specific tag
deploy/scripts/build-and-push-images.sh --tag v20260414-1

# Redeploy both Helm releases with that tag
deploy/scripts/deploy-helm.sh --tag v20260414-1 --namespace your-namespace

# One command: build, push, and redeploy
deploy/scripts/release.sh --tag v20260414-1 --namespace your-namespace
```

Useful environment overrides:

- `SERVER_IMAGE_REPOSITORY`
- `BENCHMARK_IMAGE_REPOSITORY`
- `SERVER_VALUES_FILE`
- `SERVER_EXTRA_VALUES_FILE`
- `BENCHMARK_VALUES_FILE`
- `BENCHMARK_EXTRA_VALUES_FILE`
- `SERVER_RELEASE`
- `BENCHMARK_RELEASE`
- `NAMESPACE`

## Local Kind Validation

For a clean-room local Kubernetes validation, use the canonical kind script:

```bash
./deploy/scripts/e2e-kind-setup-and-test.sh
```

This script will:

1. recreate a local `kind` cluster
2. deploy a single-node MongoDB replica set
3. build and load the dex server and benchmark worker images
4. deploy the server and benchmark worker Helm charts
5. trigger one sequential and one parallel benchmark run
6. verify completion using benchmark logs and Mongo run state

To run the same local validation flow with Datadog metrics enabled:

```bash
DD_API_KEY=your_datadog_api_key \
DD_ENDPOINT=api.datadoghq.com \
./deploy/scripts/e2e-kind-setup-and-test.sh
```

To clean up the local validation environment:

```bash
./deploy/scripts/kind-cleanup.sh
```

To trigger additional runs after the local validation environment is already up:

```bash
./deploy/scripts/trigger-kind-benchmark.sh --mode parallel --runs 10 --num-steps 20 --state-size 4096 --wait
```

## Required Secrets

Create a Kubernetes Secret containing the Atlas URI. The server chart expects a
secret/key pair configured by:

- `secrets.mongo.existingSecret`
- `secrets.mongo.uriKey`

The mounted config file should reference `${DEX_MONGO_URI}` rather than
hardcoding the connection string.

If Datadog export is enabled, provide `DD_API_KEY` through a separate Secret and
set `secrets.datadog.existingSecret`.

If the benchmark endpoint should be protected, create a Secret containing the
token referenced by `auth.existingSecret` in the benchmark chart.

## Server Config Model

The server binary now loads config in this order:

1. built-in defaults
2. optional YAML file (`-config` or `DEX_CONFIG_PATH`)
3. env var overrides

The Helm chart mounts a YAML config file and relies on runtime `${...}`
expansion for pod-specific values such as `${POD_IP}` and `${POD_NAME}`.

Important production fields:

- `persistence.mongo.uri`
- `persistence.mongo.database`
- `shard.maxShards`
- `shard.defaultShardsForNewNamespaces`
- `shard.cluster.discovery`
- `tasklist.cluster.discovery`
- `metrics.provider`
- `metrics.prometheus.listenAddress`

## Install the Server

Review and customize `deploy/helm/dex/values.yaml` or start from
`deploy/helm/dex/values-gke-atlas.yaml`.

```bash
helm upgrade --install dex deploy/helm/dex \
  -n your-namespace \
  --create-namespace \
  -f deploy/helm/dex/values-gke-atlas.yaml
```

The default chart topology is:

- `StatefulSet`
- headless Service for memberlist DNS discovery
- ClusterIP Service for run gRPC, matching gRPC, and metrics

## Install the Benchmark Worker

```bash
helm upgrade --install dex-benchmark benchmark/helm/dex-benchmark \
  -n your-namespace \
  -f benchmark/helm/dex-benchmark/values.yaml
```

Set these values before deploy:

- `server.runServiceAddress`
- `server.matchingServiceAddress`
- `benchmark.namespace`
- `benchmark.taskListName`
- `benchmark.workerRunConcurrency`

## Trigger Benchmarks

### Local port-forward

```bash
kubectl port-forward svc/dex-benchmark 9123:9123 -n your-namespace
curl "http://127.0.0.1:9123/trigger?mode=parallel&runs=10&numSteps=20&stateSize=4096"
```

### Direct access from a Mac without port-forward

Set the benchmark service type to `LoadBalancer`, for example with
`benchmark/helm/dex-benchmark/values-loadbalancer.yaml`.

```bash
helm upgrade --install dex-benchmark benchmark/helm/dex-benchmark \
  -n your-namespace \
  -f benchmark/helm/dex-benchmark/values.yaml \
  -f benchmark/helm/dex-benchmark/values-loadbalancer.yaml
```

Then fetch the external IP:

```bash
kubectl get svc dex-benchmark -n your-namespace
```

If `BENCHMARK_TRIGGER_TOKEN` is configured, include it as the
`X-Benchmark-Token` header when calling `/trigger`.

## Metrics

When `metrics.provider=prometheus`, the server starts an HTTP listener that
serves `/metrics`. The default chart exposes it on the same ClusterIP Service as
the gRPC ports. Typical local inspection:

```bash
kubectl port-forward svc/dex 9090:9090 -n your-namespace
curl http://127.0.0.1:9090/metrics
```

## Rollout Notes

- `maxShards` can be increased but must not be decreased.
- Keep shard and tasklist gossip ports distinct when `serviceMode=all`.
- Prefer `${POD_NAME}` as the effective member ID in Kubernetes.
- Atlas should be provisioned as a sharded cluster before traffic is sent to the
  server.
