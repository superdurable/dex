# Job post

A long-lived Flow that models a single job posting as durable, searchable
storage. It has no starting Step: `StepList.without_start_step` registers only
`ExternalUpdate`, so the Flow starts idle and everything happens through RPCs.

`get` reads the posting, `update` writes the indexed Attributes and schedules
`ExternalUpdate` to push the change downstream with a bounded exponential retry
policy. `Title` and `JobDescription` are full-text indexed and
`LastUpdateTimeMillis` is integer indexed, so postings can be searched and
ordered.

The Worker synchronizes these Indexed Attributes automatically before opening
its listener.

With the sample server running:

```text
http://localhost:8080/jobpost/create?title=Software+Engineer&description=in+Seattle
http://localhost:8080/jobpost/read?workflowId=<flow-id>
http://localhost:8080/jobpost/update?workflowId=<flow-id>&title=Senior+Software+Engineer&description=in+Portland&notes=testnotes
```
