# Deploy DEX to GCP GKE

Step-by-step guide for deploying the dex server and benchmark worker to a
GKE cluster.

The server defaults to the **PostgreSQL** backend
(`DEX_DB_BACKEND=postgres`); set `DEX_DB_BACKEND=mongo` to deploy against
MongoDB Atlas instead. Throughout this guide, Postgres is the default path
and Mongo/Atlas is shown as the alternative. For Postgres on GCP, Cloud SQL
for PostgreSQL (or any reachable Postgres) works; the connection string must
NOT pin a database name — the server derives the six per-store databases
(`dex_runs`, `dex_history`, `dex_blobs`, `dex_visibility`, `dex_tasklists`,
`dex_shards`) itself.

## Prerequisites

### Local tools

- `gcloud` CLI installed and authenticated (`gcloud auth login`)
- `kubectl` installed (comes with `gcloud components install kubectl`)
- `helm` v3 installed
- `docker` available for building images

### GCP

- A GCP project with billing enabled
- A GKE cluster created and running
- `kubectl` context set to the target GKE cluster
  (`gcloud container clusters get-credentials CLUSTER_NAME --region REGION`)

### MongoDB Atlas

- An Atlas sharded cluster provisioned
- A database user created (Username and Password auth)
- Network access configured so GKE pods can reach Atlas:
  - Option A: Atlas Private Endpoint / Private Service Connect (recommended)
  - Option B: IP Access List with your GKE cluster's Cloud NAT static egress IPs
- The connection string ready (Atlas console -> Connect -> Drivers)

## 1. Set environment variables

Fill in your actual values:

```bash
export NAMESPACE=dex-prod

export GCP_PROJECT=your-gcp-project
export GCP_REGION=us-central1
export AR_REPO=your-artifact-registry-repo

export SERVER_IMAGE_REPOSITORY="${GCP_REGION}-docker.pkg.dev/${GCP_PROJECT}/${AR_REPO}/dex-server"
export BENCHMARK_IMAGE_REPOSITORY="${GCP_REGION}-docker.pkg.dev/${GCP_PROJECT}/${AR_REPO}/dex-benchmark-worker"

# Image tags are separate so you can update server without rebuilding benchmark
export SERVER_IMAGE_TAG=v$(date -u +%Y%m%d-%H%M%S)
export BENCHMARK_IMAGE_TAG=v$(date -u +%Y%m%d-%H%M%S)

# Persistence backend: postgres (default) or mongo
export DEX_DB_BACKEND=postgres

# PostgreSQL DSN (default backend). No dbname — the server derives per-store
# databases. Use your Cloud SQL / Postgres host.
export DEX_POSTGRES_URI='postgres://<user>:<password>@<host>:5432/?sslmode=require'

# MongoDB Atlas connection string (only when DEX_DB_BACKEND=mongo;
# from Atlas console -> Connect -> Drivers)
export DEX_MONGO_URI='mongodb+srv://<user>:<password>@<cluster>.mongodb.net/dex?retryWrites=true&w=majority'

# Datadog (optional, leave empty to use Prometheus instead)
export DD_API_KEY='your_datadog_api_key'
export DD_ENDPOINT='api.datadoghq.com'
```

## 2. Create Artifact Registry repository (one-time)

```bash
gcloud artifacts repositories create "${AR_REPO}" \
  --repository-format=docker \
  --location="${GCP_REGION}" \
  --project="${GCP_PROJECT}"
```

If the repository already exists, this command will return an error you can
safely ignore.

## 3. Authenticate Docker to Artifact Registry

```bash
gcloud auth configure-docker "${GCP_REGION}-docker.pkg.dev"
```

## 4. Build and push images

GKE nodes run `linux/amd64`. If you are building on Apple Silicon (M1/M2/M3),
you must pass `--platform linux/amd64` or the binary will fail with
`exec format error` at runtime.

```bash
./deploy/scripts/build-and-push-images.sh --server-only --tag "${SERVER_IMAGE_TAG}" --platform linux/amd64
./deploy/scripts/build-and-push-images.sh --benchmark-only --tag "${BENCHMARK_IMAGE_TAG}" --platform linux/amd64
```

## 5. Initialize the database schema (one-time)

The server defaults to the **PostgreSQL** backend (`DEX_DB_BACKEND=postgres`);
set `DEX_DB_BACKEND=mongo` to deploy against MongoDB instead. Initialize the
schema for whichever backend you run. Both paths are idempotent.

### PostgreSQL (default)

Creates the six per-store databases (`dex_runs`, `dex_history`, `dex_blobs`,
`dex_visibility`, `dex_tasklists`, `dex_shards`) plus their tables and indexes.

