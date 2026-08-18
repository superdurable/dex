# Employer/job-seeker engagement

An employer starts an engagement with a job seeker. The Flow sends reminders until the user opts out, accepts decline and accept RPCs, records typed Attributes, and notifies an external system from independent Steps.

The engagement status uses the `CustomKeywordField` Indexed Attribute, which
the Worker synchronizes automatically.

With the sample server running:

```text
http://localhost:8080/products/engagement/start
http://localhost:8080/products/engagement/describe?workflowId=<flow-id>
http://localhost:8080/products/engagement/optout?workflowId=<flow-id>
http://localhost:8080/products/engagement/decline?workflowId=<flow-id>&notes=not-interested
http://localhost:8080/products/engagement/accept?workflowId=<flow-id>&notes=accepted
http://localhost:8080/products/engagement/list?query=CustomKeywordField%20%3D%20%27Accepted%27
```
