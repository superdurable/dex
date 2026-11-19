---
section: Fixed
---
- **dex-agents-google-adk**: a turn no longer fails with "Tool ... is not
  registered" when a Worker restarts on a deploy that renamed or removed a
  tool whose call the turn paused on. While **RunModel** resumes the ADK
  invocation, each agent gets an undeclared stand-in for every paused call's
  tool, so ADK accepts the confirmation and the call replays its record. The
  next model call uses the new tool list. A renamed single-attempt call that
  was in flight keeps its **outcome_unknown** record and never runs again.
