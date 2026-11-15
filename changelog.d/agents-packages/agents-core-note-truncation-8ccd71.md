---
section: Changed
---
- The Python, TypeScript, and Go cores cut each tool call's result in the
  turn-failure note and in the steered or stopped turn note to 2,000 UTF-8
  bytes, on a character boundary, followed by
  `… [output truncated: <omitted> of <total> bytes omitted]`. Every later turn
  resends these notes, so one large tool output no longer grows each prompt.
  The stored tool call records keep the full output. New constants:
  `TOOL_CALL_NOTE_MAX_OUTPUT_BYTES` and `TOOL_CALL_NOTE_TRUNCATION_MARKER`
  (Python), `toolCallNoteMaxOutputBytes` and `toolCallNoteTruncationMarker`
  (TypeScript), and `ToolCallNoteMaxOutputBytes` and
  `ToolCallNoteTruncationMarker` (Go).
