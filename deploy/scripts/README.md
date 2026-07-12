# Deploy Scripts

This folder contains operational helper scripts for local validation and manual
release workflows.

## Scripts

### `build-and-push-images.sh`

Builds the dex server image and the benchmark worker image from the repo
root. By default it also pushes them.

Examples:

```bash
./deploy/scripts/build-and-push-images.sh --tag v20260414-1
./deploy/scripts/build-and-push-images.sh --tag local-dev --no-push
./deploy/scripts/build-and-push-images.sh --benchmark-only --tag local-dev
```

### `deploy-helm.sh`

Deploys the server and benchmark Helm charts with a chosen image tag.

Examples:

```bash
./deploy/scripts/deploy-helm.sh --tag v20260414-1 --namespace your-namespace
./deploy/scripts/deploy-helm.sh --server-only --tag v20260414-1 --namespace your-namespace
```

### `release.sh`

Runs build/push followed by Helm deploy using the same tag.

Example:

```bash
./deploy/scripts/release.sh --tag v20260414-1 --namespace your-namespace
```

### `e2e-kind-setup-and-test.sh`

Canonical clean-room local validation script. It:

1. recreates a kind cluster
2. deploys MongoDB replica set
3. builds and loads local images
4. deploys server + benchmark Helm releases
5. triggers one sequential and one parallel benchmark run
6. verifies terminal completion through Mongo state and benchmark logs

Example:

```bash
./deploy/scripts/e2e-kind-setup-and-test.sh
```

Useful overrides:

```bash
KIND_CLUSTER_NAME=my-cluster \
KIND_NAMESPACE=my-namespace \
SEQUENTIAL_STEPS=3 \
PARALLEL_STEPS=8 \
STATE_SIZE=1024 \
./deploy/scripts/e2e-kind-setup-and-test.sh
```

Use Datadog instead of Prometheus during local validation by providing
`DD_API_KEY`:

```bash
DD_API_KEY=your_datadog_api_key \
DD_ENDPOINT=api.datadoghq.com \
./deploy/scripts/e2e-kind-setup-and-test.sh
```

### `trigger-kind-benchmark.sh`

Triggers additional benchmark runs against an already deployed benchmark worker
in the local kind validation namespace.

Examples:

```bash
./deploy/scripts/trigger-kind-benchmark.sh --mode sequential --runs 3 --num-steps 5 --state-size 256
./deploy/scripts/trigger-kind-benchmark.sh --mode parallel --runs 10 --num-steps 20 --state-size 4096 --wait
```

### `redeploy-kind-server.sh`

Builds the local server image, loads it into an existing kind cluster, and
redeploys only the server Helm release.

Examples:

```bash
./deploy/scripts/redeploy-kind-server.sh --tag dev-fix
KIND_CLUSTER_NAME=dex-e2e KIND_NAMESPACE=dex-kind ./deploy/scripts/redeploy-kind-server.sh --tag dev-fix
```

### `kind-cleanup.sh`

Cleans up the local kind validation environment. By default it uninstalls Helm
releases, deletes the validation namespace, and deletes the kind cluster.

Example:

```bash
./deploy/scripts/kind-cleanup.sh
```
