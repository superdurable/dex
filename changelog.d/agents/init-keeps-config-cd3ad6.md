---
section: Fixed
---
- Running **init** again on a valid configuration file replaces only the
  fields it asks about: **model**, **system_prompt**, **connectors.enabled**,
  **workspace**, and the **local_tools** switches. It keeps **dex.server**,
  **dex.auth**, **dex.tls**, **connectors.mcp**, **mcp_servers**,
  **approval**, **media**, **sessions**, the harness sections, and every other
  field, so a command set up for a remote Dex server no longer falls back to a
  local **dexcli dev**. At a terminal, each question offers the file's value
  as its default, and **--yes** takes those same defaults. **init** replaces a file that is not YAML or fails the
  configuration rules, copies it to **HARNESS.yaml.bak** first, and says so.
  Comments in the old file are not kept. **init** writes through a temporary
  file and a rename, so a crash never leaves a partial configuration file.
