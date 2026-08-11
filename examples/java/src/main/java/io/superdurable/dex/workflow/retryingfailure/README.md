# Retrying Worker failure

This Flow intentionally throws an `IllegalStateException` from its execute method and retries every
10 minutes. Use it to inspect an active Step's latest Worker error type, detail, gRPC status, and
Java stack trace in Dex Web.

The Step uses synchronous durability so its retry is represented as a pending backend activity.
The Flow allows up to 100 attempts and the controller starts it with a 24-hour timeout.

With `dexcli dev` and the Java examples application running:

```text
http://localhost:8080/retrying-failure/start?workflowId=java-retrying-failure-1
```

Then open the Flow in Dex Web:

```text
http://localhost:8802/flows/java-retrying-failure-1
```

Stop the demonstration when finished:

```text
http://localhost:8080/retrying-failure/stop?workflowId=java-retrying-failure-1
```
