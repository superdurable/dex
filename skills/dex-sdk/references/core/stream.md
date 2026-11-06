# Stream

A Stream carries low-latency, best-effort, resumable messages, such as progress or LLM tokens shown in a UI. It is not durable. Use a Channel or an Attribute when delivery must be durable.

## Guarantees

- A Step write is fire-and-forget. Local encoding or registration can fail at once, but Dex does not acknowledge storage, and a Stream Store failure does not fail the Step.
- A Step write is sent at once and is not rolled back when the Step later fails.
- A retry can repeat messages already sent and does not restore unsent buffered text.
- The Server keeps a bounded tail of each Stream and trims older messages. A slow reader can miss trimmed messages.
- A Server restart or failover can empty Streams, depending on the Stream Store.
- Stream capacity is shared by all Flows with the same Flow type and Stream name, not per Flow ID.
- An operator can disable the Stream Store. Client Stream calls then fail with FAILED_PRECONDITION, and Step writes are dropped without failing the Step.

Consumers rebuild canonical state from Attributes, a read-only RPC, or the Flow result after a loss, refresh, or reconnect. Stream data is never the record of business completion.

## Write

- A Step may write any number of messages to one or more Streams before its result. Each write is an implicit heartbeat.
- RPC handlers cannot write through their Context. They can use an injected Client.
- A Client write needs a nonempty source. It works even when the Flow does not exist or is inactive.
- Step messages carry the Step execution ID as their source. The source is not an idempotency key: attempts share it and every write appends.
- Write semantically complete, independent messages directly. Do not batch events only because they arrive fast; batching changes the message boundaries readers see.
- A Client write that fails with RESOURCE_EXHAUSTED because the node or the Stream is full can be retried after a short delay. A single message larger than the Stream allows never fits; do not retry it.

### LLM tokens: use the buffered text writer

When a Step calls a streaming LLM API, create one buffered text writer for the invocation before the request, and pass its write method as the token or delta callback.

- Never call a direct Stream write for each token or delta.
- The writer flushes on a timer and on a size threshold, and flushes the tail before the result or error.
- In most SDKs the code only calls write; the SDK finalizes. Python synchronous generator Steps must yield an explicit final flush.
- An empty buffer does not heartbeat. Raise the heartbeat timeout for a provider that can stay silent.
- Use separate writers for provider reasoning summaries and visible assistant text. Keep structured tool and lifecycle events on a third Stream.

```text
AnswerStep.execute(context, prompt)
    answerText = AnswerStream.bufferedText(context)
    response = llm.stream(prompt, onDelta = answerText.write)
    messages.append(response.finalMessage)      // durable copy in an Attribute
    return gracefulComplete(response.summary)
```

## Read

| Path | Order | Blocking | Use |
| --- | --- | --- | --- |
| ReadStream | forward, one message at a time | long-polls for the next write | live tail; resume from a token |
| ListStreamMessages | newest first, a page at a time | returns at once | show recent retained history |

ReadStream:

- An empty token, or one older than the retained head, starts at the current head.
- Pass the returned token unchanged to read the next message.
- Resume is best effort.

ListStreamMessages:

- An empty before-page token starts at the retained tail.
- Each nonempty next-page token is an exclusive anchor for older messages, bound to that scope. An empty next-page token marks the end.
- Writes after the first page do not enter the older-page chain.
- If trimming moves the head past the anchor, Dex returns an empty page instead of restarting from the tail.
- Page size must be positive and within the Server's limit.

Stream Store backends, capacities, and limits are in the [Server configuration reference](https://docs.superdurable.io/reference/server/configuration#streams).

## Self-check

- Is every value the user must not lose also committed to an Attribute?
- Does the UI recover from durable state after a refresh or reconnect?
- Are LLM tokens written through one buffered text writer per invocation?
- Does best-effort Stream error handling catch only service errors and keep local defects visible?
