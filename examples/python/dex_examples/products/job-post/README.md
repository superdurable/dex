# Job posting

A long-lived Flow that models a single job posting as durable, searchable
storage. It has no starting Step: `StepList.without_start_step` registers the
LinkedIn and Indeed updates, so the Flow starts idle and uses RPCs for CRUD.

`get` reads the posting, while `update` writes the indexed Attributes and starts
both job-board Steps in parallel with independent bounded retry policies.
`Title` and `JobDescription` are full-text indexed and
`LastUpdateTimeMillis` is integer indexed, so postings can be searched and
ordered.

The Worker synchronizes these Indexed Attributes automatically before opening
its listener.

With the sample server running:

```text
http://localhost:8080/products/job-post/create?title=Software+Engineer&description=in+Seattle
http://localhost:8080/products/job-post/read?workflowId=<flow-id>
http://localhost:8080/products/job-post/update?workflowId=<flow-id>&title=Senior+Software+Engineer&description=in+Portland&notes=testnotes
```
