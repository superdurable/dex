---
section: Changed
---
- A session's terminal lease lasts 600 seconds by default instead of 15, so a
  pause or a lost connection of a few minutes no longer ends a chat. The new optional
  **sessions.lease_ttl_seconds** in the configuration file sets it, from 10 to
  3600 seconds. **DEX_AGENTS_SESSION_LEASE_TTL_SECONDS** still overrides it. A
  command that exits still releases the lease at once, and a waiting command
  takes the session within about a second. A killed or paused command holds
  its session until its lease expires, up to 10 minutes by default. A failed
  renewal is tried again within 5 seconds.
  See [One terminal per session](/reference/agents/cli#one-terminal-per-session).
