---
section: Added
---
- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`: the
  configuration file's optional `dex.auth` (`none`, `api_key`, or `bearer`)
  and `dex.tls` (`ca_file`, `server_name`) connect the Worker, the Clients,
  the Worker host, and the terminal UI to a remote Dex server that requires
  TLS and a credential. Secrets come only from `DEX_API_KEY`,
  `DEX_CREDENTIAL`, or a token file. `DEX_TOKEN_URL`, `DEX_SERVER_ID`,
  `DEX_CREDENTIAL_FILE`, `DEX_TLS_CA_FILE`, and `DEX_TLS_SERVER_NAME`
  override the matching fields. The file's `dex.auth` alone turns
  authentication on.
- New configuration error codes: `insecure_token_url`,
  `auth_needs_server_url`, and `tls_needs_https_server`.
  `EffectiveConfig.dex` carries `auth` and `tls`. In TypeScript both are
  optional, so code that builds an `EffectiveConfig` itself keeps compiling.
  `resolve_dex_connection_settings` (`resolveDexConnectionSettings`) returns
  the connection settings without secrets. The `dex_connection` cases in
  `agents/conformance/cli_config.json` fix the rules.
- Terminal UI contract 1: the launch document's `dex.auth` and `dex.tls`
  carry the command's settings instead of `none` and the defaults. The
  terminal UI gets the one credential variable that `dex.auth` names.
