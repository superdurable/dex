---
section: Breaking
---
- `dex-agents-pi` (TypeScript): `write`, `edit`, and `bash` no longer wait for
  approval by default, as in Pi. Name a tool in `approval.always` to make it
  wait. `local_tools.shell.allow` has no effect on this command.
- `dex-agents-pi`: the file tools and `bash` no longer go through the CLI
  core's workspace file layer. They use Pi's own local operations and reach
  any path you can, including absolute paths, `~`, links, and hard links.
  `grep` and `find` run `rg` and `fd` and respect `.gitignore`; Pi downloads
  a missing one to `~/.pi/agent/bin` unless `PI_OFFLINE` is set.
- `dex-agents-pi`: a `bash` command without `timeout` has no timeout unless
  `pi.bash_timeout_seconds` sets one; the default was 120 seconds. A `timeout`
  from the model is no longer capped at 600 seconds. A non-zero exit or a
  timeout kill records `failed` with the output instead of a result. A cut
  output names Pi's temporary file, which the command no longer deletes.
- `dex-agents-pi`: without `workspace` in the file, the workspace is the
  directory the command starts in, and `init` writes no `workspace`. A file
  that an earlier `init` wrote keeps its `workspace: ~/dex-agent-workspace`.
  A session started without a file resumes only with that line in the file.
- `@superdurable/dex-agents-pi-cli`: `WorkspaceGrep`,
  `defaultGrepMatchTimeoutMs`, `WorkspaceGrepArguments`, and
  `defaultBashTimeoutSeconds` are removed. `PiCodingToolSource`'s
  `bashTimeoutSeconds` defaults to none, and `PiSection.bashTimeoutSeconds`
  may be `undefined`.
