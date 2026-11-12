---
section: Added
---
- Python and TypeScript cores: a session Flow can keep a summary for session
  lists in three indexed Attributes. Turn it on with
  `AgentSessionOptions.summary_attributes_indexed` (Python) or
  `summaryAttributesIndexed` (TypeScript); it is off by default, and then the
  Flow neither declares nor writes them. `flow.options` reports the setting.
  `AgentSessionTitle` (FULL_TEXT, index key `text1`) is the first user
  message with each run of ASCII whitespace made one space, cut to its first
  80 Unicode code points, and only the first turn writes it.
  `AgentSessionLastActivityAt` (DATETIME, `datetime1`) is the RFC 3339 time
  the first attempt of the Step that started or ended the latest turn
  started. `AgentSessionTurnCount` (INT, `int1`) is `AgentState.turn_count`
  as of that Step. Python exports them as `AGENT_SESSION_TITLE`,
  `AGENT_SESSION_LAST_ACTIVITY_AT`, and `AGENT_SESSION_TURN_COUNT`; the
  TypeScript Flow has `agentSessionTitle`, `agentSessionLastActivityAt`, and
  `agentSessionTurnCount`. The persisted format only gains these Attributes.
- Adapter factories take the same option: Python `summary_attributes_indexed`
  of `builtin_agent_session_flow`, `openai_agents_session_flow`,
  `pydantic_ai_session_flow`, and `adk_agent_session_flow`; TypeScript
  `summaryAttributesIndexed` of `createBuiltinAgentSessionFlow`,
  `createOpenAIAgentsSessionFlow`, `createMastraAgentSessionFlow`,
  `createVercelAiAgentSessionFlow`, and `createClaudeAgentSessionFlow`.
- Before turning the option on: the title is the user's own words, and anyone
  who can search the namespace's Flows reads it. With PostgreSQL search, a
  Worker with the option fails to start with `FAILED_PRECONDITION` when the
  namespace registered `text1`, `datetime1`, or `int1` with another type, or
  when all three keys of one of these types are taken by other names.
- Python and TypeScript cores: `list_agent_sessions` / `listAgentSessions`
  reads one page of a session Flow type's sessions, with an optional title
  search, in one `SearchFlows` call, without snapshots and without a Worker.
  A title search raises `SessionTitleSearchUnavailableError` while the
  namespace has not registered `text1`; another search error stays a
  `DexServiceError`. `sort_agent_sessions_by_last_activity` /
  `sortAgentSessionsByLastActivity` sorts loaded sessions, because Dex sorts
  only by start or close time. `build_session_title` / `buildSessionTitle`
  gives the title rule, which the conformance suite fixes in `names.json`
  `session_titles`.
- A session without the option, or one that an earlier release started,
  lists without the summary and never matches a title search. An earlier
  session gains the last activity time and the turn count at its next turn on
  a Worker with the option. During a rolling upgrade, turns that an earlier
  Worker or a Go Worker runs leave the summary as it was. An earlier session
  with no turn yet gains a title at its first turn. Turning the option off
  stops new writes; the values written before stay. A session that fails
  because its state would exceed the server's limit can keep the values of its
  last turn start.
- Python and TypeScript CLI cores: a new `sessions.summary` setting, `auto`
  by default, decides whether the commands' session Flows keep the summary.
  `auto` keeps it only with `dex.server: auto`, the local `dexcli dev`, and
  leaves a Dex server URL alone; `on` keeps it with any server; `off` never.
  The CLI core passes the result to the harness as
  `HarnessContext.is_session_summary_indexed` /
  `HarnessContext.isSessionSummaryIndexed`; every built-in harness builds its
  Flow with it, and the command refuses to start when a harness's Flow does
  not follow it, in either direction. A custom harness must honor the field.
  The TypeScript `EffectiveConfig` gains an optional `sessions` field, which
  `resolveConfig` always sets. `sessions` loads up to five pages of 50
  sessions, sorts them by last activity, says when more exist, and adds the
  time of the latest turn, the turn count, and the title. The TypeScript line
  keeps its tab-separated ID, status, and start time and appends the three
  columns, with `—` for a missing value.
