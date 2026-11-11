---
section: Added
---
- `@superdurable/dex-agents-pi`: **createPiAiModelClient** sends the images of
  built-in loop user text and tool result items as pi-ai image content, with
  the base64 of their bytes from **ModelRequest.imageBytes**, after the item's
  text. A request without an image's bytes throws a **TypeError** before pi-ai
  sends it. A model whose `input` lacks `"image"` gets pi-ai's placeholder
  text instead. New **piAiEstimatedImageInputTokens** (1600), which
  `estimateInputTokens` counts per image. Pi tool images still become
  `[image omitted: <mime type>]`. See [typescript/pi](typescript/pi/README.md).
