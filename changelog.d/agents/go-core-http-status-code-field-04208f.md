---
section: Added
---
- The Go core reads a model API error's HTTP status from an **HTTPStatusCode** field too, as the go-openai and Volcengine Ark SDK errors that CloudWeGo eino-ext model clients return have, so their transient errors get the backoff and their other 4xx errors fail the turn at once.
