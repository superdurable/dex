---
section: Added
---
- **@superdurable/dex-agents-pi**: **createPiAiModelClient** sends the images of user messages and tool source results to the model as pi-ai image content when the built-in loop session Flow has a media store. A request without an image's bytes fails the model call attempt before pi-ai sends it. A model without image input gets pi-ai's placeholder text instead. New **piAiEstimatedImageInputTokens**, counted per image by **estimateInputTokens**. Images in Pi tool results still become a text note.
