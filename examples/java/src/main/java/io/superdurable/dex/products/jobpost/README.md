# Job post

CRUD job post with indexed attributes and external-system updates. Create seeds
initial attributes; update starts the ExternalUpdate step; search uses Dex
SearchFlows.

The Worker synchronizes the job-post Indexed Attributes automatically before
opening its listener.

With the sample server running:

```text
http://localhost:8080/products/job-post/create?title=Software+Engineer&description=in+Seattle
http://localhost:8080/products/job-post/read?workflowId=<flow-id>
http://localhost:8080/products/job-post/update?workflowId=<flow-id>&title=Senior+Software+Engineer&description=in+Portland&notes=testnotes
http://localhost:8080/products/job-post/delete?workflowId=<flow-id>
http://localhost:8080/products/job-post/search?query=<query>
```
