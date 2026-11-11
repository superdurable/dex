---
section: Images
---
- Built-in agent loop, Python and TypeScript: function tools return images.
  A function tool returns the new **FunctionToolResult** with `output` and
  `images` (`functionToolResult({ output, images })` in TypeScript, which
  marks the object with `kind: "function_tool_result"`), and the new
  **ToolCallContext.media_store** (`mediaStore`) gives it the runner's media
  store, or none when the runner has none, to upload the bytes with
  `put_image` (`putImage`). The runner returns the images in
  **ToolExecutionResult.images**, so the session checks, stores, and shows
  them exactly like a tool source's images. Images from a runner without a
  media store fail the call once, without a retry, and the model sees the
  error. Strings and other JSON results are
  unchanged. See the core README, "Images from function tools".
