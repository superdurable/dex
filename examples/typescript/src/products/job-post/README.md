# Job posting

CRUD job posting with indexed Attributes and job-board updates. Create seeds
initial Attributes. The update RPC locks Title before starting the LinkedIn and
Indeed Steps in parallel. Each Step uses a destination-specific lock so repeated
updates to one job board execute serially. Search uses Dex SearchFlows.

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
