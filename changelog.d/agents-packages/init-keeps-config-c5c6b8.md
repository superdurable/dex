---
section: Fixed
---
- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`: `init` on an
  existing, valid configuration file replaces only the fields it asks about
  (`model`, `system_prompt`, `connectors.enabled`, `workspace`, and the
  `local_tools` switches) and keeps every other field, such as `dex.server`,
  `dex.auth`, `dex.tls`, `connectors.mcp`, `mcp_servers`, `approval`, `media`,
  `sessions`, and the harness sections. Before, it wrote the whole file from
  its answers, and the command then used a local `dexcli dev`. The questions
  offer the file's values as defaults, and `--yes` takes those same defaults.
  An invalid file is copied to `NAME.yaml.bak` and
  replaced, with one line that says so. The TypeScript core also asks for the
  shell commands that run without approval, as the Python core does. `init`
  writes the file and the backup through a temporary file and a rename, so a
  crash never leaves a partial file, and it writes through a symbolic link to
  the configuration without replacing the link. The merge rules are in
  `agents/conformance/cli_init.json`.
