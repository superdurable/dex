---
section: Added
---
- **@superdurable/dex-agents-pi**: **createPiAiModelClient** sends the images of user messages and tool results to the model as pi-ai image content when the built-in loop session Flow has a media store. A request without an image's bytes fails the model call attempt before pi-ai sends it. A model without image input gets pi-ai's placeholder text instead. New **piAiEstimatedImageInputTokens**, counted per image by **estimateInputTokens**. With a media store, **piAgentFunctionTool** and **piDurableFunctionTool** upload a Pi tool result's images and show them to the model after its text; without one, or for an image type the store refuses, each image still becomes a text note.
