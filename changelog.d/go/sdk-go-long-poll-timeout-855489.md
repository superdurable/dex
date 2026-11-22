---
section: Fixed
---
- **Client.ReadStream** and **Client.WaitForFlow** return **LongPollTimeoutError**, not **context.DeadlineExceeded**, when the Server's long-poll cap expires more than a second before the context deadline. Call again to keep waiting. [API reference](pathname:///api/go/latest/dex/#type-client).
- **Client.WaitForStepCompletion** and **Client.WaitForAttributeMatch** keep waiting past the Server's long-poll cap. A context deadline or **RequestTimeout** longer than the cap ended them at the cap with **context.DeadlineExceeded** or **RequestTimeoutError**.
