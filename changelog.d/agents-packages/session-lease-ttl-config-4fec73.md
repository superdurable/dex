---
section: Changed
---
- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`: a session's
  terminal lease lasts 600 seconds by default instead of 15, so a pause or a
  lost connection of a few minutes no longer ends a chat. The configuration
  file's new optional `sessions.lease_ttl_seconds`, an integer from 10 to
  3600, sets it; a value outside that range fails the schema at
  `/sessions/lease_ttl_seconds`, and the command exits with code 2.
  `DEX_AGENTS_SESSION_LEASE_TTL_SECONDS`, from 1 to 3600, still overrides it,
  and the command ignores any other value of the variable. The lease rules are
  unchanged: the command renews every third of the TTL, a clean exit releases
  the lease at once, and a waiting command still asks every second. A killed
  or paused command now holds its session for up to 10 minutes by default.
  Each lease read of an attempt waits at most 5 seconds, also while a pull
  permit is active, so an unanswered read never holds a waiting command for
  the rest of the longer permit.
  New: `SessionsConfig.lease_ttl_seconds`,
  `CommandEnvironment.has_session_lease_ttl_override` and
  `resolve_session_lease_ttl_seconds`, `defaultSessionLeaseTtlSeconds` in the
  TypeScript package index, `readSessionLeaseTtlOverrideSeconds`,
  `resolveSessionLeaseTtlSeconds`, and `OpenedSessionWorker.leaseTtlSeconds`.
  `CliEnvironment.sessionLeaseTtlSeconds` is now set only by a valid variable.