```bash
# Full init: creates the databases (needs a role with CREATEDB) then applies
# the DDL to each. Connection comes from libpq env vars.
PGHOST=<host> PGPORT=5432 PGUSER=<user> PGPASSWORD=<pw> \
  bash server/internal/persistence/postgres/schema/init.sh

# Or, if the six databases already exist, apply the DDL directly (v0.sql
# \connect-s into each database, so they must already be present):
psql "${DEX_POSTGRES_URI}" -f server/internal/persistence/postgres/schema/v0.sql
```

### MongoDB

Creates collections, indexes, and the hashed-sharding configuration for every
collection. Because all sharded collections use **hashed shard keys**, MongoDB
pre-allocates and distributes chunks across all Atlas shards automatically —
no manual `splitAt` / `moveChunk` is required.

```bash
mongosh "${DEX_MONGO_URI}" server/internal/persistence/mongo/schema/v0.js
```

If you do not have `mongosh` installed locally, use Docker:

```bash
docker run --rm -v "$(pwd)/server/internal/persistence/mongo/schema:/schema" mongo:7.0 \
  mongosh "${DEX_MONGO_URI}" /schema/v0.js
```

## 6. Create namespace and secrets

```bash
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
```

### PostgreSQL URI (default backend)

```bash
kubectl create secret generic dex-postgres \
  --from-literal=uri="${DEX_POSTGRES_URI}" \
  -n "${NAMESPACE}" \
  --dry-run=client -o yaml | kubectl apply -f -
```

### MongoDB Atlas URI (only when DEX_DB_BACKEND=mongo)

```bash
kubectl create secret generic dex-atlas \
  --from-literal=uri="${DEX_MONGO_URI}" \
  -n "${NAMESPACE}" \
  --dry-run=client -o yaml | kubectl apply -f -
```

### Datadog API key (optional)

```bash
kubectl create secret generic dex-datadog \
  --from-literal=api-key="${DD_API_KEY}" \
  -n "${NAMESPACE}" \
  --dry-run=client -o yaml | kubectl apply -f -
```

## 7. Deploy the server

### PostgreSQL backend (default, Prometheus metrics)

The chart defaults to Postgres and the `dex-postgres` secret created above.

```bash
helm upgrade --install dex deploy/helm/dex \
  -n "${NAMESPACE}" \
  --create-namespace \
  --set "image.repository=${SERVER_IMAGE_REPOSITORY}" \
  --set "image.tag=${SERVER_IMAGE_TAG}"
```

### MongoDB Atlas backend (Prometheus metrics)

`values-gke-atlas.yaml` sets `persistence.backend: mongo` and points at the
`dex-atlas` secret.

```bash
helm upgrade --install dex deploy/helm/dex \
  -n "${NAMESPACE}" \
  --create-namespace \
  -f deploy/helm/dex/values-gke-atlas.yaml \
  --set "image.repository=${SERVER_IMAGE_REPOSITORY}" \
  --set "image.tag=${SERVER_IMAGE_TAG}"
```

### Using Datadog

```bash
helm upgrade --install dex deploy/helm/dex \
  -n "${NAMESPACE}" \
  --create-namespace \
  -f deploy/helm/dex/values-gke-atlas.yaml \
  --set "image.repository=${SERVER_IMAGE_REPOSITORY}" \
  --set "image.tag=${SERVER_IMAGE_TAG}" \
  --set "secrets.datadog.existingSecret=dex-datadog" \
  --set "server.config.metrics.provider=datadog" \
  --set "server.config.metrics.maxEmittingTier=4" \
  --set "server.config.metrics.datadog.apiKey=\${DD_API_KEY}" \
  --set "server.config.metrics.datadog.endpoint=${DD_ENDPOINT}"
```

To change the number of server pods (default 3), add:

```bash
  --set "replicaCount=5"
```

To change the number of shards (default 256), add:

```bash
  --set "server.config.shard.maxShards=128" \
  --set "server.config.shard.defaultShardsForNewNamespaces=128"
```

`maxShards` can be increased but must never be decreased.

### Verify server is ready

```bash
kubectl rollout status statefulset/dex -n "${NAMESPACE}" --timeout=180s
kubectl get pods -n "${NAMESPACE}" -l app.kubernetes.io/name=dex
```

## 8. Deploy the benchmark worker

```bash
helm upgrade --install dex-benchmark benchmark/helm/dex-benchmark \
  -n "${NAMESPACE}" \
  -f benchmark/helm/dex-benchmark/values.yaml \
  --set "image.repository=${BENCHMARK_IMAGE_REPOSITORY}" \
  --set "image.tag=${BENCHMARK_IMAGE_TAG}"
```

To change the number of benchmark worker pods (default 1), add:

```bash
  --set "replicaCount=2"
```

The benchmark worker connects to the server via in-cluster DNS:

- run service: `dex:7233`
- matching service: `dex:7234`

