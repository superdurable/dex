# 90-way fan-out demo

This demo runs one execute-only `Step1`, fans out to 90 execute-only Steps, and
lets every branch request `GracefulComplete`. The resulting Flow contains 91
Step executions, uses no `WaitFor` conditions or Channels, and remains below
the continue-as-new threshold of 100.

Start Dex and Web, then run the demo from `web/`:

```bash
dexcli dev
GOWORK=off go run ./demo/fan-out-90
```

The command waits for completion and prints the Flow ID, Run ID, status, and a
direct Web URL. Set `DEX_FLOW_SERVICE_ADDRESS` or `DEX_WEB_URL` to override the
default local addresses.
