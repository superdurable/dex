---
section: Fixed
---
- In Python and TypeScript, an `outcome_unknown` call whose tool threw an error, such as a 400 answer, now shows the model that error, cut at 1,024 bytes. A killed Worker keeps the old text, as a time limit usually does. Status and retries stay the same (#2112).
- The new record field `tool_step_threw` (`toolStepThrew`) marks such a call. The Go core keeps the field but shows the old text. [Ref](../docs/reference/agents/failures.mdx#outcome-unknown-text)
