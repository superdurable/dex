### Replay Tests

### Why

Replay tests ensure Temporal workflow [determinism](https://docs.temporal.io/workflows#deterministic-constraints)
is not broken by interpreter changes.

Dex uses Temporal-only replay (not Cadence). See Temporal docs
[here](https://docs.temporal.io/develop/go/testing-suite#replay).

### Global versioning

Dex uses the [global versioning design pattern](https://medium.com/@qlong/how-to-overcome-some-maintenance-challenges-of-temporal-cadence-workflow-versioning-f893815dd18d).

After the gRPC interpreter rewrite, the global-version scheme **restarted at v1**.
Pre-rewrite histories were deleted; do not keep baselines for old global versions.

* For every new global version, add at least one new history under [`history/`](./history).
* Each version may need multiple histories for different code paths.
* Histories use binary protobuf payloads (`binary/protobuf` encoding).

### Capturing a history (gRPC era)

1. Run the Temporal integ scenario that exercises the path (unique flow id).
2. Export from a real Temporal server:

```bash
docker exec temporal-admin-tools temporal workflow show \
  --workflow-id <id> --run-id <rid> --output json \
  > server/replayTests/history/vN-<scenario>.json
```

For continue-as-new chains, export **each** run (CAN1..CANn and the final
`wf-finish` run) with the matching `--run-id`.

3. Add the filename to [`replay_test.go`](./replay_test.go) and run:

```bash
make -C server replayTests 2>&1 | tee /tmp/test-replay.log
```

Usually the source workflow is an integration test under `server/integ/`.
