---
section: Added
---
- The new Go module **github.com/superdurable/dex/agents/go/eino** runs CloudWeGo Eino ADK agents on the Go session Flow. Each model call is one RunModel Step, each tool call runs in its own Step with the agent's tool middlewares, and a deploy that changes the instruction or renames a tool takes effect at the next model call without breaking the open turn.