### Verify benchmark worker is ready

```bash
kubectl rollout status deployment/dex-benchmark -n "${NAMESPACE}" --timeout=120s
```

## 9. Trigger benchmark runs

### Option A: Port-forward (no external IP needed)

Terminal 1:

```bash
kubectl port-forward deployment/dex-benchmark 9123:9123 -n "${NAMESPACE}"
```

Terminal 2:

```bash
# Sequential benchmark
curl "http://127.0.0.1:9123/trigger?mode=sequential&runs=1&numSteps=2&stateSize=16"

# Parallel benchmark
curl "http://127.0.0.1:9123/trigger?mode=parallel&runs=1&numSteps=4&stateSize=16"

# Larger load test
curl "http://127.0.0.1:9123/trigger?mode=parallel&runs=10&numSteps=20&stateSize=4096"
```

### Option B: LoadBalancer (access from Mac without port-forward)

```bash
helm upgrade --install dex-benchmark benchmark/helm/dex-benchmark \
  -n "${NAMESPACE}" \
  -f benchmark/helm/dex-benchmark/values.yaml \
  -f benchmark/helm/dex-benchmark/values-loadbalancer.yaml \
  --set "image.repository=${BENCHMARK_IMAGE_REPOSITORY}" \
  --set "image.tag=${BENCHMARK_IMAGE_TAG}"
```

Get the external IP:

```bash
kubectl get svc dex-benchmark -n "${NAMESPACE}"
```

Then curl it directly:

```bash
curl "http://<EXTERNAL-IP>:9123/trigger?mode=parallel&runs=10&numSteps=20&stateSize=4096"
```

## 10. Verify runs completed

### Benchmark worker logs

```bash
kubectl logs deployment/dex-benchmark -n "${NAMESPACE}" --tail=300 -f
```

You should see:

- `received PollResponse`
- `executing step`
- `step completed`
- `Benchmark run completed`

### Server logs

```bash
kubectl logs statefulset/dex -n "${NAMESPACE}" --tail=300 -f
```

You should see:

- `Loaded config`
- `Run status transitioned`
- `Sync matched run ...`
- `Run ... assigned to worker via PollForRun ...`

### Dump all logs for debugging

To dump logs from all server and benchmark pods into local files:

```bash
./deploy/scripts/dump-logs.sh --namespace "${NAMESPACE}"
```

This writes one file per pod to `/tmp/dex-logs/` and prints a summary
showing line counts, error counts, and panic counts per pod.

Options:

```bash
./deploy/scripts/dump-logs.sh --namespace "${NAMESPACE}" --output-dir /tmp/my-debug --tail 20000
```

### Metrics endpoint (Prometheus mode)

```bash
kubectl port-forward svc/dex 9090:9090 -n "${NAMESPACE}"
curl http://127.0.0.1:9090/metrics | grep dex_run
```

## 11. Update server code and redeploy

After making server code changes:

```bash
export SERVER_IMAGE_TAG=v$(date -u +%Y%m%d-%H%M%S)
./deploy/scripts/build-and-push-images.sh --server-only --tag "${SERVER_IMAGE_TAG}" --platform linux/amd64
./deploy/scripts/deploy-helm.sh --server-only --tag "${SERVER_IMAGE_TAG}" --namespace "${NAMESPACE}"
```

Or as one command:

```bash
export SERVER_IMAGE_TAG=v$(date -u +%Y%m%d-%H%M%S)
./deploy/scripts/release.sh --server-only --tag "${SERVER_IMAGE_TAG}" --namespace "${NAMESPACE}"
```

After a server rollout, restart the benchmark worker to pick up fresh gRPC
connections:

```bash
kubectl rollout restart deployment/dex-benchmark -n "${NAMESPACE}"
```

## 12. Inspect run statuses

### PostgreSQL (default)

Status codes: 0 Pending, 1 WaitingForWorker, 2 Running, 3 AllStepsWaiting,
4 Completed, 5 Failed.

```bash
# Summary by status
psql "${DEX_POSTGRES_URI}/dex_runs" -c \
  "SELECT status, count(*) FROM runs GROUP BY status ORDER BY status;"

# Most recent non-terminal runs
psql "${DEX_POSTGRES_URI}/dex_runs" -c \
  "SELECT id, status, updated_at FROM runs WHERE status NOT IN (4) ORDER BY updated_at DESC LIMIT 10;"
```

### MongoDB

To see a summary of run statuses and find stuck/abnormal runs (the
`inspect-runs.js` / `dlq-ops.js` helper scripts are Mongo-only):

```bash
mongosh "${DEX_MONGO_URI}" deploy/gcp-gke/inspect-runs.js
```

Or via Docker:

```bash
docker run --rm -v "$(pwd)/deploy/gcp-gke:/scripts" mongo:7.0 \
  mongosh "${DEX_MONGO_URI}" /scripts/inspect-runs.js
```

