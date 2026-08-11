# Employer/job-seeker engagement

An employer starts an engagement with a job seeker. The Flow sends reminders
until the user opts out, accepts `decline` and `accept` RPCs, records typed
Attributes, and notifies an external system from an independent Step.

`Initialize` fans out into three concurrent Steps: `ProcessTimeout` bounds the
engagement at 60 days or until the `CompleteProcess` Channel fires, `Reminder`
loops on a timer until the seeker opts out or the status changes, and
`NotifyExternalSystem` runs once per status transition. The `accept` and
`decline` RPCs return the new status and schedule `NotifyExternalSystem` in the
same atomic operation.

`Status` is a `str`-backed enum so it stays JSON encodable when it is nested
inside `EngagementDescription`.

The engagement status uses the `CustomKeywordField` Indexed Attribute, which
the Worker synchronizes automatically.

With the sample server running:

```text
http://localhost:8080/engagement/start
http://localhost:8080/engagement/describe?workflowId=<flow-id>
http://localhost:8080/engagement/optout?workflowId=<flow-id>
http://localhost:8080/engagement/decline?workflowId=<flow-id>&notes=not-interested
http://localhost:8080/engagement/accept?workflowId=<flow-id>&notes=accepted
```
