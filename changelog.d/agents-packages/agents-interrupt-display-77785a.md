---
section: Added
---
- Python and TypeScript cores: a tool approval can say what the call acts on
  and what it does. New `AgentInterruptDisplay` `{target, summary, preview}`:
  a tool source returns it in `DeferredToolCall.approval_display`
  (`approvalDisplay`), and the session stores it in the new optional
  `AgentInterrupt.display`, on the call's record as `approval_display`
  (`approvalDisplay`), and in the `interrupt_raised` event's new `display`
  key. A queued ordered write raises its approval later with the display its
  source built. The session cuts a target over `MAX_APPROVAL_TARGET_BYTES`
  (`maxApprovalTargetBytes`, 1 KiB) and a summary over
  `MAX_APPROVAL_SUMMARY_BYTES` (`maxApprovalSummaryBytes`, 2 KiB), ending
  them with `…`, and drops a preview over `MAX_APPROVAL_PREVIEW_BYTES`
  (`maxApprovalPreviewBytes`, 16 KiB). Each new field is `null` when there is
  none, so a reader treats a missing display as "show the arguments"; a
  Worker before 0.6.0 ignores the fields and drops them when it rewrites a
  record or `PendingInterrupts`. The Go core decodes and keeps both fields.
- Python and TypeScript cores: `ConnectorToolSource` sets the display of a call
  that needs approval, with the new `build_connector_tool_approval_display`
  (`buildConnectorToolApprovalDisplay`): the target is
  `<connector>/<connection>`, and the summary names the operation, the
  connection, and up to five short scalar arguments, never the operation's
  description. A credential-like argument, such as `credentials`, `apiKey`, or
  `PGPASSWORD`, shows `<redacted>`, and string values have their inline
  credentials replaced. The new `is_credential_like_name`
  (`isCredentialLikeName`) and `redact_inline_credentials`
  (`redactInlineCredentials`) hold the rules, and
  `agents/conformance/approval_display.json` fixes them for every core.
- Python and TypeScript CLI cores: a `write_file` or `edit_file` approval names
  the workspace path and carries a unified diff of the file as it is when the
  model asks against the new content, with header lines that say so and
  `\ No newline at end of file` markers. The preview reads the file with the
  write's own link and workspace checks. It is left out for files or content
  over 64 KiB, diffs over 200 lines, paths the write would refuse, content
  with an inline credential, and credential files such as `.env`,
  `.aws/credentials`, `.kube/config`, or `secrets.yaml`. A
  `run_shell_command` approval names the working directory and the command
  with its inline credentials redacted; the call's arguments keep the full
  command. A connector tool's approval names its operation, connection, and
  short arguments.
