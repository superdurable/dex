# Large Step Graph demo

This demo creates exactly 90 execute-only Step executions: 30 serial Steps, six
parallel branches with five Steps each, then 30 serial Steps. It uses no
`WaitFor` conditions or Channels, and sets the continue-as-new threshold to 100.

Start Dex and Web, then run the demo from `web/`:

```bash
dexcli dev
GOWORK=off go run ./demo/large-step-graph
```

The command waits for completion and prints the Flow ID, Run ID, status, and a
direct Web URL. Set `DEX_FLOW_SERVICE_ADDRESS` or `DEX_WEB_URL` to override the
default local addresses.