This prints an aggregate count of runs by status, then lists the 10 most
recent runs that are not Running or Completed (e.g. Pending,
WaitingForWorker, Failed).

## 13. Inspect and replay dead letter queue (DLQ)

Failed tasks that exhaust retries are written to the `task_dlq` collection
with full diagnostic context (run ID, tasklist, error, shard, timestamps).
Use `dlq-ops.js` to inspect, replay, or purge entries.

### Inspect DLQ (summary + recent entries)

```bash
mongosh "${DEX_MONGO_URI}" deploy/gcp-gke/dlq-ops.js
```

### Replay DLQ entries (re-enqueue as dispatch tasks)

```bash
# Replay all entries
DEX_DLQ_ACTION=replay mongosh "${DEX_MONGO_URI}" deploy/gcp-gke/dlq-ops.js

# Replay entries for a specific shard
DEX_DLQ_ACTION=replay DEX_DLQ_SHARD=42 mongosh "${DEX_MONGO_URI}" deploy/gcp-gke/dlq-ops.js

# Limit number of entries replayed
DEX_DLQ_ACTION=replay DEX_DLQ_LIMIT=100 mongosh "${DEX_MONGO_URI}" deploy/gcp-gke/dlq-ops.js
```

### Purge old DLQ entries

```bash
# Purge entries older than 24 hours (default)
DEX_DLQ_ACTION=purge mongosh "${DEX_MONGO_URI}" deploy/gcp-gke/dlq-ops.js

# Purge entries older than 48 hours
DEX_DLQ_ACTION=purge DEX_DLQ_HOURS=48 mongosh "${DEX_MONGO_URI}" deploy/gcp-gke/dlq-ops.js
```

## 15. Reset data for fresh benchmarks


To delete all run data and start benchmarks from scratch without redeploying.
Both scripts truncate every table/collection but preserve the schema, so you
do NOT need to re-run the init step afterwards.

**PostgreSQL (default):**

```bash
psql "${DEX_POSTGRES_URI}" -f server/internal/persistence/postgres/schema/reset-data.sql
```

**MongoDB:**

```bash
mongosh "${DEX_MONGO_URI}" server/internal/persistence/mongo/schema/reset-data.js
```

Or via Docker:

```bash
docker run --rm -v "$(pwd)/server/internal/persistence/mongo/schema:/schema" mongo:7.0 \
  mongosh "${DEX_MONGO_URI}" /schema/reset-data.js
```

This deletes all documents from every collection but preserves indexes and
sharding configuration. You do NOT need to re-run `v0.js` afterwards.

After resetting data, restart the server pods so shard leases start clean:

```bash
kubectl rollout restart statefulset/dex -n "${NAMESPACE}"
kubectl rollout restart deployment/dex-benchmark -n "${NAMESPACE}"
```

## 16. Cleanup

Uninstall both releases:

```bash
helm uninstall dex-benchmark -n "${NAMESPACE}"
helm uninstall dex -n "${NAMESPACE}"
```

Delete namespace (removes all resources including secrets):

```bash
kubectl delete namespace "${NAMESPACE}"
```

## Quick Reference

| Task | Command |
|------|---------|
| Build and push all images | `./deploy/scripts/build-and-push-images.sh --tag TAG` |
| Deploy server + benchmark | `./deploy/scripts/deploy-helm.sh --tag TAG --namespace NS` |
| Build, push, deploy in one step | `./deploy/scripts/release.sh --tag TAG --namespace NS` |
| Redeploy server only | `./deploy/scripts/release.sh --server-only --tag TAG --namespace NS` |
| Port-forward benchmark | `kubectl port-forward deployment/dex-benchmark 9123:9123 -n NS` |
| Trigger sequential run | `curl "http://127.0.0.1:9123/trigger?mode=sequential&runs=1&numSteps=2&stateSize=16"` |
| Trigger parallel run | `curl "http://127.0.0.1:9123/trigger?mode=parallel&runs=10&numSteps=20&stateSize=4096"` |
| View benchmark logs | `kubectl logs deployment/dex-benchmark -n NS --tail=300 -f` |
| View server logs | `kubectl logs statefulset/dex -n NS --tail=300 -f` |
| Inspect run statuses | `mongosh "${DEX_MONGO_URI}" deploy/gcp-gke/inspect-runs.js` |
| Inspect pending workers | `./deploy/gcp-gke/inspect-pending-workers.sh` |
| Inspect DLQ | `mongosh "${DEX_MONGO_URI}" deploy/gcp-gke/dlq-ops.js` |
| Replay DLQ | `DEX_DLQ_ACTION=replay mongosh "${DEX_MONGO_URI}" deploy/gcp-gke/dlq-ops.js` |
