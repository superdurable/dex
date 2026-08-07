# Job post

A long-lived Flow that models a single job posting as durable, searchable
storage. It has no starting Step: `StepList.without_start_step` registers only
`ExternalUpdate`, so the Flow starts idle and everything happens through RPCs.

`get` reads the posting, `update` writes the indexed Attributes and schedules
`ExternalUpdate` to push the change downstream with a bounded exponential retry
policy. `Title` and `JobDescription` are full-text indexed and
`LastUpdateTimeMillis` is integer indexed, so postings can be searched and
ordered.

## Search attribute requirement

If using Temporal:

```bash
temporal search-attribute create -name Title -type Text -y
temporal search-attribute create -name JobDescription -type Text -y
temporal search-attribute create -name LastUpdateTimeMillis -type Int -y
```

If using Cadence:

```bash
cadence adm cl asa --search_attr_key Title --search_attr_type 0
cadence adm cl asa --search_attr_key JobDescription --search_attr_type 0
cadence adm cl asa --search_attr_key LastUpdateTimeMillis --search_attr_type 2
```

With the sample server running:

```text
http://localhost:8080/jobpost/create?title=Software+Engineer&description=in+Seattle
http://localhost:8080/jobpost/read?workflowId=<flow-id>
http://localhost:8080/jobpost/update?workflowId=<flow-id>&title=Senior+Software+Engineer&description=in+Portland&notes=testnotes
```
