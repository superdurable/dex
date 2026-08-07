# Employer/job-seeker engagement

An employer starts an engagement with a job seeker. The Flow sends reminders until the user opts out, accepts decline and accept RPCs, records typed Attributes, and notifies an external system from independent Steps.

The engagement status is indexed through Temporal's `CustomKeywordField`. Create that search attribute when running against a separately managed Temporal cluster.

Java SDK 0.0.3 does not expose SearchFlows, so `/engagement/list` is omitted.

With the sample server running:

```text
http://localhost:8080/engagement/start
http://localhost:8080/engagement/describe?workflowId=<flow-id>
http://localhost:8080/engagement/optout?workflowId=<flow-id>
http://localhost:8080/engagement/decline?workflowId=<flow-id>&notes=not-interested
http://localhost:8080/engagement/accept?workflowId=<flow-id>&notes=accepted
```
