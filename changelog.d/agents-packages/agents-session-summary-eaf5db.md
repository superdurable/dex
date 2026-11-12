---
section: Added
---
- Python and TypeScript cores: every session keeps a summary for session
  lists in three new indexed Attributes. `AgentSessionTitle` (FULL_TEXT,
  index key `text1`) is the first user message with each run of ASCII
  whitespace made one space, cut to 80 code points, and only the first turn
  writes it. `AgentSessionLastActivityAt` (DATETIME, `datetime1`) is the RFC
  3339 time Dex scheduled the Step that started or ended the latest turn.
  `AgentSessionTurnCount` (INT, `int1`) is `AgentState.turn_count` as of that
  Step. Python exports them as `AGENT_SESSION_TITLE`,
  `AGENT_SESSION_LAST_ACTIVITY_AT`, and `AGENT_SESSION_TURN_COUNT`; the
  TypeScript Flow has `agentSessionTitle`, `agentSessionLastActivityAt`, and
  `agentSessionTurnCount`. The persisted format only gains these Attributes.
- Python and TypeScript cores: `list_agent_sessions` / `listAgentSessions`
  reads one page of a session Flow type's sessions, with an optional title
  search, in one `SearchFlows` call, without snapshots and without a Worker.
  `sort_agent_sessions_by_last_activity` /
  `sortAgentSessionsByLastActivity` sorts loaded sessions, because Dex sorts
  only by start or close time. `build_session_title` / `buildSessionTitle`
  gives the title rule, which the conformance suite fixes in
  `names.json` `session_titles`.
- A session that an earlier release started has no title and never matches a
  title search. It gains the last activity time and the turn count at its
  next turn on this release. During a rolling upgrade, turns that an
  earlier Worker or a Go Worker runs leave the summary as it was, and a
  session whose first turn such a Worker ran never gets a title.
- A Worker registers the index keys `text1`, `datetime1`, and `int1` when it
  starts. With PostgreSQL search, a namespace whose three keys of one of
  these types are all taken by other names fails Worker startup with
  `ATTRIBUTE_INDEX_CAPACITY_EXCEEDED`.
- The session Flow Definition Graph lists the three Attributes in
  `v2.indexedAttributes`, so Dex Web can filter runs by them.
- Python and TypeScript CLI cores: `sessions` sorts its page of up to 50
  sessions by last activity and adds the time of the latest turn, the turn
  count, and the title. The TypeScript line keeps its tab-separated ID,
  status, and start time and appends the three columns, with `—` for a
  missing value.
