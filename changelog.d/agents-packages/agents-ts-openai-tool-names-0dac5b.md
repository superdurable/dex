---
section: Fixed
---
- TypeScript OpenAI Agents adapter and the TypeScript **dex-agents-openai-sdk**:
  the model sees each tool source's tool, such as a Connector tool, by its
  source name, as the Python adapter does. The OpenAI Agents SDK's `tool()`
  used to turn every `-` into `_`, so the model saw
  `hacker_news_daily_list_story_ids` for `hacker-news-daily_list_story_ids`,
  and two tools that differ only by `-` and `_` could not both be offered.
  Only a character the OpenAI API refuses, one other than a letter, digit,
  `_`, or `-`, still reaches the model as `_`.
