# Manual Intervention Flow

Handles an API call that may fail in a way that needs a human to decide whether
to retry or skip. Once the underlying issue is resolved, an operator sends a
retry or skip signal and the flow continues.

## Steps

1. `Init` — sets `number_of_retries` to `0` and moves to `GetData` with
   `False`.
2. `GetData` — waits for a value on the internal channel, incrementing the retry
   counter when it was reached from `Error`. It simulates an API call: the value
   `"failed"` sends the flow to `Error`, anything else sends it to `Final`.
3. `Error` — waits for either the retry or the skip signal, then goes back to
   `GetData` with `True` (retry) or forward to `Final` (skip).
4. `Final` — gracefully completes the flow, returning the number of retries.

## Channels

- `internal_channel_command` (`str`) — carries the data that stands in for the
  API call result.
- `signal_channel_command_retry` — retry the call.
- `signal_channel_command_skip` — skip the call.

## Persistence

- `number_of_retries` — an `int` attribute counting retry attempts.

## Usage

Start the flow, then publish to `internal_channel_command`. Send `"failed"` to
drive the flow into `Error`, or any other value to let it succeed. From `Error`,
publish to `signal_channel_command_retry` or `signal_channel_command_skip` to
resume.
