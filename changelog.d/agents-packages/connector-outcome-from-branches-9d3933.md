---
section: Changed
---
- Connector tools in the Python and TypeScript cores classify a call only from
  the branch the connector returns. They no longer read the connector's
  single-dispatch fence records. **uncertain** and a branch listed as
  **unknown** record **outcome_unknown**, a success branch records
  **succeeded**, and every other branch records **failed**, for a Query and a
  Mutation alike. A Mutation's **defect** and its unlisted optional branches
  now record **failed** instead of **outcome_unknown**. A branch listed as
  **rejected** records **failed** also after an earlier attempt.
- A connector Retry always goes to the tool Step's retry policy. A
  single-attempt write that asks to retry now records **outcome_unknown**,
  also when the fence recorded that nothing was sent, instead of **failed**
  with the not-sent text.
- Arguments a Worker refuses before the operation runs record **failed** on
  every attempt, also after an earlier attempt's claim.
- **classify_connector_branch** ignores **may_have_dispatched** and
  **classifyConnectorBranch** ignores **mayHaveDispatched**; both are
  deprecated. **CONNECTOR_NOT_SENT_RETRY_LATER** and
  **connectorNotSentRetryLater** are deprecated and no call records them.
