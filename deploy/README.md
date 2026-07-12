# Deploy Assets

This directory contains deployment-related assets for local validation and
production-style packaging.

## Structure

- `helm/`
  - Helm chart for the dex server. Backend-aware: it injects
    `DEX_POSTGRES_URI` (default) or `DEX_MONGO_URI` based on
    `server.config.persistence.backend`.
- `kind/`
  - Kind-specific cluster config, Postgres + Mongo manifests, and Helm values
    overrides used for local end-to-end validation.
- `scripts/`
  - Helper scripts for building images, deploying Helm releases, running local
    kind validation, and cleaning up validation resources.

## Database backend

The server defaults to the **PostgreSQL** backend
(`server.config.persistence.backend: postgres`). All local kind workflows
default to Postgres; set `DEX_DB_BACKEND=mongo` to validate against MongoDB
instead. The chart injects the matching connection secret
(`dex-postgres` or `dex-mongo`), and the scripts deploy and verify against
the selected backend.

## Most Common Workflows

Build/push and deploy:

```bash
./deploy/scripts/build-and-push-images.sh --tag your-tag
./deploy/scripts/deploy-helm.sh --tag your-tag --namespace your-namespace
```

Local clean-room kind validation:

```bash
./deploy/scripts/e2e-kind-setup-and-test.sh
```

Local clean-room kind validation with Datadog metrics:

```bash
DD_API_KEY=your_datadog_api_key ./deploy/scripts/e2e-kind-setup-and-test.sh
```

Trigger more benchmark runs after the environment is already up:

```bash
./deploy/scripts/trigger-kind-benchmark.sh --mode parallel --runs 10 --num-steps 20 --state-size 4096 --wait
```

Redeploy only the server in an already running local kind environment:

```bash
./deploy/scripts/redeploy-kind-server.sh --tag dev-fix
```

Local cleanup:

```bash
./deploy/scripts/kind-cleanup.sh
```
