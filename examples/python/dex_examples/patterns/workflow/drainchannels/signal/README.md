# Drain Signal Channels Flow

Keeps a flow alive only while there are signals left to process, and closes as
soon as the channel is drained. A new signal simply starts a new flow execution
under the same flow ID. Many short-lived flows are usually preferable to one
long-lived flow, both for cost and for versioning.

`force_complete_when_channels_empty(...)` performs the empty check atomically
with the close decision, so no signal can be lost in the race between "channel
looks empty" and "flow closes".

**Note:** atomicity only holds when a channel is consumed by a single step.

## Use cases

- Refund requests for the invoice items of one invoice, processed one at a time.
- Rate-limiting requests to a service by draining a queue with fixed bandwidth.

## Steps

`ProcessSignal`:

- On the first execution the input is non-`None`, so the message from the input
  is processed instead of one from the channel.
- Otherwise it waits for a value on `queueSignalChannel` and processes that.
- Then it sleeps 20 seconds, leaving a window for more signals to arrive.
- Finally it either loops (channel non-empty) or force-completes the flow
  (channel empty).

## Channels

- `queueSignalChannel` (`str`) — receives the signals the flow processes.

## Usage

Start the flow with a first message, then publish more values to
`queueSignalChannel` with `client.publish(...)`.
