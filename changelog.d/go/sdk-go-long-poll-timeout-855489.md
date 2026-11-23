---
section: Fixed
---
- **Client.ReadStream** and **Client.WaitForFlow** return **LongPollTimeoutError**, not **context.DeadlineExceeded**, when the Server's long-poll cap expires more than a second before the context deadline. Call again to keep waiting. [API reference](pathname:///api/go/latest/dex/#type-client).
- **Client.WaitForStepCompletion**, **Client.WaitForAttributeMatch**, and **Client.WaitForAttributeMapInstanceMatch** no longer end at the Server's long-poll cap with **context.DeadlineExceeded** or **RequestTimeoutError** while the context deadline or **RequestTimeout** is more than a second away.
