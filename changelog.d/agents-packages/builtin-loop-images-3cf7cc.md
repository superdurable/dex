---
section: Images
---
- Built-in agent loop, Python and TypeScript: images reach the model when the
  session Flow has a media store, a new optional **media_store** on
  **builtin_agent_session_flow** and **BuiltinLoopAgentRunner**
  (`mediaStore` in the new third argument of `createBuiltinAgentSessionFlow`,
  **BuiltinAgentSessionFlowOptions**, and the second argument of
  `BuiltinLoopAgentRunner`). With a store, the runner implements
  **ImageUserMessageBuilder**, its stored `user_text` and `tool_result` items
  keep image references in a new `images` list (written only when non-empty,
  so other items are unchanged), and it reads the bytes right before each
  model call into the new **ModelRequest.image_bytes** (`imageBytes`). New
  **UserTextItem.images** and **ToolResultItem.images**
  (`ModelUserTextItem.images`, `ModelToolResultItem.images`). Images of an
  **McpToolSource** built with the same store reach the model too. A store
  read failure fails that `RunModel` attempt, which retries. Context summary
  calls see text only. Without a store the session refuses images, as before,
  and a tool result's references are stored but not shown to the model. See
  the core README, "Images on the built-in loop".
- **LiteLLMModelClient** sends images as base64 `data:` URLs: `input_image`
  parts in Responses API messages and `function_call_output` outputs;
  `image_url` parts in Chat Completions user messages, and in tool messages
  for Anthropic and Bedrock. Other Chat Completions providers, such as
  OpenAI, get the tool result's images in a user message after the run of
  tool messages, with **TOOL_RESULT_IMAGES_TEXT**. Known limitation: Gemini
  models on the Gemini API and Vertex AI get user message images, but not a
  tool result's images, on either API. Their tool result ends with
  **TOOL_RESULT_IMAGES_OMITTED_TEXT** instead.
  `estimate_input_tokens` counts **ESTIMATED_IMAGE_INPUT_TOKENS** per image
  sent.
- **createAiSdkModelClient** sends images as AI SDK `file` parts in user
  messages and in tool result `content` output. `estimateInputTokens` counts
  **estimatedImageInputTokens** per image.
