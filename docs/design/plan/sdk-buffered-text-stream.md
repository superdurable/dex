# SDK buffered text Stream

## Purpose

LLM APIs often return text one token or fragment at a time. Writing every delta as a separate Dex
Stream message adds storage pressure and produces a noisy UI. The buffered text writer is an
opt-in SDK helper. It combines text locally without changing the Stream protocol or the meaning of
direct writes.

The helper supports only String Streams in Step WaitFor and Execute handlers. It concatenates text
exactly. It never inserts whitespace, punctuation, or separators, and it never splits one chunk.
Empty chunks do not start a timer or produce a message.

## Defaults and triggers

The default one-shot flush interval is one second. The default soft threshold is 16 KiB of UTF-8
data. A batch is emitted by the first of these events:

- the one-shot timer expires;
- the soft byte threshold is reached or crossed;
- the application calls flush;
- the invocation finishes.

Crossing the size threshold does not split the final chunk. Every emitted batch is an ordinary
best-effort Stream message and an implicit heartbeat. An empty buffer emits nothing and therefore
does not heartbeat.

## Invocation ownership

Go, Java, Python async, TypeScript, and Rust register each writer with the current invocation in
creation order. When the handler succeeds or fails, the invocation prevents new writes, cancels
the timer, waits for a flush already in progress, emits the nonempty tail, closes the writer, and
only then sends the final result or error.

Timer and user writes share the writer's serialization boundary. Each flush invalidates the prior
timer generation. A canceled callback cannot flush text written for a later generation. Canceling
the Worker invocation stops timers, discards unsent text, and rejects later writes.

Background output failures are latched. The next write or invocation finalization reports the
failure. If the handler also fails, SDKs retain both failures using their native error composition:
Go errors.Join, Java suppressed exceptions, Python ExceptionGroup, TypeScript AggregateError, and
Rust HandlerError metadata with appended finalization failures.

Python synchronous generators are the exception. A Context cannot emit a StepOutput without the
generator yielding it, so its writer uses cooperative elapsed-time checks. Each write or flush
returns zero or one StepOutput values for yield from, and the handler must explicitly flush the
tail before returning.

## Language APIs

- Go: NewBufferedTextStream with BufferedTextStreamFlushInterval and
  BufferedTextStreamMaxBytes options.
- Java: BufferedTextStream.create with the default or explicit Duration and byte threshold.
- Python async: Stream.buffered_text returns a writer with synchronous write and flush methods.
- Python sync: Stream.buffered_text returns a cooperative writer whose outputs are used with yield from.
- TypeScript: Stream.bufferedText accepts optional flushIntervalMs and maxBufferedBytes settings.
- Rust: Stream<String>::buffered_text or buffered_text_with_options returns a cloneable writer.

Multiple writers may exist in one invocation and finalize in creation order. Writers for the same
Stream remain independent, so applications should reuse one writer when batch boundaries matter.

## Delivery semantics

The helper changes only local batching. Each batch uses the normal Step output path and receives
source **#StepExecutionID** from Dex Server. No SDK waits for Stream Store acknowledgement. Retry
does not restore unsent buffers, roll back emitted batches, or deduplicate them.

Use direct Stream writes for complete independent messages. Use the buffered writer for deltas
whose concatenation is the intended user-visible value, such as LLM text output.
