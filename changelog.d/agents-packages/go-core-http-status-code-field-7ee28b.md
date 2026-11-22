---
section: Go core
---
- **RunModel** also reads a model API error's HTTP status from an
  `HTTPStatusCode` int field in the error chain, as the errors of go-openai
  and the Volcengine Ark SDK have. CloudWeGo eino-ext's OpenAI-family and Ark
  model clients return these errors, so a 429 or 5xx from them now gets the
  transient-error backoff, and a 400 or 401 fails the turn at once, instead
  of both getting three quick attempts. These errors carry no headers, so
  **Retry-After** does not apply to them. A field named `Code` is still never
  read as a status.
